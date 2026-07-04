// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"fmt"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/crdwatch"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// discoveryServedChecker answers "is this plural resource served at this
// group/version?" from live apiserver discovery, memoizing one discovery call
// per distinct group/version. Instances are single-use snapshots: build a
// fresh one per resolution so CRDs installed between config loads are seen.
// Not safe for concurrent use (resolution is sequential).
//
// Only an authoritative NotFound counts as "unserved". A TRANSIENT discovery
// error (apiserver blip, aggregated-API hiccup) is recorded in transientErr
// instead: silently treating it as unserved would strip optional features —
// and bounce the controller through a spurious reinit — on every blip. The
// caller checks TransientErr() after resolution and fails the whole
// resolution, which the iteration retry loop (or the next CRD event)
// re-attempts safely.
type discoveryServedChecker struct {
	discovery    discovery.DiscoveryInterface
	logger       *slog.Logger
	cache        map[string]map[string]bool // groupVersion -> plural -> served
	transientErr error
}

func newDiscoveryServedChecker(d discovery.DiscoveryInterface, logger *slog.Logger) *discoveryServedChecker {
	return &discoveryServedChecker{
		discovery: d,
		logger:    logger,
		cache:     make(map[string]map[string]bool),
	}
}

// IsServed implements coreconfig.ServedVersionChecker.
func (c *discoveryServedChecker) IsServed(apiVersion, resources string) bool {
	served, ok := c.cache[apiVersion]
	if !ok {
		served = make(map[string]bool)
		list, err := c.discovery.ServerResourcesForGroupVersion(apiVersion)
		switch {
		case apierrors.IsNotFound(err):
			c.logger.Debug("Discovery reports group/version not served",
				"group_version", apiVersion)
		case err != nil:
			c.logger.Warn("Transient discovery error; resolution will be retried",
				"group_version", apiVersion, "error", err)
			if c.transientErr == nil {
				c.transientErr = fmt.Errorf("discovery for %s: %w", apiVersion, err)
			}
		default:
			for i := range list.APIResources {
				served[list.APIResources[i].Name] = true
			}
		}
		c.cache[apiVersion] = served
	}
	return served[resources]
}

// TransientErr returns the first non-NotFound discovery error observed, or
// nil when every answer was authoritative.
func (c *discoveryServedChecker) TransientErr() error {
	return c.transientErr
}

// installEffectiveConfig is runIteration's step 2.4: derive the effective
// config, expose the resolution on /debug/vars/effectiveConfigResolution, and
// install the same transformation on the live config-change path (the
// ConfigChangeHandler) so scatter-gather validators judge exactly what a
// reinitialized iteration would load. A fresh discovery checker per call:
// CRDs installed between config loads must be seen.
func installEffectiveConfig(
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	setup *componentSetup,
	infra *persistentInfra,
	logger *slog.Logger,
) (*coreconfig.Config, error) {
	effective, resolution, err := resolveEffectiveConfig(cfg, k8sClient, logger)
	if err != nil {
		return nil, fmt.Errorf("resolving effective config: %w", err)
	}

	infra.IntrospectionRegistry.Publish("effectiveConfigResolution",
		introspection.Func(func() (any, error) { return resolution, nil }))

	setup.ConfigChangeHandler.SetEffectiveResolver(func(c *coreconfig.Config) (*coreconfig.Config, error) {
		resolved, _, resolveErr := resolveEffectiveConfig(c, k8sClient, logger)
		return resolved, resolveErr
	})

	// The CRD watch re-resolves on relevant CRD changes and reloads the
	// iteration when the outcome differs. Note: `cfg` here is the RAW config
	// (candidate lists intact) — the watch groups and re-resolution must see
	// the candidates of currently-unavailable optional resources too.
	startCRDWatch(setup, cfg, effective, resolution, k8sClient, logger)

	return effective, nil
}

// startCRDWatch launches the CRD watch (runIteration step 4.2). Groups come
// from the RAW config's candidate lists — an unavailable optional resource's
// CRD appearing is exactly the event this watch exists for. The reload only
// fires when a fresh resolution differs from this iteration's; a resolution
// ERROR (a required resource lost its served version) also reloads, so the
// next iteration fails fast and surfaces the cause in /healthz instead of
// silently serving from a stale informer cache.
func startCRDWatch(
	setup *componentSetup,
	rawCfg *coreconfig.Config,
	effectiveCfg *coreconfig.Config,
	resolution *coreconfig.Resolution,
	k8sClient *client.Client,
	logger *slog.Logger,
) {
	crdWatch := crdwatch.New(k8sClient, crdwatch.RelevantGroups(rawCfg),
		func() bool {
			_, freshResolution, resolveErr := resolveEffectiveConfig(rawCfg, k8sClient, logger)
			if resolveErr != nil {
				// Transient discovery errors surface as resolveErr too — a
				// reload on a blip would bounce the controller for nothing.
				// Skip; the debounced watcher re-fires on further CRD events
				// (and a genuinely lost REQUIRED resource keeps generating
				// them, or fails the next natural reload fast).
				logger.Warn("CRD-change re-resolution failed; skipping reload", "error", resolveErr)
				return false
			}
			return !resolution.Equal(freshResolution)
		},
		func() {
			select {
			case setup.ConfigChangeCh <- effectiveCfg:
			default: // a reload is already queued; it subsumes this one
			}
		},
		logger)
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, "crd watch", crdWatch.Start)
}

// resolveEffectiveConfig resolves the config's watched resources against live
// discovery and returns the effective config the iteration consumes (see
// coreconfig.ResolveEffective). Resolution outcomes are logged: per-resource
// resolved versions at debug, feature stripping at info (an operator whose
// routing silently lost a kind must find the cause in the log).
func resolveEffectiveConfig(cfg *coreconfig.Config, k8sClient *client.Client, logger *slog.Logger) (*coreconfig.Config, *coreconfig.Resolution, error) {
	checker := newDiscoveryServedChecker(k8sClient.Clientset().Discovery(), logger)
	effective, resolution, err := coreconfig.ResolveEffective(cfg, checker)
	if err != nil {
		return nil, nil, err
	}
	if terr := checker.TransientErr(); terr != nil {
		// Fail the whole resolution rather than act on a partial view: an
		// optional feature must not strip (nor a reload fire) because of an
		// apiserver blip. The caller retries.
		return nil, nil, fmt.Errorf("transient discovery error during resolution: %w", terr)
	}

	for name, version := range resolution.ResolvedVersions {
		logger.Debug("Watched resource resolved", "resource", name, "api_version", version)
	}
	if len(resolution.Unavailable) > 0 {
		logger.Info("Optional watched resources unavailable — dependent features stripped",
			"unavailable", resolution.Unavailable,
			"stripped_snippets", len(resolution.StrippedSnippets),
			"stripped_tests", len(resolution.StrippedTests))
	}
	return effective, resolution, nil
}

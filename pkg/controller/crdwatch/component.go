// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package crdwatch reinitializes the controller when the CustomResourceDefinitions
// backing watched resources change.
//
// The effective config (which API version each watched resource is watched at,
// and which optional features are active) is resolved from apiserver discovery
// once per iteration. This component is what makes that resolution LIVE: it
// watches CRDs in the API groups referenced by the configuration and funnels
// relevant changes — installation, in-place upgrade that changes served
// versions, removal — into the existing config-reload iteration restart. Late
// installation of an optional resource's CRD activates its features, and an
// upgrade that retires the watched version re-resolves to a served one, all
// without a helm operation or pod restart.
//
// This is operational plumbing for the controller's own watch set (see the
// operational-identity exception in the root CLAUDE.md) and is fully generic:
// the group filter and the reload decision are derived entirely from the
// configuration's candidate lists, never from built-in resource knowledge.
package crdwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timers"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// ComponentName identifies this component in logs.
const ComponentName = "crdwatch"

// DefaultDebounce batches bursts of CRD changes (an install applies many CRDs
// in quick succession) into one reload decision.
const DefaultDebounce = 2 * time.Second

// DefaultRecheckInterval is the cadence at which an inconclusive re-resolution
// (no change yet, or a transient error) is re-checked after a CRD event burst.
// The apiserver's discovery endpoint propagates a CRD apply asynchronously: a
// re-resolution racing that lag still sees the OLD state, and the CRD's later
// Established condition flip bumps no metadata.generation — so no further
// informer event ever arrives to break the stall. The recheck timer is what
// closes that window.
const DefaultRecheckInterval = 5 * time.Second

// DefaultStableChecks is how many CONSECUTIVE equal re-resolutions (since the
// last observed CRD event) it takes before the watch accepts "no effective
// change" as final and goes quiet. Errored re-resolutions reset the streak —
// an error confirms nothing.
const DefaultStableChecks = 3

// DefaultMaxErrorStreak bounds CONSECUTIVE failed re-resolutions before the
// component escalates by triggering the reload anyway. A persistently failing
// resolution (e.g. a required resource's CRD genuinely removed) must not idle
// in the recheck loop forever with only warnings: escalating hands the fault
// to the iteration restart path, where resolution failure fails fast and
// surfaces through /healthz and the iteration retry loop. Transient discovery
// blips settle long before this bound (~30s at the default cadence) fires.
const DefaultMaxErrorStreak = 6

var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// Component watches CRDs in the configured API groups and triggers a reload
// when a change would alter the effective config.
type Component struct {
	k8sClient *client.Client
	logger    *slog.Logger

	// groups is the set of API groups referenced by any watched resource's
	// candidate versions. CRDs outside these groups are ignored entirely.
	groups map[string]bool

	// shouldReload re-resolves the effective config against fresh discovery
	// and reports whether the outcome differs from the running iteration's.
	// It keeps pointless restarts (e.g. an unrelated CRD added to a watched
	// group, or a new served version that doesn't win its preference list)
	// from bouncing the controller. A non-nil error marks the answer as
	// INCONCLUSIVE (transient discovery/schema failure): the component never
	// reloads on it, but also never accepts it as final — it schedules the
	// next recheck instead.
	shouldReload func() (bool, error)

	// trigger requests the iteration restart (non-blocking; the reload
	// channel has capacity 1 and a queued reload subsumes this one).
	trigger func()

	debounce         time.Duration
	recheckInterval  time.Duration
	stableChecks     int
	maxErrorStreak   int
	waitForCacheSync func(<-chan struct{}, ...cache.InformerSynced) bool
	synced           atomic.Bool
	pending          chan struct{}
}

// New creates the CRD watch component.
//
//   - groups: from RelevantGroups(rawConfig) — the RAW config, not the
//     effective one, so groups of currently-unavailable optional resources
//     are watched too (their CRD appearing is exactly the event we want).
//   - shouldReload / trigger: see field docs.
func New(k8sClient *client.Client, groups map[string]bool, shouldReload func() (bool, error), trigger func(), logger *slog.Logger) *Component {
	return &Component{
		k8sClient:        k8sClient,
		logger:           logger.With("component", ComponentName),
		groups:           groups,
		shouldReload:     shouldReload,
		trigger:          trigger,
		debounce:         DefaultDebounce,
		recheckInterval:  DefaultRecheckInterval,
		stableChecks:     DefaultStableChecks,
		maxErrorStreak:   DefaultMaxErrorStreak,
		waitForCacheSync: cache.WaitForCacheSync,
		pending:          make(chan struct{}, 1),
	}
}

// RelevantGroups extracts the set of non-core API groups referenced by any
// watched resource's candidate versions. Core-group resources ("v1") are never
// CRDs and are skipped.
func RelevantGroups(cfg *coreconfig.Config) map[string]bool {
	groups := make(map[string]bool)
	for name := range cfg.WatchedResources {
		resource := cfg.WatchedResources[name]
		for _, candidate := range resource.CandidateVersions() {
			if group, _, found := strings.Cut(candidate, "/"); found && group != "" {
				groups[group] = true
			}
		}
	}
	return groups
}

// Start runs the CRD informer until the context is cancelled. Informer events
// observed during initial sync are baseline; a post-sync resolution check
// closes the gap between controller iterations before later events take over.
func (c *Component) Start(ctx context.Context) error {
	if len(c.groups) == 0 {
		c.logger.Debug("No CRD-backed watched resources; CRD watch idle")
		<-ctx.Done()
		return nil
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		c.k8sClient.DynamicClient(), 0, metav1.NamespaceAll, nil)
	informer := factory.ForResource(crdGVR).Informer()

	if err := informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		c.logger.Warn("CRD watch error (Reflector will retry)", "error", err)
	}); err != nil {
		return fmt.Errorf("setting CRD watch error handler: %w", err)
	}

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if group, ok := c.relevantGroup(obj); ok {
				c.noteChange("added", group, obj)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			group, ok := c.relevantGroup(newObj)
			if !ok {
				return
			}
			// Only spec changes matter for resolution: served-version
			// changes AND in-place schema-content upgrades (same served
			// versions, new field set — the RequiresFields stripping
			// depends on schema contents). metadata.generation bumps on
			// every spec change and nothing else, so it covers both while
			// still ignoring status-only and metadata churn.
			if generation(oldObj) != generation(newObj) {
				c.noteChange("spec changed", group, newObj)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if group, ok := c.relevantGroup(obj); ok {
				c.noteChange("deleted", group, obj)
			}
		},
	}); err != nil {
		return fmt.Errorf("adding CRD event handler: %w", err)
	}

	informerCtx, stopInformer := context.WithCancel(ctx)
	factory.Start(informerCtx.Done())
	defer func() {
		stopInformer()
		factory.Shutdown()
	}()

	if !c.waitForCacheSync(informerCtx.Done(), informer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("CRD informer cache sync failed")
	}
	c.synced.Store(true)
	c.logger.Debug("CRD watch synced", "groups", len(c.groups))
	// A CRD can change after the previous iteration stops but before this
	// informer's baseline sync. Compare discovery once so that gap is visible.
	select {
	case c.pending <- struct{}{}:
	default:
	}

	c.runDebounceLoop(ctx)
	return nil
}

// noteChange queues a debounced reload decision for a relevant post-sync change.
func (c *Component) noteChange(what, group string, obj any) {
	if !c.synced.Load() {
		return // initial-sync baseline, not a change
	}
	name := ""
	if u, ok := obj.(*unstructured.Unstructured); ok {
		name = u.GetName()
	}
	c.logger.Info("Watched-group CRD changed", "crd", name, "group", group, "change", what)
	select {
	case c.pending <- struct{}{}:
	default:
	}
}

// runDebounceLoop collapses bursts of CRD changes and, once quiet for the
// debounce window, runs one settle cycle per burst (see settle).
func (c *Component) runDebounceLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.pending:
		}
		if !c.settle(ctx) {
			return
		}
	}
}

// settle runs one decision cycle after a CRD event: debounce the burst, then
// re-resolve — and do NOT accept a single inconclusive answer as final. The
// apiserver's discovery endpoint propagates a CRD apply asynchronously, so a
// re-resolution racing that lag still sees the old state; the CRD's later
// Established condition flip bumps no metadata.generation, so the UpdateFunc
// filter drops it and no further informer event arrives (the
// TestGatewayAPICRDUpgradeInPlace reinstall stall). The cycle therefore
// re-checks at recheckInterval cadence until EITHER
//
//   - a re-resolution differs from the running one → reload (as before), OR
//   - the answer has been stable-equal for stableChecks CONSECUTIVE checks
//     since the last observed CRD event → accept "no change" and go quiet.
//
// An errored re-resolution is inconclusive: it never reloads (a blip must
// not bounce the controller) and never counts toward — it resets — the
// stability streak; it just schedules the next recheck. A new CRD event at
// any point restarts the debounce window and the streak.
//
// Returns false when ctx was cancelled.
func (c *Component) settle(ctx context.Context) bool {
	equalStreak := 0
	errorStreak := 0
	var timer timers.SafeTimer
	timer.Reset(c.debounce)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-c.pending:
			// The world changed again: restart the debounce window and
			// invalidate any stability observed so far.
			equalStreak = 0
			errorStreak = 0
			timer.Reset(c.debounce)
			continue
		case <-timer.Chan():
			timer.Fired()
		}

		reload, err := c.shouldReload()
		switch {
		case err != nil:
			equalStreak = 0
			errorStreak++
			if errorStreak >= c.maxErrorStreak {
				// Persistent failure, not a blip: escalate instead of idling
				// in the recheck loop forever. The reload makes the next
				// iteration re-resolve on the startup path, where a lost
				// REQUIRED resource fails fast and surfaces through /healthz
				// and the iteration retry loop instead of hiding behind
				// warnings while stale informer caches keep serving.
				c.logger.Error("CRD-change re-resolution failing persistently — escalating to reinitialization",
					"error", err, "consecutive_errors", errorStreak)
				c.trigger()
				return true
			}
			c.logger.Warn("CRD-change re-resolution failed; scheduling recheck",
				"error", err, "recheck_in", c.recheckInterval, "consecutive_errors", errorStreak)
		case reload:
			c.logger.Info("CRD change alters the effective config — triggering reinitialization")
			c.trigger()
			return true
		default:
			errorStreak = 0
			equalStreak++
			if equalStreak >= c.stableChecks {
				c.logger.Debug("CRD change does not alter the effective config; no reload",
					"stable_checks", equalStreak)
				return true
			}
			c.logger.Debug("CRD change shows no effective-config change yet; scheduling recheck",
				"stable_checks", equalStreak, "recheck_in", c.recheckInterval)
		}
		timer.Reset(c.recheckInterval)
	}
}

// relevantGroup reports the CRD's spec.group and whether it is watched.
func (c *Component) relevantGroup(obj any) (string, bool) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return "", false
	}
	group, _, _ := unstructured.NestedString(u.Object, "spec", "group")
	return group, c.groups[group]
}

// generation returns the CRD's metadata.generation (0 when unreadable).
// The apiserver bumps it on every spec change — served-version edits and
// in-place schema upgrades alike — and never on status/metadata churn.
func generation(obj any) int64 {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return 0
	}
	return u.GetGeneration()
}

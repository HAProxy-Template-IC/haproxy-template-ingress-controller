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
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// ComponentName identifies this component in logs.
const ComponentName = "crdwatch"

// DefaultDebounce batches bursts of CRD changes (an install applies many CRDs
// in quick succession) into one reload decision.
const DefaultDebounce = 2 * time.Second

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
	// from bouncing the controller.
	shouldReload func() bool

	// trigger requests the iteration restart (non-blocking; the reload
	// channel has capacity 1 and a queued reload subsumes this one).
	trigger func()

	debounce time.Duration
	synced   atomic.Bool
	pending  chan struct{}
}

// New creates the CRD watch component.
//
//   - groups: from RelevantGroups(rawConfig) — the RAW config, not the
//     effective one, so groups of currently-unavailable optional resources
//     are watched too (their CRD appearing is exactly the event we want).
//   - shouldReload / trigger: see field docs.
func New(k8sClient *client.Client, groups map[string]bool, shouldReload func() bool, trigger func(), logger *slog.Logger) *Component {
	return &Component{
		k8sClient:    k8sClient,
		logger:       logger.With("component", ComponentName),
		groups:       groups,
		shouldReload: shouldReload,
		trigger:      trigger,
		debounce:     DefaultDebounce,
		pending:      make(chan struct{}, 1),
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

// Start runs the CRD informer until the context is cancelled. Events observed
// during the informer's initial sync are baseline, not changes — only
// post-sync add/update/delete reaches the reload decision.
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
			// Only served-version changes matter for resolution; ignore
			// status-only and metadata churn.
			if !equalStringSlices(servedVersions(oldObj), servedVersions(newObj)) {
				c.noteChange("served versions changed", group, newObj)
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

	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return ctx.Err()
	}
	c.synced.Store(true)
	c.logger.Debug("CRD watch synced", "groups", len(c.groups))

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
// debounce window, re-resolves and triggers the reload if the outcome changed.
func (c *Component) runDebounceLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.pending:
		}

		timer := time.NewTimer(c.debounce)
	drain:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-c.pending:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(c.debounce)
			case <-timer.C:
				break drain
			}
		}

		if !c.shouldReload() {
			c.logger.Debug("CRD change does not alter the effective config; no reload")
			continue
		}
		c.logger.Info("CRD change alters the effective config — triggering reinitialization")
		c.trigger()
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

// servedVersions returns the CRD's served version names, sorted.
func servedVersions(obj any) []string {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	versions, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions")
	var served []string
	for _, v := range versions {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		isServed, _, _ := unstructured.NestedBool(vm, "served")
		name, _, _ := unstructured.NestedString(vm, "name")
		if isServed && name != "" {
			served = append(served, name)
		}
	}
	sort.Strings(served)
	return served
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

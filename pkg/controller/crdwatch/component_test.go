// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package crdwatch

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func crdObj(name, group string, servedVersions ...string) *unstructured.Unstructured {
	vs := make([]any, len(servedVersions))
	for i, v := range servedVersions {
		vs[i] = map[string]any{"name": v, "served": true}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"group":    group,
			"versions": vs,
		},
	}}
}

func TestRelevantGroups(t *testing.T) {
	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"httproutes": {APIVersions: []string{"gateway.example.io/v1", "gateway.example.io/v1beta1"}, Resources: "httproutes"},
			"widgets":    {APIVersion: "widgets.example.io/v1alpha1", Resources: "widgets"},
			"services":   {APIVersion: "v1", Resources: "services"}, // core group: never a CRD
		},
	}

	groups := RelevantGroups(cfg)
	assert.Equal(t, map[string]bool{
		"gateway.example.io": true,
		"widgets.example.io": true,
	}, groups)
}

// TestGeneration pins the spec-change signal: metadata.generation is read
// from the unstructured CRD (0 when absent/unreadable). The apiserver bumps
// it on every spec change — served-version edits AND in-place schema-content
// upgrades — but never on status/metadata churn, so comparing it in
// UpdateFunc catches schema upgrades that keep the served-version set
// identical (the RequiresFields re-resolution trigger) while still ignoring
// status-only updates.
func TestGeneration(t *testing.T) {
	crd := crdObj("tcproutes.g.io", "g.io", "v1")
	crd.SetGeneration(3)
	assert.Equal(t, int64(3), generation(crd))
	assert.Equal(t, int64(0), generation(&unstructured.Unstructured{Object: map[string]any{}}))
	assert.Equal(t, int64(0), generation("not an unstructured"))
}

// newTestComponent builds a component with short debounce/recheck timings
// driving runDebounceLoop directly. The returned stop func ends the loop.
func newTestComponent(t *testing.T, shouldReload func() (bool, error), trigger func()) (c *Component, stop func()) {
	t.Helper()
	c = New(nil, map[string]bool{"g.io": true}, shouldReload, trigger, slog.Default())
	c.debounce = 10 * time.Millisecond
	c.recheckInterval = 10 * time.Millisecond
	c.synced.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.runDebounceLoop(ctx); close(done) }()
	return c, func() {
		cancel()
		<-done
	}
}

// TestComponent_ReloadDecision drives the debounce loop directly: a relevant
// post-sync change triggers exactly when shouldReload says the resolution
// changed, and irrelevant-group or pre-sync events never reach it.
func TestComponent_ReloadDecision(t *testing.T) {
	tests := []struct {
		name         string
		shouldReload bool
		wantTrigger  bool
	}{
		{name: "resolution changed triggers reload", shouldReload: true, wantTrigger: true},
		{name: "resolution unchanged suppresses reload", shouldReload: false, wantTrigger: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var triggered atomic.Bool
			var calls atomic.Int64
			c, stop := newTestComponent(t,
				func() (bool, error) { calls.Add(1); return tt.shouldReload, nil },
				func() { triggered.Store(true) })
			defer stop()

			c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

			if tt.wantTrigger {
				require.Eventually(t, triggered.Load,
					time.Second, 5*time.Millisecond)
				return
			}
			// The unchanged case settles only after stableChecks consecutive
			// equal answers; give it those checks, then prove it stays quiet.
			require.Eventually(t, func() bool {
				return calls.Load() >= int64(c.stableChecks)
			}, time.Second, 5*time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			assert.False(t, triggered.Load())
		})
	}
}

// TestComponent_RecheckCatchesLaggingDiscovery pins the fix for the
// TestGatewayAPICRDUpgradeInPlace reinstall stall: a CRD event whose FIRST
// re-resolution races the apiserver's discovery-propagation lag (and thus
// sees no change) must not be accepted as final. The bounded recheck sees the
// difference on a later pass — with NO further CRD event (the Established
// condition flip bumps no generation, so none arrives) — and reloads.
func TestComponent_RecheckCatchesLaggingDiscovery(t *testing.T) {
	var triggered atomic.Bool
	var calls atomic.Int64
	c, stop := newTestComponent(t,
		func() (bool, error) {
			// First answer races the discovery lag: "no change". The second
			// sees the propagated CRD: "changed".
			return calls.Add(1) >= 2, nil
		},
		func() { triggered.Store(true) })
	defer stop()

	// Single CRD event; no further events follow.
	c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

	require.Eventually(t, triggered.Load,
		time.Second, 5*time.Millisecond)
	assert.GreaterOrEqual(t, calls.Load(), int64(2),
		"reload must come from a RECHECK, not the first resolution")
}

// TestComponent_StableEqualGoesQuiet pins the recheck bound: once the
// re-resolution has been equal for stableChecks consecutive checks, the
// component stops polling until the next CRD event.
func TestComponent_StableEqualGoesQuiet(t *testing.T) {
	var triggered atomic.Bool
	var calls atomic.Int64
	c, stop := newTestComponent(t,
		func() (bool, error) { calls.Add(1); return false, nil },
		func() { triggered.Store(true) })
	defer stop()

	c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

	require.Eventually(t, func() bool {
		return calls.Load() >= int64(c.stableChecks)
	}, time.Second, 5*time.Millisecond)
	// Quiet means: no further shouldReload polls and no trigger. Several
	// recheck intervals pass; the count must not move.
	time.Sleep(5 * c.recheckInterval)
	assert.Equal(t, int64(c.stableChecks), calls.Load(),
		"component must go quiet after stableChecks consecutive equal answers")
	assert.False(t, triggered.Load())

	// A NEW CRD event restarts the cycle.
	c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))
	require.Eventually(t, func() bool {
		return calls.Load() > int64(c.stableChecks)
	}, time.Second, 5*time.Millisecond)
}

// TestComponent_TransientErrorKeepsRechecking pins the transient-error
// semantics: an errored re-resolution never reloads and never counts toward
// the stability bound — the recheck cadence continues past stableChecks
// errors until a conclusive answer arrives (here: a difference → reload).
func TestComponent_TransientErrorKeepsRechecking(t *testing.T) {
	var triggered atomic.Bool
	var calls atomic.Int64
	c, stop := newTestComponent(t,
		func() (bool, error) {
			// More consecutive errors than the stability bound, then a
			// conclusive "changed" answer.
			if calls.Add(1) <= int64(DefaultStableChecks)+2 {
				return false, errors.New("transient discovery blip")
			}
			return true, nil
		},
		func() { triggered.Store(true) })
	defer stop()

	c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

	require.Eventually(t, triggered.Load,
		time.Second, 5*time.Millisecond)
	assert.Greater(t, calls.Load(), int64(DefaultStableChecks)+2,
		"errors must keep the recheck loop alive, not conclude it")
}

// TestComponent_InitialSyncBaselineIgnored pins that events observed before
// the informer's initial sync completes never queue a reload decision.
func TestComponent_InitialSyncBaselineIgnored(t *testing.T) {
	var triggered atomic.Bool
	c := New(nil, map[string]bool{"g.io": true},
		func() (bool, error) { return true, nil },
		func() { triggered.Store(true) },
		slog.Default())
	c.debounce = 5 * time.Millisecond
	// synced deliberately NOT set: baseline phase.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.runDebounceLoop(ctx)

	c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

	time.Sleep(50 * time.Millisecond)
	assert.False(t, triggered.Load(), "pre-sync baseline adds must not trigger a reload")
}

// TestComponent_IrrelevantGroupIgnored pins the group filter.
func TestComponent_IrrelevantGroupIgnored(t *testing.T) {
	c := New(nil, map[string]bool{"g.io": true}, func() (bool, error) { return true, nil }, func() {}, slog.Default())

	_, relevant := c.relevantGroup(crdObj("others.other.io", "other.io", "v1"))
	assert.False(t, relevant)

	group, relevant := c.relevantGroup(crdObj("tcproutes.g.io", "g.io", "v1"))
	assert.True(t, relevant)
	assert.Equal(t, "g.io", group)
}

// TestComponent_PersistentErrorEscalatesToReload pins the error-streak bound:
// a re-resolution that fails on EVERY attempt (a genuinely lost required
// resource, not a discovery blip) must not idle in the recheck loop forever —
// after DefaultMaxErrorStreak consecutive failures the component escalates by
// triggering the reload, handing the fault to the iteration restart path
// where it fails fast and surfaces via /healthz.
func TestComponent_PersistentErrorEscalatesToReload(t *testing.T) {
	var triggered atomic.Bool
	var calls atomic.Int64
	c, stop := newTestComponent(t,
		func() (bool, error) {
			calls.Add(1)
			return false, errors.New("required resource unserved")
		},
		func() { triggered.Store(true) })
	defer stop()

	c.noteChange("deleted", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

	require.Eventually(t, triggered.Load,
		time.Second, 5*time.Millisecond,
		"persistent re-resolution failure must escalate to a reload")
	assert.GreaterOrEqual(t, calls.Load(), int64(DefaultMaxErrorStreak),
		"escalation must only fire after the full error streak")
}

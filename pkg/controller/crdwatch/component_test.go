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
			c := New(nil, map[string]bool{"g.io": true},
				func() bool { return tt.shouldReload },
				func() { triggered.Store(true) },
				slog.Default())
			c.debounce = 10 * time.Millisecond
			c.synced.Store(true)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { c.runDebounceLoop(ctx); close(done) }()

			c.noteChange("added", "g.io", crdObj("tcproutes.g.io", "g.io", "v1"))

			require.Eventually(t, func() bool {
				return triggered.Load() == tt.wantTrigger
			}, time.Second, 5*time.Millisecond)
			// Give the negative case a beat to prove it stays quiet.
			if !tt.wantTrigger {
				time.Sleep(50 * time.Millisecond)
				assert.False(t, triggered.Load())
			}
			cancel()
			<-done
		})
	}
}

// TestComponent_InitialSyncBaselineIgnored pins that events observed before
// the informer's initial sync completes never queue a reload decision.
func TestComponent_InitialSyncBaselineIgnored(t *testing.T) {
	var triggered atomic.Bool
	c := New(nil, map[string]bool{"g.io": true},
		func() bool { return true },
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
	c := New(nil, map[string]bool{"g.io": true}, func() bool { return true }, func() {}, slog.Default())

	_, relevant := c.relevantGroup(crdObj("others.other.io", "other.io", "v1"))
	assert.False(t, relevant)

	group, relevant := c.relevantGroup(crdObj("tcproutes.g.io", "g.io", "v1"))
	assert.True(t, relevant)
	assert.Equal(t, "g.io", group)
}

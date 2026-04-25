// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// handleUpdate has a non-obvious field-selector transition state
// machine that has NO direct test coverage. The four cases drive
// real changes to the store and downstream OnChange callbacks:
//
//	old   new    action
//	──────────────────────────────────────────────────────
//	match match → processUpdate (normal update path)
//	match  ✗    → processDelete (resource left the filter,
//	              must be evicted from store; otherwise it
//	              persists as stale data forever)
//	  ✗   match → processAdd    (resource entered the filter,
//	              must be added; otherwise newly-relevant
//	              resources are silently missed)
//	  ✗    ✗    → no-op         (resource neither was nor is
//	              relevant; logged as filtered)
//
// Existing tests cover the "matching" path (TestWatcher_HandleUpdate_*
// in watcher_test.go) but never construct a watcher WITH a field
// selector to exercise transitions. Without these tests, a refactor
// that, e.g., reversed the old/new comparison would silently:
//
//   - leave stale resources in the store after they leave the filter
//     (templates would render against ghosts), or
//   - miss resources that newly enter the filter (templates would
//     render with an incomplete view).
//
// Build a real Watcher with a field selector and observe ChangeStats
// counters via OnChange to confirm the right delta path fired.

// transitionCounters bundles the three OnChange counter pointers so
// the test helper has a single named-return shape.
type transitionCounters struct {
	created  *atomic.Int32
	modified *atomic.Int32
	deleted  *atomic.Int32
}

// transitionFieldSelector is the selector used by every transition
// test below. Centralised so the helper signature stays parameter-
// free (lint complains when the only caller passes a constant).
const transitionFieldSelector = "spec.ingressClassName=haproxy"

func newWatcherWithFieldSelector(t *testing.T) (*Watcher, *transitionCounters) {
	t.Helper()
	c := &transitionCounters{
		created:  &atomic.Int32{},
		modified: &atomic.Int32{},
		deleted:  &atomic.Int32{},
	}

	cfg := validWatcherConfig()
	cfg.FieldSelector = transitionFieldSelector
	cfg.CallOnChangeDuringSync = true
	cfg.DebounceInterval = 5 * time.Millisecond
	cfg.OnChange = func(_ types.Store, stats types.ChangeStats) {
		c.created.Add(int32(stats.Created))
		c.modified.Add(int32(stats.Modified))
		c.deleted.Add(int32(stats.Deleted))
	}

	w, err := New(cfg, newTestClient(t), slog.Default())
	require.NoError(t, err)

	// Start so the debouncer goroutine is running. The informer never
	// receives real events because we drive handleUpdate directly.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	go func() { _ = w.Start(ctx) }()
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	return w, c
}

// makeIngress is a tiny helper to build minimal Ingress-shaped
// unstructured resources that the field selector can introspect.
// Name is parameterised so different tests can use distinct identifiers
// when needed.
func makeIngress(name, version, ingressClass string) *unstructured.Unstructured {
	spec := map[string]any{}
	if ingressClass != "" {
		spec["ingressClassName"] = ingressClass
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       "default",
				"resourceVersion": version,
			},
			"spec": spec,
		},
	}
}

func TestWatcher_HandleUpdate_BothMatch_NormalUpdate(t *testing.T) {
	// match → match: standard update path, modified counter increments.
	w, c := newWatcherWithFieldSelector(t)

	old := makeIngress("api-bothmatch", "v1", "haproxy")
	updated := makeIngress("api-bothmatch", "v2", "haproxy") // bumped version, still matches

	w.handleUpdate(old, updated)
	time.Sleep(100 * time.Millisecond) // allow debouncer to fire

	assert.Equal(t, int32(0), c.created.Load(), "old already matched, so no create")
	assert.Equal(t, int32(1), c.modified.Load(), "matching update must record one modification")
	assert.Equal(t, int32(0), c.deleted.Load(), "no delete on plain update")
}

func TestWatcher_HandleUpdate_LeavesFilter_TreatedAsDelete(t *testing.T) {
	// match → ✗: the resource was in the store and is no longer
	// relevant. handleUpdate must call processDelete to evict it.
	// A regression that called processUpdate instead would leave
	// the stale resource in the store, and templates would keep
	// rendering against it.
	w, c := newWatcherWithFieldSelector(t)

	// Old: matches the filter. Plant it in the store as if a
	// previous handleAdd had inserted it.
	old := makeIngress("api-leaves", "v1", "haproxy")
	w.handleAdd(old)

	// New: same name, different ingressClassName — no longer matches.
	updated := makeIngress("api-leaves", "v2", "nginx")

	w.handleUpdate(old, updated)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), c.deleted.Load(),
		"resource leaving the field selector must be processed as a DELETE; "+
			"otherwise stale resources persist in the store and templates render against ghosts")
	assert.Equal(t, int32(0), c.modified.Load(),
		"the leaving-filter case must NOT also fire a modify — that would double-count the change")

	// Verify the resource is actually gone from the store.
	got, err := w.Store().Get("default", "api-leaves")
	require.NoError(t, err)
	assert.Empty(t, got, "store must no longer contain the resource that left the filter")
}

func TestWatcher_HandleUpdate_EntersFilter_TreatedAsAdd(t *testing.T) {
	// ✗ → match: the resource is newly relevant. handleUpdate must
	// call processAdd to insert it into the store. A regression that
	// called processUpdate (which Update()s a non-existent key) would
	// silently miss the new resource.
	w, c := newWatcherWithFieldSelector(t)

	old := makeIngress("api-enters", "v1", "nginx")       // not relevant
	updated := makeIngress("api-enters", "v2", "haproxy") // newly relevant

	w.handleUpdate(old, updated)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), c.created.Load(),
		"resource entering the field selector must be processed as an ADD; "+
			"otherwise newly-relevant resources are silently missed")

	// Verify the resource was added.
	got, err := w.Store().Get("default", "api-enters")
	require.NoError(t, err)
	require.Len(t, got, 1, "store must contain the resource that entered the filter")
}

func TestWatcher_HandleUpdate_NeitherMatch_Ignored(t *testing.T) {
	// ✗ → ✗: nothing relevant changed. handleUpdate must be a no-op:
	// no callbacks, no state changes. A regression that fell through
	// to processUpdate would call store.Update on a key that was
	// never inserted, producing an error log on every irrelevant
	// resource update — noise that obscures real failures.
	w, c := newWatcherWithFieldSelector(t)

	old := makeIngress("api-neither", "v1", "nginx")
	updated := makeIngress("api-neither", "v2", "traefik")

	w.handleUpdate(old, updated)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(0), c.created.Load())
	assert.Equal(t, int32(0), c.modified.Load())
	assert.Equal(t, int32(0), c.deleted.Load(), "neither-match update must be a complete no-op")
}

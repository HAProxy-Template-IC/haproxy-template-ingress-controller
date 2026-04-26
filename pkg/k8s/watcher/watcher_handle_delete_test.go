// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// Watcher.handleDelete (multi-resource variant, distinct from
// SingleWatcher.handleDelete which has its own pin) had 0%
// coverage. Three early-return branches matter:
//
//  1. Direct conversion fails AND tombstone unwrap also fails
//     → return WITHOUT calling processDelete. A regression that
//     fell through with nil resource would crash the watcher
//     goroutine on the indexer.ExtractKeys nil-deref.
//
//  2. Resource doesn't match field selector → log + return.
//     This is the "delete event for a resource we never indexed"
//     case (the resource never matched our filter, so it was
//     never in our store; sending the delete to processDelete
//     would attempt to remove keys that aren't there). Without
//     this guard, store.Delete would surface "key not found"
//     errors as warnings on every irrelevant delete event.
//
//  3. Tombstone wrapping a real resource that matches the field
//     selector → unwrapped, processDelete reached. (We pin only
//     "no panic" here because processDelete needs full indexer/
//     store/debouncer wiring; the unwrap behaviour is the
//     load-bearing part.)

// stubMatcher implements the package-private fieldSelector
// interface for tests. Returning false models "this resource is
// not in our filter set so we shouldn't try to delete it".
type stubMatcher struct {
	matches bool
}

func (s *stubMatcher) Matches(_ any) (bool, error) { return s.matches, nil }

// minimalWatcher constructs a *Watcher with only the fields the
// tested early-return branches reach. indexer/store/debouncer are
// intentionally nil — the tests verify those are NOT touched.
func minimalWatcher(logger *slog.Logger, matcher fieldSelector) *Watcher {
	return &Watcher{
		logger:               logger,
		fieldSelectorMatcher: matcher,
		config: types.WatcherConfig{
			FieldSelector: "stub-selector",
		},
	}
}

func newWatcherDeleteResource(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       "haptic",
				"resourceVersion": "100",
			},
		},
	}
}

func TestWatcher_HandleDelete_TombstoneWrappingNonResourceIsNoOp(t *testing.T) {
	// Tombstone wrapping a string (defensive — should never
	// happen in practice but the inner-conversion-also-fails
	// branch must NOT reach processDelete; otherwise the watcher
	// goroutine crashes on the indexer.ExtractKeys nil-deref.
	w := minimalWatcher(slog.Default(), nil)
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "haptic/garbage-tombstone",
		Obj: "not-an-unstructured-resource",
	}

	require.NotPanics(t, func() {
		w.handleDelete(tombstone)
	}, "tombstone wrapping non-Unstructured MUST take the early-return "+
		"branch — without it processDelete would receive nil and crash "+
		"the watcher goroutine on indexer.ExtractKeys, taking down the "+
		"entire resource type's event stream")
}

func TestWatcher_HandleDelete_FieldSelectorMismatchSkipsProcessDelete(t *testing.T) {
	// matcher.Matches returns false → resource is not in our
	// filter set → MUST early-return WITHOUT calling processDelete.
	// Without this guard, processDelete would attempt to remove
	// keys for a resource we never indexed; store.Delete would
	// surface "key not found" warnings on every irrelevant delete
	// (which can be many in a busy cluster — every Service delete
	// in a namespace we don't watch, for example).
	w := minimalWatcher(slog.Default(), &stubMatcher{matches: false})
	resource := newWatcherDeleteResource("filtered-out-svc")

	require.NotPanics(t, func() {
		w.handleDelete(resource)
	}, "field-selector mismatch MUST take the skip-and-log branch — "+
		"without it processDelete is reached with nil indexer/store/"+
		"debouncer and crashes the watcher goroutine; in production it "+
		"would surface as 'key not found' warnings on every irrelevant "+
		"delete from outside the field-selector scope")
}

func TestWatcher_HandleDelete_NilDirectAndNilTombstoneIsNoOp(t *testing.T) {
	// nil obj — neither direct nor tombstone conversion succeeds.
	// The function MUST take the inner `if resource == nil` early
	// return.
	w := minimalWatcher(slog.Default(), nil)

	require.NotPanics(t, func() {
		w.handleDelete(nil)
	}, "nil obj input MUST early-return — without the inner nil check "+
		"the function would proceed past the conversion and crash "+
		"processDelete on the nil resource pointer")
}

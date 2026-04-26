// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// SingleWatcher.handleDelete has FOUR observable branches; coverage
// was 58.3%. The most load-bearing is the DeletedFinalStateUnknown
// (tombstone) branch, which K8s informers emit when the watcher
// missed a delete event mid-stream. Without that branch the
// callback would never fire for those deletes — leaving stale
// state inside the consumer (e.g. a configloader holding a
// reference to a Secret that was deleted while the watcher was
// disconnected).
//
// Three contracts pinned:
//
//  1. Direct *unstructured.Unstructured input → OnChange invoked
//     with that resource (after sync is complete).
//
//  2. DeletedFinalStateUnknown wrapping a real
//     *unstructured.Unstructured → tombstone unwrapped, OnChange
//     invoked with the underlying resource. This is the K8s-
//     informer-specific edge case: the watcher reconnected after
//     missing one or more events, and the informer hands us the
//     last known state via the tombstone wrapper.
//
//  3. Both direct conversion AND tombstone conversion fail (e.g.
//     tombstone wrapping a string or other non-resource) → log
//     warn, return WITHOUT invoking OnChange. A regression that
//     called OnChange with nil here would nil-deref every consumer
//     during informer recovery from a network blip.

// deleteTestWatcher constructs a SingleWatcher that records every
// OnChange invocation and reports as already-synced (so the
// post-sync delete branch fires).
func deleteTestWatcher(t *testing.T) (watcher *SingleWatcher, seenNames *[]string) {
	t.Helper()
	var seen []string
	w := &SingleWatcher{
		config: types.SingleWatcherConfig{
			OnChange: func(obj any) error {
				if r, ok := obj.(*unstructured.Unstructured); ok {
					seen = append(seen, r.GetName())
				}
				return nil
			},
		},
	}
	w.synced.Store(true) // disable skipDuringSync gate
	return w, &seen
}

func newDeleteResource(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       "haptic",
				"resourceVersion": "100",
			},
		},
	}
}

func TestSingleWatcher_HandleDelete_DirectResourceInvokesOnChange(t *testing.T) {
	w, seen := deleteTestWatcher(t)
	resource := newDeleteResource("direct-delete")

	w.handleDelete(resource)

	assert.Equal(t, []string{"direct-delete"}, *seen,
		"direct *unstructured.Unstructured delete MUST invoke OnChange "+
			"with the same resource — this is the normal post-sync delete path")
}

func TestSingleWatcher_HandleDelete_TombstoneWrappingResourceUnwrapsAndFires(t *testing.T) {
	// DeletedFinalStateUnknown is the K8s informer's mechanism for
	// signaling deletes that happened while the watcher was
	// disconnected. Without unwrapping the tombstone, the consumer
	// would never learn the resource was deleted and would hold
	// stale state indefinitely.
	w, seen := deleteTestWatcher(t)
	resource := newDeleteResource("tombstoned-delete")
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "haptic/tombstoned-delete",
		Obj: resource,
	}

	w.handleDelete(tombstone)

	assert.Equal(t, []string{"tombstoned-delete"}, *seen,
		"DeletedFinalStateUnknown wrapping *unstructured.Unstructured MUST "+
			"unwrap and invoke OnChange — without this branch the consumer "+
			"never learns about deletes that happened while the watcher was "+
			"disconnected (e.g. brief API server outage), holding stale "+
			"state indefinitely")
}

func TestSingleWatcher_HandleDelete_TombstoneWrappingNonResourceIsNoOp(t *testing.T) {
	// Tombstone wrapping something that's not an *unstructured
	// (defensive — should never happen in practice, but the
	// inner-conversion-also-fails branch must NOT call OnChange
	// with nil; that would crash every consumer).
	w, seen := deleteTestWatcher(t)
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "haptic/garbage-tombstone",
		Obj: "not-an-unstructured-resource",
	}

	w.handleDelete(tombstone)

	assert.Empty(t, *seen,
		"tombstone wrapping non-Unstructured MUST NOT invoke OnChange — "+
			"a regression that fell through with nil resource would nil-"+
			"deref every consumer during informer recovery from a network "+
			"blip; the warn-and-return branch is the safety latch")
}

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// convertToUnstructured is a 3-branch dispatch:
//
//	switch v := obj.(type) {
//	case *unstructured.Unstructured: return v
//	case runtime.Object:
//	    u, ok := v.(*unstructured.Unstructured); if ok { return u }
//	}
//	return nil
//
// The existing tests cover branch 1 (Unstructured input) and the
// last fall-through (nil / non-runtime input). Branch 2 — a TYPED
// runtime.Object that is NOT *unstructured.Unstructured — is
// uncovered.
//
// Branch 2 matters because of the watcher's contract with informers:
// the cache only ever delivers *unstructured.Unstructured objects
// because the watcher is built on a dynamic client. But if a future
// refactor wires up a TYPED informer (say, by mistakenly using
// kubernetes.Interface for one resource type), the informer would
// start delivering typed runtime.Objects (corev1.Pod, networkingv1.
// Ingress, etc.) into this code path. The defensive return-nil
// behaviour here is what stops those wrong-type events from being
// silently treated as Unstructured and crashing downstream code that
// calls .GetName() / .GetNamespace() / .UnstructuredContent() etc.
//
// A regression that swapped the inner assertion (e.g. always
// returned `v` instead of checking `ok`) would silently produce a
// non-nil *unstructured.Unstructured from a typed object — the
// nil-pointer case — and the next .GetName() call would crash the
// watcher goroutine.
//
// Pin the contract: a typed runtime.Object input MUST yield nil.
func TestWatcher_ConvertToUnstructured_TypedRuntimeObjectReturnsNil(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	w, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	// corev1.Pod is a runtime.Object but NOT *unstructured.Unstructured.
	// This is exactly the input shape that branch 2 was written to defend
	// against: a typed object reaching a code path that expects only
	// unstructured ones.
	pod := &corev1.Pod{}
	got := w.convertToUnstructured(pod)
	assert.Nil(t, got,
		"convertToUnstructured MUST return nil for a typed runtime.Object — "+
			"a regression that returned a non-nil *unstructured.Unstructured "+
			"from a typed input would feed the next handler a zero-value "+
			"Unstructured pointer that would nil-deref on the first "+
			".GetName() / .GetNamespace() call and crash the watcher goroutine")
}

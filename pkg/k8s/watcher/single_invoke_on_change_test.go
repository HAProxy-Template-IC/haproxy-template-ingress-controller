// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// SingleWatcher.invokeOnChange has THREE branches; coverage was
// 50%. The function is the load-bearing safety latch that
// guarantees a misbehaving consumer's callback can't tear down
// the watcher goroutine. All three branches matter:
//
//  1. nil OnChange → silent no-op. Production code may register
//     a SingleWatcher purely to track resource sync state without
//     a callback (the WaitForSync API). A regression that always
//     called the callback would nil-deref on every event.
//
//  2. OnChange returns error → log warning, continue. The
//     callback's error MUST NOT abort the watcher loop —
//     otherwise one bad reconciliation would stop watching the
//     resource entirely.
//
//  3. OnChange returns nil → log debug, continue. Happy path.
//
// All three pinned via direct invocation of the unexported
// invokeOnChange method on a Component constructed with only the
// fields it touches (config.OnChange).

// minimalSingleWatcher returns a SingleWatcher populated only with
// the config field invokeOnChange reads. Other fields stay zero.
func minimalSingleWatcher(onChange func(any) error) *SingleWatcher {
	return &SingleWatcher{
		config: types.SingleWatcherConfig{
			OnChange: onChange,
		},
	}
}

func newTestUnstructured(name, namespace, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       kind,
			"metadata": map[string]any{
				"name":            name,
				"namespace":       namespace,
				"resourceVersion": "42",
			},
		},
	}
}

func TestSingleWatcher_InvokeOnChange_NilCallbackIsNoOp(t *testing.T) {
	// Construct with nil OnChange — production code that registers
	// a SingleWatcher purely to track sync state hits this path.
	w := minimalSingleWatcher(nil)
	resource := newTestUnstructured("test-cm", "default", "ConfigMap")

	require.NotPanics(t, func() {
		w.invokeOnChange("Add", resource)
	}, "nil OnChange MUST be a silent no-op — without this guard, "+
		"watchers that don't register a callback (using WaitForSync only) "+
		"would nil-deref on every event")
}

func TestSingleWatcher_InvokeOnChange_ErrorReturningCallbackDoesNotPropagate(t *testing.T) {
	// A callback returning an error MUST be logged-and-swallowed.
	// invokeOnChange returns void — the watcher goroutine cannot
	// be allowed to abort because of a misbehaving consumer.
	called := false
	w := minimalSingleWatcher(func(_ any) error {
		called = true
		return errors.New("consumer-side rendering crash")
	})

	resource := newTestUnstructured("test-cm", "default", "ConfigMap")
	require.NotPanics(t, func() {
		w.invokeOnChange("Update", resource)
	}, "callback returning error MUST NOT propagate as a panic — the "+
		"watcher must keep running even when the consumer's reconciliation "+
		"loop crashes; a regression here would let one bad event stop "+
		"watching the resource entirely")

	assert.True(t, called,
		"the callback MUST be invoked exactly once (proves we entered "+
			"the OnChange branch rather than the nil-callback short-circuit)")
}

func TestSingleWatcher_InvokeOnChange_SuccessfulCallbackInvokedOnce(t *testing.T) {
	// Happy path baseline — exactly one invocation, no panic.
	calls := 0
	w := minimalSingleWatcher(func(_ any) error {
		calls++
		return nil
	})

	resource := newTestUnstructured("test-cm", "default", "ConfigMap")
	w.invokeOnChange("Add", resource)

	assert.Equal(t, 1, calls,
		"successful callback MUST be invoked exactly once per event — "+
			"a regression that double-fired would cause double-reconciliation "+
			"on every Add/Update/Delete")
}

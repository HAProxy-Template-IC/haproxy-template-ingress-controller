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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// shouldSkipUpdate is the resync filter that protects the controller
// from re-processing every resource on every informer relist (which
// happens roughly every InformerResyncPeriod = 30s). The contract
// has FIVE branches that all matter:
//
//  1. oldResource == nil → false (process). The Update handler may
//     receive a nil oldResource for the very first event after a
//     restart; treating it as a resync would silently drop genuine
//     work.
//  2. oldVersion empty AND newVersion empty → false (process). With
//     no version info to compare, we err on the side of processing.
//  3. oldVersion empty, newVersion non-empty → false (can't compare).
//  4. oldVersion non-empty, newVersion empty → false (can't compare).
//  5. Both versions non-empty AND equal → true (SKIP — resync).
//  6. Both versions non-empty AND differ → false (process — real update).
//
// The boolean conjunction `oldVersion != "" && newVersion != "" &&
// oldVersion == newVersion` is load-bearing. A regression that
// dropped the empty checks would falsely skip events where one side
// happened to have an empty version (a real bug pattern when objects
// are received from cache reconstructions).
//
// The TestWatcher_HandleUpdate_SkipsResyncEvent integration test
// exercises only branch 5. Pin the rest with direct unit tests.

func TestShouldSkipUpdate_DispatchTable(t *testing.T) {
	// The watcher only uses w.logger and w.config.GVR for logging
	// inside shouldSkipUpdate, so a real Watcher built via New() is
	// the simplest fixture. validWatcherConfig + newTestClient give
	// us one without exercising any actual Kubernetes round-trips.
	k8sClient := newTestClient(t)
	// Pass slog.Default() (NOT nil) — the watcher's New() does not
	// substitute a default logger, and shouldSkipUpdate logs at
	// Debug on the SKIP branch. A nil logger here would panic on
	// the SKIP test case.
	w, err := New(validWatcherConfig(), k8sClient, slog.Default())
	require.NoError(t, err)

	tests := []struct {
		name       string
		oldVersion string
		newVersion string
		oldNil     bool // if true, pass nil for oldResource
		wantSkip   bool
	}{
		{
			name:     "nil old → process (first event after restart)",
			oldNil:   true,
			wantSkip: false,
		},
		{
			name:       "both empty → process (no version info, err on safe side)",
			oldVersion: "",
			newVersion: "",
			wantSkip:   false,
		},
		{
			name:       "old empty, new set → process (can't compare)",
			oldVersion: "",
			newVersion: "v100",
			wantSkip:   false,
		},
		{
			name:       "old set, new empty → process (can't compare)",
			oldVersion: "v100",
			newVersion: "",
			wantSkip:   false,
		},
		{
			name:       "both set, equal → SKIP (resync event)",
			oldVersion: "v100",
			newVersion: "v100",
			wantSkip:   true,
		},
		{
			name:       "both set, differ → process (real update)",
			oldVersion: "v100",
			newVersion: "v101",
			wantSkip:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var oldRes *unstructured.Unstructured
			if !tt.oldNil {
				oldRes = makeUnstructuredVersion(tt.oldVersion)
			}
			newRes := makeUnstructuredVersion(tt.newVersion)

			got := w.shouldSkipUpdate(oldRes, newRes)
			assert.Equal(t, tt.wantSkip, got,
				"shouldSkipUpdate must return %v for %s — a regression that "+
					"flipped this would either silently drop real updates "+
					"(false → true regression) or cost the controller ~30s of "+
					"redundant work per resource on every informer relist "+
					"(true → false regression)",
				tt.wantSkip, tt.name)
		})
	}
}

// makeUnstructuredVersion builds a minimal *unstructured.Unstructured
// with just the resourceVersion and identifying fields shouldSkipUpdate
// reads (name + namespace, used only for log context).
func makeUnstructuredVersion(version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            "test-cm",
				"namespace":       "default",
				"resourceVersion": version,
			},
		},
	}
}

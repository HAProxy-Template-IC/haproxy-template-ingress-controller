// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package introspection

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registry has TWO public methods that the existing tests don't cover but
// are documented public API and have load-bearing behaviour:
//
//  1. GetWithField — composes Get + ExtractField. The contract has three
//     mutually-exclusive branches (path missing → error, empty field →
//     full value, non-empty field → JSONPath extraction). A regression in
//     the empty-field branch in particular would silently start running
//     ExtractField on every Get call, breaking simple `/debug/vars/<path>`
//     queries with malformed JSONPath errors.
//
//  2. Clear — documented as "used between controller iterations to prevent
//     stale references to components from previous iterations. The
//     registry can then be reused with new variables without restarting
//     the HTTP server." Three things must hold:
//       a. After Clear, Len() == 0 and Paths() is empty.
//       b. The registry remains USABLE — Publish + Get must work after
//          Clear (this is what makes "reuse without restarting HTTP
//          server" possible).
//       c. Concurrent Clear and Publish must not panic — between iterations
//          may be racy if startup overlaps shutdown.

func TestRegistry_GetWithField_DispatchTable(t *testing.T) {
	// Build a registry with one variable holding nested data so we can
	// exercise both the empty-field shortcut and the JSONPath path.
	reg := NewRegistry()
	reg.Publish("config", &mockVar{
		value: map[string]any{
			"version": "v1.2.3",
			"deep": map[string]any{
				"nested": "deep-value",
			},
		},
	})
	reg.Publish("faulty", &mockVar{err: errors.New("compute failed")})

	tests := []struct {
		name      string
		path      string
		field     string
		wantErr   bool
		wantValue any
		wantSubst string // if wantErr, must appear in error message
	}{
		{
			name: "empty field returns full value WITHOUT invoking JSONPath",
			path: "config", field: "",
			wantValue: map[string]any{
				"version": "v1.2.3",
				"deep":    map[string]any{"nested": "deep-value"},
			},
			// A regression that ran ExtractField with "" would either
			// re-roundtrip the value through JSON (changing types like
			// int → float64) or fail on the empty-expression edge case.
		},
		{
			name: "non-empty field applies JSONPath extraction",
			path: "config", field: "{.version}",
			wantValue: "v1.2.3",
		},
		{
			name: "deep JSONPath extraction works",
			path: "config", field: "{.deep.nested}",
			wantValue: "deep-value",
		},
		{
			name: "missing path → error from Get layer (no JSONPath attempt)",
			path: "missing", field: "{.version}",
			wantErr:   true,
			wantSubst: "not found",
		},
		{
			name: "Var.Get error propagates without JSONPath attempt",
			path: "faulty", field: "{.version}",
			wantErr:   true,
			wantSubst: "compute failed",
		},
		{
			name: "invalid JSONPath surfaces error AFTER successful Get",
			path: "config", field: "{.invalid[",
			wantErr:   true,
			wantSubst: "invalid jsonpath",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := reg.GetWithField(tt.path, tt.field)
			if tt.wantErr {
				require.Error(t, err,
					"GetWithField MUST surface errors from either layer "+
						"(missing path / Var.Get failure / invalid JSONPath)")
				assert.Contains(t, err.Error(), tt.wantSubst,
					"error message must contain the documented substring so "+
						"callers can disambiguate the failure source")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, got,
				"empty-field branch MUST return the full value verbatim — "+
					"a regression that re-routed it through ExtractField "+
					"would silently change types (int→float64) and break "+
					"every simple /debug/vars/<path> query")
		})
	}
}

func TestRegistry_Clear_EmptiesAndPreservesUsability(t *testing.T) {
	reg := NewRegistry()
	reg.Publish("var1", &mockVar{value: "v1"})
	reg.Publish("var2", &mockVar{value: "v2"})
	reg.Publish("nested/path", &mockVar{value: "v3"})

	require.Len(t, reg.Paths(), 3, "baseline: three variables registered")

	reg.Clear()

	// Contract a: registry is empty after Clear.
	assert.Empty(t, reg.Paths(),
		"Paths MUST return empty after Clear — pinning this protects the "+
			"/debug/vars index page from showing ghost entries")

	// Contract a (cont.): Get on a previously-published path must now fail.
	_, err := reg.Get("var1")
	assert.Error(t, err,
		"Get on a cleared path MUST return an error — silent nil-return "+
			"would let callers operate on stale half-initialized state")

	// Contract b: registry is REUSABLE after Clear. The doc says it
	// can be reused with new variables without restarting the HTTP server.
	reg.Publish("var1-fresh", &mockVar{value: "fresh"})

	got, err := reg.Get("var1-fresh")
	require.NoError(t, err,
		"Publish + Get must work after Clear — the documented purpose of "+
			"Clear is to reuse the registry between iterations without "+
			"restarting the HTTP server")
	assert.Equal(t, "fresh", got)
	assert.Len(t, reg.Paths(), 1,
		"after Clear + one Publish, exactly 1 path must be registered; a "+
			"regression that didn't reset the underlying map would leave a "+
			"partially-populated state behind")
}

func TestRegistry_Clear_ConcurrentWithPublishIsSafe(t *testing.T) {
	// Between-iteration shutdown and startup CAN overlap — the controller
	// reinit goroutine may Publish while a tear-down goroutine Clears.
	// The mutex inside the registry must serialize these. A regression
	// (e.g., dropping the lock in Clear) would race the map and panic.
	reg := NewRegistry()
	for i := range 10 {
		_ = i
		reg.Publish("seed", &mockVar{value: "x"})
	}

	var wg sync.WaitGroup
	for i := range 100 {
		idx := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			reg.Publish("racing", &mockVar{value: idx})
		}()
		go func() {
			defer wg.Done()
			reg.Clear()
		}()
	}
	wg.Wait()

	// We don't assert a specific final size — the race is intentional.
	// The point is no panic / no data race (caught by `go test -race` in
	// the make test harness).
	assert.NotPanics(t, func() {
		_ = reg.Paths()
	}, "registry must remain readable after concurrent Clear+Publish — "+
		"a regression that dropped the mutex in Clear would corrupt the map")
}

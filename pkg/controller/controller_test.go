// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"encoding/base64"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestListenerPortFromEnv(t *testing.T) {
	const envName = "HAPTIC_TEST_LISTENER_PORT"

	tests := []struct {
		name          string
		raw           string
		defaultPort   int
		allowDisabled bool
		want          int
		wantErr       string
	}{
		{name: "default", defaultPort: 9443, want: 9443},
		{name: "override", raw: "19443", defaultPort: 9443, want: 19443},
		{name: "disabled when allowed", raw: "0", defaultPort: 9090, allowDisabled: true, want: 0},
		{name: "zero rejected", raw: "0", defaultPort: 9443, wantErr: "between 1 and 65535"},
		{name: "negative rejected", raw: "-1", defaultPort: 9443, wantErr: "between 1 and 65535"},
		{name: "overflow rejected", raw: "65536", defaultPort: 9443, wantErr: "between 1 and 65535"},
		{name: "non-integer rejected", raw: "https", defaultPort: 9443, wantErr: "as a TCP port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envName, tt.raw)
			got, err := listenerPortFromEnv(envName, tt.defaultPort, tt.allowDisabled)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseSecret(t *testing.T) {
	tests := []struct {
		name      string
		resource  *unstructured.Unstructured
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid secret with credentials",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name":      "test-secret",
						"namespace": "default",
					},
					"data": map[string]any{
						"dataplane_username": base64.StdEncoding.EncodeToString([]byte("admin")),
						"dataplane_password": base64.StdEncoding.EncodeToString([]byte("secret123")),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "secret without data field",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
				},
			},
			wantErr:   true,
			errSubstr: "has no data field",
		},
		{
			name: "secret with invalid base64",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
					"data": map[string]any{
						"dataplane_username": "not-valid-base64!!!",
					},
				},
			},
			wantErr:   true,
			errSubstr: "decoding base64",
		},
		{
			name: "secret missing required credentials",
			resource: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]any{
						"name": "test-secret",
					},
					"data": map[string]any{
						"some_other_key": base64.StdEncoding.EncodeToString([]byte("value")),
					},
				},
			},
			wantErr:   true,
			errSubstr: "missing required secret key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := parseSecret(tt.resource)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, creds)
		})
	}
}

func TestStopLeaderOnlyComponents_NilComponents(t *testing.T) {
	logger := slog.Default()

	// Should not panic with nil components
	stopLeaderOnlyComponents(nil, logger)
}

func TestStopLeaderOnlyComponents_NilCancel(t *testing.T) {
	logger := slog.Default()

	// Should not panic with nil cancel
	components := &leaderOnlyComponents{
		cancel: nil,
	}
	stopLeaderOnlyComponents(components, logger)
}

func TestLeaderCallbackState_ConcurrentAccess(t *testing.T) {
	// Test that leaderCallbackState is thread-safe
	state := &leaderCallbackState{}

	// Simulate concurrent reads and writes
	done := make(chan bool)

	go func() {
		for range 100 {
			state.mu.Lock()
			state.components = &leaderOnlyComponents{}
			state.mu.Unlock()
		}
		done <- true
	}()

	go func() {
		for range 100 {
			state.mu.Lock()
			_ = state.components
			state.mu.Unlock()
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done
}

// TestComponentSetup_CleanupOrderAndIdempotence locks the contract
// that AddCleanup-registered callbacks fire in reverse-registration
// (LIFO) order — mirrors `defer` semantics so callers can layer
// dependencies cleanly — and that RunCleanups is idempotent (so
// teardown paths can call it without worrying about double-fire).
//
// Regression: the pluggable-validator manager registers Close() via
// AddCleanup so its connection pools get drained on teardown. If
// RunCleanups silently no-ops or fires in the wrong order, file
// descriptors leak across iteration restarts.
func TestComponentSetup_CleanupOrderAndIdempotence(t *testing.T) {
	setup := &componentSetup{}
	var calls []string
	setup.AddCleanup(func() { calls = append(calls, "first") })
	setup.AddCleanup(func() { calls = append(calls, "second") })
	setup.AddCleanup(func() { calls = append(calls, "third") })

	setup.RunCleanups()
	if len(calls) != 3 || calls[0] != "third" || calls[1] != "second" || calls[2] != "first" {
		t.Fatalf("cleanups fired in wrong order: %v (want LIFO: third, second, first)", calls)
	}

	// Second call must be a no-op — already drained.
	setup.RunCleanups()
	if len(calls) != 3 {
		t.Fatalf("RunCleanups not idempotent; second call re-fired callbacks: %v", calls)
	}
}

// TestComponentSetup_AddCleanupNilSafe locks that AddCleanup ignores
// nil callbacks. Callers don't have to nil-check before registering.
func TestComponentSetup_AddCleanupNilSafe(t *testing.T) {
	setup := &componentSetup{}
	setup.AddCleanup(nil)
	setup.RunCleanups() // must not panic
}

// A reinit re-runs the load gate under ReinitGraceWindow, not the startup
// probe: a grace shorter than the gate's budget lets liveness restart an
// advancing controller.
func TestReinitGraceWindowCoversLoadGate(t *testing.T) {
	if ReinitGraceWindow <= initialValidationTestsRunTimeout {
		t.Fatalf("ReinitGraceWindow (%s) must exceed initialValidationTestsRunTimeout (%s)",
			ReinitGraceWindow, initialValidationTestsRunTimeout)
	}
}

// A reinit swaps the metrics registry, so the counter is born at zero on every
// rebuild. It must therefore publish the running total: incrementing by one
// makes every sample read 1, and any two samples differ by nothing.
func TestRecordReinitializationsSurvivesRegistrySwap(t *testing.T) {
	read := func(m *metrics.Metrics) float64 {
		var out dto.Metric
		if err := m.ControllerReinitializationsTotal.(prometheus.Metric).Write(&out); err != nil {
			t.Fatalf("read counter: %v", err)
		}
		return out.GetCounter().GetValue()
	}

	// Each iteration gets a fresh registry, exactly as a reinit does.
	first := metrics.NewMetrics(prometheus.NewRegistry())
	recordReinitializations(first, 2)
	if got := read(first); got != 1 {
		t.Fatalf("after the first rebuild: got %v, want 1", got)
	}

	second := metrics.NewMetrics(prometheus.NewRegistry())
	recordReinitializations(second, 4)
	if got := read(second); got != 3 {
		t.Fatalf("after the third rebuild: got %v, want 3 (a delta of 2 from the previous sample)", got)
	}

	// The very first iteration is not a rebuild.
	initial := metrics.NewMetrics(prometheus.NewRegistry())
	recordReinitializations(initial, 1)
	if got := read(initial); got != 0 {
		t.Fatalf("first iteration: got %v, want 0", got)
	}
}

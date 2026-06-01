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

package dataplane

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// int64Ptr is a tiny helper that gives every test a non-stuttering
// literal-pointer constructor. The alternative — a local variable per
// pointer — bloats each test fixture by several lines.
func int64Ptr(v int64) *int64 { return &v }

// TestBuildRuntimeActions_FieldDeltas pins the central contract:
//
// `buildRuntimeActions` is a delta function. Each row holds a (current,
// desired) pair, the expected emitted actions, and a description of what
// the row is exercising. A "no-op" row (current == desired) must emit
// nothing — that's the gate keeping the chart's 30-slot backends from
// spamming `SetServerAddr` on metadata-only re-renders.
func TestBuildRuntimeActions_FieldDeltas(t *testing.T) {
	const backend = "mybackend"
	const server = "SRV_1"

	tests := []struct {
		name    string
		current *models.Server
		desired *models.Server
		want    string
	}{
		{
			name:    "no change: empty action set",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			want:    "",
		},
		{
			name:    "address changed: only SetServerAddr",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.2", Port: int64Ptr(8080)},
			want:    "SetServerAddr mybackend SRV_1 10.0.0.2 8080",
		},
		{
			name:    "port changed: only SetServerAddr",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(9090)},
			want:    "SetServerAddr mybackend SRV_1 10.0.0.1 9090",
		},
		{
			name: "weight changed: only SetServerWeight",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{Weight: int64Ptr(10)}},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{Weight: int64Ptr(50)}},
			want: "SetServerWeight mybackend SRV_1 50",
		},
		{
			name:    "weight added from unset: SetServerWeight",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{Weight: int64Ptr(50)}},
			want: "SetServerWeight mybackend SRV_1 50",
		},
		{
			name:    "health check port changed: only SetServerCheckPort",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{HealthCheckPort: int64Ptr(8888)}},
			want: "SetServerCheckPort mybackend SRV_1 8888",
		},
		{
			name:    "agent-check enable: EnableAgentCheck only",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{AgentCheck: "enabled"}},
			want: "EnableAgentCheck mybackend SRV_1",
		},
		{
			name: "agent-check disable: DisableAgentCheck only",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{AgentCheck: "enabled"}},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{AgentCheck: "disabled"}},
			want: "DisableAgentCheck mybackend SRV_1",
		},
		{
			name:    "agent address set: SetServerAgentAddr only",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{AgentAddr: "10.0.0.99"}},
			want: "SetServerAgentAddr mybackend SRV_1 10.0.0.99",
		},
		{
			name:    "agent send set: SetServerAgentSend only",
			current: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)},
			desired: &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{AgentSend: "ping"}},
			want: "SetServerAgentSend mybackend SRV_1 ping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := sections.NewServerUpdate(backend, tt.current, tt.desired)
			got := buildRuntimeActions([]comparator.Operation{op})
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBuildRuntimeActions_MaintenanceOrdering pins the load-bearing
// ordering invariant: a server going INTO maint emits `SetServerState
// maint` FIRST so the live worker drains BEFORE its destination changes;
// a server coming OUT of maint emits other changes first then
// `SetServerState ready` LAST so it's fully reconfigured before traffic
// resumes. Getting this wrong (the pre-delta-fix order) sends in-flight
// requests to the unreachable reserved-slot address (127.0.0.1:1) for
// the few µs between the addr command and the state command.
func TestBuildRuntimeActions_MaintenanceOrdering(t *testing.T) {
	const backend = "mybackend"
	const server = "SRV_1"

	t.Run("entering maint: state action goes FIRST", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.5", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "disabled"}}
		desired := &models.Server{Name: server, Address: "127.0.0.1", Port: int64Ptr(1),
			ServerParams: models.ServerParams{Maintenance: "enabled"}}

		op := sections.NewServerUpdate(backend, current, desired)
		got := buildRuntimeActions([]comparator.Operation{op})

		assert.Equal(t,
			"SetServerState mybackend SRV_1 maint;SetServerAddr mybackend SRV_1 127.0.0.1 1",
			got,
			"drain must complete BEFORE the slot is repointed at the unreachable address")
	})

	t.Run("leaving maint: state action goes LAST", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "127.0.0.1", Port: int64Ptr(1),
			ServerParams: models.ServerParams{Maintenance: "enabled"}}
		desired := &models.Server{Name: server, Address: "10.0.0.5", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "disabled"}}

		op := sections.NewServerUpdate(backend, current, desired)
		got := buildRuntimeActions([]comparator.Operation{op})

		assert.Equal(t,
			"SetServerAddr mybackend SRV_1 10.0.0.5 8080;SetServerState mybackend SRV_1 ready",
			got,
			"slot must be fully reconfigured BEFORE the worker takes it out of maint")
	})

	t.Run("maint state unchanged (still enabled): no SetServerState emitted", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "disabled"}}
		desired := &models.Server{Name: server, Address: "10.0.0.2", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "disabled"}}

		op := sections.NewServerUpdate(backend, current, desired)
		got := buildRuntimeActions([]comparator.Operation{op})

		assert.Equal(t, "SetServerAddr mybackend SRV_1 10.0.0.2 8080", got,
			"same Maintenance value must NOT emit a redundant SetServerState")
	})

	t.Run("entering maint without addr change: only state action", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "disabled"}}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Maintenance: "enabled"}}

		op := sections.NewServerUpdate(backend, current, desired)
		got := buildRuntimeActions([]comparator.Operation{op})

		assert.Equal(t, "SetServerState mybackend SRV_1 maint", got)
	})
}

// TestBuildRuntimeActions_MultiFieldDelta_LeavingMaint pins the full
// scale-up flow: every field of a reserved slot flips to its active
// values at once. Setup actions emit first in stable order (addr,
// weight, check-port, agent-check, agent-addr, agent-send) and the
// state action lands last because we're leaving maint.
func TestBuildRuntimeActions_MultiFieldDelta_LeavingMaint(t *testing.T) {
	const backend = "mybackend"
	const server = "SRV_1"

	current := &models.Server{Name: server, Address: "127.0.0.1", Port: int64Ptr(1),
		ServerParams: models.ServerParams{Maintenance: "enabled"}}
	desired := &models.Server{Name: server, Address: "10.0.0.2", Port: int64Ptr(9090),
		ServerParams: models.ServerParams{
			Maintenance:     "disabled",
			Weight:          int64Ptr(50),
			HealthCheckPort: int64Ptr(8888),
			AgentCheck:      "enabled",
			AgentAddr:       "10.0.0.2",
			AgentSend:       "ping",
		}}

	op := sections.NewServerUpdate(backend, current, desired)
	got := buildRuntimeActions([]comparator.Operation{op})

	assert.Equal(t,
		"SetServerAddr mybackend SRV_1 10.0.0.2 9090"+
			";SetServerWeight mybackend SRV_1 50"+
			";SetServerCheckPort mybackend SRV_1 8888"+
			";EnableAgentCheck mybackend SRV_1"+
			";SetServerAgentAddr mybackend SRV_1 10.0.0.2"+
			";SetServerAgentSend mybackend SRV_1 ping"+
			";SetServerState mybackend SRV_1 ready",
		got)
}

// TestBuildRuntimeActions_MultipleOps verifies that per-server action
// groups stay contiguous in the emitted string. Two unrelated servers
// changing simultaneously must NOT interleave (a state-change for
// server A landing between two setup actions for server B would yield
// an indeterminate apply order on the worker).
func TestBuildRuntimeActions_MultipleOps(t *testing.T) {
	ops := []comparator.Operation{
		sections.NewServerUpdate("backend1",
			&models.Server{Name: "SRV_1", Address: "10.0.0.1", Port: int64Ptr(8080)},
			&models.Server{Name: "SRV_1", Address: "10.0.0.2", Port: int64Ptr(9090)}),
		sections.NewServerUpdate("backend2",
			&models.Server{Name: "SRV_2", Address: "192.168.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{Maintenance: "disabled"}},
			&models.Server{Name: "SRV_2", Address: "192.168.0.1", Port: int64Ptr(8080),
				ServerParams: models.ServerParams{Maintenance: "enabled"}}),
	}

	got := buildRuntimeActions(ops)
	assert.Equal(t,
		"SetServerAddr backend1 SRV_1 10.0.0.2 9090"+
			";SetServerState backend2 SRV_2 maint",
		got,
	)
}

// TestBuildRuntimeActions_SafetyGuards pins the guards that protect the
// dataplane's space/semicolon-tokenized parser. Without these, an
// AgentAddr like `unix@/var/run/foo bar.sock` would split into two
// arguments and produce a silently-malformed command, and an empty
// Address with a set Port would emit `SetServerAddr backend SRV  8080`
// (double space → empty IP arg).
func TestBuildRuntimeActions_SafetyGuards(t *testing.T) {
	const backend = "mybackend"
	const server = "SRV_1"

	t.Run("empty Address with set Port: skip SetServerAddr", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		desired := &models.Server{Name: server, Address: "", Port: int64Ptr(8080)}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"empty address would tokenize as `SetServerAddr backend SRV  8080` (double space) — refuse")
	})

	t.Run("AgentAddr containing a space: skip emission", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{AgentAddr: "unix@/var/run/foo bar.sock"}}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"space in AgentAddr would split the action into garbage tokens — refuse")
	})

	t.Run("AgentAddr containing a semicolon: skip emission", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{AgentAddr: "10.0.0.99;evil"}}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"semicolon in AgentAddr would inject a forged action into the next slot — refuse")
	})

	t.Run("AgentSend containing a space: skip emission", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{AgentSend: "hello world"}}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"space in AgentSend would split the action into garbage tokens — refuse")
	})
}

// TestBuildRuntimeActions_ClearedFields pins the deletion semantics:
// a field clearing (set → unset) has no representable runtime command,
// so we emit nothing for it. The skip_reload push already wrote the
// new (cleared) value to disk; the live worker keeps the old value
// until the next reload reconciles. Same-process correctness: the
// in-memory state diverges from disk for the lifetime of the worker.
// Acceptable because the next structural change forces a reload that
// flushes everything.
func TestBuildRuntimeActions_ClearedFields(t *testing.T) {
	const backend = "mybackend"
	const server = "SRV_1"

	t.Run("weight cleared (set → nil): no action emitted", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Weight: int64Ptr(50)}}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"no `set server weight none` exists — defer to reload")
	})

	t.Run("AgentAddr cleared (set → empty): no action emitted", func(t *testing.T) {
		current := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{AgentAddr: "10.0.0.99"}}
		desired := &models.Server{Name: server, Address: "10.0.0.1", Port: int64Ptr(8080)}
		op := sections.NewServerUpdate(backend, current, desired)
		assert.Equal(t, "", buildRuntimeActions([]comparator.Operation{op}),
			"no `set server agent-addr ''` semantics — defer to reload")
	})
}

func TestBuildRuntimeActions_NonServerOpSkipped(t *testing.T) {
	ops := []comparator.Operation{
		&mockOperation{
			opType:  sections.OperationUpdate,
			section: "backend",
			desc:    "Update backend 'api'",
		},
	}
	assert.Equal(t, "", buildRuntimeActions(ops))
}

func TestBuildRuntimeActions_Empty(t *testing.T) {
	assert.Equal(t, "", buildRuntimeActions(nil))
}

// TestPartitionByRuntimeEligibility verifies the partition helper separates
// runtime-eligible server updates from everything else, so applyChanges can
// route runtime ops through skip_reload+actions and reserve force_reload
// for structural ops.
func TestPartitionByRuntimeEligibility(t *testing.T) {
	runtimeOp := sections.NewServerUpdate("backend",
		&models.Server{Name: "SRV_1", Address: "10.0.0.1", Port: int64Ptr(8080)},
		&models.Server{Name: "SRV_1", Address: "10.0.0.2", Port: int64Ptr(9090)})
	reloadOp := sections.NewServerUpdate("backend",
		&models.Server{Name: "SRV_2", Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Check: "disabled"}},
		&models.Server{Name: "SRV_2", Address: "10.0.0.1", Port: int64Ptr(8080),
			ServerParams: models.ServerParams{Check: "enabled"}})
	createOp := &mockOperation{opType: sections.OperationCreate, section: "backend"}

	tests := []struct {
		name           string
		ops            []comparator.Operation
		wantRuntime    int
		wantStructural int
	}{
		{name: "empty input", ops: nil, wantRuntime: 0, wantStructural: 0},
		{
			name:           "all runtime-eligible",
			ops:            []comparator.Operation{runtimeOp, runtimeOp},
			wantRuntime:    2,
			wantStructural: 0,
		},
		{
			name:           "all structural (create + reload-required update)",
			ops:            []comparator.Operation{createOp, reloadOp},
			wantRuntime:    0,
			wantStructural: 2,
		},
		{
			name:           "mixed: runtime + structural interleaved",
			ops:            []comparator.Operation{runtimeOp, createOp, runtimeOp, reloadOp},
			wantRuntime:    2,
			wantStructural: 2,
		},
		{
			name:           "non-update server op (CREATE) goes to structural",
			ops:            []comparator.Operation{createOp},
			wantRuntime:    0,
			wantStructural: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime, structural := partitionByRuntimeEligibility(tt.ops)
			assert.Len(t, runtime, tt.wantRuntime, "runtime partition size")
			assert.Len(t, structural, tt.wantStructural, "structural partition size")
			assert.Equal(t, len(tt.ops), len(runtime)+len(structural),
				"partitions must cover every input op exactly once")
		})
	}
}

// TestApplyChanges_ServerOnlyUpdate_NoReload is the load-bearing
// contract test for the rolling-restart hot path:
//
// A diff whose only operation is a runtime-eligible server update
// (e.g. pod IP change during rolling restart) MUST result in:
//
//  1. Exactly ONE POST to /services/haproxy/configuration/raw,
//  2. with skip_reload=true,
//  3. carrying the X-Runtime-Actions header,
//  4. and NO POST with force_reload=true (or unset → default reload).
//
// If any of these break, HAProxy reloads for every pod IP change.
// During a rolling-restart that drops in-flight connections on
// every replacement and the test loop above this one (e2e rolling-
// restart) sees non-2xx/3xx responses. Lock the property in here
// so a regression is caught in the unit suite instead of waiting
// for the 2-minute e2e cycle.
func TestApplyChanges_ServerOnlyUpdate_NoReload(t *testing.T) {
	var (
		skipReloadPushCount  atomic.Int32
		forceReloadPushCount atomic.Int32
		anyOtherPushCount    atomic.Int32
		reloadEndpointHits   atomic.Int32
		seenRuntimeActions   atomic.Value // string
	)
	seenRuntimeActions.Store("")

	orch, cleanup := createTestOrchestratorWithParser(t, func(w http.ResponseWriter, r *http.Request) {
		if v3InfoResponse(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/services/haproxy/configuration/raw" && r.Method == http.MethodPost:
			q := r.URL.Query()
			switch {
			case q.Get("skip_reload") == "true":
				skipReloadPushCount.Add(1)
				seenRuntimeActions.Store(r.Header.Get("X-Runtime-Actions"))
				w.WriteHeader(http.StatusCreated) // dataplane returns 201 on skip_reload
			case q.Get("force_reload") == "true":
				forceReloadPushCount.Add(1)
				w.Header().Set("Reload-ID", "should-never-be-issued")
				w.WriteHeader(http.StatusAccepted)
			default:
				anyOtherPushCount.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}
		case r.URL.Path == "/services/haproxy/reloads" || r.URL.Path == "/services/haproxy/runtime/reload":
			// No call should ever land here — counting just in case.
			reloadEndpointHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}, &mockConfigParser{})
	defer cleanup()

	// Construct a runtime-eligible server update: address change only.
	// IsFullyRuntimeEligible must return true so partitionByRuntimeEligibility
	// puts this in the runtime partition (= no reload path).
	current := &models.Server{Name: "SRV_1", Address: "10.0.0.1", Port: int64Ptr(8080)}
	desired := &models.Server{Name: "SRV_1", Address: "10.0.0.2", Port: int64Ptr(8080)}
	op := sections.NewServerUpdate("api-backend", current, desired)

	// Sanity-check the runtime-eligibility classification — if this flips
	// the whole assumption of the test (and the runtime-only path) is gone.
	serverOp, ok := op.(*sections.ServerUpdateOp)
	require.True(t, ok)
	require.True(t, serverOp.IsFullyRuntimeEligible(),
		"address-only delta MUST be classified runtime-eligible — that's the precondition for skip_reload")

	diff := &comparator.ConfigDiff{
		Operations: []comparator.Operation{op},
		Summary:    comparator.DiffSummary{TotalUpdates: 1},
	}
	auxDiffs := &auxiliaryFileDiffs{hasChanges: true} // hasChanges only — no individual aux file diffs

	opts := &SyncOptions{
		VerifyReload:              false, // No reload to verify; ensures the test doesn't hang on a poll
		ReloadVerificationTimeout: 5 * time.Second,
	}

	result, err := orch.applyChanges(
		context.Background(),
		"global\n  daemon\n",
		diff,
		auxDiffs,
		opts,
		42, // preCachedVersion: bypass GetVersion HTTP call
		time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)

	// The four invariants that lock the no-reload contract:
	assert.Equal(t, int32(1), skipReloadPushCount.Load(),
		"exactly one skip_reload push expected (runtime-only path)")
	assert.Equal(t, int32(0), forceReloadPushCount.Load(),
		"NO force_reload push allowed — that would reload HAProxy and drop connections")
	assert.Equal(t, int32(0), anyOtherPushCount.Load(),
		"NO push without skip_reload or force_reload — the default raw push triggers a reload too")
	assert.Equal(t, int32(0), reloadEndpointHits.Load(),
		"NO direct hit on a reload endpoint")

	assert.Equal(t,
		"SetServerAddr api-backend SRV_1 10.0.0.2 8080",
		seenRuntimeActions.Load().(string),
		"X-Runtime-Actions must carry the address-change command")

	// And the result must reflect "no reload":
	assert.False(t, result.ReloadTriggered, "ReloadTriggered must be false")
	assert.Equal(t, SyncModeRuntime, result.SyncMode)
	assert.Empty(t, result.ReloadID, "no reload was triggered, so no reload ID")
	assert.True(t, result.Success)
}

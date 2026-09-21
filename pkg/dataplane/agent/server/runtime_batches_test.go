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

package server_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

func TestRuntimeBatchesDeleteAMapWithoutPartialReadBack(t *testing.T) {
	h, list, next := largeMapDeletion(t)
	before := h.state(false)
	var verified atomic.Bool
	h.model.With(func(m *haproxytest.Model) {
		m.Reject = func(command string) (string, bool) {
			if strings.HasPrefix(command, "show map maps/host.map") {
				verified.Store(true)
			}
			return "", false
		}
	})
	result := h.apply(&next, list)
	require.True(t, result.OK, "%+v", result.Error)
	require.Equal(t, api.ResultRuntime, result.Mode)
	assert.Len(t, result.OpResults, api.MaxOpsPerApply+1)
	assert.Empty(t, h.model.MapEntries("maps/host.map"))
	require.Eventually(t, verified.Load, 10*time.Second, 10*time.Millisecond)
	after := h.state(false)
	assert.Equal(t, before.Generation+1, after.Generation)
	assert.True(t, before.HAProxy.SameWorker(after.HAProxy))
	assert.Zero(t, h.metric("haptic_agent_map_divergence_total"))
	assert.Zero(t, h.metric("haptic_agent_op_errors_total"))
}

func TestRuntimeBatchesValidateTheFinalBatchBeforeExecutingTheFirst(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	h.model.With(func(m *haproxytest.Model) {
		m.Maps["maps/host.map"] = []haproxytest.MapEntry{{Key: "example.com", Value: "be-a"}}
	})
	list := baseFiles("global\n")
	next := buildManifest("invalid-last-batch", list)
	next.ExpectedPrevPlanID = first.AppliedPlanID
	next.ExpectedPrevToken = first.AppliedToken
	next.Ops = []api.Op{{Kind: api.OpMapDel, Path: "maps/host.map", Key: "example.com"}}
	next.OpBatches = [][]api.Op{{{Kind: api.OpMapDel, Path: "maps/host.map", Key: "invalid\nkey"}}}
	result := h.apply(&next, list)
	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
	for _, command := range h.model.Sent() {
		assert.NotContains(t, command, "del map")
	}
	assert.NotEmpty(t, h.model.MapEntries("maps/host.map"))
}

func TestRejectedFinalBatchClearsThePartialWorkerBaseline(t *testing.T) {
	h, list, next := largeMapDeletion(t, withReloadInterval(time.Minute))
	h.model.With(func(m *haproxytest.Model) {
		m.Reject = func(command string) (string, bool) {
			return "Key not found.", command == fmt.Sprintf("del map maps/host.map key-%d", api.MaxOpsPerApply)
		}
	})
	result := h.apply(&next, list)
	require.True(t, result.OK, "%+v", result.Error)
	require.Equal(t, api.ResultScheduled, result.Mode)
	assert.Len(t, h.model.MapEntries("maps/host.map"), 1)
	state := h.state(false)
	assert.Empty(t, state.WorkerOpsPlanID)
	assert.Empty(t, state.WorkerOpsPlanProof)
	assert.Equal(t, float64(1), h.metric("haptic_agent_op_errors_total"))
}

func largeMapDeletion(t *testing.T, opts ...func(*options)) (*harness, []file, api.Manifest) {
	t.Helper()
	h := newHarness(t, opts...)
	list := baseFiles("global\n")
	var body strings.Builder
	ops := make([]api.Op, api.MaxOpsPerApply+1)
	for i := range ops {
		key := fmt.Sprintf("key-%d", i)
		fmt.Fprintf(&body, "%s be-a\n", key)
		ops[i] = api.Op{Kind: api.OpMapDel, Path: "maps/host.map", Key: key}
	}
	list[1].Content = body.String()
	initial := buildManifest("full-map", list)
	initial.Mode = api.ModeReload
	first := h.apply(&initial, list)
	require.True(t, first.OK)
	h.model.With(func(m *haproxytest.Model) {
		entries := make([]haproxytest.MapEntry, len(ops))
		for i := range ops {
			entries[i] = haproxytest.MapEntry{Key: ops[i].Key, Value: "be-a"}
		}
		m.Maps["maps/host.map"] = entries
	})
	require.Len(t, h.model.MapEntries("maps/host.map"), len(ops))
	list[1].Content = ""
	next := buildManifest("empty-map", list)
	next.ExpectedPrevPlanID = first.AppliedPlanID
	next.ExpectedPrevToken = first.AppliedToken
	next.Ops = ops[:api.MaxOpsPerApply]
	next.OpBatches = [][]api.Op{ops[api.MaxOpsPerApply:]}
	return h, list, next
}

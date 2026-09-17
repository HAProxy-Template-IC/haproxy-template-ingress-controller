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

package resourceapplier

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func newTestCompWithReapplyInterval(t *testing.T, d time.Duration) (*Component, *atomic.Int32) {
	t.Helper()
	client, counter := newClientWithPatchCounter()
	comp := New(&Config{
		EventBus:        testutil.NewTestBus(),
		DynamicClient:   client,
		GVRResolver:     newResolver(),
		Logger:          testutil.NewTestLogger(),
		OwnNamespace:    "haptic",
		ReapplyInterval: d,
	})
	setLeader(comp)
	return comp, counter
}

func TestApplyAndPrune_SkipsIdenticalCycleWithinReapplyInterval(t *testing.T) {
	comp, counter := newTestCompWithReapplyInterval(t, time.Hour)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)})
	comp.handleReconciliationCompleted(context.Background(), evt)
	require.Equal(t, int32(1), counter.Load())

	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(1), counter.Load(),
		"an identical cycle inside the reapply interval must not SSA the live target again")
}

func TestApplyAndPrune_ReappliesAfterReapplyInterval(t *testing.T) {
	comp, counter := newTestCompWithReapplyInterval(t, time.Nanosecond)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)})
	comp.handleReconciliationCompleted(context.Background(), evt)
	require.Equal(t, int32(1), counter.Load())

	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(2), counter.Load(),
		"past the interval the SSA is the authoritative live-state verification")
}

func TestApplyAndPrune_SkipKeepsABAVerification(t *testing.T) {
	comp, counter := newTestCompWithReapplyInterval(t, time.Hour)
	fixture := newResourceCycleFixture(t)
	a1 := fixture.completed(t, "config-a", []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}, nil, nil)
	b := fixture.completed(t, "config-b", []templating.RenderedResource{sampleResource("haptic", "svc-a", 81)}, nil, eventCycleSnapshot(t, a1))
	a2 := fixture.completed(t, "config-a", []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}, nil, eventCycleSnapshot(t, b))

	comp.handleReconciliationCompleted(context.Background(), a1)
	comp.handleReconciliationCompleted(context.Background(), b)
	comp.handleReconciliationCompleted(context.Background(), a2)
	assert.Equal(t, int32(3), counter.Load(),
		"only identical CONSECUTIVE cycles skip; A-B-A still verifies all three states")
}

func TestApplyAndPrune_ErrorClearsReapplySkipState(t *testing.T) {
	comp, counter := newTestCompWithReapplyInterval(t, time.Hour)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)})
	comp.handleReconciliationCompleted(context.Background(), evt)
	require.Equal(t, int32(1), counter.Load())

	failing := reconciliationCompletedEvent(t, []templating.RenderedResource{unresolvableResource("haptic", "svc-b")})
	comp.handleReconciliationCompleted(context.Background(), failing)

	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(2), counter.Load(),
		"after a failed cycle the live state is unknown; the next cycle must apply")
}

// unresolvableResource renders a kind the test resolver cannot map, so
// applyAndPrune fails without any SSA reaching the fake client.
func unresolvableResource(ns, name string) templating.RenderedResource {
	return templating.RenderedResource{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  ns,
		Name:       name,
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": name, "namespace": ns},
		},
	}
}

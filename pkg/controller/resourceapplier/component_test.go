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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var serviceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

type mockResolver struct {
	results map[string]schema.GroupVersionResource
}

func (m *mockResolver) Resolve(apiVersion, kind string) (schema.GroupVersionResource, error) {
	key := apiVersion + "/" + kind
	if gvr, ok := m.results[key]; ok {
		return gvr, nil
	}
	return schema.GroupVersionResource{}, fmt.Errorf("unknown kind %s/%s", apiVersion, kind)
}

func newResolver() *mockResolver {
	return &mockResolver{results: map[string]schema.GroupVersionResource{
		"v1/Service": serviceGVR,
	}}
}

func newClientWithPatchCounter() (*dynamicfake.FakeDynamicClient, *atomic.Int32) {
	scheme := runtime.NewScheme()
	c := dynamicfake.NewSimpleDynamicClient(scheme)
	patchCount := &atomic.Int32{}
	c.PrependReactor("patch", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		patchCount.Add(1)
		return true, nil, nil
	})
	c.PrependReactor("delete", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	return c, patchCount
}

func newTestComp(t *testing.T, restrict bool) (*Component, *busevents.EventBus, *atomic.Int32) {
	t.Helper()
	bus := testutil.NewTestBus()
	client, counter := newClientWithPatchCounter()
	comp := New(&Config{
		EventBus:               bus,
		DynamicClient:          client,
		GVRResolver:            newResolver(),
		Logger:                 testutil.NewTestLogger(),
		OwnNamespace:           "haptic",
		RestrictToOwnNamespace: restrict,
	})
	return comp, bus, counter
}

func setLeader(c *Component) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLeader = true
}

func sampleResource(ns, name string, port int) templating.RenderedResource {
	return templating.RenderedResource{
		APIVersion: "v1",
		Kind:       "Service",
		Namespace:  ns,
		Name:       name,
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"type":  "LoadBalancer",
				"ports": []any{map[string]any{"port": port, "targetPort": 8080}},
			},
		},
	}
}

func TestNew(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	require.NotNil(t, comp)
	assert.Equal(t, ComponentName, comp.Name())
	assert.NotNil(t, comp.eventChan)
	assert.False(t, comp.isLeader)
	assert.Equal(t, "haptic-controller", comp.managedByValue)
}

func TestApplyAndPrune_NotLeader_NoApply(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(0), counter.Load(), "non-leader must not apply")
}

func TestApplyAndPrune_LeaderApplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(1), counter.Load(), "leader must apply once")

	// Re-apply same resource: checksum dedup must skip the API call.
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(1), counter.Load(), "unchanged resource must not re-apply (checksum dedup)")
}

func TestApplyAndPrune_ChangedResourceReapplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())
	require.Equal(t, int32(1), counter.Load())

	// Change port → checksum changes → re-apply.
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 8080)}
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(2), counter.Load(), "changed payload must re-apply")
}

func TestApplyAndPrune_OrphanDeletion(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)
	deleted := &atomic.Int32{}
	if fc, ok := comp.dynamicClient.(*dynamicfake.FakeDynamicClient); ok {
		fc.PrependReactor("delete", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			deleted.Add(1)
			return true, nil, nil
		})
	}

	// First render creates two resources.
	comp.cachedResources = []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
		sampleResource("haptic", "svc-b", 81),
	}
	comp.handleReconciliationCompleted(context.Background())

	// Second render only has svc-a → svc-b must be deleted.
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())

	assert.Equal(t, int32(1), deleted.Load(), "orphan must be deleted")
}

func TestApplyAndPrune_RestrictToOwnNamespace_RefusesForeign(t *testing.T) {
	comp, _, counter := newTestComp(t, true) // restrict=true
	setLeader(comp)
	comp.cachedResources = []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),    // own namespace → allowed
		sampleResource("user-ns", "svc-b", 80),   // foreign namespace → refused
		sampleResource("", "cluster-thing", 0),   // cluster-scoped → refused
	}
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(1), counter.Load(), "only the own-namespace resource must apply")
}

func TestHandleBecameLeader_ClearsChecksumCache(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())
	require.Equal(t, int32(1), counter.Load())

	// Cache populated; same resource will be deduped.
	comp.handleReconciliationCompleted(context.Background())
	require.Equal(t, int32(1), counter.Load(), "second apply should be deduped before clear")

	// Becoming leader again clears the cache. Same resource set should
	// re-apply on next reconciliation because checksum cache is fresh.
	comp.handleBecameLeader(context.Background())
	assert.Equal(t, int32(2), counter.Load(), "BecameLeader must reapply cached set with cleared checksum cache")
}

func TestHandleLostLeadership_PausesApplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.handleLostLeadership()
	comp.cachedResources = []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(0), counter.Load(), "after losing leadership applies must stop")
}

func TestInjectManagedByLabel(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	caller := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": "x", "labels": map[string]any{"existing": "v"}},
	}
	out := comp.injectManagedByLabel(caller)
	labels := out["metadata"].(map[string]any)["labels"].(map[string]any)
	assert.Equal(t, "haptic-controller", labels[LabelManagedBy])
	assert.Equal(t, "v", labels["existing"], "existing labels must be preserved")

	// Caller's metadata.labels must not have been mutated.
	callerLabels := caller["metadata"].(map[string]any)["labels"].(map[string]any)
	_, hasManaged := callerLabels[LabelManagedBy]
	assert.False(t, hasManaged, "injectManagedByLabel must not mutate caller's labels map")
}

func TestStart_ContextCancellation(t *testing.T) {
	comp, bus, _ := newTestComp(t, true)
	bus.Start()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- comp.Start(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}
}

func TestEventDispatch_TemplateRenderedCachesResources(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	res := []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}
	evt := events.NewTemplateRenderedEvent("", nil, nil, res, 0, 0, "", "", true)
	comp.handleTemplateRendered(evt)

	comp.mu.RLock()
	cached := comp.cachedResources
	comp.mu.RUnlock()
	require.Len(t, cached, 1)
	assert.Equal(t, "svc-a", cached[0].Name)
}

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var serviceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

// fakeDiscovery is a minimal mock of discovery.DiscoveryInterface that
// returns a fixed list of namespaced resources. Embedding the upstream
// interface gives us nil-method-call panics if the recovery loop ever
// reaches for a method we haven't stubbed (a future-regression guard
// without us having to maintain every method's stub).
type fakeDiscovery struct {
	discovery.DiscoveryInterface
	namespaced []*metav1.APIResourceList
}

func (f *fakeDiscovery) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return f.namespaced, nil
}

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

// TestRecoverManagedResources_PrunesStartupOrphan covers the case where the
// controller was killed while a Gateway was deleted (so the Service we
// applied for that Gateway was never pruned). On leader-acquire we must
// discover the leftover via the managed-by label and add it to
// lastAppliedKeys so the next reconciliation prunes it.
//
// The dynamic-client fake doesn't apply label selectors to List, so we
// simulate the API-server side via a List reactor that returns only the
// labeled object — this matches what a real cluster does and tests the
// applier's logic in isolation from fake-client quirks.
func TestRecoverManagedResources_PrunesStartupOrphan(t *testing.T) {
	bus := testutil.NewTestBus()

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		serviceGVR: "ServiceList",
	})

	deleted := &atomic.Int32{}
	dynClient.PrependReactor("delete", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		deleted.Add(1)
		return true, nil, nil
	})

	// Custom List reactor: returns the orphan when the caller asks for
	// services in `haptic` namespace with the managed-by selector. Real
	// API server behaviour we mock here for the discovery path.
	listCalls := &atomic.Int32{}
	dynClient.PrependReactor("list", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		listCalls.Add(1)
		listAction, ok := action.(k8stesting.ListAction)
		if !ok {
			return false, nil, nil
		}
		if listAction.GetNamespace() != "haptic" {
			return true, &unstructured.UnstructuredList{}, nil
		}
		selector := listAction.GetListRestrictions().Labels
		match := selector != nil && selector.Matches(labels.Set{LabelManagedBy: "haptic-controller"})
		if !match {
			return true, &unstructured.UnstructuredList{}, nil
		}
		orphan := unstructured.Unstructured{}
		orphan.SetUnstructuredContent(map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "gw-orphan",
				"namespace": "haptic",
				"labels":    map[string]any{LabelManagedBy: "haptic-controller"},
			},
		})
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{orphan}}, nil
	})

	// Discovery stub reports Service as a namespace-scoped, listable+deletable type.
	disco := &fakeDiscovery{
		namespaced: []*metav1.APIResourceList{{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "services", Namespaced: true, Kind: "Service", Verbs: metav1.Verbs{"list", "delete", "get", "create", "update", "patch"}},
			},
		}},
	}

	comp := New(&Config{
		EventBus:               bus,
		DynamicClient:          dynClient,
		DiscoveryClient:        disco,
		GVRResolver:            newResolver(),
		Logger:                 testutil.NewTestLogger(),
		OwnNamespace:           "haptic",
		RestrictToOwnNamespace: false,
	})

	// Sanity check: discovery returns the list we configured.
	got, err := disco.ServerPreferredNamespacedResources()
	require.NoError(t, err)
	require.NotEmpty(t, got, "discovery fake setup is broken — ServerPreferredNamespacedResources returned empty")

	// Become leader → recovery should populate lastAppliedKeys with the orphan.
	comp.handleBecameLeader(context.Background())

	require.Greater(t, listCalls.Load(), int32(0), "list reactor never fired — discovery didn't call dynamicClient.List for services")

	comp.mu.RLock()
	keys := len(comp.lastAppliedKeys)
	comp.mu.RUnlock()
	require.Equal(t, 1, keys, "discovery should populate lastAppliedKeys with the orphan")

	// Trigger reconciliation with empty desired set — orphan must be deleted.
	comp.handleReconciliationCompleted(context.Background())
	assert.Equal(t, int32(1), deleted.Load(), "orphan discovered via label must be deleted")
}


// TestRecoverManagedResources_SkipsTypesWithout403 verifies the discovery
// loop silently skips types we don't have RBAC for (Forbidden) — the
// recovery is best-effort, not all-or-nothing.
func TestRecoverManagedResources_SkipsForbiddenTypes(t *testing.T) {
	bus := testutil.NewTestBus()
	scheme := runtime.NewScheme()
	configmapGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		serviceGVR:   "ServiceList",
		configmapGVR: "ConfigMapList",
	})

	// Reactor that returns Forbidden on any list call.
	dynClient.PrependReactor("list", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("forbidden")
	})

	disco := &fakeDiscovery{
		namespaced: []*metav1.APIResourceList{{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "services", Namespaced: true, Kind: "Service", Verbs: metav1.Verbs{"list", "delete"}},
				{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: metav1.Verbs{"list", "delete"}},
			},
		}},
	}

	comp := New(&Config{
		EventBus:        bus,
		DynamicClient:   dynClient,
		DiscoveryClient: disco,
		GVRResolver:     newResolver(),
		Logger:          testutil.NewTestLogger(),
		OwnNamespace:    "haptic",
	})

	// Should not panic, should not error externally; just silently skip.
	require.NotPanics(t, func() {
		comp.handleBecameLeader(context.Background())
	})
	comp.mu.RLock()
	keys := len(comp.lastAppliedKeys)
	comp.mu.RUnlock()
	assert.Equal(t, 0, keys, "Forbidden lists must not populate lastAppliedKeys")
}

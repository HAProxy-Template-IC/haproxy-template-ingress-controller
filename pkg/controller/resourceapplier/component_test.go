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
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
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
	var mu sync.Mutex
	uids := make(map[string]types.UID)
	var resourceVersion atomic.Uint64
	c.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		patchCount.Add(1)
		key := patch.GetNamespace() + "/" + patch.GetName()
		mu.Lock()
		uid := uids[key]
		if uid == "" {
			uid = types.UID("uid-" + key)
			uids[key] = uid
		}
		mu.Unlock()
		object := &unstructured.Unstructured{}
		object.SetName(patch.GetName())
		object.SetNamespace(patch.GetNamespace())
		object.SetUID(uid)
		object.SetResourceVersion(strconv.FormatUint(resourceVersion.Add(1), 10))
		return true, object, nil
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

type resourceCycleFixture struct {
	cycleAuthority  *rendercycle.Authority
	outputAuthority *renderoutput.Authority
	artifacts       *renderartifact.Snapshot
	events          *templating.RenderedEventSnapshot
}

func newResourceCycleFixture(tb testing.TB) *resourceCycleFixture {
	tb.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
	require.NoError(tb, err)
	cycleAuthority, err := rendercycle.NewAuthority(outputAuthority)
	require.NoError(tb, err)
	artifactBuilder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(tb, err)
	artifacts, err := artifactBuilder.Build()
	require.NoError(tb, err)
	renderedEvents, err := templating.NewEventCollector().Snapshot()
	require.NoError(tb, err)
	return &resourceCycleFixture{
		cycleAuthority: cycleAuthority, outputAuthority: outputAuthority,
		artifacts: artifacts, events: renderedEvents,
	}
}

func (f *resourceCycleFixture) snapshot(
	tb testing.TB,
	config string,
	resources []templating.RenderedResource,
	patches []templating.StatusPatch,
	previous *rendercycle.Snapshot,
) *rendercycle.Snapshot {
	tb.Helper()
	resourceCollector := templating.NewRenderedResourceCollector()
	for i := range resources {
		resource := &resources[i]
		require.NoError(tb, resourceCollector.Register(
			resource.APIVersion, resource.Kind, resource.Namespace, resource.Name, resource.Object,
		))
	}
	var previousResources *templating.RenderedResourceSnapshot
	var previousStatus *templating.StatusPatchSnapshot
	var previousOutput *renderoutput.Snapshot
	if previous != nil {
		var err error
		previousResources, err = previous.RenderedResourceSnapshot()
		require.NoError(tb, err)
		previousStatus, err = previous.StatusPatchSnapshot()
		require.NoError(tb, err)
		previousOutput, err = previous.OutputSnapshot()
		require.NoError(tb, err)
	}
	resourceSnapshot, err := resourceCollector.Snapshot(previousResources)
	require.NoError(tb, err)
	statusCollector := templating.NewStatusPatchCollector()
	for i := range patches {
		patch := &patches[i]
		require.NoError(tb, statusCollector.RegisterWithLineage(
			patch.Namespace, patch.Name, patch.APIVersion, patch.Kind,
			patch.UID, patch.ResourceVersion, patch.Variants,
		))
	}
	statusSnapshot, err := statusCollector.Snapshot(previousStatus)
	require.NoError(tb, err)
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(f.outputAuthority, config, plan, f.artifacts, previousOutput)
	require.NoError(tb, err)
	cycle, err := rendercycle.NewSnapshot(
		f.cycleAuthority, output, statusSnapshot, f.events, resourceSnapshot, previous,
	)
	require.NoError(tb, err)
	return cycle
}

func (f *resourceCycleFixture) completed(
	tb testing.TB,
	config string,
	resources []templating.RenderedResource,
	patches []templating.StatusPatch,
	previous *rendercycle.Snapshot,
	opts ...events.CorrelationOption,
) *events.ReconciliationCompletedEvent {
	tb.Helper()
	cycle := f.snapshot(tb, config, resources, patches, previous)
	occurrence := newRenderOccurrence(tb, cycle)
	event, err := events.NewReconciliationCompletedEventWithCycle(0, occurrence, opts...)
	require.NoError(tb, err)
	return event
}

func reconciliationCompletedEvent(tb testing.TB, resources []templating.RenderedResource) *events.ReconciliationCompletedEvent {
	tb.Helper()
	return newResourceCycleFixture(tb).completed(tb, "config", resources, nil, nil)
}

type renderOccurrenceEvent interface {
	RenderOccurrence() (*rendercycle.Occurrence, error)
}

func newRenderOccurrence(tb testing.TB, cycle *rendercycle.Snapshot) *rendercycle.Occurrence {
	tb.Helper()
	rendered, err := events.NewTemplateRenderedEventWithCycle(cycle, 0, "test", true)
	require.NoError(tb, err)
	occurrence, err := rendered.RenderOccurrence()
	require.NoError(tb, err)
	return occurrence
}

func eventOccurrence(tb testing.TB, event renderOccurrenceEvent) *rendercycle.Occurrence {
	tb.Helper()
	occurrence, err := event.RenderOccurrence()
	require.NoError(tb, err)
	return occurrence
}

func eventCycleSnapshot(tb testing.TB, event renderOccurrenceEvent) *rendercycle.Snapshot {
	tb.Helper()
	cycle, err := eventOccurrence(tb, event).Snapshot()
	require.NoError(tb, err)
	return cycle
}

func requireSameOccurrence(tb testing.TB, left, right renderOccurrenceEvent) {
	tb.Helper()
	same, err := eventOccurrence(tb, left).Same(eventOccurrence(tb, right))
	require.NoError(tb, err)
	require.True(tb, same)
}

func renderGateCompletedEvent(
	tb testing.TB,
	completed *events.ReconciliationCompletedEvent,
	ok, refused, newest bool,
) *events.RenderGateCompletedEvent {
	tb.Helper()
	event, err := events.NewRenderGateCompletedEventWithCycle(
		eventOccurrence(tb, completed), ok, refused, newest, "", !ok, 1,
	)
	require.NoError(tb, err)
	return event
}

func captureCycleForTest(tb testing.TB, event *events.ReconciliationCompletedEvent) *resourceCycle {
	tb.Helper()
	cycle, err := captureResourceCycle(event)
	require.NoError(tb, err)
	return cycle
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

// partialResource produces a partial-ownership rendered resource:
// carries AnnotationOwnership=OwnershipPartial and declares only the
// fields its template legitimately owns (here, a subset of
// spec.ports). Intended to exercise applyAndPrune's partial-mode
// branches without baking domain-specific naming into the test.
//
// Namespace and name are fixed to the test's standard
// controller-owned target ("haptic" / "haptic-haproxy") rather than
// parameterised — every existing caller uses these values, and the
// resourceapplier's namespace-scoping behaviour is exercised
// separately by the cross-namespace test suite.
func partialResource(ports ...int) templating.RenderedResource {
	portEntries := make([]any, 0, len(ports))
	for _, p := range ports {
		portEntries = append(portEntries, map[string]any{
			"name":       fmt.Sprintf("p-%d", p),
			"port":       p,
			"protocol":   "TCP",
			"targetPort": p,
		})
	}
	return templating.RenderedResource{
		APIVersion: "v1",
		Kind:       "Service",
		Namespace:  "haptic",
		Name:       "haptic-haproxy",
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "haptic-haproxy",
				"namespace": "haptic",
				"annotations": map[string]any{
					AnnotationOwnership: OwnershipPartial,
				},
			},
			"spec": map[string]any{"ports": portEntries},
		},
	}
}

func TestNew(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	require.NotNil(t, comp)
	assert.Equal(t, ComponentName, comp.Name())
	assert.NotNil(t, comp.Base, "must embed the component.Base event loop")
	assert.False(t, comp.isLeader)
	assert.Equal(t, DefaultManagedByValue, comp.managedByValue)
}

func TestApplyAndPrune_NotLeader_NoApply(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))
	assert.Equal(t, int32(0), counter.Load(), "non-leader must not apply")
}

func TestApplyAndPrune_LeaderAppliesEveryCycle(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)})
	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(1), counter.Load(), "leader must apply once")

	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(2), counter.Load(), "each cycle must verify the live target through SSA")
}

func TestCaptureResourceCycle_UsesOnlyAuthenticatedCycle(t *testing.T) {
	event := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})
	event.CycleSnapshot = nil
	event.RenderProof = "poison"
	event.RenderedResources = []templating.RenderedResource{sampleResource("haptic", "poison", 81)}
	event.RenderedResourceSnapshot = &templating.RenderedResourceSnapshot{}

	cycle, err := captureResourceCycle(event)
	require.NoError(t, err)
	first, err := cycle.resources.Resources()
	require.NoError(t, err)
	first[0].Object["spec"].(map[string]any)["type"] = "poison"
	second, err := cycle.resources.Resources()
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "svc-a", second[0].Name)
	assert.Equal(t, "LoadBalancer", second[0].Object["spec"].(map[string]any)["type"])
}

func TestCaptureResourceCycle_RejectsMissingAuthority(t *testing.T) {
	shadow := events.NewReconciliationCompletedEvent(
		0, "shadow", []templating.RenderedResource{sampleResource("haptic", "poison", 81)}, nil,
	)
	_, err := captureResourceCycle(shadow)
	require.ErrorContains(t, err, "render occurrence is missing")
}

func TestHandleReconciliationCompleted_InvalidShadowDoesNotPoisonNextCycle(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	shadow := events.NewReconciliationCompletedEvent(
		0, "shadow", []templating.RenderedResource{sampleResource("haptic", "poison", 81)}, nil,
	)
	comp.handleReconciliationCompleted(context.Background(), shadow)
	require.Zero(t, counter.Load())

	valid := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})
	comp.handleReconciliationCompleted(context.Background(), valid)
	assert.Equal(t, int32(1), counter.Load(), "invalid authority must not poison later exact work")
}

func TestHandleReconciliationCompleted_ABAAppliesEveryOccurrence(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	fixture := newResourceCycleFixture(t)
	a1 := fixture.completed(t, "config-a", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}, nil, nil)
	b := fixture.completed(t, "config-b", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 81),
	}, nil, eventCycleSnapshot(t, a1))
	a2 := fixture.completed(t, "config-a", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}, nil, eventCycleSnapshot(t, b))

	comp.handleReconciliationCompleted(context.Background(), a1)
	comp.handleReconciliationCompleted(context.Background(), b)
	comp.handleReconciliationCompleted(context.Background(), a2)
	assert.Equal(t, int32(3), counter.Load(), "A-B-A must verify all three live states")
}

func TestApplyAndPrune_ChangedResourceReapplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))
	require.Equal(t, int32(1), counter.Load())

	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 8080)}))
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
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{
			sampleResource("haptic", "svc-a", 80),
			sampleResource("haptic", "svc-b", 81),
		}))

	// Second render only has svc-a → svc-b must be deleted.
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))

	assert.Equal(t, int32(1), deleted.Load(), "orphan must be deleted")
}

func TestPruneOrphans_UsesExactSSAResponsePreconditions(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)
	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}))
	comp.mu.RLock()
	require.Len(t, comp.lastAppliedKeys, 1)
	var applied appliedKeyMeta
	for _, meta := range comp.lastAppliedKeys {
		applied = meta
	}
	comp.mu.RUnlock()
	require.NotEmpty(t, applied.UID)
	require.NotEmpty(t, applied.ResourceVersion)

	var captured metav1.DeleteOptions
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("delete", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		captured = action.(k8stesting.DeleteAction).GetDeleteOptions()
		return true, nil, nil
	})
	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, nil))
	require.NotNil(t, captured.Preconditions)
	require.NotNil(t, captured.Preconditions.UID)
	require.NotNil(t, captured.Preconditions.ResourceVersion)
	assert.Equal(t, applied.UID, *captured.Preconditions.UID)
	assert.Equal(t, applied.ResourceVersion, *captured.Preconditions.ResourceVersion)
}

func TestPruneOrphans_ProtectsRecreatedAndOwnershipLostResources(t *testing.T) {
	tests := []struct {
		name   string
		object func(*appliedKeyMeta) *unstructured.Unstructured
	}{
		{
			name: "same-name recreation",
			object: func(meta *appliedKeyMeta) *unstructured.Unstructured {
				object := ownedObject(meta, "8")
				object.SetUID("uid-recreated")
				return object
			},
		},
		{
			name: "ownership lost",
			object: func(meta *appliedKeyMeta) *unstructured.Unstructured {
				object := ownedObject(meta, "8")
				object.SetLabels(nil)
				return object
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp, _, _ := newTestComp(t, false)
			meta := appliedKeyMeta{
				GVR: serviceGVR, Namespace: "haptic", Name: "svc-a",
				UID: "uid-original", ResourceVersion: "7",
			}
			key := "haptic/svc-a/" + serviceGVR.String()
			comp.lastAppliedKeys[key] = meta
			client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
			client.PrependReactor("delete", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewConflict(serviceGVR.GroupResource(), meta.Name, errors.New("changed"))
			})
			client.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, tt.object(&meta), nil
			})

			deleted, failed := comp.pruneOrphans(
				context.Background(), map[string]appliedKeyMeta{}, map[string]struct{}{},
			)
			assert.Zero(t, deleted)
			assert.Zero(t, failed)
			assert.Empty(t, comp.lastAppliedKeys, "foreign or no-longer-owned object must be released from tracking")
		})
	}
}

func TestPruneOrphans_ConflictRetainsUpdatedLineageForRetry(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	meta := appliedKeyMeta{
		GVR: serviceGVR, Namespace: "haptic", Name: "svc-a",
		UID: "uid-original", ResourceVersion: "7",
	}
	key := "haptic/svc-a/" + serviceGVR.String()
	comp.lastAppliedKeys[key] = meta
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	var deleteAttempts atomic.Int32
	var secondDelete metav1.DeleteOptions
	client.PrependReactor("delete", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if deleteAttempts.Add(1) == 1 {
			return true, nil, apierrors.NewConflict(serviceGVR.GroupResource(), meta.Name, errors.New("changed"))
		}
		secondDelete = action.(k8stesting.DeleteAction).GetDeleteOptions()
		return true, nil, nil
	})
	client.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, ownedObject(&meta, "8"), nil
	})

	deleted, failed := comp.pruneOrphans(
		context.Background(), map[string]appliedKeyMeta{}, map[string]struct{}{},
	)
	assert.Zero(t, deleted)
	require.Equal(t, 1, failed)
	require.Equal(t, "8", comp.lastAppliedKeys[key].ResourceVersion)

	deleted, failed = comp.pruneOrphans(
		context.Background(), map[string]appliedKeyMeta{}, map[string]struct{}{},
	)
	assert.Equal(t, 1, deleted)
	assert.Zero(t, failed)
	require.NotNil(t, secondDelete.Preconditions)
	require.NotNil(t, secondDelete.Preconditions.ResourceVersion)
	assert.Equal(t, "8", *secondDelete.Preconditions.ResourceVersion)
	assert.Empty(t, comp.lastAppliedKeys)
}

func ownedObject(meta *appliedKeyMeta, resourceVersion string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetName(meta.Name)
	object.SetNamespace(meta.Namespace)
	object.SetUID(meta.UID)
	object.SetResourceVersion(resourceVersion)
	object.SetLabels(map[string]string{LabelManagedBy: DefaultManagedByValue})
	return object
}

func TestApplyAndPrune_PartialOwnership_NoOrphanDelete(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	deleted := &atomic.Int32{}
	if fc, ok := comp.dynamicClient.(*dynamicfake.FakeDynamicClient); ok {
		fc.PrependReactor("delete", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
			deleted.Add(1)
			return true, nil, nil
		})
	}

	// First render: partial Service patching gw-8080 in. Applies as SSA.
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{partialResource(8080)}))
	require.Equal(t, int32(1), counter.Load(), "leader must SSA the partial patch")
	require.Equal(t, int32(0), deleted.Load(), "partial-mode apply must never DELETE")

	// Second render: no resources at all. A full-ownership Service in
	// the same shape would be DELETEd here; the partial one must not.
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, nil))
	assert.Equal(t, int32(0), deleted.Load(),
		"partial-mode resource must never be deleted, even when missing from the rendered set")
}

func TestApplyAndPrune_FullToPartialOwnershipReleasesDeleteTracking(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)
	var deletes atomic.Int32
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("delete", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		deletes.Add(1)
		return true, nil, nil
	})

	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "haptic-haproxy", 80),
	}))
	comp.mu.RLock()
	require.Len(t, comp.lastAppliedKeys, 1)
	comp.mu.RUnlock()

	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, []templating.RenderedResource{
		partialResource(8080),
	}))
	assert.Zero(t, deletes.Load(), "a shared object present in the desired set must not be pruned")
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.Empty(t, comp.lastAppliedKeys, "partial ownership must release future deletion authority")
}

func TestApplyAndPrune_PartialOwnership_NoManagedByLabel(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)

	patched := &atomic.Int32{}
	var capturedPayload []byte
	if fc, ok := comp.dynamicClient.(*dynamicfake.FakeDynamicClient); ok {
		fc.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
			patched.Add(1)
			if pa, ok := action.(k8stesting.PatchAction); ok {
				capturedPayload = pa.GetPatch()
			}
			return true, nil, nil
		})
	}

	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{partialResource(8080, 8443)}))
	require.Equal(t, int32(1), patched.Load())

	// The SSA payload must NOT carry the managed-by label, and must NOT
	// retain the ownership annotation.
	require.NotEmpty(t, capturedPayload)
	assert.NotContains(t, string(capturedPayload), LabelManagedBy,
		"partial-ownership SSA must not stamp the managed-by label")
	assert.NotContains(t, string(capturedPayload), AnnotationOwnership,
		"ownership annotation must be stripped before SSA")
}

func TestApplyAndPrune_PartialOwnership_DropEntryReapplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)

	// First render owns gw-8080 + gw-8443.
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{partialResource(8080, 8443)}))
	require.Equal(t, int32(1), counter.Load())

	// Second render drops gw-8443 → checksum differs → must re-SSA so
	// the apiserver releases haptic's claim on the dropped entry.
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{partialResource(8080)}))
	assert.Equal(t, int32(2), counter.Load(),
		"changing the partial port set must re-apply so SSA releases the dropped entry")
}

func TestApplyAndPrune_RestrictToOwnNamespace_RefusesForeign(t *testing.T) {
	comp, bus, counter := newTestComp(t, true) // restrict=true
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{
			sampleResource("haptic", "svc-a", 80),  // own namespace → allowed
			sampleResource("user-ns", "svc-b", 80), // foreign namespace → refused
			sampleResource("", "cluster-thing", 0), // cluster-scoped → refused
		}))
	assert.Equal(t, int32(1), counter.Load(), "only the own-namespace resource must apply")
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
}

func TestHandleBecameLeader_DoesNotApplyUntilFreshCycle(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)})
	comp.handleReconciliationCompleted(context.Background(), evt)
	require.Equal(t, int32(1), counter.Load())

	comp.handleReconciliationCompleted(context.Background(), evt)
	require.Equal(t, int32(2), counter.Load())
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, evt, true, false, true))
	comp.mu.RLock()
	require.NotNil(t, comp.gateAcceptedCycle)
	comp.mu.RUnlock()

	comp.handleBecameLeader(context.Background())
	assert.Equal(t, int32(2), counter.Load(), "leadership alone must not apply resources")
	comp.mu.RLock()
	assert.Nil(t, comp.appliedCycle)
	assert.Nil(t, comp.gateAcceptedCycle)
	comp.mu.RUnlock()

	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(3), counter.Load(), "the fresh leader reconciliation must verify every target")
}

func TestHandleLostLeadership_PausesApplies(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	comp.handleLostLeadership()
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))
	assert.Equal(t, int32(0), counter.Load(), "after losing leadership applies must stop")
}

func TestPrepareForApply_FullOwnership_InjectsManagedByLabel(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	caller := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": "x", "labels": map[string]any{"existing": "v"}},
	}
	out := comp.prepareForApply(caller, false)
	outLabels := out["metadata"].(map[string]any)["labels"].(map[string]any)
	assert.Equal(t, DefaultManagedByValue, outLabels[LabelManagedBy])
	assert.Equal(t, "v", outLabels["existing"], "existing labels must be preserved")

	// Caller's metadata.labels must not have been mutated.
	callerLabels := caller["metadata"].(map[string]any)["labels"].(map[string]any)
	_, hasManaged := callerLabels[LabelManagedBy]
	assert.False(t, hasManaged, "prepareForApply must not mutate caller's labels map")
}

func TestPrepareForApply_PartialOwnership_OmitsManagedByLabel(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	caller := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":   "haptic-haproxy",
			"labels": map[string]any{"existing": "v"},
			"annotations": map[string]any{
				AnnotationOwnership: OwnershipPartial,
				"keep-me":           "yes",
			},
		},
	}
	out := comp.prepareForApply(caller, true)

	// Existing labels preserved, no managed-by injected.
	outLabels := out["metadata"].(map[string]any)["labels"].(map[string]any)
	_, hasManaged := outLabels[LabelManagedBy]
	assert.False(t, hasManaged, "partial-ownership applies must not claim managed-by")
	assert.Equal(t, "v", outLabels["existing"])

	// Ownership annotation stripped; other annotations preserved.
	annotations := out["metadata"].(map[string]any)["annotations"].(map[string]any)
	_, hasOwnership := annotations[AnnotationOwnership]
	assert.False(t, hasOwnership, "ownership annotation must be stripped before SSA")
	assert.Equal(t, "yes", annotations["keep-me"])

	// Caller's annotations map must not have been mutated.
	callerAnn := caller["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.Equal(t, OwnershipPartial, callerAnn[AnnotationOwnership],
		"prepareForApply must not mutate caller's annotations map")
}

func TestPrepareForApply_StripsOwnershipAnnotation_RemovesAnnotationsWhenEmpty(t *testing.T) {
	comp, _, _ := newTestComp(t, true)
	caller := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name": "haptic-haproxy",
			"annotations": map[string]any{
				AnnotationOwnership: OwnershipPartial,
			},
		},
	}
	out := comp.prepareForApply(caller, true)
	metadata := out["metadata"].(map[string]any)
	_, hasAnn := metadata["annotations"]
	assert.False(t, hasAnn, "annotations key must be removed when only entry was the ownership flag")
}

func TestIsPartialOwnership(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want bool
	}{
		{
			name: "no metadata",
			obj:  map[string]any{},
			want: false,
		},
		{
			name: "no annotations",
			obj:  map[string]any{"metadata": map[string]any{"name": "x"}},
			want: false,
		},
		{
			name: "annotation absent",
			obj: map[string]any{"metadata": map[string]any{
				"annotations": map[string]any{"other": "v"},
			}},
			want: false,
		},
		{
			name: "annotation present with partial value",
			obj: map[string]any{"metadata": map[string]any{
				"annotations": map[string]any{AnnotationOwnership: OwnershipPartial},
			}},
			want: true,
		},
		{
			name: "annotation present with other value",
			obj: map[string]any{"metadata": map[string]any{
				"annotations": map[string]any{AnnotationOwnership: "full"},
			}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &templating.RenderedResource{Object: tc.obj}
			assert.Equal(t, tc.want, isPartialOwnership(r))
		})
	}
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

// TestHandleReconciliationCompleted_ReadsResourcesFromEvent pins the
// stateless contract: resources arrive on the event payload, never via a
// side-channel cache. A regression that re-introduces the racy
// cache-then-read pattern (the bug fixed alongside StatusApplier's
// StatusPatches threading) would fail this test because the applier would
// have nothing to apply.
func TestHandleReconciliationCompleted_ReadsResourcesFromEvent(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})
	comp.handleReconciliationCompleted(context.Background(), evt)
	assert.Equal(t, int32(1), counter.Load(),
		"applier must read resources directly from the event payload, not from any cached field")
}

// These resources describe the configuration HAProxy is meant to run, so they
// move with the render gate's verdict: held while it is pinned, applied by the
// pass that releases the cycle they belong to.
func TestHandleRenderGateCompleted_HoldsAndReleasesTheCycle(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	fixture := newResourceCycleFixture(t)
	refused := fixture.completed(t, "config-a", nil, nil, nil)

	comp.handleRenderGateCompleted(context.Background(),
		renderGateCompletedEvent(t, refused, false, true, true))

	held := fixture.completed(
		t, "config-b", []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)},
		nil, eventCycleSnapshot(t, refused),
	)
	comp.handleReconciliationCompleted(context.Background(), held)
	assert.Equal(t, int32(0), counter.Load(),
		"a cycle the fleet was never given must not be advertised on the cluster")

	comp.handleRenderGateCompleted(context.Background(),
		renderGateCompletedEvent(t, held, true, false, true))
	assert.Equal(t, int32(1), counter.Load(), "the pass that names the held cycle applies it")
}

func TestHandleRenderGateCompleted_SameCycleWithDifferentOccurrenceDoesNotRelease(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	held := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, held, false, true, true))
	comp.handleReconciliationCompleted(context.Background(), held)
	foreignOccurrence := newRenderOccurrence(t, eventCycleSnapshot(t, held))
	foreignVerdict, err := events.NewRenderGateCompletedEventWithCycle(
		foreignOccurrence, true, false, true, "", false, 1,
	)
	require.NoError(t, err)

	comp.handleRenderGateCompleted(context.Background(), foreignVerdict)
	assert.Zero(t, counter.Load())
	comp.mu.RLock()
	assert.True(t, comp.gatePinned)
	comp.mu.RUnlock()

	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, held, true, false, true))
	assert.Equal(t, int32(1), counter.Load())
}

// A verdict for a plan the applier has moved past says nothing about what it
// holds, so it neither closes the latch nor re-applies anything.
func TestHandleRenderGateCompleted_IgnoresASupersededPlan(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	completed := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})

	comp.handleReconciliationCompleted(context.Background(), completed)
	require.Equal(t, int32(1), counter.Load())

	comp.handleRenderGateCompleted(context.Background(),
		renderGateCompletedEvent(t, completed, false, true, false))

	assert.Equal(t, int32(1), counter.Load(), "a straggler's refusal must not re-apply an older cycle")
	comp.mu.Lock()
	defer comp.mu.Unlock()
	assert.False(t, comp.gatePinned, "a straggler's refusal must not close the latch")
}

func TestHandleRenderGateCompleted_ShadowCannotPin(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)
	comp.handleRenderGateCompleted(context.Background(),
		events.NewRenderGateCompletedEvent("shadow", false, true, true, "", true, 1))

	comp.mu.RLock()
	pinned := comp.gatePinned
	comp.mu.RUnlock()
	assert.False(t, pinned)
}

func TestHandleRenderGateCompleted_AuthenticatedVerdictIgnoresPoisonedShadows(t *testing.T) {
	comp, _, _ := newTestComp(t, false)
	setLeader(comp)
	completed := reconciliationCompletedEvent(t, nil)
	verdict := renderGateCompletedEvent(t, completed, false, true, true)
	verdict.CycleSnapshot = nil
	verdict.RenderProof = "poison"
	verdict.PlanID = "poison"

	comp.handleRenderGateCompleted(context.Background(), verdict)

	comp.mu.RLock()
	pinned := comp.gatePinned
	comp.mu.RUnlock()
	assert.True(t, pinned)
}

func TestHandleRenderGateCompleted_PassBeforeCompletionReleasesExactCycle(t *testing.T) {
	comp, _, counter := newTestComp(t, false)
	setLeader(comp)
	fixture := newResourceCycleFixture(t)
	a := fixture.completed(t, "config-a", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}, nil, nil)
	b := fixture.completed(t, "config-b", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 81),
	}, nil, eventCycleSnapshot(t, a))

	comp.handleReconciliationCompleted(context.Background(), a)
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, a, true, false, true))
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, b, false, true, true))
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, b, true, false, true))
	require.Equal(t, int32(1), counter.Load(), "a pass alone cannot materialize a missing completion")

	comp.handleReconciliationCompleted(context.Background(), b)
	assert.Equal(t, int32(2), counter.Load())
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.False(t, comp.gatePinned)
	assert.True(t, sameResourceCycle(comp.appliedCycle, comp.gateAcceptedCycle))
}

func TestHandleRenderGateCompleted_FailedReleaseRemainsHeldForRetry(t *testing.T) {
	comp, bus, counter := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	fixture := newResourceCycleFixture(t)
	held := fixture.completed(t, "config-b", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 81),
	}, nil, nil)
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, held, false, true, true))
	comp.handleReconciliationCompleted(context.Background(), held)

	var failOnce atomic.Bool
	failOnce.Store(true)
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("patch", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failOnce.CompareAndSwap(true, false) {
			return true, nil, errors.New("temporary API failure")
		}
		return false, nil, nil
	})
	pass := renderGateCompletedEvent(t, held, true, false, true)
	comp.handleRenderGateCompleted(context.Background(), pass)
	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	comp.mu.RLock()
	require.True(t, comp.gatePinned)
	require.False(t, sameResourceCycle(comp.heldCycle, comp.appliedCycle))
	comp.mu.RUnlock()

	comp.handleRenderGateCompleted(context.Background(), pass)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	assert.Equal(t, int32(1), counter.Load())
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.False(t, comp.gatePinned)
	assert.Nil(t, comp.heldCycle)
}

func TestHandleRenderGateCompleted_RefusalRestoresAcceptedCycleAndPublishes(t *testing.T) {
	comp, bus, counter := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	fixture := newResourceCycleFixture(t)
	a := fixture.completed(t, "config-a", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}, nil, nil)
	b := fixture.completed(t, "config-b", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 81),
	}, nil, eventCycleSnapshot(t, a))

	comp.handleReconciliationCompleted(context.Background(), a)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, a, true, false, true))
	comp.handleReconciliationCompleted(context.Background(), b)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, b, false, true, true))
	reverted := testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	requireSameOccurrence(t, a, reverted)
	assert.Equal(t, int32(3), counter.Load())
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.True(t, sameResourceCycle(comp.appliedCycle, comp.gateAcceptedCycle))
	assert.True(t, comp.gatePinned)
	assert.Nil(t, comp.revertCycle)
}

func TestHandleRenderGateCompleted_FailedRevertRemainsPendingForRetry(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	fixture := newResourceCycleFixture(t)
	a := fixture.completed(t, "config-a", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	}, nil, nil)
	b := fixture.completed(t, "config-b", []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 81),
	}, nil, eventCycleSnapshot(t, a))
	comp.handleReconciliationCompleted(context.Background(), a)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	comp.handleRenderGateCompleted(context.Background(), renderGateCompletedEvent(t, a, true, false, true))
	comp.handleReconciliationCompleted(context.Background(), b)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)

	var failOnce atomic.Bool
	failOnce.Store(true)
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("patch", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failOnce.CompareAndSwap(true, false) {
			return true, nil, errors.New("temporary API failure")
		}
		return false, nil, nil
	})
	refusal := renderGateCompletedEvent(t, b, false, true, true)
	comp.handleRenderGateCompleted(context.Background(), refusal)
	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	comp.mu.RLock()
	require.True(t, sameResourceCycle(comp.revertCycle, captureCycleForTest(t, a)))
	require.True(t, sameResourceCycle(comp.appliedCycle, captureCycleForTest(t, b)))
	comp.mu.RUnlock()

	comp.handleRenderGateCompleted(context.Background(), refusal)
	reverted := testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	requireSameOccurrence(t, a, reverted)
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.Nil(t, comp.revertCycle)
	assert.True(t, sameResourceCycle(comp.appliedCycle, comp.gateAcceptedCycle))
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
		match := selector != nil && selector.Matches(labels.Set{LabelManagedBy: DefaultManagedByValue})
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
				"labels":    map[string]any{LabelManagedBy: DefaultManagedByValue},
			},
		})
		orphan.SetUID("uid-orphan")
		orphan.SetResourceVersion("7")
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
	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, nil))
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

// TestHandleReconciliationCompleted_PublishesResourcesApplied pins the
// producer side of the rendered-status ordering contract: AFTER the apply
// pass the applier must publish a ResourcesAppliedEvent forwarding the
// cycle's StatusPatches (and correlation), because the StatusApplier writes
// the "rendered" variant on that event — conditions like Accepted=True must
// never precede the infrastructure they describe. A regression that stops
// publishing, or drops the patches, silently strands every resource at its
// CRD-default status.
func TestHandleReconciliationCompleted_PublishesResourcesApplied(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()

	patches := []templating.StatusPatch{{
		Namespace:       "default",
		Name:            "gw-1",
		APIVersion:      "gateway.networking.k8s.io/v1",
		Kind:            "Gateway",
		UID:             "uid-gw-1",
		ResourceVersion: "11",
		Variants:        map[string]map[string]any{"rendered": {"conditions": []any{}}},
	}}
	evt := newResourceCycleFixture(t).completed(
		t, "config", []templating.RenderedResource{sampleResource("default", "svc-1", 80)},
		patches, nil,
		events.WithCorrelation("corr-1", "cause-1"),
	)
	comp.handleReconciliationCompleted(context.Background(), evt)

	applied := testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	requireSameOccurrence(t, evt, applied)
	statusSnapshot, err := eventCycleSnapshot(t, applied).StatusPatchSnapshot()
	require.NoError(t, err)
	actualPatches, err := statusSnapshot.Patches()
	require.NoError(t, err)
	require.Len(t, actualPatches, 1, "the cycle's status patches must be forwarded")
	assert.Equal(t, "gw-1", actualPatches[0].Name)
	assert.Equal(t, "corr-1", applied.CorrelationID(), "correlation must propagate for tracing")
}

func TestHandleReconciliationCompleted_ApplyFailureWithholdsSuccessAndRetries(t *testing.T) {
	comp, bus, counter := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()

	var failOnce atomic.Bool
	failOnce.Store(true)
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("patch", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(k8stesting.PatchAction)
		if ok && patch.GetName() == "svc-b" && failOnce.CompareAndSwap(true, false) {
			return true, nil, errors.New("temporary API failure")
		}
		return false, nil, nil
	})

	evt := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
		sampleResource("haptic", "svc-b", 81),
	})
	comp.handleReconciliationCompleted(context.Background(), evt)

	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	assert.Equal(t, int32(1), counter.Load())

	comp.handleReconciliationCompleted(context.Background(), evt)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	assert.Equal(t, int32(3), counter.Load(), "retry must verify both live targets before publishing success")
}

func TestHandleReconciliationCompleted_MissingSSAResponseLineageWithholdsSuccessAndRetries(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	var attempts atomic.Int32
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("patch", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		object := &unstructured.Unstructured{}
		object.SetName(patch.GetName())
		object.SetNamespace(patch.GetNamespace())
		if attempts.Add(1) > 1 {
			object.SetUID("uid-safe")
			object.SetResourceVersion("9")
		}
		return true, object, nil
	})
	event := reconciliationCompletedEvent(t, []templating.RenderedResource{
		sampleResource("haptic", "svc-a", 80),
	})

	comp.handleReconciliationCompleted(context.Background(), event)
	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	comp.mu.RLock()
	assert.Empty(t, comp.lastAppliedKeys)
	assert.Nil(t, comp.appliedCycle)
	comp.mu.RUnlock()

	comp.handleReconciliationCompleted(context.Background(), event)
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	assert.Equal(t, int32(2), attempts.Load())
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	require.Len(t, comp.lastAppliedKeys, 1)
}

func TestHandleReconciliationCompleted_ResolutionFailureDoesNotPrune(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()

	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)

	var deletes atomic.Int32
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("delete", "services", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		deletes.Add(1)
		return true, nil, nil
	})

	unresolved := sampleResource("haptic", "svc-a", 80)
	unresolved.Kind = "Unknown"
	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{unresolved}))

	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	assert.Equal(t, int32(0), deletes.Load(), "an incomplete desired-key set must never drive orphan pruning")
}

func TestHandleReconciliationCompleted_DeleteFailureWithholdsSuccessAndRetries(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()

	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "svc-a", 80)}))
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)

	var deleteAttempts atomic.Int32
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("delete", "services", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if deleteAttempts.Add(1) == 1 {
			return true, nil, errors.New("temporary delete failure")
		}
		return true, nil, nil
	})

	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, nil))
	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)

	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, nil))
	_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
	assert.Equal(t, int32(2), deleteAttempts.Load(), "failed orphan deletion must remain tracked for retry")
}

func TestHandleReconciliationCompleted_LineageLessOrphanNeverAuthorizesDelete(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()
	key := "haptic/svc-a/" + serviceGVR.String()
	comp.lastAppliedKeys[key] = appliedKeyMeta{
		GVR: serviceGVR, Namespace: "haptic", Name: "svc-a",
	}
	var deletes atomic.Int32
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("delete", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		deletes.Add(1)
		return true, nil, nil
	})

	comp.handleReconciliationCompleted(context.Background(), reconciliationCompletedEvent(t, nil))
	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
	assert.Zero(t, deletes.Load())
	comp.mu.RLock()
	defer comp.mu.RUnlock()
	assert.Contains(t, comp.lastAppliedKeys, key, "unsafe orphan must remain visible for retry")
}

// TestHandleReconciliationCompleted_NoPublishWhenNotLeader: a follower must
// not publish ResourcesAppliedEvent — it didn't apply anything, and the
// (leader-gated) StatusApplier acting on a follower-published event would
// break the resources-before-status ordering the event exists to guarantee.
func TestHandleReconciliationCompleted_NoPublishWhenNotLeader(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	observer := bus.Subscribe("resources-applied-observer", 10)
	bus.Start()

	comp.handleReconciliationCompleted(context.Background(),
		reconciliationCompletedEvent(t, nil))

	testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
}

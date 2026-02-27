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

package statusapplier

import (
	"context"
	"encoding/json"
	"fmt"
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

// mockGVRResolver is a test double for GVRResolver that returns configurable results.
type mockGVRResolver struct {
	results map[string]schema.GroupVersionResource
	err     error
}

func (m *mockGVRResolver) Resolve(apiVersion, kind string) (schema.GroupVersionResource, error) {
	if m.err != nil {
		return schema.GroupVersionResource{}, m.err
	}
	key := apiVersion + "/" + kind
	if gvr, ok := m.results[key]; ok {
		return gvr, nil
	}
	return schema.GroupVersionResource{}, fmt.Errorf("unknown kind %s/%s", apiVersion, kind)
}

var ingressGVR = schema.GroupVersionResource{
	Group:    "networking.k8s.io",
	Version:  "v1",
	Resource: "ingresses",
}

func newTestResolver() *mockGVRResolver {
	return &mockGVRResolver{
		results: map[string]schema.GroupVersionResource{
			"networking.k8s.io/v1/Ingress":              ingressGVR,
			"gateway.networking.k8s.io/v1/Gateway":      {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"},
			"gateway.networking.k8s.io/v1/HTTPRoute":    {Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
			"gateway.networking.k8s.io/v1beta1/Gateway": {Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "gateways"},
		},
	}
}

func newTestPatches(variants map[string]map[string]interface{}) []templating.StatusPatch {
	return []templating.StatusPatch{
		{
			Namespace:  "default",
			Name:       "my-ingress",
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Variants:   variants,
		},
	}
}

func newTestComponent(bus *busevents.EventBus, fakeClient *dynamicfake.FakeDynamicClient, resolver GVRResolver) *Component {
	return New(&Config{
		EventBus:      bus,
		DynamicClient: fakeClient,
		GVRResolver:   resolver,
		Logger:        testutil.NewTestLogger(),
	})
}

func newFakeDynamicClient() *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClient(scheme)
}

// newFakeDynamicClientWithPatchSuccess creates a fake client that accepts all patch operations.
// The default fake client rejects patches for non-existent resources, so we add a reactor
// that returns success for all status subresource patch operations.
func newFakeDynamicClientWithPatchSuccess() *dynamicfake.FakeDynamicClient {
	client := newFakeDynamicClient()
	client.PrependReactor("patch", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	return client
}

// setLeader sets the component to leader state.
func setLeader(c *Component) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLeader = true
}

func TestNew(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := newTestResolver()

	comp := newTestComponent(bus, fakeClient, resolver)

	require.NotNil(t, comp)
	assert.Equal(t, ComponentName, comp.Name())
	assert.NotNil(t, comp.eventChan)
	assert.NotNil(t, comp.checksumCache)
	assert.False(t, comp.isLeader)
}

func TestRun_ContextCancellation(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	errChan := make(chan error, 1)
	go func() {
		errChan <- comp.Start(ctx)
	}()

	time.Sleep(testutil.StartupDelay)
	cancel()

	select {
	case err := <-errChan:
		assert.NoError(t, err) // Run returns nil on context cancel
	case <-time.After(testutil.LongTimeout):
		t.Fatal("component did not stop in time after context cancel")
	}
}

func TestHandleTemplateRendered_CachesPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
	})

	// Not leader — should cache but not apply
	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, patches, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	comp.mu.RLock()
	assert.Equal(t, patches, comp.cachedPatches)
	comp.mu.RUnlock()
}

func TestHandleTemplateRendered_AppliesWhenLeader(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
	})

	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, patches, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	// Should publish StatusUpdateCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

func TestHandleTemplateRendered_SkipsWhenNotLeader(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	// Not leader
	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{}},
	})
	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, patches, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	// Should NOT publish any event (no apply when not leader)
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleTemplateRendered_SkipsEmptyPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, nil, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleReconciliationCompleted_AppliesDeployedVariant(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	// Pre-cache patches with "deployed" variant
	comp.mu.Lock()
	comp.cachedPatches = newTestPatches(map[string]map[string]interface{}{
		"deployed": {"conditions": []interface{}{map[string]interface{}{"type": "Programmed", "status": "True"}}},
	})
	comp.mu.Unlock()

	comp.handleReconciliationCompleted(context.Background(), events.NewReconciliationCompletedEvent(100))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

func TestHandleReconciliationCompleted_SkipsWithoutCachedPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	// No cached patches
	comp.handleReconciliationCompleted(context.Background(), events.NewReconciliationCompletedEvent(100))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleReconciliationFailed_DeployPhase(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.mu.Lock()
	comp.cachedPatches = newTestPatches(map[string]map[string]interface{}{
		"deployFailed": {"conditions": []interface{}{map[string]interface{}{"type": "Programmed", "status": "False"}}},
	})
	comp.mu.Unlock()

	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("deploy error", "deploy"))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployFailed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

func TestHandleReconciliationFailed_RenderPhase(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.mu.Lock()
	comp.cachedPatches = newTestPatches(map[string]map[string]interface{}{
		"renderFailed": {"conditions": []interface{}{map[string]interface{}{"type": "Accepted", "status": "False"}}},
	})
	comp.mu.Unlock()

	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("render error", "render"))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRenderFailed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

func TestHandleBecameLeader_ReplaysCachedPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	// Pre-cache patches before becoming leader
	comp.mu.Lock()
	comp.cachedPatches = newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Accepted"}}},
	})
	comp.mu.Unlock()

	comp.handleBecameLeader(context.Background())

	// Should be leader now
	comp.mu.RLock()
	assert.True(t, comp.isLeader)
	comp.mu.RUnlock()

	// Should have replayed patches
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

func TestHandleBecameLeader_ClearsChecksumCache(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	bus.Start()

	// Pre-populate checksum cache
	comp.mu.Lock()
	comp.checksumCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"] = "abc123"
	comp.mu.Unlock()

	comp.handleBecameLeader(context.Background())

	comp.mu.RLock()
	assert.Empty(t, comp.checksumCache)
	comp.mu.RUnlock()
}

func TestHandleBecameLeader_NoCachedPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	comp.handleBecameLeader(context.Background())

	comp.mu.RLock()
	assert.True(t, comp.isLeader)
	comp.mu.RUnlock()

	// No patches to replay — should NOT publish completed event
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleLostLeadership(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	setLeader(comp)

	comp.handleLostLeadership()

	comp.mu.RLock()
	assert.False(t, comp.isLeader)
	comp.mu.RUnlock()
}

func TestApplyVariant_ChecksumSkip(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
	})

	// First apply — should apply
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	event1 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, event1.AppliedCount)
	assert.Equal(t, 0, event1.SkippedCount)

	// Second apply with same patches — should skip via checksum
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	event2 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 0, event2.AppliedCount)
	assert.Equal(t, 1, event2.SkippedCount)
}

func TestApplyVariant_DifferentPayloadNotSkipped(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches1 := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
	})
	patches2 := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "False"}}},
	})

	// First apply
	comp.applyVariant(context.Background(), patches1, events.StatusPatchPhaseRendered)
	event1 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, event1.AppliedCount)

	// Different payload — should NOT skip
	comp.applyVariant(context.Background(), patches2, events.StatusPatchPhaseRendered)
	event2 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, event2.AppliedCount)
	assert.Equal(t, 0, event2.SkippedCount)
}

func TestApplyVariant_MissingVariantSkipsQuietly(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	// Patches only have "rendered" variant
	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{}},
	})

	// Ask for "deployed" which doesn't exist
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)

	// Should publish completion with 0 applied, 0 skipped (variant didn't exist)
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 0, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

func TestApplyVariant_GVRResolveError(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := &mockGVRResolver{err: fmt.Errorf("unknown resource")}
	comp := newTestComponent(bus, fakeClient, resolver)

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)

	// Should publish StatusUpdateFailedEvent
	failedEvent := testutil.WaitForEvent[*events.StatusUpdateFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "default", failedEvent.Namespace)
	assert.Equal(t, "my-ingress", failedEvent.Name)
	assert.Contains(t, failedEvent.Error, "unknown resource")
	assert.False(t, failedEvent.Retriable)
}

func TestApplyVariant_SSAPatchError(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	// Register a reactor that returns an error for patch operations
	fakeClient.PrependReactor("patch", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("conflict: resource version mismatch")
	})

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)

	// Should publish StatusUpdateFailedEvent
	failedEvent := testutil.WaitForEvent[*events.StatusUpdateFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "default", failedEvent.Namespace)
	assert.Equal(t, "my-ingress", failedEvent.Name)
	assert.Contains(t, failedEvent.Error, "conflict")
	assert.True(t, failedEvent.Retriable)

	// Should also publish StatusUpdateCompletedEvent with 0 applied
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 0, completedEvent.AppliedCount)
}

func TestApplyVariant_SSAPayloadStructure(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	bus.Start()

	var capturedPatchData []byte
	fakeClient.PrependReactor("patch", "ingresses", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		capturedPatchData = patchAction.GetPatch()
		return true, nil, nil
	})

	statusPayload := map[string]interface{}{
		"loadBalancer": map[string]interface{}{
			"ingress": []interface{}{
				map[string]interface{}{"ip": "10.0.0.1"},
			},
		},
	}
	patches := newTestPatches(map[string]map[string]interface{}{
		"deployed": statusPayload,
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)

	require.NotNil(t, capturedPatchData)

	var payload map[string]interface{}
	err := json.Unmarshal(capturedPatchData, &payload)
	require.NoError(t, err)

	// Verify SSA payload structure
	assert.Equal(t, "networking.k8s.io/v1", payload["apiVersion"])
	assert.Equal(t, "Ingress", payload["kind"])

	metadata, ok := payload["metadata"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "default", metadata["namespace"])
	assert.Equal(t, "my-ingress", metadata["name"])

	status, ok := payload["status"].(map[string]interface{})
	require.True(t, ok)
	assert.NotNil(t, status["loadBalancer"])
}

func TestApplyVariant_SSAPatchOptions(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	bus.Start()

	var capturedAction k8stesting.PatchAction
	fakeClient.PrependReactor("patch", "ingresses", func(action k8stesting.Action) (bool, runtime.Object, error) {
		capturedAction = action.(k8stesting.PatchAction)
		return true, nil, nil
	})

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)

	require.NotNil(t, capturedAction)
	assert.Equal(t, "status", capturedAction.GetSubresource())
	assert.Equal(t, "default", capturedAction.GetNamespace())
	assert.Equal(t, "my-ingress", capturedAction.GetName())
}

func TestApplyVariant_MultiplePatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := []templating.StatusPatch{
		{
			Namespace:  "default",
			Name:       "ingress-1",
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Variants: map[string]map[string]interface{}{
				"deployed": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
			},
		},
		{
			Namespace:  "production",
			Name:       "ingress-2",
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Variants: map[string]map[string]interface{}{
				"deployed": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
			},
		},
		{
			Namespace:  "default",
			Name:       "my-gateway",
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "Gateway",
			Variants: map[string]map[string]interface{}{
				"deployed": {"conditions": []interface{}{map[string]interface{}{"type": "Programmed"}}},
			},
		},
	}

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 3, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

func TestLeadershipTransition_FullCycle(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = comp.Start(ctx)
	}()
	time.Sleep(testutil.StartupDelay)

	// 1. Receive template rendered while not leader — caches only
	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Accepted"}}},
	})
	bus.Publish(events.NewTemplateRenderedEvent(
		"config", nil, patches, 0, 50, "test", "hash1", false,
	))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	// 2. Become leader — should replay cached patches
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)

	// 3. Lose leadership
	bus.Publish(events.NewLostLeadershipEvent("test-identity", "demoted"))
	time.Sleep(testutil.StartupDelay) // Wait for event to process

	// 4. Receive another template rendered — should not apply
	testutil.DrainChannel(eventChan)
	patches2 := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Accepted", "status": "True"}}},
	})
	bus.Publish(events.NewTemplateRenderedEvent(
		"config2", nil, patches2, 0, 50, "test", "hash2", false,
	))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestRestMapperResolver_Resolve(t *testing.T) {
	resolver := NewRestMapperResolver()

	tests := []struct {
		name       string
		apiVersion string
		kind       string
		wantGVR    schema.GroupVersionResource
		wantErr    bool
	}{
		{
			name:       "Ingress",
			apiVersion: "networking.k8s.io/v1",
			kind:       "Ingress",
			wantGVR:    schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		},
		{
			name:       "Gateway",
			apiVersion: "gateway.networking.k8s.io/v1",
			kind:       "Gateway",
			wantGVR:    schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"},
		},
		{
			name:       "HTTPRoute",
			apiVersion: "gateway.networking.k8s.io/v1",
			kind:       "HTTPRoute",
			wantGVR:    schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"},
		},
		{
			name:       "Policy (y suffix)",
			apiVersion: "gateway.networking.k8s.io/v1alpha2",
			kind:       "BackendPolicy",
			wantGVR:    schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "backendpolicies"},
		},
		{
			name:       "core group Service",
			apiVersion: "v1",
			kind:       "Service",
			wantGVR:    schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"},
		},
		{
			name:       "invalid apiVersion",
			apiVersion: "///invalid",
			kind:       "Foo",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvr, err := resolver.Resolve(tt.apiVersion, tt.kind)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantGVR, gvr)
		})
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"Ingress", "ingresses"},
		{"Gateway", "gateways"},
		{"HTTPRoute", "httproutes"},
		{"Service", "services"},
		{"BackendPolicy", "backendpolicies"},
		{"Address", "addresses"},
		{"Endpoints", "endpointses"}, // Known limitation: static pluralization doesn't handle all edge cases
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			assert.Equal(t, tt.want, pluralize(tt.kind))
		})
	}
}

func TestHandleEvent_RoutesCorrectly(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	bus.Start()
	ctx := context.Background()

	// Verify each event type is routed without panics
	comp.handleEvent(ctx, events.NewTemplateRenderedEvent(
		"config", nil, nil, 0, 50, "test", "hash", false,
	))
	comp.handleEvent(ctx, events.NewReconciliationCompletedEvent(100))
	comp.handleEvent(ctx, events.NewReconciliationFailedEvent("err", "deploy"))
	comp.handleEvent(ctx, events.NewBecameLeaderEvent("identity"))
	comp.handleEvent(ctx, events.NewLostLeadershipEvent("identity", "reason"))
}

func TestApplyVariant_DoesNotCacheOnFailure(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	bus.Start()

	// Make patch fail
	fakeClient.PrependReactor("patch", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("server unavailable")
	})

	patches := newTestPatches(map[string]map[string]interface{}{
		"rendered": {"conditions": []interface{}{map[string]interface{}{"type": "Ready"}}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)

	// Checksum should NOT be cached on failure
	comp.mu.RLock()
	assert.Empty(t, comp.checksumCache)
	comp.mu.RUnlock()
}

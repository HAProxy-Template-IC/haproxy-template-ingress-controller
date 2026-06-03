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
	"k8s.io/apimachinery/pkg/api/meta"
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

func newTestPatches(variants map[string]map[string]any) []templating.StatusPatch {
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

// TestHandleTemplateRendered_NoApplyWhenNotLeader pins that a non-leader
// replica reads event.StatusPatches and does NOT call the SSA path. There
// is no cached state to assert on after the call — the applier is stateless.
func TestHandleTemplateRendered_NoApplyWhenNotLeader(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})

	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, patches, nil, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleTemplateRendered_AppliesWhenLeader(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	resolver := newTestResolver()
	comp := newTestComponent(bus, fakeClient, resolver)

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})

	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, patches, nil, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	// Should publish StatusUpdateCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

// (TestHandleTemplateRendered_SkipsWhenNotLeader removed: redundant with
// TestHandleTemplateRendered_NoApplyWhenNotLeader above. Both pinned the
// same not-leader-no-apply contract.)

func TestHandleTemplateRendered_SkipsEmptyPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	templateEvent := events.NewTemplateRenderedEvent(
		"haproxy config", nil, nil, nil, 0, 100, "test", "abc123", false,
	)
	comp.handleTemplateRendered(context.Background(), templateEvent)

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// deployedPatches builds a status-patch slice carrying a "deployed" variant.
// Used by deploy-completed / deploy-skipped tests since those events now
// carry the patches inline.
func deployedPatches() []templating.StatusPatch {
	return newTestPatches(map[string]map[string]any{
		"deployed": {"conditions": []any{map[string]any{"type": "Programmed", "status": "True"}}},
	})
}

func TestHandleDeploymentCompleted_AppliesDeployedVariant(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentCompleted(context.Background(), events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 1, Succeeded: 1, StatusPatches: deployedPatches(),
	}))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

// TestHandleDeploymentCompleted_SkipsWithoutPatches: an event with no patches
// is ignored. This used to be "without cached patches"; the cache is gone
// and the equivalent signal is now "event.StatusPatches is empty".
func TestHandleDeploymentCompleted_SkipsWithoutPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentCompleted(context.Background(), events.NewDeploymentCompletedEvent(&events.DeploymentResult{Total: 1, Succeeded: 1}))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestHandleDeploymentCompleted_SkipsZeroEndpoints exercises the "no HAProxy
// pods reachable" path. The deployer publishes DeploymentCompletedEvent with
// Total=0 in that case; the status-applier must NOT flip Accepted=True since
// no HAProxy actually has the new config.
func TestHandleDeploymentCompleted_SkipsZeroEndpoints(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentCompleted(context.Background(), events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 0, Succeeded: 0, StatusPatches: deployedPatches(),
	}))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestHandleDeploymentSkipped_AppliesDeployedVariant exercises the converged
// no-op path: the deployer publishes DeploymentSkippedEvent when every
// endpoint already serves the latest rendered config. The status-applier
// must treat this equivalently to DeploymentCompletedEvent for the purpose
// of writing the "deployed" patch variant — the data plane IS at this
// config, status conditions gated on data-plane readiness should reflect it.
func TestHandleDeploymentSkipped_AppliesDeployedVariant(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentSkipped(context.Background(), events.NewDeploymentSkippedEvent(1, "config_unchanged", "hash", "podset", deployedPatches()))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

// TestHandleDeploymentSkipped_SkipsZeroEndpoints mirrors the
// completed-event zero-endpoint guard: if Total=0, there's no data plane
// to claim Programmed against.
func TestHandleDeploymentSkipped_SkipsZeroEndpoints(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentSkipped(context.Background(), events.NewDeploymentSkippedEvent(0, "config_unchanged", "hash", "podset", deployedPatches()))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleDeploymentSkipped_SkipsWithoutPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	// Empty patches; skip event should be ignored.
	comp.handleDeploymentSkipped(context.Background(), events.NewDeploymentSkippedEvent(1, "config_unchanged", "hash", "podset", nil))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestHandleReconciliationFailed_DeployPhase(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"deployFailed": {"conditions": []any{map[string]any{"type": "Programmed", "status": "False"}}},
	})
	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("deploy error", "deploy", patches))

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

	patches := newTestPatches(map[string]map[string]any{
		"renderFailed": {"conditions": []any{map[string]any{"type": "Accepted", "status": "False"}}},
	})
	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("render error", "render", patches))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRenderFailed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

// TestHandleReconciliationFailed_ValidationPhase: validation failures get
// their own StatusPatchPhaseValidateFailed variant — distinct from
// renderFailed (templating produced no output) and deployFailed (deploy
// attempted and rolled back). See issue #44.
func TestHandleReconciliationFailed_ValidationPhase(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"validateFailed": {"conditions": []any{map[string]any{"type": "Accepted", "status": "False"}}},
	})
	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("validation error", "validation", patches))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseValidateFailed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

// TestHandleReconciliationFailed_UnknownPhaseFallsBackToDeployFailed: phases
// other than "render" and "validation" — including the existing "deploy"
// and any future labels not yet wired through — fall through to the
// deployFailed variant. This preserves the historical contract for the
// Coordinator's "deploy" emissions while keeping the door open for new
// phases without changing this mapping.
func TestHandleReconciliationFailed_UnknownPhaseFallsBackToDeployFailed(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"deployFailed": {"conditions": []any{map[string]any{"type": "Programmed", "status": "False"}}},
	})
	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("err", "", patches))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployFailed, completedEvent.Phase)
}

// TestHandleReconciliationFailed_SkipsWithoutPatches: a failure event without
// patches (e.g. failure before any successful render) is silently ignored —
// there's nothing to apply.
func TestHandleReconciliationFailed_SkipsWithoutPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleReconciliationFailed(context.Background(), events.NewReconciliationFailedEvent("err", "render", nil))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestHandleBecameLeader_DoesNotReplayPatches: with the stateless applier,
// becoming leader does NOT replay any patches. The Reconciler fires a fresh
// reconciliation on BecameLeaderEvent, which produces a fresh
// TemplateRenderedEvent the applier consumes normally.
func TestHandleBecameLeader_DoesNotReplayPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	comp.handleBecameLeader(context.Background())

	comp.mu.RLock()
	assert.True(t, comp.isLeader)
	comp.mu.RUnlock()

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
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

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
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

	patches1 := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	})
	patches2 := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}},
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
	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{}},
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

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{}},
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

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
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

	statusPayload := map[string]any{
		"loadBalancer": map[string]any{
			"ingress": []any{
				map[string]any{"ip": "10.0.0.1"},
			},
		},
	}
	patches := newTestPatches(map[string]map[string]any{
		"deployed": statusPayload,
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)

	require.NotNil(t, capturedPatchData)

	var payload map[string]any
	err := json.Unmarshal(capturedPatchData, &payload)
	require.NoError(t, err)

	// Verify SSA payload structure
	assert.Equal(t, "networking.k8s.io/v1", payload["apiVersion"])
	assert.Equal(t, "Ingress", payload["kind"])

	metadata, ok := payload["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "default", metadata["namespace"])
	assert.Equal(t, "my-ingress", metadata["name"])

	status, ok := payload["status"].(map[string]any)
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

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{}},
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
			Variants: map[string]map[string]any{
				"deployed": {"conditions": []any{map[string]any{"type": "Ready"}}},
			},
		},
		{
			Namespace:  "production",
			Name:       "ingress-2",
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Variants: map[string]map[string]any{
				"deployed": {"conditions": []any{map[string]any{"type": "Ready"}}},
			},
		},
		{
			Namespace:  "default",
			Name:       "my-gateway",
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "Gateway",
			Variants: map[string]map[string]any{
				"deployed": {"conditions": []any{map[string]any{"type": "Programmed"}}},
			},
		},
	}

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 3, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

// TestLeadershipTransition_FullCycle exercises non-leader → leader → not-leader
// transitions end-to-end with the stateless applier. Key contracts:
//   - Non-leader templateRendered: no apply (event consumed, no SSA).
//   - BecameLeader: no patch replay (the Reconciler triggers a fresh
//     reconciliation separately; we just flip isLeader).
//   - Post-leader templateRendered: apply.
//   - LostLeadership: subsequent rendered events do NOT apply.
func TestLeadershipTransition_FullCycle(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	ctx := t.Context()

	go func() {
		_ = comp.Start(ctx)
	}()
	time.Sleep(testutil.StartupDelay)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Accepted"}}},
	})

	// 1. TemplateRendered while not leader — no apply.
	bus.Publish(events.NewTemplateRenderedEvent(
		"config", nil, patches, nil, 0, 50, "test", "hash1", false,
	))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	// 2. Become leader — does NOT replay anything (stateless applier).
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	// 3. TemplateRendered after becoming leader applies normally.
	bus.Publish(events.NewTemplateRenderedEvent(
		"config", nil, patches, nil, 0, 50, "test", "hash1", false,
	))
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)

	// 4. Lose leadership.
	bus.Publish(events.NewLostLeadershipEvent("test-identity", "demoted"))
	time.Sleep(testutil.StartupDelay) // Wait for event to process

	// 5. Receive another template rendered — should NOT apply.
	testutil.DrainChannel(eventChan)
	patches2 := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}}},
	})
	bus.Publish(events.NewTemplateRenderedEvent(
		"config2", nil, patches2, nil, 0, 50, "test", "hash2", false,
	))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// newFakeRESTMapper builds a RESTMapper that knows a handful of kinds,
// including resources whose plural a naive English pluralizer would get
// WRONG ("Mesh" → "meshes", not "meshs") and a fully custom plural
// ("Widget" → "widgetgrid"). A correct resolver must return the mapper's
// answer, never a guessed plural.
func newFakeRESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	add := func(group, kind, plural, singular string) {
		m.AddSpecific(
			schema.GroupVersionKind{Group: group, Version: "v1", Kind: kind},
			schema.GroupVersionResource{Group: group, Version: "v1", Resource: plural},
			schema.GroupVersionResource{Group: group, Version: "v1", Resource: singular},
			meta.RESTScopeNamespace,
		)
	}
	add("networking.k8s.io", "Ingress", "ingresses", "ingress")
	add("", "Service", "services", "service")
	add("example.com", "Mesh", "meshes", "mesh")         // naive pluralizer → "meshs"
	add("example.com", "Widget", "widgetgrid", "widget") // custom plural unrelated to kind
	return m
}

// resettableFakeMapper simulates a deferred discovery mapper whose cache
// predates a late-registered CRD: RESTMapping returns NoMatch until Reset()
// refreshes discovery, after which it delegates to an inner mapper that knows
// the kind.
type resettableFakeMapper struct {
	meta.RESTMapper
	reset bool
}

func (m *resettableFakeMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if !m.reset {
		return nil, &meta.NoKindMatchError{GroupKind: gk}
	}
	return m.RESTMapper.RESTMapping(gk, versions...)
}

func (m *resettableFakeMapper) Reset() { m.reset = true }

// A late-registered CRD whose kind isn't in the mapper's initial discovery
// cache must resolve after the resolver refreshes discovery via Reset() —
// rather than failing permanently for the iteration's lifetime.
func TestRestMapperResolver_Resolve_ResetsOnNoMatchThenRetries(t *testing.T) {
	rm := &resettableFakeMapper{RESTMapper: newFakeRESTMapper()}
	resolver := NewRestMapperResolver(rm)

	gvr, err := resolver.Resolve("networking.k8s.io/v1", "Ingress")

	require.NoError(t, err)
	assert.True(t, rm.reset, "resolver should Reset() the mapper on a NoMatch error")
	assert.Equal(t, "ingresses", gvr.Resource)
}

func TestRestMapperResolver_Resolve(t *testing.T) {
	resolver := NewRestMapperResolver(newFakeRESTMapper())

	tests := []struct {
		name         string
		apiVersion   string
		kind         string
		wantResource string
		wantErr      bool
	}{
		{
			name:         "Ingress",
			apiVersion:   "networking.k8s.io/v1",
			kind:         "Ingress",
			wantResource: "ingresses",
		},
		{
			name:         "core group Service",
			apiVersion:   "v1",
			kind:         "Service",
			wantResource: "services",
		},
		{
			// A naive pluralizer would produce "meshs"; the mapper knows "meshes".
			name:         "irregular plural comes from the mapper",
			apiVersion:   "example.com/v1",
			kind:         "Mesh",
			wantResource: "meshes",
		},
		{
			// A CRD may set spec.names.plural to anything; only the mapper knows it.
			name:         "custom plural comes from the mapper",
			apiVersion:   "example.com/v1",
			kind:         "Widget",
			wantResource: "widgetgrid",
		},
		{
			// Unknown kinds must error, not silently guess a plural.
			name:       "unknown kind errors",
			apiVersion: "example.com/v1",
			kind:       "Unknown",
			wantErr:    true,
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
			gv, _ := schema.ParseGroupVersion(tt.apiVersion)
			assert.Equal(t, gv.WithResource(tt.wantResource), gvr)
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
		"config", nil, nil, nil, 0, 50, "test", "hash", false,
	))
	comp.handleEvent(ctx, events.NewDeploymentCompletedEvent(&events.DeploymentResult{Total: 1, Succeeded: 1}))
	comp.handleEvent(ctx, events.NewReconciliationFailedEvent("err", "deploy", nil))
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

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)

	// Checksum should NOT be cached on failure
	comp.mu.RLock()
	assert.Empty(t, comp.checksumCache)
	comp.mu.RUnlock()
}

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
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
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
			Namespace:       "default",
			Name:            "my-ingress",
			APIVersion:      "networking.k8s.io/v1",
			Kind:            "Ingress",
			UID:             "uid-my-ingress",
			ResourceVersion: "1",
			Variants:        variants,
		},
	}
}

func newTestStatusPatchSnapshot(tb testing.TB, variants map[string]map[string]any) *templating.StatusPatchSnapshot {
	tb.Helper()
	return newTestStatusPatchSnapshotFromPatches(tb, newTestPatches(variants))
}

func newTestStatusPatchSnapshotFromPatches(
	tb testing.TB,
	patches []templating.StatusPatch,
) *templating.StatusPatchSnapshot {
	tb.Helper()
	collector := templating.NewStatusPatchCollector()
	for index := range patches {
		patch := &patches[index]
		require.NoError(tb, collector.RegisterWithLineage(
			patch.Namespace, patch.Name, patch.APIVersion, patch.Kind,
			patch.UID, patch.ResourceVersion, patch.Variants,
		))
	}
	snapshot, err := collector.Snapshot()
	require.NoError(tb, err)
	return snapshot
}

func newTestRenderOccurrence(tb testing.TB, patches []templating.StatusPatch) *rendercycle.Occurrence {
	tb.Helper()
	status := newTestStatusPatchSnapshotFromPatches(tb, patches)
	cycle := testutil.NewRenderCycleFixture(tb).Snapshot(tb, "global\n", status, nil)
	rendered, err := events.NewTemplateRenderedEventWithCycle(cycle, 0, "test", true)
	require.NoError(tb, err)
	occurrence, err := rendered.RenderOccurrence()
	require.NoError(tb, err)
	return occurrence
}

func newTestResourcesAppliedEvent(tb testing.TB, patches []templating.StatusPatch) *events.ResourcesAppliedEvent {
	tb.Helper()
	event, err := events.NewResourcesAppliedEventWithCycle(newTestRenderOccurrence(tb, patches))
	require.NoError(tb, err)
	return event
}

func newTestDeploymentCompletedEvent(
	tb testing.TB,
	total, succeeded, failed, pendingReloads int,
	patches []templating.StatusPatch,
) *events.DeploymentCompletedEvent {
	tb.Helper()
	result, err := events.NewDeploymentResultWithOccurrence(newTestRenderOccurrence(tb, patches))
	require.NoError(tb, err)
	result.Total = total
	result.Succeeded = succeeded
	result.Failed = failed
	result.PendingReloads = pendingReloads
	event, err := events.NewDeploymentCompletedEventWithCycle(result)
	require.NoError(tb, err)
	return event
}

func newTestDeploymentSkippedEvent(
	tb testing.TB,
	total int,
	reason string,
	patches []templating.StatusPatch,
) *events.DeploymentSkippedEvent {
	tb.Helper()
	event, err := events.NewDeploymentSkippedEventWithCycle(
		newTestRenderOccurrence(tb, patches), total, reason, "podset",
	)
	require.NoError(tb, err)
	return event
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
	var revision atomic.Uint64
	revision.Store(1)
	client.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction, ok := action.(k8stesting.PatchAction)
		if !ok {
			return true, nil, fmt.Errorf("unexpected action %T", action)
		}
		var payload map[string]any
		if err := json.Unmarshal(patchAction.GetPatch(), &payload); err != nil {
			return true, nil, err
		}
		metadata, ok := payload["metadata"].(map[string]any)
		if !ok {
			return true, nil, fmt.Errorf("patch metadata has type %T", payload["metadata"])
		}
		uid, _ := metadata["uid"].(string)
		result := &unstructured.Unstructured{}
		result.SetUID(types.UID(uid))
		result.SetResourceVersion(strconv.FormatUint(revision.Add(1), 10))
		return true, result, nil
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
	assert.NotNil(t, comp.Base, "must embed the component.Base event loop")
	assert.NotNil(t, comp.statusCache)
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
// replica receives the event and does NOT call the SSA path. There
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

	renderedEvent := newTestResourcesAppliedEvent(t, patches)
	comp.handleResourcesApplied(context.Background(), renderedEvent)

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

	renderedEvent := newTestResourcesAppliedEvent(t, patches)
	comp.handleResourcesApplied(context.Background(), renderedEvent)

	// Should publish StatusUpdateCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
	assert.Equal(t, 0, completedEvent.SkippedCount)
}

func TestHandleResourcesAppliedMaterializesSnapshotPhaseOnly(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()
	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
		"deployed": {"conditions": []any{map[string]any{"type": "Programmed"}}},
	})
	comp.handleResourcesApplied(context.Background(), newTestResourcesAppliedEvent(t, patches))

	completed := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completed.Phase)
	assert.Equal(t, 1, completed.AppliedCount)
}

func TestSuccessHandlersRejectUnauthenticatedLegacyPayloads(t *testing.T) {
	statusSnapshot := newTestStatusPatchSnapshot(t, map[string]map[string]any{
		"rendered": {"conditions": []any{}},
	})
	statusPatches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{}},
	})
	for _, test := range []struct {
		name   string
		handle func(*Component)
	}{
		{
			name: "resources applied with invalid snapshot shadow",
			handle: func(comp *Component) {
				comp.handleResourcesApplied(context.Background(),
					events.NewResourcesAppliedEventWithStatusSnapshot(&templating.StatusPatchSnapshot{}))
			},
		},
		{
			name: "resources applied with mutable and immutable shadows",
			handle: func(comp *Component) {
				event := events.NewResourcesAppliedEventWithStatusSnapshot(statusSnapshot)
				event.StatusPatches = statusPatches
				comp.handleResourcesApplied(context.Background(), event)
			},
		},
		{
			name: "deployment completed",
			handle: func(comp *Component) {
				comp.handleDeploymentCompleted(context.Background(), events.NewDeploymentCompletedEvent(
					&events.DeploymentResult{Total: 1, Succeeded: 1, StatusPatchSnapshot: statusSnapshot},
				))
			},
		},
		{
			name: "deployment skipped",
			handle: func(comp *Component) {
				comp.handleDeploymentSkipped(context.Background(), events.NewDeploymentSkippedEvent(
					1, "config_unchanged", "hash", "podset", statusPatches,
				))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := testutil.NewTestBus()
			fakeClient := newFakeDynamicClientWithPatchSuccess()
			comp := newTestComponent(bus, fakeClient, newTestResolver())
			eventChan := bus.Subscribe("test", 50)
			bus.Start()
			setLeader(comp)

			test.handle(comp)

			failed := testutil.WaitForEvent[*events.StatusUpdateFailedEvent](t, eventChan, testutil.EventTimeout)
			assert.Equal(t, "status-patch-snapshot", failed.GVR)
			assert.Empty(t, fakeClient.Actions())
		})
	}
}

func TestHandleResourcesAppliedCycleIgnoresPoisonedStatusShadows(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()
	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Authenticated"}}},
	})
	event := newTestResourcesAppliedEvent(t, patches)
	event.CycleSnapshot = nil
	event.RenderProof = "poison"
	event.StatusPatches = newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "MutablePoison"}}},
	})
	event.StatusPatchSnapshot = newTestStatusPatchSnapshot(t, map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "SnapshotPoison"}}},
	})

	comp.handleResourcesApplied(context.Background(), event)

	completed := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, completed.AppliedCount)
	require.Len(t, fakeClient.Actions(), 1)
	patchAction, ok := fakeClient.Actions()[0].(k8stesting.PatchAction)
	require.True(t, ok)
	patch := string(patchAction.GetPatch())
	assert.Contains(t, patch, "Authenticated")
	assert.NotContains(t, patch, "MutablePoison")
	assert.NotContains(t, patch, "SnapshotPoison")
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

	renderedEvent := newTestResourcesAppliedEvent(t, nil)
	comp.handleResourcesApplied(context.Background(), renderedEvent)

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

	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 1, 1, 0, 0, deployedPatches(),
	))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployed, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)
}

// TestHandleDeploymentCompleted_PartialSuccessIsNotProgrammed: a partial deploy
// (Succeeded>0 && Failed>0) must NOT apply the "deployed" variant.
//
// This asserts the opposite of what it used to. Gateway API defines Programmed as
// the data plane being configured, and a fleet where one replica still serves the
// previous config is not that: NodePort round-robins, so a request landing on the
// un-pushed replica gets the old routing (the 503 SC-- documented in
// ingress_rolling_restart_test.go). Reporting deployed there advertises an address
// the fleet does not uniformly serve, and external-dns and cert-manager act on it.
func TestHandleDeploymentCompleted_PartialSuccessIsNotProgrammed(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 2, 1, 1, 0, deployedPatches(),
	))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployFailed, completedEvent.Phase,
		"a fleet with one replica on the old config is not Programmed")
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

	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 1, 1, 0, 0, nil,
	))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// A fleet that accepted the config behind a paced reload is neither deployed
// nor failed: no variant applies until the deployer observes the fleet running
// it and publishes a DeploymentSkippedEvent.
func TestHandleDeploymentCompleted_PendingReloadsApplyNoVariant(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 2, 0, 0, 2, deployedPatches(),
	))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	comp.handleDeploymentSkipped(context.Background(), newTestDeploymentSkippedEvent(
		t, 2, events.SkipReasonReloadObserved, deployedPatches(),
	))
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployed, completedEvent.Phase)
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

	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 0, 0, 0, 0, deployedPatches(),
	))

	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestHandleDeploymentCompleted_FullFailureAppliesDeployFailedVariant pins that
// a fully-failed deploy (endpoints existed, but none took the new config:
// Total>0, Succeeded==0) surfaces the "deployFailed" variant (Programmed=False
// with a reason) rather than silently freezing the last status. The per-endpoint
// deploy path emits no ReconciliationFailedEvent, so this handler is the only
// place that can signal the failure. Distinct from the Total==0 "nothing
// deployed" case, which stays a no-op.
func TestHandleDeploymentCompleted_FullFailureAppliesDeployFailedVariant(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"deployFailed": {"conditions": []any{map[string]any{
			"type": "Programmed", "status": "False", "reason": "DeploymentFailed",
		}}},
	})
	comp.handleDeploymentCompleted(context.Background(), newTestDeploymentCompletedEvent(
		t, 2, 0, 2, 0, patches,
	))

	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseDeployFailed, completedEvent.Phase,
		"a fully-failed deploy must apply the deployFailed (Programmed=False) variant")
	assert.Equal(t, 1, completedEvent.AppliedCount)
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

	comp.handleDeploymentSkipped(context.Background(), newTestDeploymentSkippedEvent(
		t, 1, "config_unchanged", deployedPatches(),
	))

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

	comp.handleDeploymentSkipped(context.Background(), newTestDeploymentSkippedEvent(
		t, 0, "config_unchanged", deployedPatches(),
	))

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
	comp.handleDeploymentSkipped(context.Background(), newTestDeploymentSkippedEvent(
		t, 1, "config_unchanged", nil,
	))

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
// ResourcesAppliedEvent the applier consumes normally.
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

func TestHandleBecameLeader_ClearsStatusCache(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	bus.Start()

	comp.mu.Lock()
	comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"] = statusCacheEntry{
		uid: "uid-my-ingress",
	}
	comp.mu.Unlock()

	comp.handleBecameLeader(context.Background())

	comp.mu.RLock()
	assert.Empty(t, comp.statusCache)
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

func TestApplyVariant_ExactObservedLineageSkips(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())

	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	event1 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, event1.AppliedCount)
	assert.Equal(t, 0, event1.SkippedCount)

	patches[0].ResourceVersion = "2"
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	event2 := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 0, event2.AppliedCount)
	assert.Equal(t, 1, event2.SkippedCount)
}

func TestApplyVariant_CrossPhaseOverwriteForcesReapply(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Programmed", "status": "False"}}},
		"deployed": {"conditions": []any{map[string]any{"type": "Programmed", "status": "True"}}},
	})
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	first := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, first.AppliedCount)

	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseDeployed)
	second := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, second.AppliedCount)

	patches[0].ResourceVersion = "3"
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	third := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, third.AppliedCount)
	assert.Equal(t, 0, third.SkippedCount)
	assert.Len(t, fakeClient.Actions(), 3)
}

func TestApplyVariant_RecreatedResourceCannotReuseStatusCache(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	first := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, first.AppliedCount)

	patches[0].UID = "uid-recreated"
	patches[0].ResourceVersion = "1"
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	second := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, second.AppliedCount)
	assert.Equal(t, 0, second.SkippedCount)
	assert.Len(t, fakeClient.Actions(), 2)
}

func TestApplyVariant_ABARequiresObservedLineage(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"status": "A"}}},
	})
	for index, value := range []string{"A", "B", "A"} {
		patches[0].Variants["rendered"] = map[string]any{
			"conditions": []any{map[string]any{"status": value}},
		}
		patches[0].ResourceVersion = strconv.Itoa(index + 1)
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		completed := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Equal(t, 1, completed.AppliedCount)
		assert.Equal(t, 0, completed.SkippedCount)
	}
	assert.Len(t, fakeClient.Actions(), 3)
}

func TestApplyVariant_MissingLineageAppliesWithoutCache(t *testing.T) {
	tests := map[string]struct {
		uid             string
		resourceVersion string
	}{
		"missing UID":              {resourceVersion: "1"},
		"missing resource version": {uid: "uid-my-ingress"},
		"both missing":             {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			bus := testutil.NewTestBus()
			fakeClient := newFakeDynamicClientWithPatchSuccess()
			comp := newTestComponent(bus, fakeClient, newTestResolver())
			eventChan := bus.Subscribe("test", 50)
			bus.Start()

			patches := newTestPatches(map[string]map[string]any{
				"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
			})
			patches[0].UID = test.uid
			patches[0].ResourceVersion = test.resourceVersion
			comp.mu.Lock()
			comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"] = statusCacheEntry{
				uid: "poison", baseResourceVersion: "poison", latestResourceVersion: "poison",
			}
			comp.mu.Unlock()

			for range 2 {
				comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
				completed := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](
					t, eventChan, testutil.EventTimeout,
				)
				assert.Equal(t, 1, completed.AppliedCount)
				assert.Zero(t, completed.SkippedCount)
			}

			actions := fakeClient.Actions()
			require.Len(t, actions, 2)
			for _, action := range actions {
				patchAction, ok := action.(k8stesting.PatchAction)
				require.True(t, ok)
				var payload map[string]any
				require.NoError(t, json.Unmarshal(patchAction.GetPatch(), &payload))
				metadata, ok := payload["metadata"].(map[string]any)
				require.True(t, ok)
				assert.NotContains(t, metadata, "uid")
				assert.NotContains(t, metadata, "resourceVersion")
			}
			comp.mu.RLock()
			assert.Empty(t, comp.statusCache)
			comp.mu.RUnlock()
		})
	}
}

func TestApplyVariant_MissingLineageFailureDoesNotCache(t *testing.T) {
	tests := map[string]struct {
		uid             string
		resourceVersion string
	}{
		"missing UID":              {resourceVersion: "1"},
		"missing resource version": {uid: "uid-my-ingress"},
		"both missing":             {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			bus := testutil.NewTestBus()
			fakeClient := newFakeDynamicClient()
			fakeClient.PrependReactor("patch", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("server unavailable")
			})
			comp := newTestComponent(bus, fakeClient, newTestResolver())
			eventChan := bus.Subscribe("test", 50)
			bus.Start()

			patches := newTestPatches(map[string]map[string]any{
				"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
			})
			patches[0].UID = test.uid
			patches[0].ResourceVersion = test.resourceVersion
			comp.mu.Lock()
			comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"] = statusCacheEntry{
				uid: "poison", baseResourceVersion: "poison", latestResourceVersion: "poison",
			}
			comp.mu.Unlock()

			comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
			failed := testutil.WaitForEvent[*events.StatusUpdateFailedEvent](t, eventChan, testutil.EventTimeout)
			assert.Contains(t, failed.Error, "server unavailable")
			completed := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](
				t, eventChan, testutil.EventTimeout,
			)
			assert.Zero(t, completed.AppliedCount)
			assert.Zero(t, completed.SkippedCount)
			assert.Len(t, fakeClient.Actions(), 1)
			comp.mu.RLock()
			assert.Empty(t, comp.statusCache)
			comp.mu.RUnlock()
		})
	}
}

func TestApplyVariant_MissingAndExactLineageTransitions(t *testing.T) {
	t.Run("exact missing exact cannot reuse pre-missing cache", func(t *testing.T) {
		bus := testutil.NewTestBus()
		fakeClient := newFakeDynamicClientWithPatchSuccess()
		comp := newTestComponent(bus, fakeClient, newTestResolver())
		eventChan := bus.Subscribe("test", 50)
		bus.Start()

		patches := newTestPatches(map[string]map[string]any{
			"rendered": {"conditions": []any{map[string]any{"status": "A"}}},
		})
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		first := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Equal(t, 1, first.AppliedCount)

		patches[0].UID = ""
		patches[0].ResourceVersion = ""
		patches[0].Variants["rendered"] = map[string]any{
			"conditions": []any{map[string]any{"status": "B"}},
		}
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		missing := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Equal(t, 1, missing.AppliedCount)
		assert.Zero(t, missing.SkippedCount)

		patches[0].UID = "uid-my-ingress"
		patches[0].ResourceVersion = "2"
		patches[0].Variants["rendered"] = map[string]any{
			"conditions": []any{map[string]any{"status": "A"}},
		}
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		afterMissing := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](
			t, eventChan, testutil.EventTimeout,
		)
		assert.Equal(t, 1, afterMissing.AppliedCount)
		assert.Zero(t, afterMissing.SkippedCount)
		assert.Len(t, fakeClient.Actions(), 3)

		patches[0].ResourceVersion = "4"
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		exactRepeat := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](
			t, eventChan, testutil.EventTimeout,
		)
		assert.Zero(t, exactRepeat.AppliedCount)
		assert.Equal(t, 1, exactRepeat.SkippedCount)
		assert.Len(t, fakeClient.Actions(), 3)
	})

	t.Run("missing exact establishes a fresh exact cache", func(t *testing.T) {
		bus := testutil.NewTestBus()
		fakeClient := newFakeDynamicClientWithPatchSuccess()
		comp := newTestComponent(bus, fakeClient, newTestResolver())
		eventChan := bus.Subscribe("test", 50)
		bus.Start()

		patches := newTestPatches(map[string]map[string]any{
			"rendered": {"conditions": []any{map[string]any{"status": "A"}}},
		})
		patches[0].UID = ""
		patches[0].ResourceVersion = ""
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		missing := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Equal(t, 1, missing.AppliedCount)

		patches[0].UID = "uid-my-ingress"
		patches[0].ResourceVersion = "2"
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		exact := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Equal(t, 1, exact.AppliedCount)
		assert.Zero(t, exact.SkippedCount)

		patches[0].ResourceVersion = "3"
		comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
		repeat := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
		assert.Zero(t, repeat.AppliedCount)
		assert.Equal(t, 1, repeat.SkippedCount)
		assert.Len(t, fakeClient.Actions(), 2)
	})
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
			Namespace:       "default",
			Name:            "ingress-1",
			APIVersion:      "networking.k8s.io/v1",
			Kind:            "Ingress",
			UID:             "uid-ingress-1",
			ResourceVersion: "1",
			Variants: map[string]map[string]any{
				"deployed": {"conditions": []any{map[string]any{"type": "Ready"}}},
			},
		},
		{
			Namespace:       "production",
			Name:            "ingress-2",
			APIVersion:      "networking.k8s.io/v1",
			Kind:            "Ingress",
			UID:             "uid-ingress-2",
			ResourceVersion: "1",
			Variants: map[string]map[string]any{
				"deployed": {"conditions": []any{map[string]any{"type": "Ready"}}},
			},
		},
		{
			Namespace:       "default",
			Name:            "my-gateway",
			APIVersion:      "gateway.networking.k8s.io/v1",
			Kind:            "Gateway",
			UID:             "uid-my-gateway",
			ResourceVersion: "1",
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
	bus.Publish(newTestResourcesAppliedEvent(t, patches))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	// 2. Become leader — does NOT replay anything (stateless applier).
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))
	testutil.AssertNoEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.NoEventTimeout)

	// 3. ResourcesApplied after becoming leader applies normally.
	bus.Publish(newTestResourcesAppliedEvent(t, patches))
	completedEvent := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, events.StatusPatchPhaseRendered, completedEvent.Phase)
	assert.Equal(t, 1, completedEvent.AppliedCount)

	// 4. Lose leadership.
	bus.Publish(events.NewLostLeadershipEvent("test-identity", "demoted"))
	time.Sleep(testutil.StartupDelay) // Wait for event to process

	// 5. Receive another rendered patch set — should NOT apply.
	testutil.DrainChannel(eventChan)
	patches2 := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}}},
	})
	bus.Publish(newTestResourcesAppliedEvent(t, patches2))
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
	comp.ctx = context.Background()

	// Verify each event type is routed without panics
	comp.HandleEvent(newTestResourcesAppliedEvent(t, nil))
	comp.HandleEvent(newTestDeploymentCompletedEvent(t, 1, 1, 0, 0, nil))
	comp.HandleEvent(events.NewReconciliationFailedEvent("err", "deploy", nil))
	comp.HandleEvent(events.NewBecameLeaderEvent("identity"))
	comp.HandleEvent(events.NewLostLeadershipEvent("identity", "reason"))
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

	comp.mu.RLock()
	assert.Empty(t, comp.statusCache)
	comp.mu.RUnlock()
}

// The render's resourceVersion goes stale whenever something bumps the object
// without changing what the render reads (the controller's own spec apply, an
// annotation, a field the watcher ignores). The apply then conflicts; the
// applier fetches the object, keeps the UID precondition, and applies once
// more at the current version.
func TestApplyVariant_ConflictRetriesAtCurrentResourceVersion(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	var patchVersions []string
	fakeClient.PrependReactor("patch", "ingresses", func(action k8stesting.Action) (bool, runtime.Object, error) {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(action.(k8stesting.PatchAction).GetPatch(), &payload))
		version, _ := payload["metadata"].(map[string]any)["resourceVersion"].(string)
		patchVersions = append(patchVersions, version)
		if version != "7" {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "networking.k8s.io", Resource: "ingresses"}, "my-ingress",
				errors.New("the object has been modified"))
		}
		result := &unstructured.Unstructured{}
		result.SetUID("uid-my-ingress")
		result.SetResourceVersion("8")
		return true, result, nil
	})
	fakeClient.PrependReactor("get", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		current := &unstructured.Unstructured{}
		current.SetUID("uid-my-ingress")
		current.SetResourceVersion("7")
		return true, current, nil
	})
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()
	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	first := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 1, first.AppliedCount)
	assert.Equal(t, []string{"1", "7"}, patchVersions)

	// The render still carries the stale version; the cache maps it to the
	// applied one, so an identical payload skips without another conflict.
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	second := testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, 0, second.AppliedCount)
	assert.Equal(t, 1, second.SkippedCount)
	assert.Equal(t, []string{"1", "7"}, patchVersions)
}

// A conflict on an object that was recreated is not retried: the UID no
// longer matches the render's, and the next render carries the new object.
func TestApplyVariant_ConflictOnRecreatedObjectFails(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClient()
	patchCalls := 0
	fakeClient.PrependReactor("patch", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Group: "networking.k8s.io", Resource: "ingresses"}, "my-ingress",
			errors.New("the object has been modified"))
	})
	fakeClient.PrependReactor("get", "ingresses", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		current := &unstructured.Unstructured{}
		current.SetUID("uid-recreated")
		current.SetResourceVersion("9")
		return true, current, nil
	})
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()
	setLeader(comp)

	patches := newTestPatches(map[string]map[string]any{
		"rendered": {"conditions": []any{map[string]any{"type": "Ready"}}},
	})
	comp.applyVariant(context.Background(), patches, events.StatusPatchPhaseRendered)
	failed := testutil.WaitForEvent[*events.StatusUpdateFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.True(t, failed.Retriable)
	assert.Equal(t, 1, patchCalls)
}

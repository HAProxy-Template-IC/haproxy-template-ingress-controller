// Copyright 2026 Philipp Hossner
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
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func namespaceTerminatingError() *apierrors.StatusError {
	err := apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "target",
		errors.New("unable to create new content in namespace retiring because it is being terminated"))
	err.ErrStatus.Details.Causes = []metav1.StatusCause{{Type: corev1.NamespaceTerminatingCause}}
	return err
}

func newApplyLogTestComponent(t *testing.T) (*Component, *busevents.EventBus, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	bus := testutil.NewTestBus()
	client, _ := newClientWithPatchCounter()
	comp := New(&Config{
		EventBus: bus, DynamicClient: client, GVRResolver: newResolver(), OwnNamespace: "haptic",
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	setLeader(comp)
	return comp, bus, logs
}

func TestResourceApplyFailureClassification(t *testing.T) {
	wrongStatus := namespaceTerminatingError()
	wrongStatus.ErrStatus.Reason = metav1.StatusReasonInternalError
	wrongStatus.ErrStatus.Code = 500
	for _, tc := range []struct {
		name      string
		applyErr  error
		mixed     bool
		wantError bool
	}{
		{name: "namespace terminating", applyErr: namespaceTerminatingError()},
		{name: "wrapped namespace terminating", applyErr: fmt.Errorf("apply: %w", namespaceTerminatingError())},
		{name: "authorization", applyErr: apierrors.NewForbidden(serviceGVR.GroupResource(), "target", errors.New("access denied")), wantError: true},
		{name: "text without cause", applyErr: apierrors.NewForbidden(serviceGVR.GroupResource(), "target",
			errors.New("unable to create new content in namespace retiring because it is being terminated")), wantError: true},
		{name: "cause without forbidden", applyErr: wrongStatus, wantError: true},
		{name: "mixed failure", applyErr: namespaceTerminatingError(), mixed: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp, bus, logs := newApplyLogTestComponent(t)
			client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
			client.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if tc.mixed && action.(k8stesting.PatchAction).GetName() == "other" {
					return true, nil, errors.New("API failure")
				}
				return true, nil, tc.applyErr
			})
			processed := bus.SubscribeTypes("processed", 4, events.EventTypeResourcesProcessed)
			applied := bus.SubscribeTypes("applied", 4, events.EventTypeResourcesApplied)
			bus.Start()
			event := reconciliationCompletedEvent(t, []templating.RenderedResource{
				sampleResource("retiring", "target", 80), sampleResource("haptic", "other", 80),
			})
			comp.handleReconciliationCompleted(t.Context(), event)
			requireSameOccurrence(t, event, testutil.WaitForEvent[*events.ResourcesProcessedEvent](t, processed, testutil.EventTimeout))
			testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, applied, testutil.NoEventTimeout)
			assert.Nil(t, comp.appliedCycle)
			if tc.wantError {
				assert.Contains(t, logs.String(), `level=ERROR msg="Rendered resources did not converge; status publication deferred"`)
			} else {
				assert.NotContains(t, logs.String(), "level=ERROR")
				assert.Contains(t, logs.String(), "namespace termination")
			}
		})
	}
}

func TestNamespaceTerminationPreservesConvergenceBarriers(t *testing.T) {
	for _, path := range []string{"normal", "held", "revert"} {
		t.Run(path, func(t *testing.T) {
			comp, bus, logs := newApplyLogTestComponent(t)
			observer := bus.SubscribeTypes("applied", 4, events.EventTypeResourcesApplied)
			bus.Start()
			fixture := newResourceCycleFixture(t)
			prior := fixture.completed(t, "prior", []templating.RenderedResource{sampleResource("haptic", "orphan", 80)}, nil, nil)
			pending := fixture.completed(t, "pending", []templating.RenderedResource{sampleResource("retiring", "target", 80)}, nil,
				eventCycleSnapshot(t, prior))
			comp.handleReconciliationCompleted(t.Context(), prior)
			_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout)
			priorCycle := comp.appliedCycle
			pendingCycle := captureCycleForTest(t, pending)
			prepareDeferredCyclePath(comp, path, pendingCycle)
			client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
			client.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, namespaceTerminatingError()
			})
			client.ClearActions()
			logs.Reset()
			switch path {
			case "normal":
				comp.handleReconciliationCompleted(t.Context(), pending)
			case "held":
				comp.handleRenderGateCompleted(t.Context(), renderGateCompletedEvent(t, pending, true, false, true))
			case "revert":
				comp.handleRenderGateCompleted(t.Context(), renderGateCompletedEvent(t, prior, false, true, true))
			}
			testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, observer, testutil.NoEventTimeout)
			assert.NotContains(t, logs.String(), "level=ERROR")
			assertDeferredCycleState(t, comp, path, priorCycle, pendingCycle)

			recovered := fixture.completed(t, "after namespace deletion", nil, nil, eventCycleSnapshot(t, pending))
			comp.handleReconciliationCompleted(t.Context(), recovered)
			if path != "normal" {
				comp.handleRenderGateCompleted(t.Context(), renderGateCompletedEvent(t, recovered, true, false, true))
			}
			requireSameOccurrence(t, recovered, testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, observer, testutil.EventTimeout))
			assert.Empty(t, comp.lastAppliedKeys)
			assert.False(t, comp.gatePinned)
			assertPrunedOwnedResource(t, client)
		})
	}
}

func prepareDeferredCyclePath(comp *Component, path string, pending *resourceCycle) {
	if path == "held" {
		comp.gatePinned = true
		comp.heldCycle = pending
	}
	if path == "revert" {
		comp.gatePinned = true
		comp.gateAcceptedCycle = pending
		comp.revertCycle = pending
	}
}

func assertDeferredCycleState(t *testing.T, comp *Component, path string, prior, pending *resourceCycle) {
	t.Helper()
	assert.Same(t, prior, comp.appliedCycle)
	assert.Equal(t, path != "normal", comp.gatePinned)
	if path == "held" {
		assert.Same(t, pending, comp.heldCycle)
	}
	if path == "revert" {
		assert.Same(t, pending, comp.revertCycle)
	}
	require.Len(t, comp.lastAppliedKeys, 1)
	for _, action := range comp.dynamicClient.(*dynamicfake.FakeDynamicClient).Actions() {
		assert.NotEqual(t, "delete", action.GetVerb())
	}
}

func assertPrunedOwnedResource(t *testing.T, client *dynamicfake.FakeDynamicClient) {
	t.Helper()
	var deletions int
	for _, action := range client.Actions() {
		if action.GetVerb() != "delete" {
			continue
		}
		deletions++
		deletion := action.(k8stesting.DeleteAction)
		assert.Equal(t, "orphan", deletion.GetName())
		preconditions := deletion.GetDeleteOptions().Preconditions
		require.NotNil(t, preconditions)
		require.NotNil(t, preconditions.UID)
		require.NotNil(t, preconditions.ResourceVersion)
		assert.EqualValues(t, "uid-haptic/orphan", *preconditions.UID)
		assert.Equal(t, "1", *preconditions.ResourceVersion)
	}
	assert.Equal(t, 1, deletions)
}

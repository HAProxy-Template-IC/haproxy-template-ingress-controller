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

package configpublisher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	v1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	crdclientfake "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

func TestFailedRenderStopsQueuedAndActivePublication(t *testing.T) {
	c := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	c.activePublicationCancel = cancel
	work := &publishWorkItem{deployDriven: true}
	require.True(t, c.publishWorkCurrent(work))
	c.handleEvent(t.Context(), &events.ReconciliationFailedEvent{Error: "critical fetch failed"})
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, c.publishWorkCurrent(work))
	require.False(t, c.publishWorkCurrent(&publishWorkItem{}))
	c.handleTemplateRendered(&events.TemplateRenderedEvent{})
	require.False(t, c.publishWorkCurrent(work), "an unauthenticated event must not resume publication")
	c.handleTemplateRendered(newControllerPublisherTemplateFixture(t).event)
	require.True(t, c.publishWorkCurrent(work))
}

func TestNewPublicationTermClearsRetainedFailureLatch(t *testing.T) {
	c := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	c.handleEvent(t.Context(), &events.ReconciliationFailedEvent{Error: "failed"})
	c.preparePublicationTerm()
	require.True(t, c.publishWorkCurrent(&publishWorkItem{}))
}

func TestSnapshotPublicationPreservesIndependentWorkerReceipt(t *testing.T) {
	fixture := newControllerPublisherTemplateFixture(t)
	entry, err := renderedConfigEntryFromEvent(fixture.event)
	require.NoError(t, err)
	entry.confirmedPod = &v1.PodDeploymentStatus{PodName: "worker", PodUID: "uid", AppliedPlanID: entry.planID}
	copied := cloneRenderedConfigEntry(entry)
	request := (&Component{}).buildPublishRequest(publishConfigIdentity{name: "fleet", namespace: "default"}, copied)
	require.NotNil(t, request.OutputSnapshot)
	require.Equal(t, entry.confirmedPod, request.ConfirmedPod)
	entry.confirmedPod.PodName = "mutated"
	require.Equal(t, "worker", copied.confirmedPod.PodName)
	copied.confirmedPod.PodName = "mutated-copy"
	require.Equal(t, "worker", request.ConfirmedPod.PodName)
}

func TestLatePassingGateDoesNotResumePublicationAfterRenderFailure(t *testing.T) {
	c := New(nil, busevents.NewEventBus(10), testutil.NewTestLogger())
	fixture := newControllerPublisherTemplateFixture(t)
	c.handleTemplateRendered(fixture.event)
	c.handleEvent(t.Context(), &events.ReconciliationFailedEvent{Error: "newer render failed"})
	occurrence, err := fixture.event.RenderOccurrence()
	require.NoError(t, err)
	verdict, err := events.NewRenderGateCompletedEventWithCycle(occurrence, true, false, true, "", false, 1)
	require.NoError(t, err)
	c.handleRenderGateCompleted(verdict)
	require.False(t, c.publishWorkCurrent(&publishWorkItem{deployDriven: true}))
	c.handleTemplateRendered(newControllerPublisherTemplateFixture(t).event)
	require.True(t, c.publishWorkCurrent(&publishWorkItem{deployDriven: true}))
}

func TestAcknowledgedCheckpointWaitsForPublicationRecovery(t *testing.T) {
	for _, stage := range []string{"queued", "inflight", "gate pinned"} {
		t.Run(stage, func(t *testing.T) {
			c, client, _ := newPublicationAuthorityComponent(t)
			fixture := newControllerPublisherTemplateFixture(t)
			entry, err := renderedConfigEntryFromEvent(fixture.event)
			require.NoError(t, err)
			entry.confirmedPod = &v1.PodDeploymentStatus{
				PodName: "worker", PodUID: "worker-uid", PodRuntimeID: "runtime",
				Checksum: entry.contentChecksum, AppliedPlanID: entry.planID,
				RunningPlanID: entry.planID, Mode: "reload",
			}
			template := &v1.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{Name: "test-config", Namespace: "default", UID: "template-uid"}}
			c.templateConfig, c.hasTemplateConfig = template, true
			work := c.makePublishWorkItem("acknowledged", template, entry, true)
			fail := func() {
				c.handleEvent(t.Context(), &events.ReconciliationFailedEvent{Error: "critical source unavailable"})
			}
			if stage == "inflight" {
				interruptFirstConfigCreate(t.Context(), c, client)
				c.executePublish(t.Context(), work)
			} else {
				if stage == "gate pinned" {
					c.gatePinned = true
				} else {
					fail()
				}
				c.enqueueDeployed(work)
			}
			before := len(client.Actions())
			c.flushPendingPublish(t.Context())
			require.Len(t, client.Actions(), before, "blocked publication must issue no API requests")
			require.Equal(t, 1, c.deployedQueueDepth(), "the receipt must survive the failure")
			c.handleTemplateRendered(fixture.event)
			if stage == "gate pinned" {
				c.flushPendingPublish(t.Context())
				require.Len(t, client.Actions(), before, "a render alone must not clear a gate refusal")
				occurrence, occurrenceErr := fixture.event.RenderOccurrence()
				require.NoError(t, occurrenceErr)
				verdict, verdictErr := events.NewRenderGateCompletedEventWithCycle(occurrence, true, false, true, "", false, 1)
				require.NoError(t, verdictErr)
				c.handleRenderGateCompleted(verdict)
			}
			c.flushPendingPublish(t.Context())
			require.Zero(t, c.deployedQueueDepth())
			current, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs(template.Namespace).Get(t.Context(), configpublisher.GenerateRuntimeConfigName(template.Name), metav1.GetOptions{})
			require.NoError(t, err)
			require.Len(t, current.Status.RetainedConfigs, 1)
			retained, err := c.publisher.LoadRetained(t.Context(), template.Namespace, template.Name, template.UID)
			require.NoError(t, err)
			require.Len(t, retained, 1)
			require.Equal(t, entry.contentChecksum, retained[0].Reference.Checksum)
		})
	}
}

func interruptFirstConfigCreate(ctx context.Context, c *Component, client *crdclientfake.Clientset) {
	interrupted := false
	client.PrependReactor("create", "haproxycfgs", func(k8stesting.Action) (bool, runtime.Object, error) {
		if interrupted {
			return false, nil, nil
		}
		interrupted = true
		c.handleEvent(ctx, &events.ReconciliationFailedEvent{Error: "critical source unavailable"})
		return true, nil, context.Canceled
	})
}

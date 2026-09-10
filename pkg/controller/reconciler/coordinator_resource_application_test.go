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

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestCoordinatorWaitsForResourceApplication(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "test config", nil, nil)
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus, Pipeline: &mockPipeline{result: &pipeline.PipelineResult{CycleSnapshot: cycle}},
		StoreProvider: stores.NewRealStoreProvider(nil), Logger: logger,
	})
	completed := bus.SubscribeTypes("resource-application-test", 16, events.EventTypeReconciliationCompleted)
	rendered := bus.SubscribeTypes("resource-render-test", 16, events.EventTypeTemplateRendered)
	bus.Start()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- coordinator.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(testutil.EventTimeout):
			t.Error("coordinator did not stop while resource application was pending")
		}
		bus.UnsubscribeTyped(completed)
		bus.UnsubscribeTyped(rendered)
		assert.Zero(t, bus.SubscriberCount())
	})
	select {
	case <-coordinator.SubscriptionReady():
	case <-time.After(testutil.EventTimeout):
		t.Fatal("coordinator did not subscribe")
	}
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	first := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, completed, testutil.EventTimeout)
	_ = testutil.WaitForEvent[*events.TemplateRenderedEvent](t, rendered, testutil.EventTimeout)
	bus.Publish(events.NewReconciliationTriggeredEvent("forced", false))
	for range 32 {
		for range 128 {
			bus.Publish(events.NewReconciliationTriggeredEvent("second", true))
		}
		require.Eventually(t, func() bool { return len(coordinator.eventChan) == 0 },
			testutil.EventTimeout, time.Millisecond)
	}
	select {
	case <-completed:
		t.Fatal("the next render started before resource application finished")
	case <-time.After(testutil.NoEventTimeout):
	}
	acknowledgeResourceApplication(t, bus, first)
	second := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, completed, testutil.EventTimeout)
	nextRender := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, rendered, testutil.EventTimeout)
	assert.Equal(t, "forced", nextRender.TriggerReason)
	firstOccurrence, err := first.RenderOccurrence()
	require.NoError(t, err)
	secondOccurrence, err := second.RenderOccurrence()
	require.NoError(t, err)
	same, err := firstOccurrence.Same(secondOccurrence)
	require.NoError(t, err)
	assert.False(t, same)
	assert.Zero(t, bus.DroppedEventsCritical())
}

func acknowledgeResourceApplication(t *testing.T, bus *busevents.EventBus, event *events.ReconciliationCompletedEvent) {
	t.Helper()
	occurrence, err := event.RenderOccurrence()
	require.NoError(t, err)
	processed, err := events.NewResourcesProcessedEvent(occurrence, events.PropagateCorrelation(event))
	require.NoError(t, err)
	bus.Publish(processed)
}

func TestResourceApplicationWaitMatchesExactOccurrence(t *testing.T) {
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "same config", nil, nil)
	first, err := rendercycle.NewOccurrence(cycle)
	require.NoError(t, err)
	second, err := rendercycle.NewOccurrence(cycle)
	require.NoError(t, err)
	firstProcessed, err := events.NewResourcesProcessedEvent(first)
	require.NoError(t, err)
	secondProcessed, err := events.NewResourcesProcessedEvent(second)
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		receipts []busevents.Event
		wantErr  string
	}{
		{name: "same cycle other occurrence", receipts: []busevents.Event{firstProcessed}, wantErr: "subscription closed"},
		{name: "stale then current", receipts: []busevents.Event{firstProcessed, secondProcessed}},
		{name: "missing identity", receipts: []busevents.Event{&events.ResourcesProcessedEvent{}}, wantErr: "occurrence"},
		{name: "nil event", receipts: []busevents.Event{(*events.ResourcesProcessedEvent)(nil)}, wantErr: "invalid event type"},
		{name: "other event", receipts: []busevents.Event{events.NewReconciliationTriggeredEvent("wrong", true)}, wantErr: "invalid event type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processed := make(chan busevents.Event, len(tc.receipts))
			for _, receipt := range tc.receipts {
				processed <- receipt
			}
			close(processed)
			err := waitForResourceApplication(t.Context(), processed, second)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	processed := make(chan busevents.Event, 1)
	processed <- secondProcessed
	require.ErrorIs(t, waitForResourceApplication(ctx, processed, second), context.Canceled)
	require.NoError(t, waitForResourceApplication(t.Context(), nil, nil))
}

func TestResourceApplicationWaitCancellationAtWakeup(t *testing.T) {
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "config", nil, nil)
	occurrence, err := rendercycle.NewOccurrence(cycle)
	require.NoError(t, err)
	receipt, err := events.NewResourcesProcessedEvent(occurrence)
	require.NoError(t, err)
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		waiting := &cancelMailboxOnWaitContext{Context: ctx, onWait: cancel}
		processed := make(chan busevents.Event, 1)
		processed <- receipt
		require.ErrorIs(t, waitForResourceApplication(waiting, processed, occurrence), context.Canceled)
	}
}

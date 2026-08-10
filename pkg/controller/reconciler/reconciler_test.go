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

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// The Reconciler has no reconciler-level debounce: a resource change is
// published as a ReconciliationTriggeredEvent immediately, with reason
// "resource_change".
func TestReconciler_LeadingEdgeTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	// Subscribe to reconciliation triggered events
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	// Start reconciler in background
	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	startTime := time.Now()

	// Publish a resource change event (not initial sync)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	elapsed := time.Since(startTime)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent")
	assert.Equal(t, "resource_change", receivedEvent.Reason)
	assert.Less(t, elapsed, 100*time.Millisecond,
		"A resource change should trigger immediately")
}

func TestReconciler_IndexSynchronizedTriggersImmediate(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish index synchronized event
	resourceCounts := map[string]int{
		"ingresses": 10,
		"services":  5,
	}
	bus.Publish(events.NewIndexSynchronizedEvent(resourceCounts))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "index_synchronized", receivedEvent.Reason)
}

func TestReconciler_SkipInitialSyncEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish initial sync events
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       10,
		IsInitialSync: true,
	}))

	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Created:       5,
		IsInitialSync: true,
	}))

	// Initial-sync events are filtered, so no reconciliation should be triggered.
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 300*time.Millisecond)
}

// index_synchronized still fires immediately, and a second resource_change
// fires its OWN ReconciliationTriggeredEvent — there is no reconciler-level
// timer to coalesce changes into. We expect three distinct triggers in
// order: resource_change, index_synchronized, resource_change.
func TestReconciler_IndexSynchronizedFiresImmediatelyAlongsideResourceChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// First resource change - triggers immediately.
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// index_synchronized fires immediately.
	bus.Publish(events.NewIndexSynchronizedEvent(map[string]int{"ingresses": 10}))
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, secondEvent)
	assert.Equal(t, "index_synchronized", secondEvent.Reason)

	// A second resource change fires its OWN trigger (not coalesced away).
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))
	thirdEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, thirdEvent)
	assert.Equal(t, "resource_change", thirdEvent.Reason)
}

// became_leader still fires immediately, and a second resource_change fires
// its OWN ReconciliationTriggeredEvent — there is no reconciler-level timer
// to coalesce changes into. We expect three distinct triggers in order:
// resource_change, became_leader, resource_change.
func TestReconciler_BecameLeaderFiresImmediatelyAlongsideResourceChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First resource change fires immediately.
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// became_leader fires immediately.
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))
	secondEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, secondEvent)
	assert.Equal(t, "became_leader", secondEvent.Reason)

	// A second resource change fires its OWN trigger (not coalesced away).
	bus.Publish(events.NewResourceIndexUpdatedEvent("services", types.ChangeStats{
		Modified:      1,
		IsInitialSync: false,
	}))
	thirdEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, thirdEvent)
	assert.Equal(t, "resource_change", thirdEvent.Reason)
}

// The reconciler exits cleanly when its context is cancelled. There is no
// timer machinery involved — Start() simply returns nil.
func TestReconciler_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start reconciler
	done := make(chan error, 1)
	go func() {
		done <- reconciler.Start(ctx)
	}()

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish a resource change - triggers immediately.
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		IsInitialSync: false,
	}))

	// Wait for the immediate trigger
	firstEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
	require.NotNil(t, firstEvent)
	assert.Equal(t, "resource_change", firstEvent.Reason)

	// Cancel context.
	cancel()

	// Should return quickly with no error.
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Reconciler did not shut down within timeout")
	}
}

// HAProxy pods are deployment targets, not configuration sources. Changes to HAProxy pods
// should trigger deployment-only reconciliation via HAProxyPodsDiscoveredEvent → Deployer component,
// not full reconciliation (render + validate + deploy).
func TestReconciler_SkipHAProxyPodChanges(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewResourceIndexUpdatedEvent("haproxy-pods", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// The haproxy-pods filter drops the change, so no reconciliation triggers.
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, 300*time.Millisecond)
}

// This ensures the haproxy-pods filter doesn't break reconciliation for actual configuration sources.
func TestReconciler_NonHAProxyPodChangesStillTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)

	// Give the reconciler time to start listening
	time.Sleep(testutil.StartupDelay)

	// Publish ingress resource change (not haproxy-pods)
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent, "Should receive ReconciliationTriggeredEvent for non-HAProxy pod resources")
	assert.Equal(t, "resource_change", receivedEvent.Reason)
}

func TestReconciler_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	assert.Equal(t, ComponentName, reconciler.Name())
}

func TestReconciler_HandleHTTPResourceChange(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewHTTPResourceUpdatedEvent("http://example.com/blocklist.txt", "abc123", 1024))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "http_resource_change", receivedEvent.Reason)
}

func TestReconciler_HandleHTTPResourceAccepted(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewHTTPResourceAcceptedEvent("http://example.com/blocklist.txt", "def456", 2048))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "http_resource_accepted", receivedEvent.Reason)
}

// When leadership is acquired, the reconciler should trigger an immediate reconciliation
// to ensure leader-only components (renderer, drift monitor) receive fresh state.
func TestReconciler_HandleBecameLeader(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Should trigger immediately
	receivedEvent := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)

	require.NotNil(t, receivedEvent)
	assert.Equal(t, "became_leader", receivedEvent.Reason)
}

// N rapid resource_change events each produce their OWN
// ReconciliationTriggeredEvent — there is no reconciler-level coalescing by
// timer. The count of received resource_change triggers must equal N.
func TestReconciler_RapidResourceChangesEachTrigger(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	const n = 5
	for i := 0; i < n; i++ {
		bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
			Modified:      1,
			IsInitialSync: false,
		}))
	}

	for i := 0; i < n; i++ {
		got := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.EventTimeout)
		require.NotNilf(t, got, "expected trigger %d of %d", i+1, n)
		assert.Equal(t, "resource_change", got.Reason)
	}

	// No extra triggers beyond the N we published.
	testutil.AssertNoEvent[*events.ReconciliationTriggeredEvent](t, eventChan, testutil.NoEventTimeout)
}

// Coalescibility is stamped on the published ReconciliationTriggeredEvent.
// State-update reasons (resource_change, http_resource_change) are
// coalescible; command reasons (index_synchronized, http_resource_accepted,
// drift_prevention, became_leader) are not.
func TestReconciler_PublishedEventCoalescibleFlag(t *testing.T) {
	type tc struct {
		name        string
		wantReason  string
		coalescible bool
		publish     func()
	}

	bus, logger := testutil.NewTestBusAndLogger()
	reconciler := New(bus, logger)
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()
	go reconciler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	cases := []tc{
		{
			name:        "resource_change is coalescible",
			wantReason:  "resource_change",
			coalescible: true,
			publish: func() {
				bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", types.ChangeStats{
					Modified:      1,
					IsInitialSync: false,
				}))
			},
		},
		{
			name:        "http_resource_change is coalescible",
			wantReason:  "http_resource_change",
			coalescible: true,
			publish: func() {
				bus.Publish(events.NewHTTPResourceUpdatedEvent("http://example.com/a.txt", "h1", 10))
			},
		},
		{
			name:        "index_synchronized is not coalescible",
			wantReason:  "index_synchronized",
			coalescible: false,
			publish: func() {
				bus.Publish(events.NewIndexSynchronizedEvent(map[string]int{"ingresses": 1}))
			},
		},
		{
			name:        "http_resource_accepted is not coalescible",
			wantReason:  "http_resource_accepted",
			coalescible: false,
			publish: func() {
				bus.Publish(events.NewHTTPResourceAcceptedEvent("http://example.com/a.txt", "h2", 20))
			},
		},
		{
			name:        "drift_prevention is not coalescible",
			wantReason:  events.TriggerReasonDriftPrevention,
			coalescible: false,
			publish: func() {
				bus.Publish(events.NewDriftPreventionTriggeredEvent(30 * time.Minute))
			},
		},
		{
			name:        "became_leader is not coalescible",
			wantReason:  "became_leader",
			coalescible: false,
			publish: func() {
				bus.Publish(events.NewBecameLeaderEvent("test-pod"))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.publish()
			got := testutil.WaitForEventWithPredicate[*events.ReconciliationTriggeredEvent](
				t, eventChan, testutil.EventTimeout,
				func(e *events.ReconciliationTriggeredEvent) bool {
					return e.Reason == c.wantReason
				})
			require.NotNil(t, got, "expected a ReconciliationTriggeredEvent with reason %q", c.wantReason)
			assert.Equal(t, c.coalescible, got.Coalescible(),
				"reason %q should have coalescible=%v", c.wantReason, c.coalescible)
		})
	}
}

func TestReconciler_HandleEvent_UnknownEvent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	reconciler := New(bus, logger)

	// Should not panic for unknown events
	unknownEvent := events.NewReconciliationCompletedEvent(0, nil, nil)
	reconciler.HandleEvent(unknownEvent)
}

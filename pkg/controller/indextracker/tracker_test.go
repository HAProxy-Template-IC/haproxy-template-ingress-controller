package indextracker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestNew(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services", "pods"}

	tracker := New(bus, logger, resourceNames)

	require.NotNil(t, tracker)
	assert.Len(t, tracker.expectedResources, 3)
	assert.False(t, tracker.expectedResources["ingresses"])
	assert.False(t, tracker.expectedResources["services"])
	assert.False(t, tracker.expectedResources["pods"])
	assert.Empty(t, tracker.resourceCounts)
	assert.False(t, tracker.allSynced)
}

func TestNew_EmptyResourceList(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{}

	tracker := New(bus, logger, resourceNames)

	require.NotNil(t, tracker)
	assert.Empty(t, tracker.expectedResources)
	assert.True(t, tracker.allResourcesSynced()) // Empty list is already synced
}

func TestHandleResourceSyncComplete_SingleResource(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Initially not synced
	assert.False(t, tracker.expectedResources["ingresses"])
	assert.False(t, tracker.allResourcesSynced())

	// Simulate receiving ResourceSyncCompleteEvent
	event := events.NewResourceSyncCompleteEvent("ingresses", 42)
	tracker.handleResourceSyncComplete(event)

	// Should now be synced
	assert.True(t, tracker.expectedResources["ingresses"])
	assert.True(t, tracker.allResourcesSynced())
	assert.Equal(t, 42, tracker.resourceCounts["ingresses"])
}

func TestHandleResourceSyncComplete_MultipleResources(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services", "pods"}

	tracker := New(bus, logger, resourceNames)

	// Initially not synced
	assert.False(t, tracker.allResourcesSynced())

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 10))
	assert.True(t, tracker.expectedResources["ingresses"])
	assert.False(t, tracker.allResourcesSynced())

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("services", 20))
	assert.True(t, tracker.expectedResources["services"])
	assert.False(t, tracker.allResourcesSynced())

	// Sync pods - should trigger all synced
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("pods", 30))
	assert.True(t, tracker.expectedResources["pods"])
	assert.True(t, tracker.allResourcesSynced())

	// Verify counts
	assert.Equal(t, 10, tracker.resourceCounts["ingresses"])
	assert.Equal(t, 20, tracker.resourceCounts["services"])
	assert.Equal(t, 30, tracker.resourceCounts["pods"])
}

func TestHandleResourceSyncComplete_UnexpectedResource(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Should not panic or crash, just log warning
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("unknown", 99))

	// Unexpected resource should not be tracked
	assert.False(t, tracker.expectedResources["unknown"])
	assert.False(t, tracker.allResourcesSynced())
}

func TestHandleResourceSyncComplete_DuplicateEvent(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 42))
	assert.True(t, tracker.expectedResources["ingresses"])
	assert.Equal(t, 42, tracker.resourceCounts["ingresses"])

	// Duplicate event with different count - should be ignored
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 100))

	// Count should not change
	assert.Equal(t, 42, tracker.resourceCounts["ingresses"])
}

func TestStart_PublishesIndexSynchronizedEvent(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services"}

	tracker := New(bus, logger, resourceNames)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Start tracker in goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = tracker.Start(ctx)
	}()

	// Give tracker time to start
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewResourceSyncCompleteEvent("ingresses", 10))
	bus.Publish(events.NewResourceSyncCompleteEvent("services", 20))

	indexSyncEvent := testutil.WaitForEvent[*events.IndexSynchronizedEvent](t, eventChan, testutil.LongTimeout)

	// Verify event
	assert.Len(t, indexSyncEvent.ResourceCounts, 2)
	assert.Equal(t, 10, indexSyncEvent.ResourceCounts["ingresses"])
	assert.Equal(t, 20, indexSyncEvent.ResourceCounts["services"])
}

func TestStart_DoesNotPublishDuplicateIndexSynchronizedEvent(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Start tracker in goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = tracker.Start(ctx)
	}()

	// Give tracker time to start
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewResourceSyncCompleteEvent("ingresses", 10))

	// Wait for first IndexSynchronizedEvent
	timeout := time.After(500 * time.Millisecond)
	eventCount := 0

loop:
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.IndexSynchronizedEvent); ok {
				eventCount++
			}
		case <-timeout:
			break loop
		}
	}

	// Should only receive one IndexSynchronizedEvent
	assert.Equal(t, 1, eventCount)

	// Publish duplicate sync complete event
	bus.Publish(events.NewResourceSyncCompleteEvent("ingresses", 10))

	// Wait again
	timeout = time.After(500 * time.Millisecond)
duplicateLoop:
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.IndexSynchronizedEvent); ok {
				eventCount++
			}
		case <-timeout:
			break duplicateLoop
		}
	}

	// Should still only have one event
	assert.Equal(t, 1, eventCount)
}

func TestSyncedCount(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services", "pods"}

	tracker := New(bus, logger, resourceNames)

	tracker.mu.Lock()
	assert.Equal(t, 0, tracker.syncedCount())
	tracker.mu.Unlock()

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 10))

	tracker.mu.Lock()
	assert.Equal(t, 1, tracker.syncedCount())
	tracker.mu.Unlock()

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("services", 20))

	tracker.mu.Lock()
	assert.Equal(t, 2, tracker.syncedCount())
	tracker.mu.Unlock()

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("pods", 30))

	tracker.mu.Lock()
	assert.Equal(t, 3, tracker.syncedCount())
	tracker.mu.Unlock()
}

func TestAllResourcesSynced(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services"}

	tracker := New(bus, logger, resourceNames)

	tracker.mu.Lock()
	assert.False(t, tracker.allResourcesSynced())
	tracker.mu.Unlock()

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 10))

	tracker.mu.Lock()
	assert.False(t, tracker.allResourcesSynced())
	tracker.mu.Unlock()

	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("services", 20))

	tracker.mu.Lock()
	assert.True(t, tracker.allResourcesSynced())
	tracker.mu.Unlock()
}

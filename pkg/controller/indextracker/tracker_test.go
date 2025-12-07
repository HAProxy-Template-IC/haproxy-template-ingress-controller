package indextracker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
	busevents "haproxy-template-ic/pkg/events"
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
	assert.True(t, tracker.AllSynced()) // Empty list is already synced
}

func TestHandleResourceSyncComplete_SingleResource(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Initially not synced
	assert.False(t, tracker.IsResourceSynced("ingresses"))
	assert.False(t, tracker.AllSynced())

	// Simulate receiving ResourceSyncCompleteEvent
	event := events.NewResourceSyncCompleteEvent("ingresses", 42)
	tracker.handleResourceSyncComplete(event)

	// Should now be synced
	assert.True(t, tracker.IsResourceSynced("ingresses"))
	assert.True(t, tracker.AllSynced())

	count, err := tracker.GetResourceCount("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 42, count)
}

func TestHandleResourceSyncComplete_MultipleResources(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services", "pods"}

	tracker := New(bus, logger, resourceNames)

	// Initially not synced
	assert.False(t, tracker.AllSynced())

	// Sync ingresses
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 10))
	assert.True(t, tracker.IsResourceSynced("ingresses"))
	assert.False(t, tracker.AllSynced())

	// Sync services
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("services", 20))
	assert.True(t, tracker.IsResourceSynced("services"))
	assert.False(t, tracker.AllSynced())

	// Sync pods - should trigger all synced
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("pods", 30))
	assert.True(t, tracker.IsResourceSynced("pods"))
	assert.True(t, tracker.AllSynced())

	// Verify counts
	counts := tracker.GetAllResourceCounts()
	assert.Equal(t, 10, counts["ingresses"])
	assert.Equal(t, 20, counts["services"])
	assert.Equal(t, 30, counts["pods"])
}

func TestHandleResourceSyncComplete_UnexpectedResource(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Should not panic or crash, just log warning
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("unknown", 99))

	// Unexpected resource should not be tracked
	assert.False(t, tracker.IsResourceSynced("unknown"))
	assert.False(t, tracker.AllSynced())
}

func TestHandleResourceSyncComplete_DuplicateEvent(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// First event
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 42))
	assert.True(t, tracker.IsResourceSynced("ingresses"))

	count, err := tracker.GetResourceCount("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 42, count)

	// Duplicate event with different count - should be ignored
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 100))

	// Count should not change
	count, err = tracker.GetResourceCount("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 42, count)
}

func TestGetResourceCount_NotSynced(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Resource exists but hasn't synced yet
	count, err := tracker.GetResourceCount("ingresses")
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "has not synced yet")
}

func TestGetResourceCount_Unknown(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)

	// Unknown resource
	count, err := tracker.GetResourceCount("unknown")
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "unknown resource type")
}

func TestGetAllResourceCounts_ReturnsDefensiveCopy(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses"}

	tracker := New(bus, logger, resourceNames)
	tracker.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("ingresses", 42))

	// Get counts
	counts := tracker.GetAllResourceCounts()
	assert.Equal(t, 42, counts["ingresses"])

	// Modify returned map
	counts["ingresses"] = 999

	// Internal state should not be affected
	count, err := tracker.GetResourceCount("ingresses")
	require.NoError(t, err)
	assert.Equal(t, 42, count)
}

func TestStart_PublishesIndexSynchronizedEvent(t *testing.T) {
	bus := busevents.NewEventBus(10)
	logger := slog.Default()
	resourceNames := []string{"ingresses", "services"}

	tracker := New(bus, logger, resourceNames)

	// Subscribe to events
	eventChan := bus.Subscribe(10)
	bus.Start()

	// Start tracker in goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = tracker.Start(ctx)
	}()

	// Give tracker time to start
	time.Sleep(50 * time.Millisecond)

	// Publish sync complete events
	bus.Publish(events.NewResourceSyncCompleteEvent("ingresses", 10))
	bus.Publish(events.NewResourceSyncCompleteEvent("services", 20))

	// Wait for IndexSynchronizedEvent
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

	// Subscribe to events
	eventChan := bus.Subscribe(10)
	bus.Start()

	// Start tracker in goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = tracker.Start(ctx)
	}()

	// Give tracker time to start
	time.Sleep(50 * time.Millisecond)

	// Publish sync complete event
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

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

// Package indextracker provides the IndexSynchronizationTracker that monitors
// resource watcher synchronization and publishes an event when all are synced.
//
// The tracker:
//   - Subscribes to ResourceSyncCompleteEvent
//   - Tracks which resource types have completed initial sync
//   - Publishes IndexSynchronizedEvent when ALL resources are synced
//   - Allows the controller to wait for complete data before reconciliation
package indextracker

import (
	"context"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// ComponentName is the unique identifier for the index synchronization tracker component.
const ComponentName = "index-tracker"

// EventBufferSize is the buffer size for the event subscription.
const EventBufferSize = 100

// IndexSynchronizationTracker monitors resource synchronization and publishes
// an event when all resource types have completed initial sync.
type IndexSynchronizationTracker struct {
	*component.Base

	expectedResources map[string]bool // resourceTypeName -> synced
	resourceCounts    map[string]int  // resourceTypeName -> count
	mu                sync.Mutex
	allSynced         bool
}

// New creates a new IndexSynchronizationTracker.
//
// Parameters:
//   - eventBus: EventBus for publishing/subscribing to events
//   - logger: Logger for diagnostic messages
//   - resourceNames: List of resource type names that must sync (from Config.WatchedResources keys)
//
// The tracker expects to receive a ResourceSyncCompleteEvent for each resource type
// in resourceNames before publishing IndexSynchronizedEvent.
func New(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	resourceNames []string,
) *IndexSynchronizationTracker {
	expectedResources := make(map[string]bool, len(resourceNames))
	for _, name := range resourceNames {
		expectedResources[name] = false
	}

	t := &IndexSynchronizationTracker{
		expectedResources: expectedResources,
		resourceCounts:    make(map[string]int),
		allSynced:         false,
	}
	// The Base subscribes to the EventBus during construction (before
	// EventBus.Start()). This ensures proper startup synchronization without
	// timing-based sleeps. Typed subscription (EventTypes, not a catch-all)
	// so we only receive events we handle (reduces buffer pressure).
	t.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    t,
		EventTypes: []string{events.EventTypeResourceSyncComplete},
	})
	return t
}

// Start begins monitoring resource synchronization events by running the
// embedded component.Base event loop until ctx is cancelled.
func (t *IndexSynchronizationTracker) Start(ctx context.Context) error {
	t.Logger().Debug("index synchronization tracker started",
		"expected_resources", len(t.expectedResources))

	return t.Base.Start(ctx)
}

// HandleEvent implements component.EventHandler: it dispatches
// ResourceSyncCompleteEvent to the sync tracker.
func (t *IndexSynchronizationTracker) HandleEvent(event busevents.Event) {
	if syncEvent, ok := event.(*events.ResourceSyncCompleteEvent); ok {
		t.handleResourceSyncComplete(syncEvent)
	}
}

// handleResourceSyncComplete processes a ResourceSyncCompleteEvent.
//
// When all expected resources have synced, publishes IndexSynchronizedEvent.
func (t *IndexSynchronizationTracker) handleResourceSyncComplete(event *events.ResourceSyncCompleteEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	resourceTypeName := event.ResourceTypeName
	initialCount := event.InitialCount

	// Check if this is an expected resource
	if _, expected := t.expectedResources[resourceTypeName]; !expected {
		t.Logger().Warn("received sync complete for unexpected resource",
			"resource_type", resourceTypeName)
		return
	}

	// Check if already marked as synced
	if t.expectedResources[resourceTypeName] {
		t.Logger().Debug("resource already marked as synced, ignoring duplicate event",
			"resource_type", resourceTypeName)
		return
	}

	// Mark as synced
	t.expectedResources[resourceTypeName] = true
	t.resourceCounts[resourceTypeName] = initialCount

	t.Logger().Debug("resource synced",
		"resource_type", resourceTypeName,
		"initial_count", initialCount,
		"synced_count", t.syncedCount(),
		"total_expected", len(t.expectedResources))

	// Check if all resources are now synced
	if t.allResourcesSynced() && !t.allSynced {
		t.allSynced = true

		t.Logger().Debug("all resource indices synchronized",
			"total_resources", len(t.expectedResources),
			"resource_counts", t.resourceCounts)

		// Publish IndexSynchronizedEvent
		t.EventBus().Publish(events.NewIndexSynchronizedEvent(t.resourceCounts))
	}
}

// syncedCount returns the number of resources that have synced.
// Must be called with mu held.
func (t *IndexSynchronizationTracker) syncedCount() int {
	count := 0
	for _, synced := range t.expectedResources {
		if synced {
			count++
		}
	}
	return count
}

// allResourcesSynced returns true if all expected resources have synced.
// Must be called with mu held.
func (t *IndexSynchronizationTracker) allResourcesSynced() bool {
	for _, synced := range t.expectedResources {
		if !synced {
			return false
		}
	}
	return true
}

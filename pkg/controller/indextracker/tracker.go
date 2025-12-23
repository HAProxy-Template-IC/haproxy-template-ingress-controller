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
	"fmt"
	"log/slog"
	"sync"

	"haptic/pkg/controller/events"
	busevents "haptic/pkg/events"
)

// IndexSynchronizationTracker monitors resource synchronization and publishes
// an event when all resource types have completed initial sync.
type IndexSynchronizationTracker struct {
	eventBus          *busevents.EventBus
	eventChan         <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger            *slog.Logger
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

	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	eventChan := eventBus.Subscribe(100)

	return &IndexSynchronizationTracker{
		eventBus:          eventBus,
		eventChan:         eventChan,
		logger:            logger,
		expectedResources: expectedResources,
		resourceCounts:    make(map[string]int),
		allSynced:         false,
	}
}

// Start begins monitoring resource synchronization events.
//
// This method:
//   - Tracks which resources have synced
//   - Publishes IndexSynchronizedEvent when all expected resources are synced
//   - Runs until ctx is cancelled
func (t *IndexSynchronizationTracker) Start(ctx context.Context) error {
	t.logger.Debug("index synchronization tracker started",
		"expected_resources", len(t.expectedResources))

	for {
		select {
		case event := <-t.eventChan:
			// Handle ResourceSyncCompleteEvent
			if syncEvent, ok := event.(*events.ResourceSyncCompleteEvent); ok {
				t.handleResourceSyncComplete(syncEvent)
			}

		case <-ctx.Done():
			t.logger.Debug("index synchronization tracker stopping")
			return nil
		}
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
		t.logger.Warn("received sync complete for unexpected resource",
			"resource_type", resourceTypeName)
		return
	}

	// Check if already marked as synced
	if t.expectedResources[resourceTypeName] {
		t.logger.Debug("resource already marked as synced, ignoring duplicate event",
			"resource_type", resourceTypeName)
		return
	}

	// Mark as synced
	t.expectedResources[resourceTypeName] = true
	t.resourceCounts[resourceTypeName] = initialCount

	t.logger.Debug("resource synced",
		"resource_type", resourceTypeName,
		"initial_count", initialCount,
		"synced_count", t.syncedCount(),
		"total_expected", len(t.expectedResources))

	// Check if all resources are now synced
	if t.allResourcesSynced() && !t.allSynced {
		t.allSynced = true

		t.logger.Debug("all resource indices synchronized",
			"total_resources", len(t.expectedResources),
			"resource_counts", t.resourceCounts)

		// Publish IndexSynchronizedEvent
		t.eventBus.Publish(events.NewIndexSynchronizedEvent(t.resourceCounts))
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

// IsResourceSynced returns true if the specified resource type has synced.
func (t *IndexSynchronizationTracker) IsResourceSynced(resourceTypeName string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.expectedResources[resourceTypeName]
}

// AllSynced returns true if all expected resources have synced.
func (t *IndexSynchronizationTracker) AllSynced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.allResourcesSynced()
}

// GetResourceCount returns the number of resources loaded during initial sync.
//
// Returns:
//   - count if the resource has synced
//   - 0 and error if resource hasn't synced or is unknown
func (t *IndexSynchronizationTracker) GetResourceCount(resourceTypeName string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	synced, exists := t.expectedResources[resourceTypeName]
	if !exists {
		return 0, fmt.Errorf("unknown resource type: %s", resourceTypeName)
	}

	if !synced {
		return 0, fmt.Errorf("resource type %s has not synced yet", resourceTypeName)
	}

	return t.resourceCounts[resourceTypeName], nil
}

// GetAllResourceCounts returns a map of all resource counts.
//
// Returns a copy to prevent external modification.
func (t *IndexSynchronizationTracker) GetAllResourceCounts() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()

	counts := make(map[string]int, len(t.resourceCounts))
	for k, v := range t.resourceCounts {
		counts[k] = v
	}
	return counts
}

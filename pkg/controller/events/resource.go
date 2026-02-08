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

package events

import (
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// ResourceIndexUpdatedEvent is published when a watched Kubernetes resource.
// has been added, updated, or deleted in the local index.
type ResourceIndexUpdatedEvent struct {
	// ResourceTypeName identifies the resource type from config (e.g., "ingresses", "services").
	ResourceTypeName string

	// ChangeStats provides detailed change statistics including Created, Modified, Deleted counts
	// and whether this event occurred during initial sync.
	ChangeStats types.ChangeStats

	timestamp time.Time
}

// NewResourceIndexUpdatedEvent creates a new ResourceIndexUpdatedEvent.
// Performs a value copy of ChangeStats (it's a small struct with no pointers).
func NewResourceIndexUpdatedEvent(resourceTypeName string, changeStats types.ChangeStats) *ResourceIndexUpdatedEvent {
	return &ResourceIndexUpdatedEvent{
		ResourceTypeName: resourceTypeName,
		ChangeStats:      changeStats,
		timestamp:        time.Now(),
	}
}

func (e *ResourceIndexUpdatedEvent) EventType() string    { return EventTypeResourceIndexUpdated }
func (e *ResourceIndexUpdatedEvent) Timestamp() time.Time { return e.timestamp }

// ResourceSyncCompleteEvent is published when a resource watcher has completed.
// its initial sync with the Kubernetes API.
type ResourceSyncCompleteEvent struct {
	// ResourceTypeName identifies the resource type from config (e.g., "ingresses").
	ResourceTypeName string

	// InitialCount is the number of resources loaded during initial sync.
	InitialCount int

	timestamp time.Time
}

// NewResourceSyncCompleteEvent creates a new ResourceSyncCompleteEvent.
func NewResourceSyncCompleteEvent(resourceTypeName string, initialCount int) *ResourceSyncCompleteEvent {
	return &ResourceSyncCompleteEvent{
		ResourceTypeName: resourceTypeName,
		InitialCount:     initialCount,
		timestamp:        time.Now(),
	}
}

func (e *ResourceSyncCompleteEvent) EventType() string    { return EventTypeResourceSyncComplete }
func (e *ResourceSyncCompleteEvent) Timestamp() time.Time { return e.timestamp }

// IndexSynchronizedEvent is published when all resource watchers have completed.
// their initial sync and the system has a complete view of all resources.
//
// This is a critical milestone - the controller waits for this event before.
// starting reconciliation to ensure it has complete data.
type IndexSynchronizedEvent struct {
	// ResourceCounts maps resource types to their counts.
	ResourceCounts map[string]int
	timestamp      time.Time
}

// NewIndexSynchronizedEvent creates a new IndexSynchronizedEvent.
// Performs defensive copy of the resource counts map.
func NewIndexSynchronizedEvent(resourceCounts map[string]int) *IndexSynchronizedEvent {
	// Defensive copy of map
	countsCopy := make(map[string]int, len(resourceCounts))
	for k, v := range resourceCounts {
		countsCopy[k] = v
	}

	return &IndexSynchronizedEvent{
		ResourceCounts: countsCopy,
		timestamp:      time.Now(),
	}
}

func (e *IndexSynchronizedEvent) EventType() string    { return EventTypeIndexSynchronized }
func (e *IndexSynchronizedEvent) Timestamp() time.Time { return e.timestamp }

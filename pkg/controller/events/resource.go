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
	"maps"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// ResourceIndexUpdatedEvent is published when a watched Kubernetes resource.
// has been added, updated, or deleted in the local index.
type ResourceIndexUpdatedEvent struct {
	// ResourceTypeName identifies the watched resource type as configured in
	// spec.watchedResources (the plural form, e.g. "services").
	ResourceTypeName string

	// ChangeStats provides detailed change statistics including Created, Modified, Deleted counts
	// and whether this event occurred during initial sync.
	ChangeStats types.ChangeStats

	timestamped
}

// NewResourceIndexUpdatedEvent creates a new ResourceIndexUpdatedEvent.
// Performs a value copy of ChangeStats (it's a small struct with no pointers).
func NewResourceIndexUpdatedEvent(resourceTypeName string, changeStats types.ChangeStats) *ResourceIndexUpdatedEvent {
	return &ResourceIndexUpdatedEvent{
		ResourceTypeName: resourceTypeName,
		ChangeStats:      changeStats,
		timestamped:      newTimestamped(),
	}
}

func (e *ResourceIndexUpdatedEvent) EventType() string { return EventTypeResourceIndexUpdated }

// ResourceSyncCompleteEvent is published when a resource watcher has completed.
// its initial sync with the Kubernetes API.
type ResourceSyncCompleteEvent struct {
	// ResourceTypeName identifies the resource type from config (e.g., "ingresses").
	ResourceTypeName string

	// InitialCount is the number of resources loaded during initial sync.
	InitialCount int

	timestamped
}

// NewResourceSyncCompleteEvent creates a new ResourceSyncCompleteEvent.
func NewResourceSyncCompleteEvent(resourceTypeName string, initialCount int) *ResourceSyncCompleteEvent {
	return &ResourceSyncCompleteEvent{
		ResourceTypeName: resourceTypeName,
		InitialCount:     initialCount,
		timestamped:      newTimestamped(),
	}
}

func (e *ResourceSyncCompleteEvent) EventType() string { return EventTypeResourceSyncComplete }

// IndexSynchronizedEvent is published when all resource watchers have completed.
// their initial sync and the system has a complete view of all resources.
//
// This is a critical milestone - the controller waits for this event before.
// starting reconciliation to ensure it has complete data.
type IndexSynchronizedEvent struct {
	// ResourceCounts maps resource types to their counts.
	ResourceCounts map[string]int
	timestamped
}

// NewIndexSynchronizedEvent creates a new IndexSynchronizedEvent.
// Performs defensive copy of the resource counts map.
func NewIndexSynchronizedEvent(resourceCounts map[string]int) *IndexSynchronizedEvent {
	// Defensive copy of map
	countsCopy := make(map[string]int, len(resourceCounts))
	maps.Copy(countsCopy, resourceCounts)

	return &IndexSynchronizedEvent{
		ResourceCounts: countsCopy,
		timestamped:    newTimestamped(),
	}
}

func (e *IndexSynchronizedEvent) EventType() string { return EventTypeIndexSynchronized }

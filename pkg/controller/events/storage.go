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

import "time"

// -----------------------------------------------------------------------------
// Storage Events (Auxiliary Files).
// -----------------------------------------------------------------------------

// StorageSyncStartedEvent is published when auxiliary file synchronization begins.
type StorageSyncStartedEvent struct {
	// Phase describes which sync phase: "pre-config", "config", "post-config"
	Phase     string
	Endpoints []interface{}
	timestamp time.Time
}

// NewStorageSyncStartedEvent creates a new StorageSyncStartedEvent.
// Performs defensive copy of the endpoints slice.
func NewStorageSyncStartedEvent(phase string, endpoints []interface{}) *StorageSyncStartedEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &StorageSyncStartedEvent{
		Phase:     phase,
		Endpoints: endpointsCopy,
		timestamp: time.Now(),
	}
}

func (e *StorageSyncStartedEvent) EventType() string    { return EventTypeStorageSyncStarted }
func (e *StorageSyncStartedEvent) Timestamp() time.Time { return e.timestamp }

// StorageSyncCompletedEvent is published when auxiliary file synchronization completes.
type StorageSyncCompletedEvent struct {
	Phase string

	// Stats contains sync statistics.
	// Type: interface{} to avoid circular dependencies.
	Stats interface{}

	DurationMs int64
	timestamp  time.Time
}

// NewStorageSyncCompletedEvent creates a new StorageSyncCompletedEvent.
func NewStorageSyncCompletedEvent(phase string, stats interface{}, durationMs int64) *StorageSyncCompletedEvent {
	return &StorageSyncCompletedEvent{
		Phase:      phase,
		Stats:      stats,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *StorageSyncCompletedEvent) EventType() string    { return EventTypeStorageSyncCompleted }
func (e *StorageSyncCompletedEvent) Timestamp() time.Time { return e.timestamp }

// StorageSyncFailedEvent is published when auxiliary file synchronization fails.
type StorageSyncFailedEvent struct {
	Phase     string
	Error     string
	timestamp time.Time
}

// NewStorageSyncFailedEvent creates a new StorageSyncFailedEvent.
func NewStorageSyncFailedEvent(phase, err string) *StorageSyncFailedEvent {
	return &StorageSyncFailedEvent{
		Phase:     phase,
		Error:     err,
		timestamp: time.Now(),
	}
}

func (e *StorageSyncFailedEvent) EventType() string    { return EventTypeStorageSyncFailed }
func (e *StorageSyncFailedEvent) Timestamp() time.Time { return e.timestamp }

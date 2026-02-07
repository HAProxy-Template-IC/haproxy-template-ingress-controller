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

// Package leadership provides utilities for handling leadership transitions
// in event-driven components.
package leadership

import (
	"sync"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// StateReplayer caches the latest event of type T and re-publishes it
// on demand (typically in response to BecameLeaderEvent).
//
// This solves the "late subscriber problem" where leader-only components
// start subscribing AFTER all-replica components have already published
// critical state events. By caching and replaying the last state, new
// leaders receive the current state immediately.
//
// Thread-safe for concurrent access.
type StateReplayer[T busevents.Event] struct {
	mu       sync.RWMutex
	event    T
	hasState bool
	eventBus *busevents.EventBus
}

// NewStateReplayer creates a new StateReplayer that publishes replay events
// to the given EventBus.
func NewStateReplayer[T busevents.Event](eventBus *busevents.EventBus) *StateReplayer[T] {
	return &StateReplayer[T]{
		eventBus: eventBus,
	}
}

// Cache stores the event for later replay.
// Only the latest event is retained (previous events are overwritten).
func (r *StateReplayer[T]) Cache(event T) {
	r.mu.Lock()
	r.event = event
	r.hasState = true
	r.mu.Unlock()
}

// Replay re-publishes the cached event to the EventBus.
// Returns true if an event was replayed, false if no state was cached.
func (r *StateReplayer[T]) Replay() bool {
	r.mu.RLock()
	hasState := r.hasState
	event := r.event
	r.mu.RUnlock()

	if !hasState {
		return false
	}

	r.eventBus.Publish(event)
	return true
}

// HasState returns whether an event has been cached.
func (r *StateReplayer[T]) HasState() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hasState
}

// Get returns the cached event and whether it exists.
func (r *StateReplayer[T]) Get() (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.event, r.hasState
}

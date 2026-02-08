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

const (
	// MaxPreStartBufferSize is the maximum number of events that can be buffered
	// before EventBus.Start() is called. This prevents unbounded memory growth
	// during startup if many events are published before subscribers are ready.
	// Events exceeding this limit are dropped with a warning.
	MaxPreStartBufferSize = 1000
)

// DropInfo contains information about a dropped event for debugging.
type DropInfo struct {
	EventType      string // The type of event that was dropped
	SubscriberName string // Name of the subscriber whose buffer was full
	BufferSize     int    // Total buffer size
	EventTypes     string // For typed subscriptions, the event types being filtered (comma-separated)
}

// DropCallback is called when an event is dropped due to a full subscriber buffer.
// This callback pattern keeps the EventBus domain-agnostic while allowing
// the controller layer to handle drops with appropriate logging and metrics.
type DropCallback func(info DropInfo)

// Event is the base interface for all events in the system.
// Events are used for asynchronous pub/sub communication between components.
type Event interface {
	// EventType returns a unique identifier for this event type.
	// Convention: use dot-notation like "config.parsed" or "deployment.completed"
	EventType() string

	// Timestamp returns when this event occurred.
	// Used for event correlation and temporal analysis.
	Timestamp() time.Time
}

// CoalescibleEvent is an optional interface for events that support coalescing.
// Events implementing this interface can be safely skipped when a newer event
// of the same type is available in the queue.
//
// This interface follows the Interface Segregation Principle - only events that
// need coalescing implement it, keeping the base Event interface minimal.
//
// The Coalescible() method is set by the event emitter (not derived from event
// fields), following the Single Responsibility Principle - the emitter knows
// the context and decides whether this specific event instance can be coalesced.
type CoalescibleEvent interface {
	Event
	// Coalescible returns true if this event can be safely skipped when a newer
	// event of the same type is available. The emitter sets this based on context:
	// - true: "state update" events where only the latest matters
	// - false: "command" events that must be processed individually
	Coalescible() bool
}

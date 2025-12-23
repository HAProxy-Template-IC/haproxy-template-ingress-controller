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

// Package events provides an event bus for component coordination in the HAPTIC controller.
//
// The event bus supports two communication patterns:
// 1. Async pub/sub: Fire-and-forget event publishing for observability and loose coupling
// 2. Sync request-response: Scatter-gather pattern for coordinated validation and queries
package events

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

const (
	// MaxPreStartBufferSize is the maximum number of events that can be buffered
	// before EventBus.Start() is called. This prevents unbounded memory growth
	// during startup if many events are published before subscribers are ready.
	// Events exceeding this limit are dropped with a warning.
	MaxPreStartBufferSize = 1000
)

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

// EventBus provides centralized pub/sub coordination for all controller components.
//
// The EventBus supports two patterns:
// - Publish() for async fire-and-forget events (observability, notifications)
// - Request() for sync scatter-gather pattern (validation, queries)
//
// EventBus is thread-safe and can be used concurrently from multiple goroutines.
//
// Startup Coordination:
// Events published before Start() is called are buffered and replayed after Start().
// This prevents race conditions during component initialization.
//
// Typed Subscriptions:
// In addition to universal subscriptions (Subscribe), the EventBus supports typed
// subscriptions (SubscribeTypes) that filter events at the bus level for efficiency.
type EventBus struct {
	subscribers      []chan Event
	typedSubscribers []*typedSubscription
	mu               sync.RWMutex

	// Startup coordination
	started        bool
	startMu        sync.Mutex
	preStartBuffer []Event
}

// NewEventBus creates a new EventBus.
//
// The bus starts in buffering mode - events published before Start() is called
// will be buffered and replayed when Start() is invoked. This ensures no events
// are lost during component initialization.
//
// The capacity parameter sets the initial buffer size for pre-start events.
// Recommended: 100 for most applications.
func NewEventBus(capacity int) *EventBus {
	return &EventBus{
		subscribers:      make([]chan Event, 0),
		typedSubscribers: make([]*typedSubscription, 0),
		started:          false,
		preStartBuffer:   make([]Event, 0, capacity),
	}
}

// Publish sends an event to all subscribers.
//
// If Start() has not been called yet, the event is buffered and will be
// replayed when Start() is invoked. This prevents events from being lost
// during component initialization.
//
// After Start() is called, this is a non-blocking operation. If a subscriber's
// channel is full, the event is dropped for that subscriber to prevent slow
// consumers from blocking the entire system.
//
// Returns the number of subscribers that successfully received the event.
// Returns 0 if event was buffered (before Start()).
func (b *EventBus) Publish(event Event) int {
	// Check if bus has started
	b.startMu.Lock()
	if !b.started {
		// Buffer event for replay after Start(), with capacity limit
		if len(b.preStartBuffer) >= MaxPreStartBufferSize {
			slog.Warn("Pre-start buffer capacity exceeded, dropping event",
				"capacity", MaxPreStartBufferSize,
				"event_type", event.EventType())
		} else {
			b.preStartBuffer = append(b.preStartBuffer, event)
		}
		b.startMu.Unlock()
		return 0
	}
	b.startMu.Unlock()

	// Bus has started - publish to subscribers
	b.mu.RLock()
	defer b.mu.RUnlock()

	sent := 0
	// Send to universal subscribers
	for _, ch := range b.subscribers {
		select {
		case ch <- event:
			sent++
		default:
			// Channel full, subscriber is lagging - drop event
			// This prevents slow consumers from blocking the system
		}
	}

	// Send to typed subscribers (filtered at bus level)
	for _, sub := range b.typedSubscribers {
		if sub.filterFunc(event) {
			select {
			case sub.outputChan <- event:
				sent++
			default:
				// Channel full, subscriber is lagging - drop event
			}
		}
	}
	return sent
}

// Subscribe creates a new subscription to the event bus.
//
// The returned channel will receive all events published to the bus.
// The bufferSize parameter controls the channel buffer size - larger
// buffers reduce the chance of dropped events for slow consumers.
//
// Subscribers must continuously read from the channel to avoid
// dropped events. A bufferSize of 100 is recommended for most use cases.
//
// The returned channel is read-only and will never be closed.
// To stop receiving events, the subscriber should call Unsubscribe()
// to remove the subscription and prevent memory leaks.
//
// IMPORTANT: For all-replica components, call this method BEFORE EventBus.Start()
// to ensure buffered events are received. Subscribing after Start() will trigger
// a warning as it may indicate a bug. For leader-only components that intentionally
// subscribe late (after leader election), use SubscribeLeaderOnly() instead.
func (b *EventBus) Subscribe(bufferSize int) <-chan Event {
	return b.subscribeInternal(bufferSize, false)
}

// SubscribeLeaderOnly creates a subscription for leader-only components.
//
// This method is identical to Subscribe() but does not log a warning when
// called after EventBus.Start(). Use this for components that only run on the
// leader replica and are intentionally started after leader election.
//
// Leader-only components rely on the state replay mechanism: all-replica components
// re-publish their cached state when BecameLeaderEvent is received, ensuring
// leader-only components don't miss critical state even though they subscribe late.
//
// The returned channel is read-only and will never be closed.
// To stop receiving events, the subscriber should call Unsubscribe()
// to remove the subscription and prevent memory leaks.
func (b *EventBus) SubscribeLeaderOnly(bufferSize int) <-chan Event {
	return b.subscribeInternal(bufferSize, true)
}

// subscribeInternal handles subscription creation with optional late subscription warning.
func (b *EventBus) subscribeInternal(bufferSize int, isLeaderOnly bool) <-chan Event {
	// Check if subscribing after Start() - may miss buffered events
	b.startMu.Lock()
	started := b.started
	b.startMu.Unlock()

	if started && !isLeaderOnly {
		// Get caller info for debugging
		_, file, line, ok := runtime.Caller(2)
		caller := "unknown"
		if ok {
			// Extract just the filename for brevity
			for i := len(file) - 1; i >= 0; i-- {
				if file[i] == '/' {
					file = file[i+1:]
					break
				}
			}
			caller = file
		}

		slog.Warn("Subscription after EventBus.Start() may miss buffered events - use SubscribeLeaderOnly for leader-only components",
			"caller", caller,
			"line", line)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, bufferSize)
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscription from the event bus.
//
// This method should be called when a subscriber no longer needs to receive
// events, to prevent memory leaks. After calling Unsubscribe, the channel
// will no longer receive events.
//
// Note: The channel is not closed by this method. The subscriber is responsible
// for draining any remaining events from the channel if needed.
//
// This method is safe to call multiple times for the same channel.
func (b *EventBus) Unsubscribe(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, sub := range b.subscribers {
		if sub == ch {
			// Remove subscriber by replacing with last element and truncating
			b.subscribers[i] = b.subscribers[len(b.subscribers)-1]
			b.subscribers = b.subscribers[:len(b.subscribers)-1]
			return
		}
	}
}

// UnsubscribeTyped removes a typed subscription from the event bus.
//
// This method should be called when a subscriber no longer needs to receive
// events from a typed subscription (created via SubscribeTypes), to prevent
// memory leaks.
//
// Note: The channel is not closed by this method. The subscriber is responsible
// for draining any remaining events from the channel if needed.
//
// This method is safe to call multiple times for the same channel.
func (b *EventBus) UnsubscribeTyped(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, sub := range b.typedSubscribers {
		if sub.outputChan == ch {
			// Remove subscriber by replacing with last element and truncating
			b.typedSubscribers[i] = b.typedSubscribers[len(b.typedSubscribers)-1]
			b.typedSubscribers = b.typedSubscribers[:len(b.typedSubscribers)-1]
			return
		}
	}
}

// Start releases all buffered events and switches the bus to normal operation mode.
//
// This method should be called after all components have subscribed to the bus
// during application startup. It ensures that no events are lost during the
// initialization phase.
//
// Behavior:
//  1. Marks the bus as started
//  2. Replays all buffered events to subscribers in order
//  3. Clears the buffer
//  4. All subsequent Publish() calls go directly to subscribers
//
// This method is idempotent - calling it multiple times has no additional effect.
// Thread-safe and can be called concurrently with Publish() and Subscribe().
//
// Example:
//
//	bus := NewEventBus(100)
//
//	// Components subscribe during setup
//	commentator := NewEventCommentator(bus, logger, 1000)
//	validator := NewValidator(bus)
//	// ... more subscribers ...
//
//	// Release buffered events
//	bus.Start()
func (b *EventBus) Start() {
	b.startMu.Lock()
	defer b.startMu.Unlock()

	// Idempotent - return if already started
	if b.started {
		return
	}

	// Mark as started (must be done before replaying to avoid recursion)
	b.started = true

	// Replay buffered events to subscribers
	b.replayBufferedEvents()
}

// replayBufferedEvents sends all buffered events to subscribers.
// Must be called while holding startMu lock.
func (b *EventBus) replayBufferedEvents() {
	if len(b.preStartBuffer) == 0 {
		return
	}

	b.mu.RLock()
	subscribers := b.subscribers
	typedSubs := b.typedSubscribers
	b.mu.RUnlock()

	for _, event := range b.preStartBuffer {
		b.replayEventToSubscribers(event, subscribers, typedSubs)
	}

	// Clear buffer
	b.preStartBuffer = nil
}

// replayEventToSubscribers sends a single event to all subscribers.
func (b *EventBus) replayEventToSubscribers(event Event, subscribers []chan Event, typedSubs []*typedSubscription) {
	// Send to universal subscribers
	for _, ch := range subscribers {
		select {
		case ch <- event:
			// Event sent
		default:
			// Channel full - drop event (same behavior as normal Publish)
		}
	}

	// Send to typed subscribers (filtered at bus level)
	for _, sub := range typedSubs {
		if sub.filterFunc(event) {
			select {
			case sub.outputChan <- event:
				// Event sent
			default:
				// Channel full - drop event
			}
		}
	}
}

// Request sends a request event and waits for responses using the scatter-gather pattern.
//
// This is a synchronous operation that:
// 1. Publishes the request event to all subscribers (scatter phase)
// 2. Collects response events matching the request ID (gather phase)
// 3. Returns when all expected responders have replied or timeout occurs
//
// The request must implement the Request interface to provide a unique RequestID
// for correlating responses.
//
// Use this method when you need coordinated responses from multiple components,
// such as multi-phase validation or distributed queries.
//
// Example:
//
//	req := NewConfigValidationRequest(config, version)
//	result, err := bus.Request(ctx, req, RequestOptions{
//	    Timeout: 10 * time.Second,
//	    ExpectedResponders: []string{"basic", "template", "jsonpath"},
//	})
func (b *EventBus) Request(ctx context.Context, request Request, opts RequestOptions) (*RequestResult, error) {
	return executeRequest(ctx, b, request, opts)
}

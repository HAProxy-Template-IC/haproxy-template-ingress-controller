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
	"sync/atomic"
	"time"
)

const (
	// MaxPreStartBufferSize is the maximum number of events that can be buffered
	// before EventBus.Start() is called. This prevents unbounded memory growth
	// during startup if many events are published before subscribers are ready.
	// Events exceeding this limit are dropped with a warning.
	MaxPreStartBufferSize = 1000
)

// DropCallback is called when an event is dropped due to a full subscriber buffer.
// The eventType parameter contains the event's type string.
// This callback pattern keeps the EventBus domain-agnostic while allowing
// the controller layer to handle drops with appropriate logging and metrics.
type DropCallback func(eventType string)

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

// subscriber represents a universal subscription to the event bus.
type subscriber struct {
	ch    chan Event
	lossy bool // If true, drops are silent (no onDrop callback)
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
//
// Lossy Subscriptions:
// For observability components where occasional drops are acceptable, use SubscribeLossy()
// or SubscribeTypesLossy(). These subscriptions silently drop events without triggering
// the onDrop callback, and drops are counted separately in DroppedEventsObservability().
//
// Event Drop Monitoring:
// When subscriber buffers are full, events are dropped to prevent blocking.
// Use SetDropCallback() to receive notifications when critical drops occur.
// DroppedEventsCritical() returns drops from business-critical subscribers.
// DroppedEventsObservability() returns expected drops from lossy subscribers.
type EventBus struct {
	subscribers      []subscriber // Changed from []chan Event to support lossy flag
	typedSubscribers []*typedSubscription
	mu               sync.RWMutex

	// Startup coordination
	started        bool
	startMu        sync.Mutex
	preStartBuffer []Event

	// Event drop monitoring - separate counters for different subscriber types
	droppedEventsCritical      uint64       // atomic: drops from critical subscribers (triggers WARN)
	droppedEventsObservability uint64       // atomic: drops from lossy subscribers (silent)
	onDrop                     DropCallback // optional callback for drop notifications (critical only)
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
		subscribers:      make([]subscriber, 0),
		typedSubscribers: make([]*typedSubscription, 0),
		started:          false,
		preStartBuffer:   make([]Event, 0, capacity),
	}
}

// recordDrop records a dropped event and optionally calls the onDrop callback.
// This consolidates drop handling logic into a single place (DRY principle).
//
// For lossy subscribers, drops are counted in droppedEventsObservability and
// no callback is triggered (these drops are expected and acceptable).
//
// For critical subscribers, drops are counted in droppedEventsCritical and
// the onDrop callback is triggered (these drops indicate a problem).
func (b *EventBus) recordDrop(eventType string, lossy bool) {
	if lossy {
		atomic.AddUint64(&b.droppedEventsObservability, 1)
		// No callback for lossy subscribers - drops are expected
	} else {
		atomic.AddUint64(&b.droppedEventsCritical, 1)
		if b.onDrop != nil {
			b.onDrop(eventType)
		}
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
	eventType := event.EventType()

	// Send to universal subscribers
	for _, sub := range b.subscribers {
		select {
		case sub.ch <- event:
			sent++
		default:
			// Channel full, subscriber is lagging - drop event
			b.recordDrop(eventType, sub.lossy)
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
				b.recordDrop(eventType, sub.lossy)
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
	return b.subscribeInternal(bufferSize, false, false)
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
	return b.subscribeInternal(bufferSize, true, false)
}

// SubscribeLossy creates a subscription that silently drops events when buffer is full.
//
// Use this for observability components (like commentator, debug/events) where
// occasional event drops are acceptable and expected during high load. Drops from
// lossy subscribers:
//   - Are counted in DroppedEventsObservability() (for metrics)
//   - Do NOT trigger the onDrop callback (no WARN logs)
//
// This prevents log spam from expected drops in observability components while
// still allowing monitoring via metrics.
//
// The returned channel is read-only and will never be closed.
// To stop receiving events, the subscriber should call Unsubscribe()
// to remove the subscription and prevent memory leaks.
func (b *EventBus) SubscribeLossy(bufferSize int) <-chan Event {
	return b.subscribeInternal(bufferSize, false, true)
}

// subscribeInternal handles subscription creation with optional late subscription warning.
//
// Parameters:
//   - bufferSize: Size of the channel buffer
//   - suppressLateWarning: If true, don't warn when subscribing after Start()
//   - lossy: If true, drops are silent (no onDrop callback, counted separately)
//
// Set suppressLateWarning to true for:
//   - Leader-only components that intentionally subscribe after leader election
//   - Internal infrastructure (e.g., scatter-gather) that creates temporary subscriptions
//
// Set lossy to true for:
//   - Observability components where occasional drops are acceptable
//   - Debug components that shouldn't affect system behavior
func (b *EventBus) subscribeInternal(bufferSize int, suppressLateWarning, lossy bool) <-chan Event {
	// Check if subscribing after Start() - may miss buffered events
	b.startMu.Lock()
	started := b.started
	b.startMu.Unlock()

	if started && !suppressLateWarning {
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

		slog.Warn("Subscription after EventBus.Start() may miss buffered events",
			"caller", caller,
			"line", line,
			"hint", "use SubscribeLeaderOnly() for leader-only components")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, bufferSize)
	b.subscribers = append(b.subscribers, subscriber{ch: ch, lossy: lossy})
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
		if sub.ch == ch {
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

// Pause temporarily suspends event delivery, buffering events for later replay.
//
// This reuses the existing preStartBuffer infrastructure used during startup.
// Events published while paused are buffered and will be replayed when Start()
// is called again.
//
// Use cases:
//   - Leadership transition (pause while starting leader-only components)
//   - Hot reload scenarios
//   - Testing
//
// This method is idempotent - calling it when already paused has no effect.
// Thread-safe and can be called concurrently with Publish() and Subscribe().
//
// Example:
//
//	// During leadership transition
//	bus.Pause()                                    // Buffer events
//	bus.Publish(BecameLeaderEvent{})               // Buffered
//	startLeaderOnlyComponents()                    // Components subscribe
//	bus.Start()                                    // Replay buffered events
func (b *EventBus) Pause() {
	b.startMu.Lock()
	defer b.startMu.Unlock()

	// Idempotent - return if already paused
	if !b.started {
		return
	}

	// Return to buffering mode
	b.started = false
	b.preStartBuffer = make([]Event, 0, 100)

	slog.Debug("EventBus paused, entering buffering mode")
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
func (b *EventBus) replayEventToSubscribers(event Event, subscribers []subscriber, typedSubs []*typedSubscription) {
	eventType := event.EventType()

	// Send to universal subscribers
	for _, sub := range subscribers {
		select {
		case sub.ch <- event:
			// Event sent
		default:
			// Channel full - drop event (same behavior as normal Publish)
			b.recordDrop(eventType, sub.lossy)
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
				b.recordDrop(eventType, sub.lossy)
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

// SetDropCallback sets a callback to be invoked when events are dropped
// from CRITICAL (non-lossy) subscriber buffers. The callback receives the
// event type string.
//
// This callback is NOT called for lossy subscribers (created via SubscribeLossy()
// or SubscribeTypesLossy()). Lossy drops are expected and silently counted
// in DroppedEventsObservability().
//
// This callback pattern keeps the EventBus domain-agnostic while allowing
// the controller layer to handle drops with appropriate logging and metrics.
//
// Set to nil to disable drop notifications.
//
// Example:
//
//	bus.SetDropCallback(func(eventType string) {
//	    slog.Warn("Event dropped from critical subscriber", "event_type", eventType)
//	    metrics.EventsDroppedCritical.Inc()
//	})
func (b *EventBus) SetDropCallback(cb DropCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onDrop = cb
}

// DroppedEventsCritical returns the number of events dropped from
// business-critical (non-lossy) subscribers.
//
// Non-zero values indicate a problem that needs attention - critical
// subscribers are not keeping up with event volume.
func (b *EventBus) DroppedEventsCritical() uint64 {
	return atomic.LoadUint64(&b.droppedEventsCritical)
}

// DroppedEventsObservability returns the number of events dropped from
// lossy subscribers (observability components like commentator, debug/events).
//
// Non-zero values are expected and acceptable during high load. These drops
// don't affect controller operation - they just mean some log entries or
// debug info was skipped.
func (b *EventBus) DroppedEventsObservability() uint64 {
	return atomic.LoadUint64(&b.droppedEventsObservability)
}

// DroppedEvents returns the total number of events that have been dropped
// due to full subscriber buffers since the EventBus was created.
//
// This is the sum of DroppedEventsCritical() + DroppedEventsObservability()
// and is kept for backwards compatibility.
//
// For more actionable monitoring, use the separate counters:
//   - DroppedEventsCritical() - Alert if > 0 (indicates a problem)
//   - DroppedEventsObservability() - Expected during high load (no action needed)
func (b *EventBus) DroppedEvents() uint64 {
	return b.DroppedEventsCritical() + b.DroppedEventsObservability()
}

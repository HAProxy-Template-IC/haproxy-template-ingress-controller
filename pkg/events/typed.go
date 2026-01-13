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
	"context"
	"log/slog"
	"runtime"
	"strings"
)

// typedSubscription represents a subscription filtered by event type.
type typedSubscription struct {
	eventTypes    []string
	eventTypesStr string // Comma-separated event types for debugging
	outputChan    chan Event
	filterFunc    func(Event) bool
	name          string // Subscriber name for debugging (e.g., "renderer", "deployer")
	bufferSize    int    // Original buffer size for debugging
	lossy         bool   // If true, drops are silent (no onDrop callback)
}

// SubscribeTypes creates a subscription that only receives events of the specified types.
//
// This is more efficient than universal Subscribe() when a component only cares about
// specific event types, as filtering happens at the EventBus level rather than in
// each subscriber's event loop.
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "reconciler", "renderer")
//   - bufferSize: Size of the output channel buffer
//   - eventTypes: Event type strings to filter for (from Event.EventType())
//
// Returns a channel that receives only events matching the specified types.
// The channel is read-only and will never be closed.
//
// To stop receiving events and prevent memory leaks, call UnsubscribeTyped()
// with the returned channel.
//
// Example:
//
//	eventChan := bus.SubscribeTypes("executor", 100,
//	    "reconciliation.triggered",
//	    "template.rendered",
//	    "validation.completed")
//	defer bus.UnsubscribeTyped(eventChan) // Clean up when done
//	for event := range eventChan {
//	    // Only receives the specified event types
//	}
func (b *EventBus) SubscribeTypes(name string, bufferSize int, eventTypes ...string) <-chan Event {
	return b.subscribeTypesInternal(name, bufferSize, eventTypes, false, false)
}

// SubscribeTypesLeaderOnly creates a typed subscription for leader-only components.
//
// This method is identical to SubscribeTypes() but is semantically named to indicate
// it's intended for leader-only components that subscribe after leader election.
//
// Leader-only components rely on the state replay mechanism: all-replica components
// re-publish their cached state when BecameLeaderEvent is received, ensuring
// leader-only components don't miss critical state even though they subscribe late.
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "deployer", "scheduler")
//   - bufferSize: Size of the output channel buffer
//   - eventTypes: Event type strings to filter for (from Event.EventType())
//
// Returns a channel that receives only events matching the specified types.
// The channel is read-only and will never be closed.
//
// To stop receiving events and prevent memory leaks, call UnsubscribeTyped()
// with the returned channel.
//
// Example:
//
//	// In a leader-only component's constructor (after BecameLeaderEvent)
//	eventChan := bus.SubscribeTypesLeaderOnly("scheduler", 50,
//	    events.EventTypeTemplateRendered,
//	    events.EventTypeValidationCompleted,
//	    events.EventTypeLostLeadership)
//	defer bus.UnsubscribeTyped(eventChan)
func (b *EventBus) SubscribeTypesLeaderOnly(name string, bufferSize int, eventTypes ...string) <-chan Event {
	return b.subscribeTypesInternal(name, bufferSize, eventTypes, true, false)
}

// SubscribeTypesLossy creates a typed subscription that silently drops events when full.
//
// Use this for observability components that filter by event type but where occasional
// drops are acceptable. Drops from lossy subscribers:
//   - Are counted in DroppedEventsObservability() (for metrics)
//   - Do NOT trigger the onDrop callback (no WARN logs)
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "metrics", "debug-events")
//   - bufferSize: Size of the output channel buffer
//   - eventTypes: Event type strings to filter for (from Event.EventType())
//
// Returns a channel that receives only events matching the specified types.
// The channel is read-only and will never be closed.
//
// To stop receiving events and prevent memory leaks, call UnsubscribeTyped()
// with the returned channel.
func (b *EventBus) SubscribeTypesLossy(name string, bufferSize int, eventTypes ...string) <-chan Event {
	return b.subscribeTypesInternal(name, bufferSize, eventTypes, false, true)
}

// subscribeTypesInternal creates a typed subscription with event type filtering.
//
// Parameters:
//   - name: Subscriber name for debugging
//   - bufferSize: Size of the output channel buffer
//   - eventTypes: Event type strings to filter for
//   - suppressLateWarning: If true, suppresses warning when subscribing after Start()
//   - lossy: If true, drops are silent (no onDrop callback, counted separately)
func (b *EventBus) subscribeTypesInternal(name string, bufferSize int, eventTypes []string, suppressLateWarning, lossy bool) <-chan Event {
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

		slog.Warn("Typed subscription after EventBus.Start() may miss buffered events",
			"caller", caller,
			"line", line,
			"event_types", eventTypes,
			"hint", "use SubscribeTypesLeaderOnly() for leader-only components")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Create type lookup map for efficient filtering
	typeSet := make(map[string]struct{}, len(eventTypes))
	for _, t := range eventTypes {
		typeSet[t] = struct{}{}
	}

	// Create filter function
	filterFunc := func(e Event) bool {
		_, ok := typeSet[e.EventType()]
		return ok
	}

	// Create output channel
	outputChan := make(chan Event, bufferSize)

	// Create internal subscription with filter
	sub := &typedSubscription{
		eventTypes:    eventTypes,
		eventTypesStr: strings.Join(eventTypes, ","),
		outputChan:    outputChan,
		filterFunc:    filterFunc,
		name:          name,
		bufferSize:    bufferSize,
		lossy:         lossy,
	}

	// Register typed subscription
	b.typedSubscribers = append(b.typedSubscribers, sub)

	return outputChan
}

// Subscribe is a generic function that returns a typed channel for a specific event type.
//
// This provides compile-time type safety by returning a channel of the specific event type T.
// Events are filtered to only include those that match type T.
//
// The generic type T must be a pointer to a struct that implements the Event interface.
// This is required because events are typically published as pointer types.
//
// Parameters:
//   - ctx: Context for the goroutine lifetime
//   - bus: The EventBus to subscribe to
//   - bufferSize: Size of the output channel buffer
//
// Returns a channel of type T that receives only events of that type.
//
// Example:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	eventChan := events.Subscribe[*ReconciliationTriggeredEvent](ctx, bus, 100)
//	for event := range eventChan {
//	    // event is already *ReconciliationTriggeredEvent
//	    fmt.Println(event.Reason)
//	}
//
// Note: The context controls the lifetime of the internal forwarding goroutine.
// When the context is cancelled, the goroutine stops, the subscription is
// removed from the bus, and the channel will stop receiving events.
func Subscribe[T Event](ctx context.Context, bus *EventBus, bufferSize int) <-chan T {
	// Subscribe to universal channel (internal usage - use generic type name)
	universalChan := bus.Subscribe("generic-typed", bufferSize)

	// Create typed output channel
	typedChan := make(chan T, bufferSize)

	// Start forwarding goroutine
	go func() {
		defer bus.Unsubscribe(universalChan) // Clean up subscription on exit

		for {
			select {
			case event := <-universalChan:
				// Type assert and forward if match
				if typed, ok := event.(T); ok {
					select {
					case typedChan <- typed:
						// Sent successfully
					default:
						// Channel full - drop event (same behavior as universal)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return typedChan
}

// SubscribeMultiple is a generic function that returns a typed channel for multiple event types.
//
// This is useful when a component needs to receive events of different types that share
// a common interface or base type.
//
// Parameters:
//   - ctx: Context for the goroutine lifetime
//   - bus: The EventBus to subscribe to
//   - bufferSize: Size of the output channel buffer
//   - types: Event type strings to filter for
//
// Returns a channel that receives events matching any of the specified types.
//
// Example:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	eventChan := events.SubscribeMultiple(ctx, bus, 100,
//	    "reconciliation.triggered",
//	    "reconciliation.started",
//	    "reconciliation.completed")
//	for event := range eventChan {
//	    switch e := event.(type) {
//	    case *ReconciliationTriggeredEvent:
//	        // handle
//	    case *ReconciliationStartedEvent:
//	        // handle
//	    }
//	}
func SubscribeMultiple(ctx context.Context, bus *EventBus, bufferSize int, types ...string) <-chan Event {
	// Subscribe to universal channel (internal usage - use generic type name)
	universalChan := bus.Subscribe("multiple-typed", bufferSize)

	// Create type lookup map for efficient filtering
	typeSet := make(map[string]struct{}, len(types))
	for _, t := range types {
		typeSet[t] = struct{}{}
	}

	// Create output channel
	outputChan := make(chan Event, bufferSize)

	// Start forwarding goroutine
	go func() {
		defer bus.Unsubscribe(universalChan) // Clean up subscription on exit

		for {
			select {
			case event := <-universalChan:
				// Check if event type matches
				if _, ok := typeSet[event.EventType()]; ok {
					select {
					case outputChan <- event:
						// Sent successfully
					default:
						// Channel full - drop event
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return outputChan
}

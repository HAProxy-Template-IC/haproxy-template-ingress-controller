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
	"log/slog"
	"runtime"
)

// subscriber represents a universal subscription to the event bus.
type subscriber struct {
	ch         chan Event
	name       string // Subscriber name for debugging (e.g., "commentator", "reconciler")
	bufferSize int    // Original buffer size for debugging
	lossy      bool   // If true, drops are silent (no onDrop callback)
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
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "commentator", "reconciler")
//   - bufferSize: Size of the channel buffer
func (b *EventBus) Subscribe(name string, bufferSize int) <-chan Event {
	return b.subscribeInternal(name, bufferSize, false, false)
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
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "deployer", "scheduler")
//   - bufferSize: Size of the channel buffer
func (b *EventBus) SubscribeLeaderOnly(name string, bufferSize int) <-chan Event {
	return b.subscribeInternal(name, bufferSize, true, false)
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
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "commentator")
//   - bufferSize: Size of the channel buffer
func (b *EventBus) SubscribeLossy(name string, bufferSize int) <-chan Event {
	return b.subscribeInternal(name, bufferSize, false, true)
}

// subscribeInternal handles subscription creation with optional late subscription warning.
//
// Parameters:
//   - name: Subscriber name for debugging (e.g., "commentator", "reconciler")
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
func (b *EventBus) subscribeInternal(name string, bufferSize int, suppressLateWarning, lossy bool) <-chan Event {
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
	b.subscribers = append(b.subscribers, subscriber{
		ch:         ch,
		name:       name,
		bufferSize: bufferSize,
		lossy:      lossy,
	})
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

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

// Package coalesce provides utilities for event coalescing in controller components.
//
// Event coalescing implements the "latest wins" pattern where intermediate events
// are skipped when newer events of the same type are available. This prevents
// queue backlog when events arrive faster than they can be processed.
//
// Only events that implement CoalescibleEvent and return Coalescible() == true
// are coalesced. Other events are passed to the handleOther callback.
package coalesce

import (
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// DrainLatest drains the event channel and returns the latest coalescible event
// of type T. Non-coalescible events and events of other types are passed to handleOther.
// Returns the zero value of T and 0 if no coalescible events were found.
//
// An event is coalescible if:
//  1. It matches type T
//  2. It implements CoalescibleEvent interface
//  3. Its Coalescible() method returns true
//
// Usage pattern:
//
//	func (c *Component) handleSomeEvent(event *events.SomeEvent) {
//	    c.performWork(event)
//
//	    // After work completes, drain for latest coalescible event
//	    for {
//	        latest, superseded := coalesce.DrainLatest[*events.SomeEvent](
//	            c.eventChan,
//	            c.handleEvent, // Handle non-coalescible and other event types
//	        )
//	        if latest == nil {
//	            return
//	        }
//	        c.logger.Debug("Processing coalesced event",
//	            "superseded_count", superseded)
//	        c.performWork(latest)
//	    }
//	}
func DrainLatest[T busevents.Event](
	eventChan <-chan busevents.Event,
	handleOther func(busevents.Event),
) (latest T, supersededCount int) {
	winner, superseded := drainLatest(eventChan, handleOther, func(event busevents.Event) bool {
		_, matchesType := event.(T)
		return matchesType
	})
	if winner == nil {
		return latest, superseded // zero value of T, 0
	}
	return winner.(T), superseded
}

// DrainLatestByType is the runtime-typed sibling of DrainLatest. Instead of a
// compile-time type parameter it matches events whose EventType() equals
// eventType, returning the latest coalescible match as a busevents.Event (nil
// when none was found). All other events are passed to handleOther. Components
// that select on a dynamic event-type string (e.g. component.Base's coalescing
// loop, which reads the type from a CoalescingHandler) use this; consumers with
// a static type use the generic DrainLatest.
func DrainLatestByType(
	eventChan <-chan busevents.Event,
	eventType string,
	handleOther func(busevents.Event),
) (latest busevents.Event, supersededCount int) {
	return drainLatest(eventChan, handleOther, func(event busevents.Event) bool {
		return event.EventType() == eventType
	})
}

// drainLatest is the shared "latest coalescible wins" drain loop behind
// DrainLatest and DrainLatestByType. It non-blockingly pulls events off
// eventChan; an event is a candidate when match(event) is true AND it is a
// coalescible CoalescibleEvent. Candidates supersede earlier candidates;
// every non-candidate (wrong match or not coalescible) is passed to
// handleOther as it arrives. Returns the latest candidate (nil when none) and
// the count of superseded earlier candidates.
func drainLatest(
	eventChan <-chan busevents.Event,
	handleOther func(busevents.Event),
	match func(busevents.Event) bool,
) (latest busevents.Event, supersededCount int) {
	for {
		select {
		case event := <-eventChan:
			if !match(event) {
				handleOther(event)
				continue
			}

			// Check if event implements CoalescibleEvent and is coalescible
			coalescible, ok := event.(busevents.CoalescibleEvent)
			if !ok || !coalescible.Coalescible() {
				// Matches but not coalescible - must process
				handleOther(event)
				continue
			}

			// Coalescible - supersede previous
			if latest != nil {
				supersededCount++
			}
			latest = event
		default:
			// No more events in channel
			return latest, supersededCount
		}
	}
}

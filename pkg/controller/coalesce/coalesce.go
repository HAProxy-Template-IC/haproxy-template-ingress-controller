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
	hasLatest := false
	for {
		select {
		case event := <-eventChan:
			typed, matchesType := event.(T)
			if !matchesType {
				handleOther(event)
				continue
			}

			// Check if event implements CoalescibleEvent and is coalescible
			coalescible, ok := event.(busevents.CoalescibleEvent)
			if !ok || !coalescible.Coalescible() {
				// Type matches but not coalescible - must process
				handleOther(event)
				continue
			}

			// Coalescible - supersede previous
			if hasLatest {
				supersededCount++
			}
			latest = typed
			hasLatest = true
		default:
			// No more events in channel
			return latest, supersededCount
		}
	}
}

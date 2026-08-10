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
// are coalesced, and only within an uninterrupted run of such events. Any other
// event (different type, or same type but not coalescible) is a run boundary:
// the held latest event of the current run is flushed FIRST, then the boundary
// event is passed to handleOther. This preserves arrival order across event
// types and guarantees the coalesced type cannot be starved: an earlier
// design held the run's latest back until the channel drained empty, and under
// sustained mixed traffic (e.g. deployment-completed status applies each taking
// longer than the event arrival gap) that point never came — rendered status
// patches were starved for the entire burst (54s observed in gateway-api
// conformance) while newer other-type events were dispatched ahead of the older
// held event.
package coalesce

import (
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// DrainLatest drains the event channel until it is momentarily empty.
// Uninterrupted runs of coalescible events of type T collapse to their latest
// element, delivered via flush together with the count of superseded events.
// Every other event is a run boundary: the held run is flushed first, then the
// event is passed to handleOther, preserving arrival order.
//
// An event joins the current run if:
//  1. It matches type T
//  2. It implements CoalescibleEvent interface
//  3. Its Coalescible() method returns true
//
// Usage pattern:
//
//	func (c *Component) handleSomeEvent(event *events.SomeEvent) {
//	    c.performWork(event)
//
//	    // After work completes, drain queued events: consecutive coalescible
//	    // SomeEvents collapse to their latest, everything else is handled in
//	    // arrival order.
//	    coalesce.DrainLatest(
//	        c.eventChan,
//	        c.handleEvent, // Handle non-coalescible and other event types
//	        func(latest *events.SomeEvent, superseded int) {
//	            c.performWork(latest)
//	        },
//	    )
//	}
func DrainLatest[T busevents.Event](
	eventChan <-chan busevents.Event,
	handleOther func(busevents.Event),
	flush func(latest T, supersededCount int),
) {
	drainLatest(eventChan, handleOther, func(event busevents.Event) bool {
		_, matchesType := event.(T)
		return matchesType
	}, func(latest busevents.Event, supersededCount int) {
		flush(latest.(T), supersededCount)
	})
}

// drainLatest is the shared coalescing drain loop behind DrainLatest and
// DrainLatestByType. It non-blockingly pulls events off eventChan; an event
// joins the current run when match(event) is true AND it is a coalescible
// CoalescibleEvent, superseding the run's earlier events. Any other event is a
// run boundary: the held run is flushed BEFORE the event goes to handleOther,
// so cross-type arrival order is preserved and sustained other-type traffic
// cannot starve the coalesced type. The trailing run is flushed before
// returning when the channel is empty.
//
// NOTE: reconciler.Coordinator.coalesceQueuedTriggers is a deliberately
// DIFFERENT hand-rolled drain, not an accidental duplicate — it merges an
// entire drained run (coalescible or not) into a single re-render,
// exploiting the fact that a render always reads current store state; this
// one preserves per-event dispatch with arrival ordering.
func drainLatest(
	eventChan <-chan busevents.Event,
	handleOther func(busevents.Event),
	match func(busevents.Event) bool,
	flush func(latest busevents.Event, supersededCount int),
) {
	var latest busevents.Event
	superseded := 0
	emit := func() {
		if latest == nil {
			return
		}
		event, count := latest, superseded
		latest, superseded = nil, 0
		flush(event, count)
	}
	for {
		select {
		case event := <-eventChan:
			if match(event) {
				if coalescible, ok := event.(busevents.CoalescibleEvent); ok && coalescible.Coalescible() {
					if latest != nil {
						superseded++
					}
					latest = event
					continue
				}
			}
			emit()
			handleOther(event)
		default:
			emit()
			return
		}
	}
}

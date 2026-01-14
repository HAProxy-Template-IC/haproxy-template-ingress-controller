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

package coalesce

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// testEvent is a simple event for testing that doesn't implement CoalescibleEvent.
type testEvent struct {
	value     string
	timestamp time.Time
}

func (e *testEvent) EventType() string    { return "test.event" }
func (e *testEvent) Timestamp() time.Time { return e.timestamp }

// coalescibleEvent implements CoalescibleEvent for testing.
type coalescibleEvent struct {
	value       string
	timestamp   time.Time
	coalescible bool
}

func (e *coalescibleEvent) EventType() string    { return "coalescible.event" }
func (e *coalescibleEvent) Timestamp() time.Time { return e.timestamp }
func (e *coalescibleEvent) Coalescible() bool    { return e.coalescible }

// otherCoalescibleEvent is a different type implementing CoalescibleEvent.
type otherCoalescibleEvent struct {
	value       string
	timestamp   time.Time
	coalescible bool
}

func (e *otherCoalescibleEvent) EventType() string    { return "other.coalescible" }
func (e *otherCoalescibleEvent) Timestamp() time.Time { return e.timestamp }
func (e *otherCoalescibleEvent) Coalescible() bool    { return e.coalescible }

func TestDrainLatest_EmptyChannel(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.Nil(t, latest)
	assert.Equal(t, 0, superseded)
	assert.Empty(t, handled)
}

func TestDrainLatest_SingleCoalescibleEvent(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	event := &coalescibleEvent{value: "first", coalescible: true}
	ch <- event

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.Equal(t, event, latest)
	assert.Equal(t, 0, superseded)
	assert.Empty(t, handled)
}

func TestDrainLatest_MultipleCoalescibleEvents(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	ch <- &coalescibleEvent{value: "first", coalescible: true}
	ch <- &coalescibleEvent{value: "second", coalescible: true}
	ch <- &coalescibleEvent{value: "third", coalescible: true}

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.NotNil(t, latest)
	assert.Equal(t, "third", latest.value)
	assert.Equal(t, 2, superseded)
	assert.Empty(t, handled)
}

func TestDrainLatest_NonCoalescibleEventsPassedToHandler(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	// Mix of coalescible and non-coalescible
	ch <- &coalescibleEvent{value: "c1", coalescible: true}
	ch <- &coalescibleEvent{value: "c2", coalescible: false} // Not coalescible
	ch <- &coalescibleEvent{value: "c3", coalescible: true}

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.NotNil(t, latest)
	assert.Equal(t, "c3", latest.value)
	assert.Equal(t, 1, superseded) // Only c1 was superseded
	assert.Len(t, handled, 1)      // c2 was passed to handler
	assert.Equal(t, "c2", handled[0].(*coalescibleEvent).value)
}

func TestDrainLatest_DifferentEventTypesPassedToHandler(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	ch <- &coalescibleEvent{value: "target1", coalescible: true}
	ch <- &testEvent{value: "other"} // Different type
	ch <- &coalescibleEvent{value: "target2", coalescible: true}

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.NotNil(t, latest)
	assert.Equal(t, "target2", latest.value)
	assert.Equal(t, 1, superseded)
	assert.Len(t, handled, 1)
	assert.Equal(t, "other", handled[0].(*testEvent).value)
}

func TestDrainLatest_EventWithoutCoalescibleInterface(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	// testEvent doesn't implement CoalescibleEvent
	ch <- &testEvent{value: "first"}
	ch <- &testEvent{value: "second"}

	latest, superseded := DrainLatest[*testEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	// Events without CoalescibleEvent interface are not coalescible
	assert.Nil(t, latest)
	assert.Equal(t, 0, superseded)
	assert.Len(t, handled, 2) // Both passed to handler
}

func TestDrainLatest_MixedEventTypes(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	// Various event types in the channel
	ch <- &coalescibleEvent{value: "c1", coalescible: true}
	ch <- &otherCoalescibleEvent{value: "o1", coalescible: true}
	ch <- &testEvent{value: "t1"}
	ch <- &coalescibleEvent{value: "c2", coalescible: true}
	ch <- &coalescibleEvent{value: "c3", coalescible: false}
	ch <- &coalescibleEvent{value: "c4", coalescible: true}

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.NotNil(t, latest)
	assert.Equal(t, "c4", latest.value)
	assert.Equal(t, 2, superseded) // c1 and c2 were superseded
	assert.Len(t, handled, 3)      // o1, t1, and c3 were passed to handler
}

func TestDrainLatest_OnlyNonCoalescible(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	var handled []busevents.Event

	ch <- &coalescibleEvent{value: "c1", coalescible: false}
	ch <- &coalescibleEvent{value: "c2", coalescible: false}

	latest, superseded := DrainLatest[*coalescibleEvent](ch, func(e busevents.Event) {
		handled = append(handled, e)
	})

	assert.Nil(t, latest)
	assert.Equal(t, 0, superseded)
	assert.Len(t, handled, 2) // All passed to handler
}

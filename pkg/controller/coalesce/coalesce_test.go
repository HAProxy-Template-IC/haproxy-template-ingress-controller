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
	"github.com/stretchr/testify/require"
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

// flushRecord captures one flush callback invocation.
type flushRecord struct {
	value      string
	superseded int
}

// drainRecorder runs DrainLatest over ch recording both callbacks and the
// combined delivery order ("flush:v" / "other:v" entries).
func drainRecorder(ch chan busevents.Event) (flushes []flushRecord, handled []busevents.Event, order []string) {
	DrainLatest(
		ch,
		func(e busevents.Event) {
			handled = append(handled, e)
			switch ev := e.(type) {
			case *coalescibleEvent:
				order = append(order, "other:"+ev.value)
			case *otherCoalescibleEvent:
				order = append(order, "other:"+ev.value)
			case *testEvent:
				order = append(order, "other:"+ev.value)
			}
		},
		func(latest *coalescibleEvent, superseded int) {
			flushes = append(flushes, flushRecord{value: latest.value, superseded: superseded})
			order = append(order, "flush:"+latest.value)
		},
	)
	return flushes, handled, order
}

func TestDrainLatest_EmptyChannel(t *testing.T) {
	ch := make(chan busevents.Event, 10)

	flushes, handled, _ := drainRecorder(ch)

	assert.Empty(t, flushes)
	assert.Empty(t, handled)
}

func TestDrainLatest_SingleCoalescibleEvent(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "first", coalescible: true}

	flushes, handled, _ := drainRecorder(ch)

	require.Len(t, flushes, 1)
	assert.Equal(t, flushRecord{value: "first", superseded: 0}, flushes[0])
	assert.Empty(t, handled)
}

func TestDrainLatest_MultipleCoalescibleEvents(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "first", coalescible: true}
	ch <- &coalescibleEvent{value: "second", coalescible: true}
	ch <- &coalescibleEvent{value: "third", coalescible: true}

	flushes, handled, _ := drainRecorder(ch)

	require.Len(t, flushes, 1, "an uninterrupted run collapses to one flush")
	assert.Equal(t, flushRecord{value: "third", superseded: 2}, flushes[0])
	assert.Empty(t, handled)
}

func TestDrainLatest_NonCoalescibleEventEndsRun(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "c1", coalescible: true}
	ch <- &coalescibleEvent{value: "c2", coalescible: false} // Not coalescible: run boundary
	ch <- &coalescibleEvent{value: "c3", coalescible: true}

	flushes, handled, order := drainRecorder(ch)

	// c1's run is flushed BEFORE c2 is handled — arrival order is preserved,
	// runs do not span boundary events.
	require.Len(t, flushes, 2)
	assert.Equal(t, flushRecord{value: "c1", superseded: 0}, flushes[0])
	assert.Equal(t, flushRecord{value: "c3", superseded: 0}, flushes[1])
	require.Len(t, handled, 1)
	assert.Equal(t, "c2", handled[0].(*coalescibleEvent).value)
	assert.Equal(t, []string{"flush:c1", "other:c2", "flush:c3"}, order)
}

func TestDrainLatest_DifferentEventTypeEndsRun(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "target1", coalescible: true}
	ch <- &testEvent{value: "other"} // Different type: run boundary
	ch <- &coalescibleEvent{value: "target2", coalescible: true}

	flushes, handled, order := drainRecorder(ch)

	require.Len(t, flushes, 2)
	assert.Equal(t, flushRecord{value: "target1", superseded: 0}, flushes[0])
	assert.Equal(t, flushRecord{value: "target2", superseded: 0}, flushes[1])
	require.Len(t, handled, 1)
	assert.Equal(t, "other", handled[0].(*testEvent).value)
	assert.Equal(t, []string{"flush:target1", "other:other", "flush:target2"}, order)
}

func TestDrainLatest_EventWithoutCoalescibleInterface(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &testEvent{value: "first"}
	ch <- &testEvent{value: "second"}

	var handled []busevents.Event
	DrainLatest(
		ch,
		func(e busevents.Event) { handled = append(handled, e) },
		func(latest *testEvent, superseded int) {
			t.Fatalf("flush must not be called for events without CoalescibleEvent, got %q", latest.value)
		},
	)

	assert.Len(t, handled, 2) // Both passed to handler
}

func TestDrainLatest_MixedEventTypes(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "c1", coalescible: true}
	ch <- &otherCoalescibleEvent{value: "o1", coalescible: true}
	ch <- &testEvent{value: "t1"}
	ch <- &coalescibleEvent{value: "c2", coalescible: true}
	ch <- &coalescibleEvent{value: "c3", coalescible: false}
	ch <- &coalescibleEvent{value: "c4", coalescible: true}

	flushes, handled, order := drainRecorder(ch)

	require.Len(t, flushes, 3)
	assert.Equal(t, []flushRecord{
		{value: "c1", superseded: 0},
		{value: "c2", superseded: 0},
		{value: "c4", superseded: 0},
	}, flushes)
	assert.Len(t, handled, 3) // o1, t1, and c3 were passed to handler
	assert.Equal(t, []string{"flush:c1", "other:o1", "other:t1", "flush:c2", "other:c3", "flush:c4"}, order)
}

func TestDrainLatest_OnlyNonCoalescible(t *testing.T) {
	ch := make(chan busevents.Event, 10)
	ch <- &coalescibleEvent{value: "c1", coalescible: false}
	ch <- &coalescibleEvent{value: "c2", coalescible: false}

	flushes, handled, _ := drainRecorder(ch)

	assert.Empty(t, flushes)
	assert.Len(t, handled, 2) // All passed to handler
}

// TestDrainLatest_SustainedOtherTrafficCannotStarveCoalesced is the regression
// test for the conformance-observed starvation: the pre-fix drain held the
// coalesced event back until the channel was empty, so under sustained
// other-type traffic (each dispatch slower than the arrival gap) the coalesced
// type was never delivered — rendered status patches starved for 54s while
// deployment-completed applies flowed. The fix flushes the held event at every
// run boundary: even when the handler keeps refilling the channel with
// other-type events (simulated here), every coalescible event is delivered
// before the other-type event that arrived after it.
func TestDrainLatest_SustainedOtherTrafficCannotStarveCoalesced(t *testing.T) {
	ch := make(chan busevents.Event, 64)
	ch <- &coalescibleEvent{value: "c1", coalescible: true}
	ch <- &testEvent{value: "t1"}

	var order []string
	refills := 0
	DrainLatest(
		ch,
		func(e busevents.Event) {
			order = append(order, "other:"+e.(*testEvent).value)
			// Simulate sustained traffic: while this (slow) handler runs, a
			// new coalescible event and a new other event arrive, so the
			// channel is never empty between other-type dispatches.
			if refills < 3 {
				refills++
				ch <- &coalescibleEvent{value: "c-refill", coalescible: true}
				ch <- &testEvent{value: "t-refill"}
			}
		},
		func(latest *coalescibleEvent, superseded int) {
			order = append(order, "flush:"+latest.value)
		},
	)

	// The channel was non-empty from start to finish, yet every coalescible
	// event was flushed before the other-type event that followed it.
	assert.Equal(t, []string{
		"flush:c1", "other:t1",
		"flush:c-refill", "other:t-refill",
		"flush:c-refill", "other:t-refill",
		"flush:c-refill", "other:t-refill",
	}, order)
}

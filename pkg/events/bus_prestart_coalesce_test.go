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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyedDeltaEvent is a minimal PreStartCoalescibleEvent: a per-key additive
// counter, the same shape as ResourceIndexUpdatedEvent's ChangeStats.
type keyedDeltaEvent struct {
	key   string
	delta int
	ts    time.Time
}

func (e *keyedDeltaEvent) EventType() string    { return "test.keyed.delta" }
func (e *keyedDeltaEvent) Timestamp() time.Time { return e.ts }

func (e *keyedDeltaEvent) PreStartCoalesceKey() string {
	return e.EventType() + "/" + e.key
}

func (e *keyedDeltaEvent) CoalesceWith(prev Event) Event {
	p, ok := prev.(*keyedDeltaEvent)
	if !ok || p.key != e.key {
		return e
	}
	return &keyedDeltaEvent{key: e.key, delta: e.delta + p.delta, ts: e.ts}
}

// plainEvent is an unkeyed event with a caller-chosen type string.
type plainEvent struct {
	eventType string
	ts        time.Time
}

func (e *plainEvent) EventType() string    { return e.eventType }
func (e *plainEvent) Timestamp() time.Time { return e.ts }

// A level-triggered stream can outrun the pre-start buffer many times over;
// keyed events must merge instead of dropping, whatever the volume.
func TestPublish_PreStartCoalescesKeyedEvents(t *testing.T) {
	bus := NewEventBus(10)
	sub := bus.Subscribe("prestart-coalesce-sub", 16)

	const keys = 3
	total := MaxPreStartBufferSize * 3
	for i := range total {
		bus.Publish(&keyedDeltaEvent{key: fmt.Sprintf("k%d", i%keys), delta: 1, ts: time.Now()})
	}

	assert.Equal(t, uint64(0), bus.DroppedEventsCritical(),
		"keyed events must merge in the pre-start buffer, not drop")

	bus.Start()

	deltas := map[string]int{}
	for range keys {
		select {
		case ev := <-sub:
			e, ok := ev.(*keyedDeltaEvent)
			require.True(t, ok, "unexpected event %T", ev)
			deltas[e.key] += e.delta
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for merged events")
		}
	}
	assert.Len(t, deltas, keys, "one merged event per key")
	sum := 0
	for _, d := range deltas {
		sum += d
	}
	assert.Equal(t, total, sum, "merging must not lose any delta")

	select {
	case ev := <-sub:
		t.Fatalf("expected exactly %d merged events, got extra %T", keys, ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// Unkeyed events keep the existing bounded-buffer behavior: the cap and the
// critical-drop accounting are unchanged.
func TestPublish_PreStartCapStillAppliesToUnkeyedEvents(t *testing.T) {
	bus := NewEventBus(10)
	_ = bus.Subscribe("prestart-cap-sub", 1)

	for range MaxPreStartBufferSize + 10 {
		bus.Publish(&plainEvent{eventType: "test.unkeyed", ts: time.Now()})
	}

	assert.Equal(t, uint64(10), bus.DroppedEventsCritical(),
		"unkeyed events past the cap are still critical drops")
}

// Pause() re-enters buffering mode; keyed events must merge there too, or a
// leadership transition on a busy cluster hits the same overflow.
func TestPause_CoalescesKeyedEventsWhileBuffering(t *testing.T) {
	bus := NewEventBus(10)
	sub := bus.Subscribe("pause-coalesce-sub", 16)
	bus.Start()
	bus.Pause()

	total := MaxPreStartBufferSize * 2
	for range total {
		bus.Publish(&keyedDeltaEvent{key: "k0", delta: 1, ts: time.Now()})
	}

	assert.Equal(t, uint64(0), bus.DroppedEventsCritical())

	bus.Start()

	select {
	case ev := <-sub:
		e, ok := ev.(*keyedDeltaEvent)
		require.True(t, ok, "unexpected event %T", ev)
		assert.Equal(t, total, e.delta, "merged event must carry the full sum")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for merged event")
	}
}

// Interleaving keyed and unkeyed events must preserve the unkeyed events and
// their order while the keyed ones merge in place.
func TestPublish_PreStartCoalescingPreservesUnkeyedEvents(t *testing.T) {
	bus := NewEventBus(10)
	sub := bus.Subscribe("prestart-mixed-sub", 16)

	bus.Publish(&keyedDeltaEvent{key: "k0", delta: 1, ts: time.Now()})
	bus.Publish(&plainEvent{eventType: "test.unkeyed.first", ts: time.Now()})
	bus.Publish(&keyedDeltaEvent{key: "k0", delta: 2, ts: time.Now()})
	bus.Publish(&plainEvent{eventType: "test.unkeyed.second", ts: time.Now()})

	bus.Start()

	var types []string
	for range 3 {
		select {
		case ev := <-sub:
			types = append(types, ev.EventType())
			if e, ok := ev.(*keyedDeltaEvent); ok {
				assert.Equal(t, 3, e.delta)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for replayed events")
		}
	}
	assert.Equal(t, []string{"test.keyed.delta", "test.unkeyed.first", "test.unkeyed.second"}, types)
}

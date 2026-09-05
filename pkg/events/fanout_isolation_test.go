// Copyright 2026 Philipp Hossner
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type isolatedTestEvent struct {
	value     string
	canonical string
}

func (e *isolatedTestEvent) EventType() string    { return "test.isolated" }
func (e *isolatedTestEvent) Timestamp() time.Time { return time.Time{} }

func (e *isolatedTestEvent) CloneForSubscriber() Event {
	return &isolatedTestEvent{value: e.canonical, canonical: e.canonical}
}

func TestFanoutIsolatedEventGivesEachSubscriberAnIndependentContainer(t *testing.T) {
	bus := NewEventBus(1)
	first := bus.Subscribe("first", 1)
	second := bus.SubscribeTypes("second", 1, "test.isolated")
	bus.Start()

	published := &isolatedTestEvent{value: "shadow", canonical: "sealed"}
	assert.Equal(t, 2, bus.Publish(published))
	published.value = "publisher poison"

	firstEvent := (<-first).(*isolatedTestEvent)
	secondEvent := (<-second).(*isolatedTestEvent)
	assert.NotSame(t, published, firstEvent)
	assert.NotSame(t, published, secondEvent)
	assert.NotSame(t, firstEvent, secondEvent)
	assert.Equal(t, "sealed", firstEvent.value)
	assert.Equal(t, "sealed", secondEvent.value)

	firstEvent.value = "subscriber poison"
	assert.Equal(t, "sealed", secondEvent.value)
}

func TestFanoutIsolatedEventIsFrozenBeforePreStartBuffering(t *testing.T) {
	bus := NewEventBus(1)
	subscriber := bus.Subscribe("subscriber", 1)
	published := &isolatedTestEvent{value: "shadow", canonical: "sealed"}

	assert.Zero(t, bus.Publish(published))
	published.canonical = "publisher poison"
	bus.Start()

	received := (<-subscriber).(*isolatedTestEvent)
	assert.Equal(t, "sealed", received.value)
}

type invalidIsolatedTestEvent struct {
	clone Event
}

func (e *invalidIsolatedTestEvent) EventType() string    { return "test.invalid-isolated" }
func (e *invalidIsolatedTestEvent) Timestamp() time.Time { return time.Time{} }
func (e *invalidIsolatedTestEvent) CloneForSubscriber() Event {
	return e.clone
}

func TestFanoutIsolatedEventRejectsInvalidClone(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		event := &invalidIsolatedTestEvent{}
		require.PanicsWithValue(t, "fanout-isolated event returned a nil clone", func() {
			cloneForSubscriber(event)
		})
	})

	t.Run("different event type", func(t *testing.T) {
		event := &invalidIsolatedTestEvent{clone: &isolatedTestEvent{canonical: "sealed"}}
		require.PanicsWithValue(t, "fanout-isolated event clone changed event type", func() {
			cloneForSubscriber(event)
		})
	})
}

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

package leadership

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// testEvent is a minimal Event implementation for testing.
type testEvent struct {
	value     string
	timestamp time.Time
}

func (e *testEvent) EventType() string    { return "test.event" }
func (e *testEvent) Timestamp() time.Time { return e.timestamp }

func newTestEvent(value string) *testEvent {
	return &testEvent{value: value, timestamp: time.Now()}
}

func TestStateReplayer_InitialState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	assert.False(t, replayer.HasState())

	event, ok := replayer.Get()
	assert.False(t, ok)
	assert.Nil(t, event)
}

func TestStateReplayer_Cache(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	event := newTestEvent("hello")
	replayer.Cache(event)

	assert.True(t, replayer.HasState())

	cached, ok := replayer.Get()
	require.True(t, ok)
	assert.Equal(t, "hello", cached.value)
}

func TestStateReplayer_CacheOverwrites(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	replayer.Cache(newTestEvent("first"))
	replayer.Cache(newTestEvent("second"))

	cached, ok := replayer.Get()
	require.True(t, ok)
	assert.Equal(t, "second", cached.value)
}

func TestStateReplayer_ReplayWithoutState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	replayed := replayer.Replay()
	assert.False(t, replayed)
}

func TestStateReplayer_ReplayWithState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	event := newTestEvent("replayed")
	replayer.Cache(event)

	replayed := replayer.Replay()
	assert.True(t, replayed)

	// Verify event was published to bus
	select {
	case received := <-eventChan:
		te, ok := received.(*testEvent)
		require.True(t, ok)
		assert.Equal(t, "replayed", te.value)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for replayed event")
	}
}

func TestStateReplayer_ConcurrentAccess(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	replayer := NewStateReplayer[*testEvent](bus)

	var wg sync.WaitGroup

	// Concurrent writers
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replayer.Cache(newTestEvent("writer"))
		}()
	}

	// Concurrent readers
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replayer.HasState()
			replayer.Get()
			replayer.Replay()
		}()
	}

	wg.Wait()

	// After all goroutines complete, state should be cached
	assert.True(t, replayer.HasState())
}

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

package component

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// queueCoalescingRecorder is a blockingRecorder that declares two types and
// optionally opts into whole-queue coalescing.
type queueCoalescingRecorder struct {
	blockingRecorder
	across bool
}

func (h *queueCoalescingRecorder) CoalescesOn() []string {
	return []string{events.EventTypeResourcesApplied, events.EventTypeDeploymentSkipped}
}

func (h *queueCoalescingRecorder) CoalescesAcrossQueue() bool { return h.across }

const alternatingPairs = 20

// runAlternatingBurst blocks the handler on its first event, publishes
// alternatingPairs (deployment.skipped, resources.applied) pairs while it is
// blocked, returns the queue depth at that point, then releases the handler
// and returns the dispatched events once at least want arrived.
func runAlternatingBurst(t *testing.T, across bool, want int) (queued int, got []busevents.Event) {
	t.Helper()
	bus := busevents.NewEventBus(64)
	h := &queueCoalescingRecorder{
		blockingRecorder: blockingRecorder{gate: make(chan struct{}), started: make(chan struct{}, 1)},
		across:           across,
	}
	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "queue-coalesce",
		BufferSize: 64,
		Handler:    h,
		EventTypes: []string{events.EventTypeResourcesApplied, events.EventTypeDeploymentSkipped},
	})
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()

	bus.Publish(events.NewResourcesAppliedEvent(nil))
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}

	published := 1
	publish := func(e busevents.Event) {
		bus.Publish(e)
		published++
		require.Eventually(t, func() bool {
			return h.startedCount()+base.mailboxAbsorbed() >= published
		}, 2*time.Second, time.Millisecond, "intake must absorb event %d", published)
	}
	for i := 0; i < alternatingPairs; i++ {
		publish(events.NewDeploymentSkippedEvent(i, "test", "", "", nil))
		publish(events.NewResourcesAppliedEvent(nil))
	}
	base.mbMu.Lock()
	queued = len(base.mbQueue)
	base.mbMu.Unlock()

	go func() {
		for {
			select {
			case h.gate <- struct{}{}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	require.Eventually(t, func() bool { return len(h.snapshot()) >= want },
		3*time.Second, 10*time.Millisecond, "expected %d dispatches", want)
	base.Stop()
	<-done
	return queued, h.snapshot()
}

// A stream that strictly alternates two declared types never forms a run, so
// run-only collapsing leaves the whole backlog queued (the status-applier
// backlog measured at 2048 patch sets / 6 GB).
func TestBase_QueueCoalescing_RunOnlyKeepsAlternatingBacklog(t *testing.T) {
	queued, got := runAlternatingBurst(t, false, 1+2*alternatingPairs)
	assert.Equal(t, 2*alternatingPairs, queued, "an alternating stream never collapses run-wise")
	assert.Len(t, got, 1+2*alternatingPairs)
}

// With CoalescesAcrossQueue the queue holds at most the newest event of each
// declared type, and the dispatched events are those newest ones in arrival
// order.
func TestBase_QueueCoalescing_AcrossQueueBoundsAlternatingBacklog(t *testing.T) {
	queued, got := runAlternatingBurst(t, true, 3)
	assert.Equal(t, 2, queued, "one queued entry per declared type")
	require.Len(t, got, 3)
	assert.Equal(t, events.EventTypeDeploymentSkipped, got[1].EventType())
	assert.Equal(t, alternatingPairs-1, got[1].(*events.DeploymentSkippedEvent).Total, "the newest of the type survives")
	assert.Equal(t, events.EventTypeResourcesApplied, got[2].EventType())
}

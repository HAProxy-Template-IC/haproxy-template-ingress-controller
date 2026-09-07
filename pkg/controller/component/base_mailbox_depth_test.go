// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package component

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// TestBase_MailboxDepthIsReportedWhileRunning pins that a running mailbox
// reports its queue length by component name and disappears once stopped.
func TestBase_MailboxDepthIsReportedWhileRunning(t *testing.T) {
	bus := busevents.NewEventBus(16)
	h := &blockingRecorder{gate: make(chan struct{}), started: make(chan struct{}, 1)}
	base := New(&Config{
		EventBus:   bus,
		Logger:     discardLogger(),
		Name:       "mailbox-depth",
		BufferSize: 8,
		Handler:    h,
		EventTypes: []string{events.EventTypeReconciliationTriggered, events.EventTypeBecameLeader},
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = base.Start(ctx)
		close(done)
	}()
	bus.Start()

	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first event never started processing")
	}
	// Alternating types defeat run-only coalescing, so every event queues.
	for range 3 {
		bus.Publish(events.NewReconciliationTriggeredEvent("queued", true))
		bus.Publish(events.NewBecameLeaderEvent("leader"))
	}
	require.Eventually(t, func() bool {
		return MailboxDepths()["mailbox-depth"] == 6
	}, 2*time.Second, time.Millisecond, "the queued events are reported: %v", MailboxDepths())

	cancel()
	close(h.gate)
	<-done
	_, running := MailboxDepths()["mailbox-depth"]
	assert.False(t, running, "a stopped mailbox no longer reports")
}

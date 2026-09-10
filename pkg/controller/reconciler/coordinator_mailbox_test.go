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

package reconciler

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestCoordinatorMailboxBoundsTriggerRuns(t *testing.T) {
	mailbox := &coordinatorMailbox{notify: make(chan struct{}, 1)}
	for range 10000 {
		mailbox.enqueue(events.NewReconciliationTriggeredEvent("resource_change", true))
	}
	latest := events.NewReconciliationTriggeredEvent("latest", true)
	mailbox.enqueue(latest)
	require.Len(t, mailbox.queue, 1)
	assert.Same(t, latest, mailbox.queue[0])
	forced := events.NewReconciliationTriggeredEvent("forced", false)
	mailbox.enqueue(forced)
	mailbox.enqueue(events.NewReconciliationTriggeredEvent("other forced", false))
	mailbox.enqueue(latest)
	require.Len(t, mailbox.queue, 1)
	assert.Same(t, forced, mailbox.queue[0])
}

func TestCoordinatorMailboxPreservesEveryGateVerdict(t *testing.T) {
	mailbox := &coordinatorMailbox{notify: make(chan struct{}, 1)}
	for range 100 {
		mailbox.enqueue(events.NewRenderGateCompletedEvent("plan", false, true, true, "refused", false, 1))
	}
	require.Len(t, mailbox.queue, 100)
	for range 100 {
		_, ok := mailbox.pop()
		require.True(t, ok)
	}
	assert.Nil(t, mailbox.queue)
}

func TestCoordinatorMailboxClosedInputDrains(t *testing.T) {
	input := make(chan busevents.Event, 2)
	trigger := events.NewReconciliationTriggeredEvent("latest", true)
	input <- trigger
	close(input)
	mailbox := newCoordinatorMailbox(t.Context(), input)
	defer mailbox.stop()
	<-mailbox.done
	got, ok := mailbox.next(t.Context())
	require.True(t, ok)
	assert.Same(t, trigger, got)
	_, ok = mailbox.next(t.Context())
	assert.False(t, ok)
}

func TestCoordinatorMailboxCancellationDiscardsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	mailbox := newCoordinatorMailbox(ctx, nil)
	mailbox.enqueue(events.NewReconciliationTriggeredEvent("stale", true))
	cancel()
	_, ok := mailbox.next(ctx)
	assert.False(t, ok)
	mailbox.stop()
	assert.Nil(t, mailbox.queue)
	mailbox.stop()
}

type cancelMailboxOnWaitContext struct {
	context.Context
	onWait func()
	once   sync.Once
}

func (c *cancelMailboxOnWaitContext) Done() <-chan struct{} {
	c.once.Do(c.onWait)
	return c.Context.Done()
}

func TestCoordinatorMailboxCancellationWhileWaitingDiscardsPending(t *testing.T) {
	for range 100 {
		mailbox := &coordinatorMailbox{notify: make(chan struct{}, 1), done: make(chan struct{})}
		ctx, cancel := context.WithCancel(t.Context())
		waitingContext := &cancelMailboxOnWaitContext{Context: ctx, onWait: func() {
			// Enter cancellation after next's initial empty pop, before it selects a wakeup.
			mailbox.enqueue(events.NewRenderGateCompletedEvent("plan", true, false, true, "", false, 1))
			cancel()
			close(mailbox.done)
		}}
		_, ok := mailbox.next(waitingContext)
		cancel()
		require.False(t, ok, "cancellation must not return a pending gate verdict")
	}
}

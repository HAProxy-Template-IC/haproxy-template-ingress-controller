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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type coordinatorMailbox struct {
	mu     sync.Mutex
	queue  []busevents.Event
	notify chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

func newCoordinatorMailbox(ctx context.Context, input <-chan busevents.Event) *coordinatorMailbox {
	intakeCtx, cancel := context.WithCancel(ctx)
	mailbox := &coordinatorMailbox{notify: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel}
	go mailbox.receive(intakeCtx, input)
	return mailbox
}

func (m *coordinatorMailbox) stop() {
	m.cancel()
	<-m.done
	m.mu.Lock()
	m.queue = nil
	m.mu.Unlock()
}

func (m *coordinatorMailbox) receive(ctx context.Context, input <-chan busevents.Event) {
	defer close(m.done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-input:
			if !open {
				return
			}
			m.enqueue(event)
		}
	}
}

func (m *coordinatorMailbox) enqueue(event busevents.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if trigger, ok := event.(*events.ReconciliationTriggeredEvent); ok && len(m.queue) > 0 {
		last := len(m.queue) - 1
		if previous, sameRun := m.queue[last].(*events.ReconciliationTriggeredEvent); sameRun {
			if previous.Coalescible() {
				m.queue[last] = trigger
			}
			return
		}
	}
	// Gate verdicts separate trigger runs so later renders see the settled baseline.
	m.queue = append(m.queue, event)
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

func (m *coordinatorMailbox) pop() (busevents.Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == 0 {
		return nil, false
	}
	event := m.queue[0]
	m.queue[0] = nil
	m.queue = m.queue[1:]
	if len(m.queue) == 0 {
		m.queue = nil
	}
	return event, true
}

func (m *coordinatorMailbox) next(ctx context.Context) (busevents.Event, bool) {
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		if event, ok := m.pop(); ok {
			return event, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-m.done:
			if ctx.Err() != nil {
				return nil, false
			}
			return m.pop()
		case <-m.notify:
		}
	}
}

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

import "sync"

// ReadySignal is a one-shot signal used by leader-only components to let
// callers wait until the component has finished subscribing to the event
// bus. Leader-only components subscribe in Start() (not in their
// constructor), and the controller start-up sequence waits on
// SubscriptionReady() before proceeding so published events are not lost.
//
// Embed *ReadySignal in a leader-only component to pick up the accessor
// and Mark helpers instead of repeating the channel plumbing in each file.
//
// Example:
//
//	type Component struct {
//	    *component.ReadySignal
//	    // ... other fields
//	}
//
//	func New(...) *Component {
//	    return &Component{ReadySignal: component.NewReadySignal(), ...}
//	}
//
//	func (c *Component) Start(ctx context.Context) error {
//	    c.eventChan = bus.SubscribeTypesLeaderOnly(...)
//	    c.MarkReady()
//	    // ... event loop
//	}
type ReadySignal struct {
	ch   chan struct{}
	once sync.Once
}

// NewReadySignal constructs an un-signalled ReadySignal.
func NewReadySignal() *ReadySignal {
	return &ReadySignal{ch: make(chan struct{})}
}

// SubscriptionReady returns a channel that is closed when MarkReady is
// called. It implements lifecycle.SubscriptionReadySignaler (checked via
// interface satisfaction at call sites rather than by import to avoid a
// cyclic dependency on pkg/lifecycle).
func (r *ReadySignal) SubscriptionReady() <-chan struct{} {
	return r.ch
}

// MarkReady closes the underlying channel exactly once. Subsequent calls
// are no-ops, so components can invoke it defensively at the end of their
// subscription step without caring whether Start has already run.
func (r *ReadySignal) MarkReady() {
	r.once.Do(func() {
		close(r.ch)
	})
}

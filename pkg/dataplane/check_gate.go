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

package dataplane

import (
	"context"
	"sync"
	"time"
)

// CheckGate bounds concurrent `haproxy -c` runs. Each gate owns one slot, so a
// caller with its own gate never queues behind another's checks; minInterval
// additionally caps a gate's duty cycle by spacing run starts.
//
// The admission webhook and the reconcile gate hold separate gates on purpose:
// admission answers on a request deadline and must never wait out a fleet-sized
// config check.
type CheckGate struct {
	slot        chan struct{}
	minInterval time.Duration

	mu      sync.Mutex
	nextRun time.Time
}

// NewCheckGate returns a gate with one slot. A non-positive minInterval
// disables the duty-cycle cap.
func NewCheckGate(minInterval time.Duration) *CheckGate {
	return NewCheckGateN(1, minInterval)
}

// NewCheckGateN returns a gate allowing `slots` concurrent checks (minimum 1).
// Use slots > 1 only for a batch of independent checks that should run across
// cores — the validationTests load gate, whose worker pool otherwise serializes
// every `haproxy -c` behind a single slot. Admission and the reconcile gate stay
// single-slot on purpose. The duty-cycle cap (minInterval) spaces run starts
// across all slots, so pair slots > 1 with minInterval == 0.
func NewCheckGateN(slots int, minInterval time.Duration) *CheckGate {
	if slots < 1 {
		slots = 1
	}
	return &CheckGate{slot: make(chan struct{}, slots), minInterval: minInterval}
}

// enter blocks until this gate's slot is free and the duty cycle allows a run.
// A cancelled context leaves without holding the slot; every successful enter
// must be paired with leave.
func (g *CheckGate) enter(ctx context.Context) error {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if err := g.awaitDutyCycle(ctx); err != nil {
		<-g.slot
		return err
	}
	return nil
}

func (g *CheckGate) leave() {
	<-g.slot
}

// awaitDutyCycle waits out the remainder of the interval since the last run
// started, then claims the next start slot.
func (g *CheckGate) awaitDutyCycle(ctx context.Context) error {
	if g.minInterval <= 0 {
		return nil
	}
	g.mu.Lock()
	wait := time.Until(g.nextRun)
	g.mu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}

	g.mu.Lock()
	g.nextRun = time.Now().Add(g.minInterval)
	g.mu.Unlock()
	return nil
}

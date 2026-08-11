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

// Package throttle provides leading-edge refractory throttle helpers used by
// controller components that bound their write rate to slow downstream
// systems (e.g. the Kubernetes API).
//
// "Leading edge" means: the first work item submitted after the throttle is
// idle fires immediately. Subsequent items submitted within the refractory
// period are deferred — the helper signals via FiredCh() once the period
// expires so the caller can flush whatever has accumulated in its own
// pending queue.
//
// The helper does NOT buffer work items. Callers retain ownership of pending
// state because that state often has caller-specific semantics (e.g.
// per-key coalescing, cleanup of replaced entries). The helper only owns the
// timing decision: "may I fire now?"
package throttle

import (
	"sync"
	"time"
)

// LeadingEdge enforces a leading-edge refractory throttle. It is safe for
// concurrent use; all methods take an internal mutex.
//
// A zero-value LeadingEdge has interval == 0, meaning the gate is always
// open and ScheduleFlush is a no-op. Use New to configure the interval.
type LeadingEdge struct {
	interval time.Duration

	mu        sync.Mutex
	lastFire  time.Time
	timer     *time.Timer
	callbacks sync.WaitGroup
	stopped   bool

	firedCh chan struct{}
}

// New returns a LeadingEdge with the given refractory interval. If interval
// is zero or negative, Available always returns true and ScheduleFlush is a
// no-op — the throttle effectively passes everything through. The returned
// FiredCh has buffer 1 so timer signals never block.
func New(interval time.Duration) *LeadingEdge {
	return &LeadingEdge{
		interval: interval,
		firedCh:  make(chan struct{}, 1),
	}
}

// Available reports whether the throttle gate is currently open. Returns
// true when the throttle is disabled (interval <= 0) or when the refractory
// period since the last MarkFired call has elapsed. Callers should fire
// immediately if true, else call ScheduleFlush to defer.
func (t *LeadingEdge) Available() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	if t.interval <= 0 {
		return true
	}
	return time.Since(t.lastFire) >= t.interval
}

// MarkFired records the current time as the most recent fire, opening a
// fresh refractory window. Callers should invoke this after a successful
// fire (typically inside the work that the worker just executed).
func (t *LeadingEdge) MarkFired() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.interval <= 0 {
		return
	}
	t.lastFire = time.Now()

	hadSignal := false
	select {
	case <-t.firedCh:
		hadSignal = true
	default:
	}
	if t.timer != nil {
		if t.timer.Stop() {
			t.callbacks.Done()
			t.scheduleLocked(t.interval)
		}
		return
	}
	if hadSignal {
		t.scheduleLocked(t.interval)
	}
}

// ScheduleFlush arms a one-shot timer that signals on FiredCh once the
// remaining refractory period expires. Multiple calls share one timer.
//
// If the throttle is disabled, ScheduleFlush is a no-op — callers should
// gate it behind Available() in that case anyway.
func (t *LeadingEdge) ScheduleFlush() {
	t.mu.Lock()
	if t.stopped || t.interval <= 0 || t.timer != nil {
		t.mu.Unlock()
		return
	}
	remaining := t.interval - time.Since(t.lastFire)
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	t.scheduleLocked(remaining)
	t.mu.Unlock()
}

func (t *LeadingEdge) scheduleLocked(delay time.Duration) {
	t.callbacks.Add(1)
	t.timer = time.AfterFunc(delay, func() {
		defer t.callbacks.Done()

		t.mu.Lock()
		defer t.mu.Unlock()
		if t.stopped {
			t.timer = nil
			return
		}
		remaining := t.interval - time.Since(t.lastFire)
		if remaining > 0 {
			if remaining < time.Millisecond {
				remaining = time.Millisecond
			}
			t.scheduleLocked(remaining)
			return
		}
		t.timer = nil
		select {
		case t.firedCh <- struct{}{}:
		default:
		}
	})
}

// FiredCh returns the channel that signals "refractory period has expired —
// flush your pending queue now." The channel has buffer 1 so signals never
// block; consumers should treat each receive as "drain whatever is pending"
// rather than per-item delivery.
func (t *LeadingEdge) FiredCh() <-chan struct{} {
	return t.firedCh
}

// Stop cancels pending wakeups and waits for any running timer callback.
func (t *LeadingEdge) Stop() {
	t.mu.Lock()
	t.stopped = true
	if t.timer != nil && t.timer.Stop() {
		t.timer = nil
		t.callbacks.Done()
	}
	t.mu.Unlock()

	t.callbacks.Wait()
	for {
		select {
		case <-t.firedCh:
		default:
			return
		}
	}
}

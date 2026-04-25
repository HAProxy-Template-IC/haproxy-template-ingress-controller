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

// Package timers provides safe timer management for event-driven controller components.
//
// SafeTimer wraps *time.Timer with safe stop/drain/reset operations that avoid
// the common pitfalls of Go timer usage (leaked channels, double-drain races).
// It is designed for single-goroutine use within select loops.
package timers

import "time"

// SafeTimer wraps *time.Timer with safe stop, drain, reset, and channel-get operations.
// It must only be used from a single goroutine (no internal synchronization).
type SafeTimer struct {
	timer *time.Timer
}

// Chan returns the timer's channel, or nil if no timer is active.
// A nil channel blocks forever in a select, which is the desired behavior
// when there is no active timer.
func (t *SafeTimer) Chan() <-chan time.Time {
	if t.timer == nil {
		return nil
	}
	return t.timer.C
}

// Stop stops the timer if running, drains the channel, and clears the reference.
func (t *SafeTimer) Stop() {
	if t.timer != nil {
		t.stopAndDrain()
		t.timer = nil
	}
}

// Reset stops any existing timer and starts a new one with the given duration.
// This implements trailing-edge debounce: every call resets the countdown.
func (t *SafeTimer) Reset(d time.Duration) {
	if t.timer == nil {
		t.timer = time.NewTimer(d)
		return
	}
	t.stopAndDrain()
	t.timer.Reset(d)
}

// stopAndDrain stops the underlying timer if it hasn't already fired and
// performs a non-blocking drain of t.timer.C, so a pending tick from a
// just-expired timer can't leak into the next Reset/EnsureRunning cycle.
// Must only be called when t.timer != nil.
func (t *SafeTimer) stopAndDrain() {
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
}

// EnsureRunning starts a timer with the given duration only if no timer is currently active.
// If a timer is already running, this is a no-op.
// This implements leading-edge debounce: only the first call starts the timer.
func (t *SafeTimer) EnsureRunning(d time.Duration) {
	if t.timer != nil {
		return
	}
	t.timer = time.NewTimer(d)
}

// Fired should be called when the timer's channel is read in a select case.
// It clears the internal reference so a new timer can be started.
func (t *SafeTimer) Fired() {
	t.timer = nil
}

// Active reports whether a timer is currently running.
func (t *SafeTimer) Active() bool {
	return t.timer != nil
}

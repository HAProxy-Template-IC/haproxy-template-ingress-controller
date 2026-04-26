// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package reconciler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// ensureRefractoryTimer has a defensive "remaining <= 0" branch that
// the existing TestReconciler_RefractoryBatching does not exercise.
// The function computes:
//
//	remaining := r.debounceInterval - now.Sub(r.lastTriggerTime)
//	if remaining <= 0 {
//	    remaining = r.debounceInterval
//	}
//	r.debounceTimer.EnsureRunning(remaining)
//
// The "<= 0" branch fires when lastTriggerTime is unexpectedly far in
// the past — for example, when the field has never been written
// (zero-value time.Time) or when system clock skew makes now appear
// later than the bookkeeping expects. Without the guard, EnsureRunning
// would receive a negative duration and time.NewTimer(neg) fires
// IMMEDIATELY: the refractory window collapses to zero and the
// leading-edge debounce stops debouncing — a flurry of resource
// changes would each trigger a reconciliation back-to-back, defeating
// the whole point of the refractory period.
//
// Pin the contract: regardless of how stale lastTriggerTime is, the
// timer must NOT fire before at least one debounceInterval has passed
// since ensureRefractoryTimer was called.
//
// We use a generous debounceInterval (5 minutes) and a generous
// "didn't fire" window (50ms) so this test is not flaky on slow CI.
// A regression that dropped the guard would cause the timer to fire
// in microseconds, which is well below 50ms.
func TestEnsureRefractoryTimer_NegativeRemainingFallsBackToFullInterval(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	const longInterval = 5 * time.Minute
	r := New(bus, logger, &Config{DebounceInterval: longInterval})

	// Force the "remaining <= 0" condition by leaving lastTriggerTime
	// at its zero value. now.Sub(zero) is roughly the current Unix
	// time in nanoseconds — vastly larger than longInterval — so
	// `remaining = longInterval - hugeNumber` is hugely negative.
	r.lastTriggerTime = time.Time{}

	r.ensureRefractoryTimer(time.Now())

	assert.True(t, r.debounceTimer.Active(),
		"timer must be started even when remaining is negative — "+
			"the guard branch exists precisely to recover from the "+
			"corrupted-state case")

	// The crucial assertion: the timer must NOT fire immediately.
	// With the guard, `remaining = longInterval` (5 minutes) so 50ms
	// is nowhere near the fire time. Without the guard, the negative
	// duration would cause time.NewTimer to fire in microseconds and
	// this select would land in the timer-channel case.
	select {
	case <-r.debounceTimer.Chan():
		t.Fatal("debounce timer fired immediately — the negative-remaining " +
			"guard MUST clamp the duration to a full debounceInterval. " +
			"A regression that removed the guard would collapse the " +
			"refractory window to zero, and a flurry of resource changes " +
			"would each fire a reconciliation back-to-back — exactly the " +
			"thrashing the leading-edge debounce was built to prevent.")
	case <-time.After(50 * time.Millisecond):
		// Expected: the timer is set to 5 minutes, far longer than
		// our wait window — so it does not fire.
	}

	r.debounceTimer.Stop()
}

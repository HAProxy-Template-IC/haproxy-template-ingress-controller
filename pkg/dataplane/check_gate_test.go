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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two gates are two slots: the admission webhook must never wait out a
// fleet-sized config check the reconcile gate is running.
func TestCheckGate_SeparateGatesDoNotBlockEachOther(t *testing.T) {
	busy := NewCheckGate(0)
	other := NewCheckGate(0)

	require.NoError(t, busy.enter(t.Context()))
	defer busy.leave()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, other.enter(ctx), "a gate of its own is never held by another's check")
	other.leave()
}

// One gate is one slot: a second check waits for the first to finish.
func TestCheckGate_OneSlotSerializesChecks(t *testing.T) {
	gate := NewCheckGate(0)
	require.NoError(t, gate.enter(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	assert.Error(t, gate.enter(ctx), "the slot is taken, so the second check must wait")

	gate.leave()
	require.NoError(t, gate.enter(t.Context()))
	gate.leave()
}

// N slots let N checks run at once (the validationTests load gate) but no more:
// the (N+1)th waits. This is what lets the testrunner's worker pool run
// `haproxy -c` across cores instead of serializing on a single slot.
func TestCheckGateN_AllowsNConcurrentThenBlocks(t *testing.T) {
	gate := NewCheckGateN(2, 0)
	require.NoError(t, gate.enter(t.Context()))
	require.NoError(t, gate.enter(t.Context()), "second slot is free")

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	assert.Error(t, gate.enter(ctx), "both slots are taken, so the third check must wait")

	gate.leave()
	require.NoError(t, gate.enter(t.Context()), "a freed slot admits the next check")
	gate.leave()
	gate.leave()
}

// A non-positive slot count is clamped to one, so a caller can't accidentally
// build a zero-slot gate that deadlocks every check.
func TestCheckGateN_ClampsToAtLeastOneSlot(t *testing.T) {
	gate := NewCheckGateN(0, 0)
	require.NoError(t, gate.enter(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	assert.Error(t, gate.enter(ctx), "clamped to one slot, so the second check waits")
	gate.leave()
}

// The duty-cycle cap spaces run STARTS, which is what bounds the CPU a render
// storm can take from admission.
func TestCheckGate_DutyCycleSpacesRunStarts(t *testing.T) {
	const interval = 60 * time.Millisecond
	gate := NewCheckGate(interval)

	require.NoError(t, gate.enter(t.Context()))
	first := time.Now()
	gate.leave()

	require.NoError(t, gate.enter(t.Context()))
	second := time.Now()
	gate.leave()

	assert.GreaterOrEqual(t, second.Sub(first), interval,
		"the second run must not start before the interval since the first one did")
}

// A cancelled waiter leaves without the slot, so it cannot wedge the gate for
// the next caller.
func TestCheckGate_CancelledWaiterReleasesTheSlot(t *testing.T) {
	gate := NewCheckGate(time.Hour)

	require.NoError(t, gate.enter(t.Context()))
	gate.leave()

	cause := errors.New("term over")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	require.ErrorIs(t, gate.enter(ctx), cause)

	// The hour-long duty cycle still applies, but the slot itself is free: a
	// caller that waits it out is not blocked by the cancelled one.
	select {
	case gate.slot <- struct{}{}:
		<-gate.slot
	default:
		t.Fatal("a cancelled waiter left the slot taken")
	}
}

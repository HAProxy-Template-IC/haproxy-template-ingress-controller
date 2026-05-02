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

package throttle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeadingEdge_Disabled verifies that interval <= 0 disables the throttle:
// Available is always true, MarkFired/ScheduleFlush are no-ops.
func TestLeadingEdge_Disabled(t *testing.T) {
	t.Parallel()

	for _, interval := range []time.Duration{0, -time.Second} {
		throttle := New(interval)
		assert.True(t, throttle.Available(), "available with interval=%v", interval)

		throttle.MarkFired()
		assert.True(t, throttle.Available(), "still available after MarkFired with interval=%v", interval)

		throttle.ScheduleFlush()
		select {
		case <-throttle.FiredCh():
			t.Fatalf("FiredCh signalled with interval=%v but throttle is disabled", interval)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestLeadingEdge_LeadingEdgeFires verifies the gate is open when freshly
// constructed: the very first submission can fire immediately.
func TestLeadingEdge_LeadingEdgeFires(t *testing.T) {
	t.Parallel()

	throttle := New(100 * time.Millisecond)
	assert.True(t, throttle.Available(), "fresh throttle gate should be open")
}

// TestLeadingEdge_RefractoryClosesGate verifies that after MarkFired,
// Available returns false until the interval elapses.
func TestLeadingEdge_RefractoryClosesGate(t *testing.T) {
	t.Parallel()

	throttle := New(50 * time.Millisecond)
	throttle.MarkFired()

	assert.False(t, throttle.Available(), "gate should be closed inside refractory")

	require.Eventually(t, throttle.Available, 200*time.Millisecond, 5*time.Millisecond,
		"gate should reopen after refractory expires")
}

// TestLeadingEdge_ScheduleFlushSignals verifies that ScheduleFlush arms a
// timer whose firing lands on FiredCh.
func TestLeadingEdge_ScheduleFlushSignals(t *testing.T) {
	t.Parallel()

	throttle := New(30 * time.Millisecond)
	throttle.MarkFired()
	throttle.ScheduleFlush()

	select {
	case <-throttle.FiredCh():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected FiredCh signal within 500ms")
	}
}

// TestLeadingEdge_MultipleScheduleFlushCoalesce verifies that the helper's
// FiredCh emits at least one wakeup when ScheduleFlush is called repeatedly
// inside the refractory window, and that the total number of signals never
// exceeds the number of calls. The cap-1 channel coalesces signals that
// arrive while the buffer is full, but a consumer that drains between
// AfterFunc firings can still observe up to N signals for N calls; the
// guarantee is "no signal explosion," not "exactly one." The worker side
// is fine with this — it just calls processAllPendingStatusWork on each
// wake, which is idempotent on an empty pending queue.
func TestLeadingEdge_MultipleScheduleFlushCoalesce(t *testing.T) {
	t.Parallel()

	const calls = 3
	throttle := New(30 * time.Millisecond)
	throttle.MarkFired()

	for range calls {
		throttle.ScheduleFlush()
	}

	// First receive is required: at least one wakeup must arrive.
	select {
	case <-throttle.FiredCh():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected at least one FiredCh signal")
	}

	// Drain any extras. Total signals (1 + extras) must not exceed `calls`.
	extras := 0
	for {
		select {
		case <-throttle.FiredCh():
			extras++
		case <-time.After(60 * time.Millisecond):
			goto Done
		}
	}
Done:
	total := 1 + extras
	assert.LessOrEqual(t, total, calls,
		"helper must not amplify ScheduleFlush calls into more signals — "+
			"the cap-1 channel bounds in-flight signals; got %d signals from %d calls", total, calls)
}

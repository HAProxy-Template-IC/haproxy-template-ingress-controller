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
	defer throttle.Stop()
	throttle.MarkFired()
	throttle.ScheduleFlush()

	select {
	case <-throttle.FiredCh():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected FiredCh signal within 500ms")
	}
}

func TestLeadingEdge_MultipleScheduleFlushCoalesce(t *testing.T) {
	t.Parallel()

	const calls = 3
	throttle := New(30 * time.Millisecond)
	defer throttle.Stop()
	throttle.MarkFired()

	for range calls {
		throttle.ScheduleFlush()
	}

	select {
	case <-throttle.FiredCh():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected at least one FiredCh signal")
	}

	select {
	case <-throttle.FiredCh():
		t.Fatal("repeated ScheduleFlush calls created more than one timer signal")
	case <-time.After(60 * time.Millisecond):
	}
}

func TestLeadingEdge_StopCancelsAndDrainsWakeup(t *testing.T) {
	t.Parallel()

	throttle := New(time.Hour)
	throttle.MarkFired()
	throttle.ScheduleFlush()
	throttle.Stop()
	throttle.Stop()

	assert.False(t, throttle.Available())
	select {
	case <-throttle.FiredCh():
		t.Fatal("stopped throttle delivered a pending wakeup")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLeadingEdge_MarkFiredRearmsPendingWakeup(t *testing.T) {
	t.Parallel()

	const interval = 120 * time.Millisecond
	throttle := New(interval)
	defer throttle.Stop()
	throttle.mu.Lock()
	throttle.lastFire = time.Now().Add(-100 * time.Millisecond)
	throttle.mu.Unlock()
	throttle.ScheduleFlush()
	throttle.MarkFired()

	select {
	case <-throttle.FiredCh():
		t.Fatal("pending wakeup fired on the previous refractory deadline")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-throttle.FiredCh():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("rearmed wakeup did not fire on the new refractory deadline")
	}
}

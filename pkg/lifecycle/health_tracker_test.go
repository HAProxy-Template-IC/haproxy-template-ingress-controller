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

package lifecycle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthTracker_ActivityBased_Healthy(t *testing.T) {
	tracker := NewActivityTracker("test-component", 100*time.Millisecond)

	// Should be healthy immediately after creation
	err := tracker.Check()
	assert.NoError(t, err)
}

func TestHealthTracker_ActivityBased_Stalled(t *testing.T) {
	tracker := NewActivityTracker("test-component", 10*time.Millisecond)

	// Wait for stall timeout to pass
	time.Sleep(20 * time.Millisecond)

	err := tracker.Check()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-component stalled")
	assert.Contains(t, err.Error(), "no activity")
}

func TestHealthTracker_ActivityBased_RecordActivity(t *testing.T) {
	tracker := NewActivityTracker("test-component", 50*time.Millisecond)

	// Wait a bit but record activity before timeout
	time.Sleep(30 * time.Millisecond)
	tracker.RecordActivity()

	// Should still be healthy
	err := tracker.Check()
	assert.NoError(t, err)
}

func TestHealthTracker_ProcessingBased_IdleHealthy(t *testing.T) {
	tracker := NewProcessingTracker("test-component", 100*time.Millisecond)

	// Should be healthy when idle
	err := tracker.Check()
	assert.NoError(t, err)
	assert.False(t, tracker.IsProcessing())
}

func TestHealthTracker_ProcessingBased_ProcessingHealthy(t *testing.T) {
	tracker := NewProcessingTracker("test-component", 100*time.Millisecond)

	tracker.StartProcessing()
	defer tracker.EndProcessing()

	// Should be healthy while processing (under timeout)
	err := tracker.Check()
	assert.NoError(t, err)
	assert.True(t, tracker.IsProcessing())
}

func TestHealthTracker_ProcessingBased_ProcessingStalled(t *testing.T) {
	tracker := NewProcessingTracker("test-component", 10*time.Millisecond)

	tracker.StartProcessing()
	defer tracker.EndProcessing()

	// Wait for processing timeout
	time.Sleep(20 * time.Millisecond)

	err := tracker.Check()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-component stalled")
	assert.Contains(t, err.Error(), "processing")
}

func TestHealthTracker_ProcessingBased_EndProcessingClearsState(t *testing.T) {
	tracker := NewProcessingTracker("test-component", 10*time.Millisecond)

	tracker.StartProcessing()
	time.Sleep(20 * time.Millisecond)
	tracker.EndProcessing()

	// Should be healthy after EndProcessing (idle state)
	err := tracker.Check()
	assert.NoError(t, err)
	assert.False(t, tracker.IsProcessing())
}

func TestHealthTracker_ProcessingDuration(t *testing.T) {
	tracker := NewProcessingTracker("test-component", 100*time.Millisecond)

	// Should be 0 when idle
	assert.Equal(t, time.Duration(0), tracker.ProcessingDuration())

	tracker.StartProcessing()
	time.Sleep(10 * time.Millisecond)

	// Should be non-zero when processing
	duration := tracker.ProcessingDuration()
	assert.True(t, duration >= 10*time.Millisecond, "expected duration >= 10ms, got %v", duration)

	tracker.EndProcessing()

	// Should be 0 again after EndProcessing
	assert.Equal(t, time.Duration(0), tracker.ProcessingDuration())
}

func TestActivityStallTimeout(t *testing.T) {
	// 60s interval should give 90s timeout
	timeout := ActivityStallTimeout(60 * time.Second)
	assert.Equal(t, 90*time.Second, timeout)
}

func TestDefaultProcessingTimeout(t *testing.T) {
	// Default should be 2 minutes
	assert.Equal(t, 2*time.Minute, DefaultProcessingTimeout)
}

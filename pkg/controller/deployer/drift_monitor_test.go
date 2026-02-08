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

package deployer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestNewDriftPreventionMonitor(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	interval := 5 * time.Minute

	monitor := NewDriftPreventionMonitor(bus, logger, interval)

	require.NotNil(t, monitor)
	assert.Equal(t, interval, monitor.driftPreventionInterval)
	// eventChan is set in Start() for leader-only pattern, not in constructor
	assert.Nil(t, monitor.eventChan)
}

func TestDriftPreventionMonitor_Start(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := monitor.Start(ctx)

	// Start returns nil on graceful shutdown
	require.NoError(t, err)
}

func TestDriftPreventionMonitor_ResetDriftTimer(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	// Reset timer
	monitor.resetDriftTimer()

	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	assert.NotNil(t, monitor.driftTimer)
	assert.NotNil(t, monitor.driftTimerChan)
	assert.True(t, monitor.timerActive)
	assert.False(t, monitor.lastDeploymentTime.IsZero())
}

func TestDriftPreventionMonitor_StopDriftTimer(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	// Start timer first
	monitor.resetDriftTimer()

	// Stop timer
	monitor.stopDriftTimer()

	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	assert.False(t, monitor.timerActive)
}

func TestDriftPreventionMonitor_GetDriftTimerChan(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	t.Run("returns closed channel when no timer", func(t *testing.T) {
		// Timer not started yet
		ch := monitor.getDriftTimerChan()
		require.NotNil(t, ch)

		// Should be closed (will return immediately)
		select {
		case _, ok := <-ch:
			assert.False(t, ok, "channel should be closed")
		default:
			t.Fatal("channel should have been closed")
		}
	})

	t.Run("returns timer channel when active", func(t *testing.T) {
		monitor.resetDriftTimer()

		ch := monitor.getDriftTimerChan()
		require.NotNil(t, ch)

		// Channel should be the timer channel (not closed)
		// We can't easily test this without waiting for the timer,
		// so we just verify the channel is non-nil
		assert.NotNil(t, ch)
	})
}

func TestDriftPreventionMonitor_HandleDeploymentCompleted(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	// Set initial state
	monitor.resetDriftTimer()
	oldTime := monitor.lastDeploymentTime

	// Small delay
	time.Sleep(10 * time.Millisecond)

	// Handle deployment completed
	monitor.handleDeploymentCompleted()

	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	// Last deployment time should be updated
	assert.True(t, monitor.lastDeploymentTime.After(oldTime))
}

func TestDriftPreventionMonitor_HandleDriftTimerExpired(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	// Initialize timer state
	monitor.resetDriftTimer()

	// Handle timer expiration
	monitor.handleDriftTimerExpired()

	// Wait for DriftPreventionTriggeredEvent
	timeout := time.After(500 * time.Millisecond)
waitLoop:
	for {
		select {
		case e := <-eventChan:
			if triggered, ok := e.(*events.DriftPreventionTriggeredEvent); ok {
				assert.True(t, triggered.TimeSinceLastDeployment >= 0)
				break waitLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for DriftPreventionTriggeredEvent")
		}
	}
}

func TestDriftPreventionMonitor_HandleEvent(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	t.Run("routes DeploymentCompletedEvent", func(t *testing.T) {
		monitor.resetDriftTimer()
		oldTime := monitor.lastDeploymentTime

		time.Sleep(10 * time.Millisecond)

		event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
			Total:      1,
			Succeeded:  1,
			DurationMs: 100,
		})
		monitor.handleEvent(event)

		monitor.mu.Lock()
		defer monitor.mu.Unlock()

		assert.True(t, monitor.lastDeploymentTime.After(oldTime))
	})

	t.Run("ignores unknown events", func(t *testing.T) {
		// Should not panic
		otherEvent := events.NewValidationStartedEvent()
		monitor.handleEvent(otherEvent)
	})
}

func TestDriftPreventionMonitor_TimerTriggersEvent(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	// Use short interval for testing
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start monitor in goroutine
	go monitor.Start(ctx)

	// Wait for drift prevention event
	timeout := time.After(150 * time.Millisecond)
waitLoop:
	for {
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DriftPreventionTriggeredEvent); ok {
				break waitLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for DriftPreventionTriggeredEvent from timer")
		}
	}
}

func TestDriftMonitor_Name(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 1*time.Minute)

	assert.Equal(t, DriftMonitorComponentName, monitor.Name())
}

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

	monitor.resetDriftTimer()
	defer monitor.driftTimer.Stop()

	assert.NotNil(t, monitor.driftTimer.Chan())
	assert.False(t, monitor.lastDeploymentTime.IsZero())
}

func TestDriftPreventionMonitor_TimerChannel(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	assert.Nil(t, monitor.driftTimer.Chan())

	monitor.resetDriftTimer()
	defer monitor.driftTimer.Stop()

	assert.NotNil(t, monitor.driftTimer.Chan())
}

func TestDriftPreventionMonitor_HandleDeploymentCompleted(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	monitor.resetDriftTimer()
	defer monitor.driftTimer.Stop()
	oldTime := monitor.lastDeploymentTime

	time.Sleep(10 * time.Millisecond)

	monitor.handleDeploymentCompleted()

	assert.True(t, monitor.lastDeploymentTime.After(oldTime))
}

func TestDriftPreventionMonitor_HandleDriftTimerExpired(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 100*time.Millisecond)

	monitor.resetDriftTimer()
	defer monitor.driftTimer.Stop()

	monitor.handleDriftTimerExpired()

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
		defer monitor.driftTimer.Stop()
		oldTime := monitor.lastDeploymentTime

		time.Sleep(10 * time.Millisecond)

		event := events.NewDeploymentCompletedEvent(&events.DeploymentResult{
			Total:      1,
			Succeeded:  1,
			DurationMs: 100,
		})
		monitor.handleEvent(event)

		assert.True(t, monitor.lastDeploymentTime.After(oldTime))
	})

	t.Run("ignores unknown events", func(t *testing.T) {
		otherEvent := events.NewReconciliationCompletedEvent(0, "", nil, nil)
		monitor.handleEvent(otherEvent)
	})
}

func TestDriftPreventionMonitor_TimerTriggersEvent(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- monitor.Start(ctx)
	}()

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

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("drift monitor did not stop")
	}
}

func TestDriftMonitor_Name(t *testing.T) {
	bus := testutil.NewTestBus()
	monitor := NewDriftPreventionMonitor(bus, testutil.NewTestLogger(), 1*time.Minute)

	assert.Equal(t, DriftMonitorComponentName, monitor.Name())
}

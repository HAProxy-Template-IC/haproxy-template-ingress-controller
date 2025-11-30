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
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/dataplane"
	busevents "haproxy-template-ic/pkg/events"
)

// testSchedulerLogger creates a logger for scheduler tests.
func testSchedulerLogger() *slog.Logger {
	var w = io.Discard
	if testing.Verbose() {
		w = os.Stderr
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// TestNewDeploymentScheduler tests scheduler creation.
func TestNewDeploymentScheduler(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testSchedulerLogger()
	minInterval := 100 * time.Millisecond

	scheduler := NewDeploymentScheduler(bus, logger, minInterval)

	require.NotNil(t, scheduler)
	assert.Equal(t, minInterval, scheduler.minDeploymentInterval)
	assert.NotNil(t, scheduler.eventChan)
}

// TestDeploymentScheduler_Start tests scheduler startup and shutdown.
func TestDeploymentScheduler_Start(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := scheduler.Start(ctx)

	// Start returns nil on graceful shutdown
	require.NoError(t, err)
}

// TestDeploymentScheduler_HandleTemplateRendered tests template rendered event handling.
func TestDeploymentScheduler_HandleTemplateRendered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 100*time.Millisecond)

	event := events.NewTemplateRenderedEvent(
		"global\n  daemon\n",        // haproxyConfig
		"",                          // validationHAProxyConfig
		nil,                         // validationPaths
		&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
		&dataplane.AuxiliaryFiles{}, // validationAuxiliaryFiles
		2,                           // auxFileCount
		50,                          // durationMs
	)

	scheduler.handleTemplateRendered(event)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()

	assert.Equal(t, "global\n  daemon\n", scheduler.lastRenderedConfig)
	assert.NotNil(t, scheduler.lastAuxiliaryFiles)
}

// TestDeploymentScheduler_HandleValidationCompleted tests validation completed event handling.
func TestDeploymentScheduler_HandleValidationCompleted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventChan := bus.Subscribe(50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("caches validated config", func(t *testing.T) {
		// Set rendered config first
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = "global\n  daemon\n"
		scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100)

		scheduler.handleValidationCompleted(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.True(t, scheduler.hasValidConfig)
		assert.Equal(t, "global\n  daemon\n", scheduler.lastValidatedConfig)
	})

	t.Run("no rendered config available", func(t *testing.T) {
		// Reset state
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = ""
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100)

		// Should not panic when no config available
		scheduler.handleValidationCompleted(ctx, event)
	})

	t.Run("schedules deployment when endpoints available", func(t *testing.T) {
		// Set rendered config and endpoints
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = "global\n  daemon\n"
		scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		scheduler.currentEndpoints = []interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
		}
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100)

		scheduler.handleValidationCompleted(ctx, event)

		// Wait for deployment scheduled event
		timeout := time.After(500 * time.Millisecond)
	waitLoop:
		for {
			select {
			case e := <-eventChan:
				if _, ok := e.(*events.DeploymentScheduledEvent); ok {
					break waitLoop
				}
			case <-timeout:
				t.Fatal("timeout waiting for DeploymentScheduledEvent")
			}
		}
	})
}

// TestDeploymentScheduler_HandlePodsDiscovered tests pod discovery event handling.
func TestDeploymentScheduler_HandlePodsDiscovered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventChan := bus.Subscribe(50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("updates endpoints", func(t *testing.T) {
		endpoints := []interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
			dataplane.Endpoint{URL: "http://localhost:5556"},
		}

		event := events.NewHAProxyPodsDiscoveredEvent(endpoints, len(endpoints))

		scheduler.handlePodsDiscovered(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Len(t, scheduler.currentEndpoints, 2)
	})

	t.Run("skips deployment without valid config", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewHAProxyPodsDiscoveredEvent([]interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
		}, 1)

		scheduler.handlePodsDiscovered(ctx, event)

		// Should not schedule deployment (no valid config)
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DeploymentScheduledEvent); ok {
				t.Fatal("should not schedule deployment without valid config")
			}
		case <-time.After(50 * time.Millisecond):
			// Expected - no deployment scheduled
		}
	})

	t.Run("schedules deployment with valid config", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = true
		scheduler.lastValidatedConfig = "global\n  daemon\n"
		scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
		scheduler.mu.Unlock()

		event := events.NewHAProxyPodsDiscoveredEvent([]interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
		}, 1)

		scheduler.handlePodsDiscovered(ctx, event)

		// Wait for deployment scheduled event
		timeout := time.After(500 * time.Millisecond)
	waitLoop:
		for {
			select {
			case e := <-eventChan:
				if _, ok := e.(*events.DeploymentScheduledEvent); ok {
					break waitLoop
				}
			case <-timeout:
				t.Fatal("timeout waiting for DeploymentScheduledEvent")
			}
		}
	})
}

// TestDeploymentScheduler_HandleDriftPreventionTriggered tests drift prevention handling.
func TestDeploymentScheduler_HandleDriftPreventionTriggered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventChan := bus.Subscribe(50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("skips without valid config", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewDriftPreventionTriggeredEvent(5 * time.Minute)

		scheduler.handleDriftPreventionTriggered(ctx, event)

		// Should not schedule deployment
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DeploymentScheduledEvent); ok {
				t.Fatal("should not schedule deployment without valid config")
			}
		case <-time.After(50 * time.Millisecond):
			// Expected
		}
	})

	t.Run("schedules deployment with valid config and endpoints", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = true
		scheduler.lastValidatedConfig = "global\n  daemon\n"
		scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
		scheduler.currentEndpoints = []interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
		}
		scheduler.mu.Unlock()

		event := events.NewDriftPreventionTriggeredEvent(5 * time.Minute)

		scheduler.handleDriftPreventionTriggered(ctx, event)

		// Wait for deployment scheduled event
		timeout := time.After(500 * time.Millisecond)
	waitLoop:
		for {
			select {
			case e := <-eventChan:
				if _, ok := e.(*events.DeploymentScheduledEvent); ok {
					break waitLoop
				}
			case <-timeout:
				t.Fatal("timeout waiting for DeploymentScheduledEvent")
			}
		}
	})
}

// TestDeploymentScheduler_HandleDeploymentCompleted tests deployment completion handling.
func TestDeploymentScheduler_HandleDeploymentCompleted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	scheduler.schedulerMutex.Lock()
	scheduler.deploymentInProgress = true
	scheduler.schedulerMutex.Unlock()

	event := events.NewDeploymentCompletedEvent(2, 2, 0, 100)

	scheduler.handleDeploymentCompleted(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.False(t, scheduler.deploymentInProgress)
	assert.False(t, scheduler.lastDeploymentEndTime.IsZero())
}

// TestDeploymentScheduler_HandleConfigPublished tests config published handling.
func TestDeploymentScheduler_HandleConfigPublished(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	event := events.NewConfigPublishedEvent(
		"test-config",
		"test-namespace",
		5, // mapFileCount
		3, // secretCount
	)

	scheduler.handleConfigPublished(event)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()

	assert.Equal(t, "test-config", scheduler.runtimeConfigName)
	assert.Equal(t, "test-namespace", scheduler.runtimeConfigNamespace)
}

// TestDeploymentScheduler_HandleLostLeadership tests leadership loss handling.
func TestDeploymentScheduler_HandleLostLeadership(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	// Set up state that should be cleared
	scheduler.schedulerMutex.Lock()
	scheduler.deploymentInProgress = true
	scheduler.pendingDeployment = &scheduledDeployment{
		config: "test",
		reason: "test",
	}
	scheduler.schedulerMutex.Unlock()

	event := events.NewLostLeadershipEvent("test-pod", "leadership_lost")

	scheduler.handleLostLeadership(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.False(t, scheduler.deploymentInProgress)
	assert.Nil(t, scheduler.pendingDeployment)
}

// TestDeploymentScheduler_ScheduleOrQueue tests queueing behavior.
func TestDeploymentScheduler_ScheduleOrQueue(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)
	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("queues when deployment in progress", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.deploymentInProgress = true
		scheduler.pendingDeployment = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config", nil, []interface{}{}, "test")

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		require.NotNil(t, scheduler.pendingDeployment)
		assert.Equal(t, "test", scheduler.pendingDeployment.reason)
	})

	t.Run("latest wins when queueing", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.deploymentInProgress = true
		scheduler.pendingDeployment = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config1", nil, []interface{}{}, "first")
		scheduler.scheduleOrQueue(ctx, "config2", nil, []interface{}{}, "second")

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		require.NotNil(t, scheduler.pendingDeployment)
		assert.Equal(t, "second", scheduler.pendingDeployment.reason)
		assert.Equal(t, "config2", scheduler.pendingDeployment.config)
	})
}

// TestDeploymentScheduler_HandleEvent tests event type routing.
func TestDeploymentScheduler_HandleEvent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	scheduler := NewDeploymentScheduler(bus, testSchedulerLogger(), 0)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("routes TemplateRenderedEvent", func(t *testing.T) {
		event := events.NewTemplateRenderedEvent(
			"global\n  daemon\n",        // haproxyConfig
			"",                          // validationHAProxyConfig
			nil,                         // validationPaths
			&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
			&dataplane.AuxiliaryFiles{}, // validationAuxiliaryFiles
			2,                           // auxFileCount
			50,                          // durationMs
		)

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Equal(t, "global\n  daemon\n", scheduler.lastRenderedConfig)
	})

	t.Run("routes ValidationCompletedEvent", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = "global\n"
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100)

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.True(t, scheduler.hasValidConfig)
	})

	t.Run("routes HAProxyPodsDiscoveredEvent", func(t *testing.T) {
		event := events.NewHAProxyPodsDiscoveredEvent([]interface{}{
			dataplane.Endpoint{URL: "http://localhost:5555"},
		}, 1)

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Len(t, scheduler.currentEndpoints, 1)
	})

	t.Run("routes ConfigPublishedEvent", func(t *testing.T) {
		event := events.NewConfigPublishedEvent(
			"test-config",
			"test-namespace",
			5, // mapFileCount
			3, // secretCount
		)

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Equal(t, "test-config", scheduler.runtimeConfigName)
	})

	t.Run("routes LostLeadershipEvent", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.deploymentInProgress = true
		scheduler.schedulerMutex.Unlock()

		event := events.NewLostLeadershipEvent("test-pod", "test")

		scheduler.handleEvent(ctx, event)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.False(t, scheduler.deploymentInProgress)
	})

	t.Run("ignores unknown events", func(t *testing.T) {
		// Should not panic
		otherEvent := events.NewValidationStartedEvent()
		scheduler.handleEvent(ctx, otherEvent)
	})
}

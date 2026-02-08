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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func TestNewDeploymentScheduler(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	minInterval := 100 * time.Millisecond
	timeout := 30 * time.Second

	scheduler := NewDeploymentScheduler(bus, logger, minInterval, timeout)

	require.NotNil(t, scheduler)
	assert.Equal(t, minInterval, scheduler.minDeploymentInterval)
	assert.Equal(t, timeout, scheduler.deploymentTimeout)
	// eventChan is nil after construction - subscribed in Start() for leader-only components
	assert.Nil(t, scheduler.eventChan)
}

func TestDeploymentScheduler_Start(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 100*time.Millisecond, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := scheduler.Start(ctx)

	// Start returns nil on graceful shutdown
	require.NoError(t, err)
}

func TestDeploymentScheduler_HandleTemplateRendered(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 100*time.Millisecond, 30*time.Second)

	event := events.NewTemplateRenderedEvent(
		"global\n  daemon\n",        // haproxyConfig
		&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
		2,                           // auxFileCount
		50,                          // durationMs
		"",                          // triggerReason
		"",                          // contentChecksum
		true,                        // coalescible
	)

	scheduler.handleTemplateRendered(event)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()

	assert.Equal(t, "global\n  daemon\n", scheduler.lastRenderedConfig)
	assert.NotNil(t, scheduler.lastAuxiliaryFiles)
}

func TestDeploymentScheduler_HandleValidationCompleted(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("caches validated config", func(t *testing.T) {
		// Set rendered config first
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = "global\n  daemon\n"
		scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100, "", nil, true)

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

		event := events.NewValidationCompletedEvent([]string{}, 100, "", nil, true)

		// Should not panic when no config available
		scheduler.handleValidationCompleted(ctx, event)
	})

	t.Run("schedules deployment when endpoints available", func(t *testing.T) {
		// Set rendered config and endpoints
		scheduler.mu.Lock()
		scheduler.lastRenderedConfig = "global\n  daemon\n"
		scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
		scheduler.currentEndpoints = []dataplane.Endpoint{
			{URL: "http://localhost:5555"},
		}
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewValidationCompletedEvent([]string{}, 100, "", nil, true)

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

func TestDeploymentScheduler_HandlePodsDiscovered(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("updates endpoints", func(t *testing.T) {
		endpoints := []dataplane.Endpoint{
			{URL: "http://localhost:5555"},
			{URL: "http://localhost:5556"},
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

		event := events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{
			{URL: "http://localhost:5555"},
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

		event := events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{
			{URL: "http://localhost:5555"},
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

// When validation fails for any reason, the scheduler should deploy the last known good config.
func TestDeploymentScheduler_HandleValidationFailed(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("deploys cached config on any validation failure", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = true
		scheduler.lastValidatedConfig = "global\n  daemon\n"
		scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
		scheduler.currentEndpoints = []dataplane.Endpoint{
			{URL: "http://localhost:5555"},
		}
		scheduler.mu.Unlock()

		// Any trigger reason should trigger fallback deployment
		event := events.NewValidationFailedEvent([]string{"error"}, 100, "config_change")

		scheduler.handleValidationFailed(ctx, event)

		// Wait for deployment scheduled event
		timeout := time.After(500 * time.Millisecond)
	waitLoop:
		for {
			select {
			case e := <-eventChan:
				if scheduled, ok := e.(*events.DeploymentScheduledEvent); ok {
					assert.Equal(t, "validation_fallback", scheduled.Reason)
					break waitLoop
				}
			case <-timeout:
				t.Fatal("timeout waiting for DeploymentScheduledEvent")
			}
		}
	})

	t.Run("skips fallback without valid config", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = false
		scheduler.mu.Unlock()

		event := events.NewValidationFailedEvent([]string{"error"}, 100, "config_change")

		scheduler.handleValidationFailed(ctx, event)

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

	t.Run("skips fallback without endpoints", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = true
		scheduler.lastValidatedConfig = "global\n  daemon\n"
		scheduler.lastValidatedAux = &dataplane.AuxiliaryFiles{}
		scheduler.currentEndpoints = []dataplane.Endpoint{} // No endpoints
		scheduler.mu.Unlock()

		event := events.NewValidationFailedEvent([]string{"error"}, 100, "config_change")

		scheduler.handleValidationFailed(ctx, event)

		// Should not schedule deployment
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DeploymentScheduledEvent); ok {
				t.Fatal("should not schedule deployment without endpoints")
			}
		case <-time.After(50 * time.Millisecond):
			// Expected
		}
	})
}

func TestDeploymentScheduler_HandleDeploymentCompleted(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	scheduler.schedulerMutex.Lock()
	scheduler.state.phase = phaseDeploying
	scheduler.schedulerMutex.Unlock()

	event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      2,
		Succeeded:  2,
		DurationMs: 100,
	})

	scheduler.handleDeploymentCompleted(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.Equal(t, phaseIdle, scheduler.state.phase)
	assert.False(t, scheduler.state.lastDeploymentEndTime.IsZero())
}

func TestDeploymentScheduler_HandleConfigPublished(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

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

func TestDeploymentScheduler_HandleLostLeadership(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	// Set up state that should be cleared
	scheduler.schedulerMutex.Lock()
	scheduler.state.phase = phaseDeploying
	scheduler.state.pending = &scheduledDeployment{
		config: "test",
		reason: "test",
	}
	scheduler.schedulerMutex.Unlock()

	event := events.NewLostLeadershipEvent("test-pod", "leadership_lost")

	scheduler.handleLostLeadership(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.Equal(t, phaseIdle, scheduler.state.phase)
	assert.Nil(t, scheduler.state.pending)
}

func TestDeploymentScheduler_ScheduleOrQueue(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("queues when deployment in progress", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.phase = phaseDeploying
		scheduler.state.pending = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{}, "test", "test-correlation-id", true)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		require.NotNil(t, scheduler.state.pending)
		assert.Equal(t, "test", scheduler.state.pending.reason)
	})

	t.Run("latest wins when queueing", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.phase = phaseDeploying
		scheduler.state.pending = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config1", nil, nil, []dataplane.Endpoint{}, "first", "correlation-1", true)
		scheduler.scheduleOrQueue(ctx, "config2", nil, nil, []dataplane.Endpoint{}, "second", "correlation-2", true)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		require.NotNil(t, scheduler.state.pending)
		assert.Equal(t, "second", scheduler.state.pending.reason)
		assert.Equal(t, "config2", scheduler.state.pending.config)
	})
}

func TestDeploymentScheduler_HandleEvent(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	ctx := context.Background()
	scheduler.ctx = ctx

	t.Run("routes TemplateRenderedEvent", func(t *testing.T) {
		event := events.NewTemplateRenderedEvent(
			"global\n  daemon\n",        // haproxyConfig
			&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
			2,                           // auxFileCount
			50,                          // durationMs
			"",                          // triggerReason
			"",                          // contentChecksum
			true,                        // coalescible
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

		event := events.NewValidationCompletedEvent([]string{}, 100, "", nil, true)

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.True(t, scheduler.hasValidConfig)
	})

	t.Run("routes HAProxyPodsDiscoveredEvent", func(t *testing.T) {
		event := events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{
			{URL: "http://localhost:5555"},
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
		scheduler.state.phase = phaseDeploying
		scheduler.schedulerMutex.Unlock()

		event := events.NewLostLeadershipEvent("test-pod", "test")

		scheduler.handleEvent(ctx, event)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.Equal(t, phaseIdle, scheduler.state.phase)
	})

	t.Run("routes DeploymentCompletedEvent", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.phase = phaseDeploying
		scheduler.schedulerMutex.Unlock()

		event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
			Total:      1,
			Succeeded:  1,
			DurationMs: 50,
		})

		scheduler.handleEvent(ctx, event)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.Equal(t, phaseIdle, scheduler.state.phase)
	})

	t.Run("routes DriftPreventionTriggeredEvent", func(t *testing.T) {
		scheduler.mu.Lock()
		scheduler.hasValidConfig = false // Ensure no deployment scheduled
		scheduler.mu.Unlock()

		event := events.NewDriftPreventionTriggeredEvent(5 * time.Minute)

		// Should not panic
		scheduler.handleEvent(ctx, event)
	})

	t.Run("ignores unknown events", func(t *testing.T) {
		// Should not panic
		otherEvent := events.NewValidationStartedEvent()
		scheduler.handleEvent(ctx, otherEvent)
	})

	t.Run("routes ConfigValidatedEvent", func(t *testing.T) {
		templateConfig := &v1alpha1.HAProxyTemplateConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-template",
				Namespace: "my-namespace",
			},
		}
		event := events.NewConfigValidatedEvent(nil, templateConfig, "v1", "sv1")

		scheduler.handleEvent(ctx, event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Equal(t, "my-template", scheduler.templateConfigName)
		assert.Equal(t, "my-namespace", scheduler.templateConfigNamespace)
	})
}

func TestDeploymentScheduler_Name(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 100*time.Millisecond, 30*time.Second)

	assert.Equal(t, SchedulerComponentName, scheduler.Name())
}

func TestDeploymentScheduler_HandleConfigValidated(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	t.Run("caches template config metadata", func(t *testing.T) {
		templateConfig := &v1alpha1.HAProxyTemplateConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-template",
				Namespace: "test-ns",
			},
		}
		event := events.NewConfigValidatedEvent(nil, templateConfig, "v1", "sv1")

		scheduler.handleConfigValidated(event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Equal(t, "test-template", scheduler.templateConfigName)
		assert.Equal(t, "test-ns", scheduler.templateConfigNamespace)
	})

	t.Run("ignores non-HAProxyTemplateConfig", func(t *testing.T) {
		// Reset state
		scheduler.mu.Lock()
		scheduler.templateConfigName = ""
		scheduler.templateConfigNamespace = ""
		scheduler.mu.Unlock()

		// Create event with a non-HAProxyTemplateConfig
		event := events.NewConfigValidatedEvent(nil, "not-a-template-config", "v1", "sv1")

		// Should not panic and should not change state
		scheduler.handleConfigValidated(event)

		scheduler.mu.RLock()
		defer scheduler.mu.RUnlock()

		assert.Equal(t, "", scheduler.templateConfigName)
		assert.Equal(t, "", scheduler.templateConfigNamespace)
	})
}

// with a pending deployment.
func TestDeploymentScheduler_HandleDeploymentCompleted_WithPending(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	scheduler.ctx = context.Background()

	// Set up state with deployment in progress and pending
	scheduler.schedulerMutex.Lock()
	scheduler.state.phase = phaseDeploying
	scheduler.state.pending = &scheduledDeployment{
		config:        "pending-config",
		auxFiles:      nil,
		endpoints:     []dataplane.Endpoint{{URL: "http://localhost:5555"}},
		reason:        "pending-deployment",
		correlationID: "correlation-123",
	}
	scheduler.schedulerMutex.Unlock()

	event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      1,
		Succeeded:  1,
		DurationMs: 100,
	})

	scheduler.handleDeploymentCompleted(event)

	// The pending deployment should be scheduled
	timeout := time.After(500 * time.Millisecond)
waitLoop:
	for {
		select {
		case e := <-eventChan:
			if scheduled, ok := e.(*events.DeploymentScheduledEvent); ok {
				assert.Equal(t, "pending-config", scheduled.Config)
				assert.Equal(t, "pending-deployment", scheduled.Reason)
				break waitLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for DeploymentScheduledEvent")
		}
	}
}

func TestDeploymentScheduler_ScheduleWithRateLimit(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	// Use longer rate limit to test the path
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 50*time.Millisecond, 30*time.Second)
	scheduler.ctx = context.Background()

	// Set last deployment time to recent past
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()

	start := time.Now()

	// Schedule deployment - should be rate limited
	go scheduler.scheduleWithRateLimitUnlocked(
		context.Background(),
		"config",
		nil,
		nil,
		[]dataplane.Endpoint{{URL: "http://localhost:5555"}},
		"test-rate-limit",
		"correlation-456",
		true, // coalescible
	)

	// Wait for deployment scheduled event
	timeout := time.After(500 * time.Millisecond)
waitLoop:
	for {
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DeploymentScheduledEvent); ok {
				elapsed := time.Since(start)
				// Should have been delayed by rate limiting (at least 50ms)
				assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(40)) // Allow some tolerance
				break waitLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for DeploymentScheduledEvent")
		}
	}
}

func TestDeploymentScheduler_ScheduleWithRateLimit_ContextCancellation(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	// Use long rate limit
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 5*time.Second, 30*time.Second)

	// Set last deployment time to recent past
	scheduler.schedulerMutex.Lock()
	scheduler.state.phase = phaseRateLimiting
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.scheduleWithRateLimitUnlocked(
			ctx,
			"config",
			nil,
			nil,
			[]dataplane.Endpoint{},
			"test-cancel",
			"correlation-789",
			true, // coalescible
		)
		close(done)
	}()

	// Cancel context after short delay
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Should return quickly due to context cancellation
	select {
	case <-done:
		// Expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("scheduling should have been cancelled")
	}

	// Phase should be idle after cancellation
	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	assert.Equal(t, phaseIdle, scheduler.state.phase)
}

func TestDeploymentScheduler_ScheduleWithRateLimit_ComputeRuntimeConfig(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	scheduler.ctx = context.Background()

	// Set template config name but not runtime config name
	scheduler.mu.Lock()
	scheduler.templateConfigName = "my-template"
	scheduler.templateConfigNamespace = "my-namespace"
	scheduler.runtimeConfigName = "" // Not set
	scheduler.mu.Unlock()

	go scheduler.scheduleWithRateLimitUnlocked(
		context.Background(),
		"config",
		nil,
		nil,
		[]dataplane.Endpoint{},
		"test-compute-runtime",
		"correlation-compute",
		true, // coalescible
	)

	// Wait for deployment scheduled event
	timeout := time.After(500 * time.Millisecond)
waitLoop:
	for {
		select {
		case e := <-eventChan:
			if scheduled, ok := e.(*events.DeploymentScheduledEvent); ok {
				// Runtime config name should be computed from template config name
				assert.NotEmpty(t, scheduled.RuntimeConfigName)
				assert.Equal(t, "my-namespace", scheduled.RuntimeConfigNamespace)
				break waitLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for DeploymentScheduledEvent")
		}
	}
}

func TestDeploymentScheduler_ScheduleWithPendingWhileScheduling(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 50*time.Millisecond, 30*time.Second)
	scheduler.ctx = context.Background()

	// Set last deployment time to trigger rate limiting
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()

	// Start first scheduling
	go scheduler.scheduleWithRateLimitUnlocked(
		context.Background(),
		"config1",
		nil,
		nil,
		[]dataplane.Endpoint{},
		"first",
		"correlation-1",
		true, // coalescible
	)

	// Add pending deployment while first is being rate limited
	time.Sleep(10 * time.Millisecond)
	scheduler.schedulerMutex.Lock()
	scheduler.state.pending = &scheduledDeployment{
		config:        "config2",
		auxFiles:      nil,
		endpoints:     []dataplane.Endpoint{},
		reason:        "second",
		correlationID: "correlation-2",
	}
	scheduler.schedulerMutex.Unlock()

	// Collect events - should see both deployments
	eventsReceived := 0
	timeout := time.After(1 * time.Second)
collectLoop:
	for {
		select {
		case e := <-eventChan:
			if _, ok := e.(*events.DeploymentScheduledEvent); ok {
				eventsReceived++
				if eventsReceived >= 2 {
					break collectLoop
				}
			}
		case <-timeout:
			// At minimum we should have received the first deployment
			if eventsReceived == 0 {
				t.Fatal("timeout waiting for first DeploymentScheduledEvent")
			}
			break collectLoop
		}
	}

	// Should have received at least the first deployment
	assert.GreaterOrEqual(t, eventsReceived, 1)
}

func TestDeploymentScheduler_StatePhases(t *testing.T) {
	t.Run("phase string representation", func(t *testing.T) {
		assert.Equal(t, "idle", phaseIdle.String())
		assert.Equal(t, "rate_limiting", phaseRateLimiting.String())
		assert.Equal(t, "deploying", phaseDeploying.String())
		assert.Equal(t, "unknown", deploymentPhase(99).String())
	})

	t.Run("initial phase is idle", func(t *testing.T) {
		bus := testutil.NewTestBus()
		scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.Equal(t, phaseIdle, scheduler.state.phase)
	})

	t.Run("timeout only fires in deploying phase", func(t *testing.T) {
		bus := testutil.NewTestBus()
		bus.Start()
		scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 1*time.Millisecond)
		ctx := context.Background()

		// In rate limiting phase with expired timeout - should NOT fire
		scheduler.schedulerMutex.Lock()
		scheduler.state.phase = phaseRateLimiting
		scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second)
		scheduler.schedulerMutex.Unlock()

		scheduler.checkDeploymentTimeout(ctx)

		scheduler.schedulerMutex.Lock()
		// Phase should be unchanged - timeout checker skips rate limiting phase
		assert.Equal(t, phaseRateLimiting, scheduler.state.phase)
		scheduler.schedulerMutex.Unlock()

		// In deploying phase with expired timeout - SHOULD fire
		scheduler.schedulerMutex.Lock()
		scheduler.state.phase = phaseDeploying
		scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second)
		scheduler.state.activeCorrelationID = "test-correlation"
		scheduler.schedulerMutex.Unlock()

		scheduler.checkDeploymentTimeout(ctx)

		scheduler.schedulerMutex.Lock()
		assert.Equal(t, phaseIdle, scheduler.state.phase)
		scheduler.schedulerMutex.Unlock()
	})
}

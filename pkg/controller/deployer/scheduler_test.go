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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// initLoopChannels creates the deploy-loop coordination channels WITHOUT starting
// the loop. Use it for tests that call handleDeploymentCompleted /
// checkDeploymentTimeout directly: those call signalCompleted(), which would be a
// silent no-op on a nil channel (a send case on a nil channel is never selected,
// so it always falls through to default). Creating the channels makes the signal
// observable and matches the production invariant that Start() created them.
func initLoopChannels(s *DeploymentScheduler) {
	s.pendingSignal = make(chan struct{}, 1)
	s.completed = make(chan struct{}, 1)
	s.loopDone = make(chan struct{})
}

// startLoopForTest wires the deploy-loop channels and starts runDeployLoop in a
// goroutine bound to ctx (mirroring what Start() does). The caller owns ctx and
// must cancel it (via defer cancel()) so the loop exits and doesn't leak. The
// loop closes loopDone on exit; tests that need to join it can select on
// s.loopDone.
func startLoopForTest(t *testing.T, s *DeploymentScheduler, ctx context.Context) {
	t.Helper()
	s.ctx = ctx
	initLoopChannels(s)
	go s.runDeployLoop(ctx)
}

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
		nil,                         // statusPatches
		nil,                         // renderedResources
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

	// Run the deploy loop so scheduleOrQueue's pending → published event flows
	// (minInterval=0 → the loop emits immediately on signal).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

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

		// The loop picks up the pending deploy and publishes it.
		testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	})
}

func TestDeploymentScheduler_HandlePodsDiscovered(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	// Run the deploy loop so the valid-config subtest's pending deploy is emitted.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

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

		// The loop picks up the pending deploy and publishes it.
		testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	})
}

// When validation fails for any reason, the scheduler should deploy the last known good config.
func TestDeploymentScheduler_HandleValidationFailed(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	// Run the deploy loop so the fallback subtest's pending deploy is emitted.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

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

		// The loop picks up the pending fallback deploy and publishes it.
		scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
		assert.Equal(t, "validation_fallback", scheduled.Reason)
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
	// handleDeploymentCompleted calls signalCompleted(), which is a no-op send on
	// a nil channel unless the loop channels exist. Create them (no loop running).
	initLoopChannels(scheduler)

	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.schedulerMutex.Unlock()

	event := events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total:      2,
		Succeeded:  2,
		DurationMs: 100,
	})

	scheduler.handleDeploymentCompleted(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.False(t, scheduler.state.deployInFlight)
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
	scheduler.state.deployInFlight = true
	scheduler.state.pending = &scheduledDeployment{
		config: "test",
		reason: "test",
	}
	scheduler.schedulerMutex.Unlock()

	event := events.NewLostLeadershipEvent("test-pod", "leadership_lost")

	scheduler.handleLostLeadership(event)

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()

	assert.False(t, scheduler.state.deployInFlight)
	assert.Nil(t, scheduler.state.pending)
}

// TestDeploymentScheduler_ScheduleOrQueue verifies the latest-wins pending slot.
// No deploy loop is running here, so the pending slot stays populated for
// inspection; deployInFlight=true also keeps it from being eligible for dispatch
// even if a loop were running. scheduleOrQueue calls signalLoop(), which needs
// the loop channels to exist, so create them.
func TestDeploymentScheduler_ScheduleOrQueue(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx
	initLoopChannels(scheduler)

	t.Run("sets pending slot", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.deployInFlight = true
		scheduler.state.pending = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{}, "test", "test-correlation-id", nil, true, "")

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		require.NotNil(t, scheduler.state.pending)
		assert.Equal(t, "test", scheduler.state.pending.reason)
	})

	t.Run("latest wins in pending slot", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.deployInFlight = true
		scheduler.state.pending = nil
		scheduler.schedulerMutex.Unlock()

		scheduler.scheduleOrQueue(ctx, "config1", nil, nil, []dataplane.Endpoint{}, "first", "correlation-1", nil, true, "")
		scheduler.scheduleOrQueue(ctx, "config2", nil, nil, []dataplane.Endpoint{}, "second", "correlation-2", nil, true, "")

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
	// DeploymentCompletedEvent routing calls signalCompleted(); give it channels.
	initLoopChannels(scheduler)

	t.Run("routes TemplateRenderedEvent", func(t *testing.T) {
		event := events.NewTemplateRenderedEvent(
			"global\n  daemon\n",        // haproxyConfig
			&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
			nil,                         // statusPatches
			nil,                         // renderedResources
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
		scheduler.state.deployInFlight = true
		scheduler.schedulerMutex.Unlock()

		event := events.NewLostLeadershipEvent("test-pod", "test")

		scheduler.handleEvent(ctx, event)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.False(t, scheduler.state.deployInFlight)
	})

	t.Run("routes DeploymentCompletedEvent", func(t *testing.T) {
		scheduler.schedulerMutex.Lock()
		scheduler.state.deployInFlight = true
		scheduler.schedulerMutex.Unlock()

		event := events.NewDeploymentCompletedEvent(&events.DeploymentResult{
			Total:      1,
			Succeeded:  1,
			DurationMs: 50,
		})

		scheduler.handleEvent(ctx, event)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.False(t, scheduler.state.deployInFlight)
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
		otherEvent := events.NewReconciliationCompletedEvent(0, nil, nil)
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

// TestDeploymentScheduler_HandleDeploymentCompleted_WithPending verifies the
// convergence contract that a deployment queued while another is in flight is
// dispatched by the loop AFTER the in-flight one completes, then the loop goes
// quiet. handleDeploymentCompleted no longer re-schedules itself (that second
// scheduling path was the source of the reload storm); it only clears
// deployInFlight and signals the loop, which picks up pending on its next cycle.
//
// Scheduling-independent by construction: rather than poking schedulerState
// directly (the old version set deployInFlight + pending under the mutex WITHOUT
// signalling the loop, so whether the loop's first iteration observed pending
// before or after the poke — and thus whether it parked on pendingSignal, which
// handleDeploymentCompleted never signals — was a pure goroutine race that timed
// out ~intermittently, issue #69), this drives the loop into a GENUINE in-flight
// deploy via the production scheduleOrQueue path. Every wakeup edge the loop
// relies on is therefore exercised the way production drives it, and pacing is on
// the loop's own observable output (the published DeploymentScheduledEvent), never
// on sleeps or exact interleavings.
func TestDeploymentScheduler_HandleDeploymentCompleted_WithPending(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	// Drive the loop into a real in-flight deploy through the production path: the
	// loop grabs this pending, dispatches it (publishing its scheduled event), and
	// parks in awaitCompletion holding deployInFlight — the genuine state this test
	// needs, reached without touching schedulerState. Observing the published event
	// is the deterministic signal that the loop is now awaiting completion.
	scheduler.scheduleOrQueue(ctx, "in-flight-config", nil, nil,
		[]dataplane.Endpoint{{URL: "http://localhost:5555"}},
		"in-flight-deployment", "in-flight-corr", nil, true, "")
	inFlight := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	require.Equal(t, "in-flight-config", inFlight.Config)

	// Queue a second deploy behind the in-flight one. latest-wins fills the single
	// pending slot; the loop cannot dispatch it until the in-flight deploy completes.
	scheduler.scheduleOrQueue(ctx, "pending-config", nil, nil,
		[]dataplane.Endpoint{{URL: "http://localhost:5555"}},
		"pending-deployment", "correlation-123", nil, true, "")

	// Completing the in-flight deploy releases awaitCompletion; the loop then grabs
	// the pending deployment and emits its scheduled event. Order of these two
	// signals is irrelevant — scheduleOrQueue's signalLoop guarantees the loop
	// re-checks pending regardless of when completion lands.
	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total:      1,
		Succeeded:  1,
		DurationMs: 100,
	}))

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "pending-config", scheduled.Config)
	assert.Equal(t, "pending-deployment", scheduled.Reason)

	// Quiescence: with no completion signalled for the pending deploy, the loop is
	// parked in awaitCompletion. Nothing further may be dispatched.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.NoEventTimeout)
}

// TestDeploymentScheduler_ScheduleWithRateLimit verifies the loop waits out
// minDeploymentInterval (measured from the last deploy's end time) before
// emitting the scheduled event. The old design slept inside a per-schedule
// goroutine; the rate-limit wait now lives in the single runDeployLoop.
func TestDeploymentScheduler_ScheduleWithRateLimit(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	// Use longer rate limit to test the path
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 50*time.Millisecond, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set last deployment time to recent past, then start the loop: the next
	// deploy must wait out the 50ms interval since that end time.
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()
	startLoopForTest(t, scheduler, ctx)

	start := time.Now()

	// nil parsedConfig → cold-start structural lane (no runtime-raw apply), and
	// empty endpoints → nothing to apply anyway.
	scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{},
		"test-rate-limit", "correlation-456", nil, true, "")

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	_ = scheduled
	elapsed := time.Since(start)
	// Should have been delayed by rate limiting (at least ~50ms, with tolerance).
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(40))
}

// TestDeploymentScheduler_ScheduleWithRateLimit_ContextCancellation verifies the
// loop exits promptly while waiting out the rate-limit interval when its context
// is cancelled, closing loopDone. This replaces the old test that cancelled a
// per-schedule goroutine's wait.
func TestDeploymentScheduler_ScheduleWithRateLimit_ContextCancellation(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	// Use long rate limit so the loop is parked in its interval wait when we cancel.
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 5*time.Second, 30*time.Second)

	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	startLoopForTest(t, scheduler, ctx)

	// Enqueue work so the loop advances from "wait for pending" into the
	// (5s) interval wait.
	scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{},
		"test-cancel", "correlation-789", nil, true, "")

	// Give the loop a moment to enter the interval wait, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// The loop must return quickly (close loopDone) on cancellation.
	select {
	case <-scheduler.loopDone:
		// Expected
	case <-time.After(testutil.LongTimeout):
		t.Fatal("deploy loop should have exited on context cancellation")
	}
}

// TestDeploymentScheduler_ScheduleWithRateLimit_ComputeRuntimeConfig verifies the
// loop's publishScheduled computes the runtime config name from the cached
// template config name when no ConfigPublishedEvent has set it yet.
func TestDeploymentScheduler_ScheduleWithRateLimit_ComputeRuntimeConfig(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set template config name but not runtime config name
	scheduler.mu.Lock()
	scheduler.templateConfigName = "my-template"
	scheduler.templateConfigNamespace = "my-namespace"
	scheduler.runtimeConfigName = "" // Not set
	scheduler.mu.Unlock()

	startLoopForTest(t, scheduler, ctx)

	// nil parsedConfig → cold-start structural lane (no runtime-raw apply), and
	// empty endpoints → nothing to apply anyway.
	scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{},
		"test-compute-runtime", "correlation-compute", nil, true, "")

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	// Runtime config name should be computed from template config name
	assert.NotEmpty(t, scheduled.RuntimeConfigName)
	assert.Equal(t, "my-namespace", scheduled.RuntimeConfigNamespace)
}

// TestDeploymentScheduler_DeployInFlightState replaces the old
// TestDeploymentScheduler_StatePhases. The phase state machine
// (deploymentPhase + its String() method) is gone; deployInFlight is the
// single boolean replacement. The "timeout only fires in deploying phase"
// subtest is rewritten as "timeout only fires when deployInFlight".
func TestDeploymentScheduler_DeployInFlightState(t *testing.T) {
	t.Run("initial deployInFlight is false", func(t *testing.T) {
		bus := testutil.NewTestBus()
		scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

		scheduler.schedulerMutex.Lock()
		defer scheduler.schedulerMutex.Unlock()

		assert.False(t, scheduler.state.deployInFlight)
	})

	t.Run("timeout only fires when deployInFlight", func(t *testing.T) {
		bus := testutil.NewTestBus()
		bus.Start()
		scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 1*time.Millisecond)
		initLoopChannels(scheduler)
		ctx := context.Background()

		// Not in flight but with an expired start time - timeout MUST NOT fire.
		scheduler.schedulerMutex.Lock()
		scheduler.state.deployInFlight = false
		scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second)
		scheduler.schedulerMutex.Unlock()

		scheduler.checkDeploymentTimeout(ctx)

		scheduler.schedulerMutex.Lock()
		// Unchanged - the timeout checker guards on deployInFlight.
		assert.False(t, scheduler.state.deployInFlight)
		scheduler.schedulerMutex.Unlock()

		// In flight with an expired timeout - SHOULD fire and clear deployInFlight.
		scheduler.schedulerMutex.Lock()
		scheduler.state.deployInFlight = true
		scheduler.state.deploymentStartTime = time.Now().Add(-10 * time.Second)
		scheduler.state.activeCorrelationID = "test-correlation"
		scheduler.schedulerMutex.Unlock()

		scheduler.checkDeploymentTimeout(ctx)

		scheduler.schedulerMutex.Lock()
		assert.False(t, scheduler.state.deployInFlight)
		scheduler.schedulerMutex.Unlock()
	})
}

// countScheduledEvents drains eventChan for the given window and returns every
// DeploymentScheduledEvent seen. Used by the coalescing/no-burst tests to assert
// on the exact count published over a fixed observation window.
func countScheduledEvents(eventChan <-chan busevents.Event, window time.Duration) []*events.DeploymentScheduledEvent {
	var got []*events.DeploymentScheduledEvent
	deadline := time.After(window)
	for {
		select {
		case e := <-eventChan:
			if scheduled, ok := e.(*events.DeploymentScheduledEvent); ok {
				got = append(got, scheduled)
			}
		case <-deadline:
			return got
		}
	}
}

// TestScheduler_NoBurstUnderConcurrentReconciles is the headline regression test
// for the reload-storm fix. With a single deploy loop owning the rate-limit
// timer, N concurrent scheduleOrQueue calls (each latest-wins overwriting the
// single pending slot) collapse into EXACTLY ONE DeploymentScheduledEvent after
// minDeploymentInterval — the loop then parks in awaitCompletion. The OLD phase
// machine spawned a rate-limit goroutine per schedule and would burst ~N events.
//
// nil parsedConfig → cold-start structural lane (so the interval applies and the
// loop coalesces); empty endpoints → no real dataplane client is needed.
func TestScheduler_NoBurstUnderConcurrentReconciles(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.SubscribeTypes("burst-watcher", 200, events.EventTypeDeploymentScheduled)
	bus.Start()

	const interval = 100 * time.Millisecond
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), interval, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A deploy just ended → the first deploy must wait out the full interval, so
	// all N concurrent schedules land in the single pending slot before the loop
	// fires once.
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()
	startLoopForTest(t, scheduler, ctx)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			reason := "reconcile-" + strconv.Itoa(i)
			corr := "corr-" + strconv.Itoa(i)
			scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{},
				reason, corr, nil, true, "")
		}(i)
	}
	wg.Wait()

	// Observe well past the interval (≈2.5×). The loop coalesces all 50 into ONE
	// deploy, publishes it, then blocks in awaitCompletion (no completion is ever
	// signalled), so no further events can appear.
	got := countScheduledEvents(eventChan, 250*time.Millisecond)
	require.Len(t, got, 1,
		"the single deploy loop must coalesce all %d concurrent schedules into exactly one "+
			"DeploymentScheduledEvent — more than one means concurrent rate-limit timers (the "+
			"reload storm) have returned", n)

	// The loop is now parked in awaitCompletion holding deployInFlight. Drive a
	// completion and assert it's ready for the next deploy.
	scheduler.schedulerMutex.Lock()
	inFlight := scheduler.state.deployInFlight
	scheduler.schedulerMutex.Unlock()
	assert.True(t, inFlight, "after emitting the coalesced deploy the loop must be in-flight")

	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total:      1,
		Succeeded:  1,
		DurationMs: 10,
	}))

	scheduler.schedulerMutex.Lock()
	defer scheduler.schedulerMutex.Unlock()
	assert.False(t, scheduler.state.deployInFlight,
		"completion must clear deployInFlight so the loop is ready for the next deploy")
}

// TestScheduler_LatestWinsCoalescing verifies that several schedules landing
// before the rate-limit interval elapses collapse to ONE deploy carrying the
// LAST config (latest-wins on the single pending slot).
func TestScheduler_LatestWinsCoalescing(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.SubscribeTypes("coalesce-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	const interval = 100 * time.Millisecond
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), interval, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Deploy just ended → the loop waits the interval, giving us time to overwrite
	// the pending slot three times before it fires.
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()
	startLoopForTest(t, scheduler, ctx)

	scheduler.scheduleOrQueue(ctx, "A", nil, nil, []dataplane.Endpoint{}, "first", "corr-a", nil, true, "")
	scheduler.scheduleOrQueue(ctx, "B", nil, nil, []dataplane.Endpoint{}, "second", "corr-b", nil, true, "")
	scheduler.scheduleOrQueue(ctx, "C", nil, nil, []dataplane.Endpoint{}, "third", "corr-c", nil, true, "")

	got := countScheduledEvents(eventChan, 250*time.Millisecond)
	require.Len(t, got, 1, "three pre-interval schedules must coalesce into exactly one deploy")
	assert.Equal(t, "C", got[0].Config,
		"latest-wins: the single emitted deploy must carry the newest config (C), not A or B")
}

// TestScheduler_LoopStopsOnContextCancel verifies runDeployLoop exits and closes
// loopDone when its context is cancelled — the join point Start() relies on for
// clean shutdown.
func TestScheduler_LoopStopsOnContextCancel(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), 100*time.Millisecond, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	startLoopForTest(t, scheduler, ctx)

	// Loop is parked waiting for pending work; cancelling must release it.
	cancel()

	select {
	case <-scheduler.loopDone:
		// Expected - loop exited and closed loopDone.
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runDeployLoop did not exit (loopDone not closed) within timeout after context cancel")
	}
}

// TestScheduler_IntervalUsesCompletionEndTime verifies the rate-limit interval is
// measured from the last deploy's END time (lastDeploymentEndTime), i.e. the loop
// delays the first deploy by ≈minDeploymentInterval when a deploy just completed.
func TestScheduler_IntervalUsesCompletionEndTime(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.SubscribeTypes("interval-watcher", 50, events.EventTypeDeploymentScheduled)
	bus.Start()

	const interval = 200 * time.Millisecond
	scheduler := NewDeploymentScheduler(bus, testutil.NewTestLogger(), interval, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Mark a deploy as having just ended, then start the loop and schedule once.
	scheduler.schedulerMutex.Lock()
	scheduler.state.lastDeploymentEndTime = time.Now()
	scheduler.schedulerMutex.Unlock()
	startLoopForTest(t, scheduler, ctx)

	start := time.Now()
	scheduler.scheduleOrQueue(ctx, "config", nil, nil, []dataplane.Endpoint{},
		"rate-limited", "corr-interval", nil, true, "")

	testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.VeryLongTimeout)
	elapsed := time.Since(start)
	// Rate-limited from the completion end time. Allow tolerance for timer/scheduler jitter.
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(150),
		"the deploy must be delayed ≈minDeploymentInterval measured from lastDeploymentEndTime")
}

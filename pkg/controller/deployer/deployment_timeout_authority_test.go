// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

func TestDeploymentTimeoutCancelsBlockedExecutorOutOfBand(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	testDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		select {
		case <-request.Context().Done():
			select {
			case requestCanceled <- struct{}{}:
			default:
			}
		case <-testDone:
		}
	}))
	t.Cleanup(func() {
		close(testDone)
		server.Close()
	})

	bus := testutil.NewTestBus()
	completed := bus.SubscribeTypes("completion-watcher", 10, events.EventTypeDeploymentCompleted)
	executor := createTestDeployer(bus)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	executorDone := make(chan error, 1)
	go func() { executorDone <- executor.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-executorDone)
	})
	select {
	case <-executor.SubscriptionReady():
	case <-time.After(testutil.LongTimeout):
		t.Fatal("deployer did not become ready")
	}

	scheduled := events.NewDeploymentScheduledEvent(
		"global\n  daemon\n", nil, nil,
		[]dataplane.Endpoint{{URL: server.URL, Username: "admin", Password: "password"}},
		"runtime", "default", "test", "checksum-A", nil, false,
		events.WithCorrelation("shared-trace", "validation"),
	)
	bus.Publish(scheduled)
	select {
	case <-requestStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("fake endpoint did not receive the deployment request")
	}

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, time.Millisecond)
	initLoopChannels(scheduler)
	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.deploymentStartTime = time.Now().Add(-time.Second)
	scheduler.state.activeDeploymentID = scheduled.EventID()
	scheduler.state.activeCorrelationID = scheduled.CorrelationID()
	scheduler.schedulerMutex.Unlock()
	scheduler.checkDeploymentTimeout(context.Background())

	select {
	case <-requestCanceled:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("timeout cancellation did not reach the blocked Dataplane request")
	}
	completion := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, completed, testutil.LongTimeout)
	assert.Equal(t, scheduled.EventID(), completion.DeploymentID)
}

func TestDeploymentSchedulerRejectsMismatchedCompletionBeforeMutation(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, time.Second)
	initLoopChannels(scheduler)

	parsed := &parser.StructuredConfig{}
	pending := &scheduledDeployment{config: "pending-B"}
	scheduler.schedulerMutex.Lock()
	scheduler.state = schedulerState{
		deployInFlight:      true,
		activeDeploymentID:  "deployment-B",
		activeCorrelationID: "shared-trace",
		deploymentStartTime: time.Now(),
		pending:             pending,
	}
	scheduler.lastDispatchedParsed = parsed
	scheduler.lastDispatchedConfig = "dispatched-B"
	scheduler.lastActivatedConfig = "activated-before-B"
	stateBefore := scheduler.state
	scheduler.schedulerMutex.Unlock()

	scheduler.mu.Lock()
	scheduler.lastDeployedConfigHash = "last-good"
	scheduler.lastDeployedPodSetHash = "pods-before-B"
	scheduler.mu.Unlock()

	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		DeploymentID:    "deployment-A",
		Total:           1,
		Succeeded:       1,
		ContentChecksum: "stale-A",
	}, events.WithCorrelation("shared-trace", "deployment-A")))

	scheduler.schedulerMutex.Lock()
	assert.Equal(t, stateBefore, scheduler.state)
	assert.Same(t, parsed, scheduler.lastDispatchedParsed)
	assert.Equal(t, "dispatched-B", scheduler.lastDispatchedConfig)
	assert.Equal(t, "activated-before-B", scheduler.lastActivatedConfig)
	scheduler.schedulerMutex.Unlock()
	scheduler.mu.RLock()
	assert.Equal(t, "last-good", scheduler.lastDeployedConfigHash)
	assert.Equal(t, "pods-before-B", scheduler.lastDeployedPodSetHash)
	scheduler.mu.RUnlock()
	select {
	case <-scheduler.completed:
		t.Fatal("mismatched completion signaled the active deployment")
	default:
	}
}

func TestTimedOutDeploymentRetiresBeforeQueuedWorkAdvances(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledEvents := bus.SubscribeTypes("scheduled-watcher", 20, events.EventTypeDeploymentScheduled)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	scheduler.scheduleOrQueue(ctx, "config-A", nil, nil, oneEndpoint(), "A", "shared-trace", nil, false, "checksum-A")
	deploymentA := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledEvents, testutil.LongTimeout)
	scheduler.scheduleOrQueue(ctx, "config-B", nil, nil, oneEndpoint(), "B", "shared-trace", nil, false, "checksum-B")

	scheduler.schedulerMutex.Lock()
	scheduler.state.deploymentStartTime = time.Now().Add(-time.Second)
	scheduler.schedulerMutex.Unlock()
	scheduler.checkDeploymentTimeout(ctx)

	scheduler.schedulerMutex.Lock()
	assert.True(t, scheduler.state.deployInFlight)
	assert.True(t, scheduler.state.deploymentTimedOut)
	assert.Equal(t, deploymentA.EventID(), scheduler.state.activeDeploymentID)
	scheduler.schedulerMutex.Unlock()
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledEvents, testutil.NoEventTimeout)

	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		DeploymentID: deploymentA.EventID(), Total: 1, Failed: 1,
	}, events.WithCorrelation("shared-trace", deploymentA.EventID())))
	deploymentB := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledEvents, testutil.LongTimeout)
	require.Equal(t, "config-B", deploymentB.Config)

	scheduler.scheduleOrQueue(ctx, "config-C", nil, nil, oneEndpoint(), "C", "shared-trace", nil, false, "checksum-C")
	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		DeploymentID: deploymentA.EventID(), Total: 1, Succeeded: 1, ContentChecksum: "checksum-A",
	}, events.WithCorrelation("shared-trace", deploymentA.EventID())))

	scheduler.schedulerMutex.Lock()
	assert.True(t, scheduler.state.deployInFlight)
	assert.Equal(t, deploymentB.EventID(), scheduler.state.activeDeploymentID)
	scheduler.schedulerMutex.Unlock()
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledEvents, testutil.NoEventTimeout)

	scheduler.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		DeploymentID: deploymentB.EventID(), Total: 1, Succeeded: 1, ContentChecksum: "checksum-B",
	}, events.WithCorrelation("shared-trace", deploymentB.EventID())))
	deploymentC := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledEvents, testutil.LongTimeout)
	assert.Equal(t, "config-C", deploymentC.Config)
}

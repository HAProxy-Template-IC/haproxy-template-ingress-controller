// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func TestDeploymentCompletionRecordsItsOccurrenceNotTheLatestRender(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	deployed := mustTestOccurrence("config-A", "plan-A", nil)
	later := mustTestOccurrence("config-B", "plan-B", nil)

	scheduler.mu.Lock()
	scheduler.lastRenderedOccurrence = later
	scheduler.mu.Unlock()
	scheduler.schedulerMutex.Lock()
	scheduler.state.deployInFlight = true
	scheduler.state.activeDeploymentID = "deployment-A"
	scheduler.state.activeOccurrence = deployed
	scheduler.schedulerMutex.Unlock()

	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total: 1, Succeeded: 1, PodSetHash: "pod-set-A",
	}))

	scheduler.mu.RLock()
	recorded := scheduler.lastDeployedOccurrence
	scheduler.mu.RUnlock()
	require.True(t, sameOccurrence(deployed, recorded))
	require.False(t, sameOccurrence(later, recorded))
}

func TestDeploymentCompletionRecordsTargetedPodSet(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	oldEndpoints := []dataplane.Endpoint{{URL: "https://haproxy:5555", PodName: "haproxy-0", PodUID: "uid-old"}}
	replacement := []dataplane.Endpoint{{URL: "https://haproxy:5555", PodName: "haproxy-0", PodUID: "uid-new"}}
	oldPodSetHash := computePodSetHash(oldEndpoints)
	replacementPodSetHash := computePodSetHash(replacement)
	require.NotEqual(t, oldPodSetHash, replacementPodSetHash)

	scheduler.mu.Lock()
	scheduler.currentEndpoints = replacement
	scheduler.mu.Unlock()
	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total: 1, Succeeded: 1, PodSetHash: oldPodSetHash,
	}))

	scheduler.mu.RLock()
	recorded := scheduler.lastDeployedPodSetHash
	scheduler.mu.RUnlock()
	require.Equal(t, oldPodSetHash, recorded)
}

func TestEmptyOrFailedCompletionDoesNotReplaceLastDeployedOccurrence(t *testing.T) {
	tests := []struct {
		name   string
		result *events.DeploymentResult
	}{
		{name: "empty", result: &events.DeploymentResult{}},
		{name: "failed", result: &events.DeploymentResult{Total: 2, Succeeded: 1, Failed: 1, PodSetHash: "pods"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := testutil.NewTestBus()
			bus.Start()
			scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
			prior := mustTestOccurrence("config-good", "plan-good", nil)
			attempted := mustTestOccurrence("config-new", "plan-new", nil)
			scheduler.mu.Lock()
			scheduler.lastDeployedOccurrence = prior
			scheduler.mu.Unlock()
			scheduler.schedulerMutex.Lock()
			scheduler.state.deployInFlight = true
			scheduler.state.activeDeploymentID = "deployment-new"
			scheduler.state.activeOccurrence = attempted
			scheduler.schedulerMutex.Unlock()

			scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler, test.result))

			scheduler.mu.RLock()
			recorded := scheduler.lastDeployedOccurrence
			scheduler.mu.RUnlock()
			require.True(t, sameOccurrence(prior, recorded))
		})
	}
}

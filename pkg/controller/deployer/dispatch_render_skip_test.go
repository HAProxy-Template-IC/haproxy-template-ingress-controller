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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestDispatchRenderSkipsOnlyTheAuthenticatedDeployedOutput(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("skip", 10)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	endpoints := []dataplane.Endpoint{{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodUID: "uid-A"}}
	occurrence := mustTestOccurrence("global\n  daemon\n", "plan-stable", nil)

	scheduler.mu.Lock()
	scheduler.lastRenderedOccurrence = occurrence
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedOccurrence = occurrence
	scheduler.lastDispatchedOccurrence = occurrence
	scheduler.lastDeployedPodSetHash = computePodSetHash(endpoints)
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	scheduler.dispatchRender(t.Context(), "corr-skip", true, "config_validation")

	skipped := testutil.WaitForEvent[*events.DeploymentSkippedEvent](t, eventChan, testutil.EventTimeout)
	require.Equal(t, "config_unchanged", skipped.Reason)
	require.Equal(t, computePodSetHash(endpoints), skipped.PodSetHash)
	carried, err := skipped.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, sameOccurrence(occurrence, carried))
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComputePodSetHashIncludesCompleteEndpointAuthority(t *testing.T) {
	base := dataplane.Endpoint{
		URL: "http://10.0.0.1:5555", Username: "admin", Password: "secret",
		PodName: "pod-A", PodNamespace: "haptic", PodUID: "uid-old",
		DetectedMajorVersion: 3, DetectedMinorVersion: 2, DetectedFullVersion: "3.2.1",
	}
	tests := []struct {
		name   string
		mutate func(*dataplane.Endpoint)
	}{
		{name: "URL", mutate: func(endpoint *dataplane.Endpoint) { endpoint.URL = "http://10.0.0.2:5555" }},
		{name: "username", mutate: func(endpoint *dataplane.Endpoint) { endpoint.Username = "operator" }},
		{name: "password", mutate: func(endpoint *dataplane.Endpoint) { endpoint.Password = "rotated" }},
		{name: "pod name", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodName = "pod-B" }},
		{name: "pod namespace", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodNamespace = "other" }},
		{name: "pod UID", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodUID = "uid-new" }},
		{name: "pod runtime", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodRuntimeID = "runtime-new" }},
		{name: "major version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedMajorVersion = 4 }},
		{name: "minor version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedMinorVersion = 3 }},
		{name: "full version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedFullVersion = "3.2.1-ee1" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			require.NotEqual(t, computePodSetHash([]dataplane.Endpoint{base}), computePodSetHash([]dataplane.Endpoint{changed}))
		})
	}
}

func TestEndpointAuthorityHashIsKeyed(t *testing.T) {
	endpoint := dataplane.Endpoint{
		URL: "http://10.0.0.1:5555", Username: "admin", Password: "secret",
		PodName: "pod-A", PodNamespace: "haptic", PodUID: "uid-old",
	}
	require.NotEqual(t,
		hashEndpointAuthorityWithKey(&endpoint, []byte("first-key")),
		hashEndpointAuthorityWithKey(&endpoint, []byte("second-key")),
	)
}

func TestDispatchRenderDoesNotSkipSameURLReplacement(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("replacement", 10)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	oldEndpoint := dataplane.Endpoint{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodUID: "uid-old"}
	replacement := oldEndpoint
	replacement.PodUID = "uid-new"
	occurrence := mustTestOccurrence("global\n  daemon\n", "plan-replacement", nil)
	scheduler.mu.Lock()
	scheduler.lastRenderedOccurrence = occurrence
	scheduler.currentEndpoints = []dataplane.Endpoint{replacement}
	scheduler.lastDeployedOccurrence = occurrence
	scheduler.lastDispatchedOccurrence = occurrence
	scheduler.lastDeployedPodSetHash = computePodSetHash([]dataplane.Endpoint{oldEndpoint})
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	scheduler.dispatchRender(ctx, "corr-authority", true, "config_validation")

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	require.Equal(t, "uid-new", scheduled.Endpoints[0].PodUID)
}

func TestDispatchRenderDriftPreventionBypassesSkip(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("drift", 10)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	endpoints := []dataplane.Endpoint{{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodUID: "uid-A"}}
	occurrence := mustTestOccurrence("global\n  daemon\n", "plan-drift", nil)
	scheduler.mu.Lock()
	scheduler.lastRenderedOccurrence = occurrence
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedOccurrence = occurrence
	scheduler.lastDispatchedOccurrence = occurrence
	scheduler.lastDeployedPodSetHash = computePodSetHash(endpoints)
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	scheduler.dispatchRender(ctx, "corr-drift", false, events.TriggerReasonDriftPrevention)

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	carried, err := scheduled.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, sameOccurrence(occurrence, carried))
}

func TestTemplateAndScheduledEventsKeepStatusSnapshotInOccurrence(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("status", 10)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "gw", "gateway.networking.k8s.io/v1", "Gateway",
		map[string]map[string]any{"deployed": {"owner": "stable"}},
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	occurrence := mustTestOccurrence("global\n  daemon\n", "plan-status", snapshot)
	rendered, err := events.NewTemplateRenderedEventWithOccurrence(occurrence, 0, "test", true)
	require.NoError(t, err)
	scheduler.mu.Lock()
	scheduler.currentEndpoints = []dataplane.Endpoint{{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodUID: "uid-A"}}
	scheduler.mu.Unlock()

	scheduler.handleTemplateRendered(ctx, rendered)

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.EventTimeout)
	carried, err := scheduled.RenderOccurrence()
	require.NoError(t, err)
	require.True(t, sameOccurrence(occurrence, carried))
	identity, err := inspectOccurrence(carried)
	require.NoError(t, err)
	require.Same(t, snapshot, identity.statusPatches)
}

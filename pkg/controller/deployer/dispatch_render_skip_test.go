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

// dispatchRender has a load-bearing optimization branch that the existing
// TestDeploymentScheduler_DispatchRender does NOT cover: the canSkip path that
// suppresses redundant deployments when the rendered config and pod set both
// match the last successfully-deployed state.
//
// Two contracts pinned:
//
//  1. Skip when both content checksum AND pod-set hash match the
//     last successful deploy → NO DeploymentScheduledEvent
//     published. Without this branch every reconciliation that
//     produced unchanged output (extremely common during steady
//     state with endpoint churn that doesn't change membership)
//     would re-deploy the same config to every pod, defeating
//     the whole content-deduplication system.
//
//  2. Drift prevention bypasses the skip → DeploymentScheduledEvent
//     IS published even when the hashes match. Drift prevention
//     is the recovery path for HAProxy pods that may have drifted
//     OUT of sync with the cached state; if we skipped on hash
//     match here, drift recovery would silently never deploy and
//     the system could never self-heal from out-of-band changes.

func TestDispatchRender_SkipsWhenConfigAndPodSetUnchanged(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	const checksum = "stable-content-checksum"
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
		{URL: "http://10.0.0.2:5555", PodName: "pod-B", PodNamespace: "haptic"},
	}
	podSetHash := computePodSetHash(endpoints)

	// Set up state: prior deployment succeeded with the SAME
	// checksum and pod set, so the cache-hit branch must skip the
	// new deploy.
	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedConfigHash = checksum   // ← match
	scheduler.lastDeployedPodSetHash = podSetHash // ← match
	scheduler.lastDeployedTime = time.Now()       // ← non-zero (deployment really happened)
	scheduler.mu.Unlock()

	// Use a non-drift reason so the bypass doesn't fire.
	scheduler.dispatchRender(ctx, "corr-skip", true, "config_validation")

	// The skip branch MUST NOT publish a DeploymentScheduledEvent.
	// AssertNoEvent waits its full timeout and fails if one shows up.
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.NoEventTimeout)
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

func TestDispatchRender_DoesNotSkipSameURLReplacement(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("replacement", 10)
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	oldEndpoint := dataplane.Endpoint{
		URL: "http://10.0.0.1:5555", Username: "admin", Password: "secret",
		PodName: "pod-A", PodNamespace: "haptic", PodUID: "uid-old",
		DetectedMajorVersion: 3, DetectedMinorVersion: 2, DetectedFullVersion: "3.2.1",
	}
	replacement := oldEndpoint
	replacement.PodUID = "uid-new"
	const checksum = "stable-content-checksum"
	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = []dataplane.Endpoint{replacement}
	scheduler.lastDeployedConfigHash = checksum
	scheduler.lastDeployedPodSetHash = computePodSetHash([]dataplane.Endpoint{oldEndpoint})
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	scheduler.dispatchRender(ctx, "corr-authority", true, "config_validation")

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, eventChan, testutil.LongTimeout)
	require.Equal(t, "uid-new", scheduled.Endpoints[0].PodUID)
}

// TestDispatchRender_PublishesDeploymentSkippedOnCacheHit pins the
// contract that the skip branch ALSO publishes a DeploymentSkippedEvent so
// the status-applier can write the "deployed" status variant. Without this,
// resources whose addition produces no config change (Gateway with no
// routes attached, status-only deltas) would stay at the CRD-default
// condition state indefinitely (e.g. Programmed=Unknown / obsGen=missing,
// which the Gateway-API conformance helper reports as "generation 0").
func TestDispatchRender_PublishesDeploymentSkippedOnCacheHit(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	ctx := context.Background()
	scheduler.ctx = ctx

	const checksum = "stable-content-checksum"
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
		{URL: "http://10.0.0.2:5555", PodName: "pod-B", PodNamespace: "haptic"},
	}
	podSetHash := computePodSetHash(endpoints)

	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedConfigHash = checksum
	scheduler.lastDeployedPodSetHash = podSetHash
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	scheduler.dispatchRender(ctx, "corr-cache-hit", true, "config_validation")

	skipped := testutil.WaitForEvent[*events.DeploymentSkippedEvent](
		t, eventChan, testutil.EventTimeout)
	require.NotNil(t, skipped, "skip branch must publish DeploymentSkippedEvent")
	require.Equal(t, len(endpoints), skipped.Total,
		"Total should reflect the endpoint count, mirroring DeploymentCompletedEvent.Total")
	require.Equal(t, "config_unchanged", skipped.Reason)
	require.Equal(t, checksum, skipped.ConfigHash)
	require.Equal(t, podSetHash, skipped.PodSetHash)
}

func TestDispatchRender_DriftPreventionBypassesSkip(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	// Drive the deploy loop: scheduleOrQueue now only sets pending + signals the
	// loop, which is the goroutine that publishes DeploymentScheduledEvent.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	const checksum = "stable-content-checksum"
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
	}
	podSetHash := computePodSetHash(endpoints)

	// Same setup as the skip test — hashes deliberately match.
	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = checksum
	scheduler.currentEndpoints = endpoints
	scheduler.lastDeployedConfigHash = checksum
	scheduler.lastDeployedPodSetHash = podSetHash
	scheduler.lastDeployedTime = time.Now()
	scheduler.mu.Unlock()

	// CRITICAL difference: the reason is drift_prevention. The skip branch
	// MUST be bypassed even though hashes match. Drift is non-coalescible.
	scheduler.dispatchRender(ctx, "corr-drift", false, events.TriggerReasonDriftPrevention)

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.LongTimeout)
	require.NotNil(t, scheduled,
		"drift prevention MUST bypass the canSkip cache hit — the recovery "+
			"path needs to actually deploy in case HAProxy pods have drifted "+
			"OUT of sync with the cached state. A regression that respected "+
			"the cache here would silently break drift recovery and the "+
			"system could never self-heal from out-of-band changes")
}

// TestHandleTemplateRendered_CachesStatusPatches pins the cache step that
// the rest of this file's skip-branch test, the pod-discovery path, and
// the validation-fallback path all rely on: TemplateRenderedEvent's
// StatusPatches must be stored on the scheduler so a later
// scheduleOrQueue / DeploymentSkippedEvent can carry them. Regression
// fuse for the "patches travel on deploy events" architecture — if this
// caching breaks, every downstream deploy/skip event emits zero patches
// and the StatusApplier silently stops writing the "deployed" variant.
func TestHandleTemplateRendered_CachesStatusPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	patches := []templating.StatusPatch{
		{Name: "gw", Kind: "Gateway"},
		{Name: "route", Kind: "HTTPRoute"},
	}
	event := events.NewTemplateRenderedEvent(
		"haproxy config",
		&dataplane.AuxiliaryFiles{},
		patches,
		nil, 0, 50, "test", "checksum", nil, "", true,
	)

	scheduler.handleTemplateRendered(t.Context(), event)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	require.Equal(t, 2, len(scheduler.lastValidatedStatusPatches))
	require.Equal(t, "gw", scheduler.lastValidatedStatusPatches[0].Name)
}

// TestDispatchRender_DeploymentScheduledCarriesStatusPatches pins
// the end-to-end carry from TemplateRenderedEvent → cached lastValidatedStatusPatches
// → DeploymentScheduledEvent on the happy path (config changed, no skip).
// Companion to the skip-path test above; together they cover both event
// types the scheduler emits.
func TestDispatchRender_DeploymentScheduledCarriesStatusPatches(t *testing.T) {
	bus := testutil.NewTestBus()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	// Drive the deploy loop: scheduleOrQueue now only sets pending + signals the
	// loop, which is the goroutine that publishes DeploymentScheduledEvent.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLoopForTest(t, scheduler, ctx)

	patches := []templating.StatusPatch{
		{Name: "gw", Kind: "Gateway"},
	}
	endpoints := []dataplane.Endpoint{
		{URL: "http://10.0.0.1:5555", PodName: "pod-A", PodNamespace: "haptic"},
	}

	scheduler.mu.Lock()
	scheduler.lastRenderedConfig = "global\n  daemon\n"
	scheduler.lastAuxiliaryFiles = &dataplane.AuxiliaryFiles{}
	scheduler.lastContentChecksum = "new-checksum" // differs from lastDeployedConfigHash
	scheduler.lastRenderedStatusPatches = patches
	scheduler.currentEndpoints = endpoints
	// lastDeployedTime zero → canSkip predicate is false → real deploy path.
	scheduler.mu.Unlock()

	scheduler.dispatchRender(ctx, "corr-patches", true, "config_validation")

	scheduled := testutil.WaitForEvent[*events.DeploymentScheduledEvent](
		t, eventChan, testutil.EventTimeout)
	require.NotNil(t, scheduled, "deploy path must publish DeploymentScheduledEvent")
	require.Equal(t, 1, len(scheduled.StatusPatches),
		"DeploymentScheduledEvent must carry the cached StatusPatches so "+
			"the Deployer can forward them into DeploymentCompletedEvent")
	require.Equal(t, "gw", scheduled.StatusPatches[0].Name)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	require.Equal(t, patches, scheduler.lastValidatedStatusPatches,
		"dispatch promotes the render's patches with the config they belong to")
}

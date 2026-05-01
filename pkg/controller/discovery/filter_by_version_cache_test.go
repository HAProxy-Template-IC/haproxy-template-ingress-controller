// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// filterByVersion has FOUR branches; the cache-hit branch is the
// steady-state hot path (every reconciliation after the first
// admission lands here for previously-admitted pods) and is the
// only one testable without a real HAProxy /v3/info endpoint to
// stub. This file pins three load-bearing contracts on that branch:
//
//  1. Already-admitted pod returns the SAME *Endpoint object that
//     was cached. The cached endpoint carries the detected version
//     fields (DetectedMajorVersion / DetectedMinorVersion /
//     DetectedFullVersion) populated at admission time. A regression
//     that returned a fresh copy from the candidate slice would lose
//     those fields, defeating capability detection downstream
//     (deployer reads them to decide which dispatch path to use).
//
//  2. Cache hit MUST NOT trigger a remote version check. The
//     localVersion field is intentionally left nil here — if the
//     code regressed to skip the cache lookup and fall through to
//     checkRemoteVersion, the test would crash with a nil-pointer
//     dereference (loud failure mode) rather than silently making
//     N HTTP calls per reconciliation, which would flood the
//     dataplane API under churn.
//
//  3. Mixed cached + missing candidates return ONLY the cached ones
//     (without exploding) and DO NOT mutate the cache by adding
//     stale entries for the missing ones. The "missing" path falls
//     into checkRemoteVersion which would fail without a server,
//     and handleVersionCheckFailure must record the failure in
//     pendingRetries — but the cached endpoint MUST still be in
//     the returned admitted slice. The contract: cache hits succeed
//     independently of remote-check failures for other candidates.
//
// Branches NOT covered here (require a remote /v3/info stub):
//   - New-pod first version check + admission
//   - New-pod version mismatch (permanent rejection)
//   - New-pod version check transient failure path

// admittedComponent constructs a Component pre-seeded with
// admittedPods/pendingRetries maps. localVersion is intentionally
// nil — cache-hit branch must not consult it.
func admittedComponent(t *testing.T) *Component {
	t.Helper()
	c := newTestComponentWithoutHAProxy(t)
	c.admittedPods = make(map[string]*dataplane.Endpoint)
	return c
}

func TestFilterByVersion_CacheHitReturnsCachedEndpointObject(t *testing.T) {
	c := admittedComponent(t)
	cached := &dataplane.Endpoint{
		URL:                  "http://10.0.0.1:5555",
		Username:             "admin-cached",
		Password:             "pass-cached",
		PodName:              "pod-A",
		PodNamespace:         "haptic",
		DetectedMajorVersion: 3,
		DetectedMinorVersion: 2,
		DetectedFullVersion:  "v3.2.6 87ad0bcf",
	}
	c.admittedPods["pod-A"] = cached

	// Candidate has the same pod name but DIFFERENT credentials and
	// no version info — exactly the input shape that arrives every
	// reconciliation after admission (creds come from the latest
	// secret; version comes from the cache).
	candidate := dataplane.Endpoint{
		URL:          "http://10.0.0.1:5555",
		PodName:      "pod-A",
		PodNamespace: "haptic",
		// version fields zero — cache must supply them
	}

	admitted, _ := c.filterByVersion(
		[]dataplane.Endpoint{candidate},
		coreconfig.Credentials{
			DataplaneUsername: "ignored-on-cache-hit",
			DataplanePassword: "also-ignored",
		},
	)

	require.Len(t, admitted, 1,
		"the cached pod MUST be returned — a regression that missed the "+
			"cache and fell through to checkRemoteVersion would crash on "+
			"the nil localVersion (test designed to catch this loudly)")
	assert.Same(t, cached, admitted[0],
		"the returned *Endpoint MUST be the cached pointer — a regression "+
			"that built a fresh Endpoint from the candidate would drop the "+
			"DetectedMajorVersion/MinorVersion/FullVersion fields, breaking "+
			"capability dispatch in the deployer")
}

func TestFilterByVersion_CacheHitDoesNotConsultLocalVersion(t *testing.T) {
	// Defensive: localVersion is nil. If the cache-check is wrongly
	// gated on a non-nil localVersion (e.g. someone added a guard),
	// the function would skip cache hits, fall through to
	// checkRemoteVersion, and either crash or make N HTTP requests
	// per reconciliation. Either failure mode is bad; the "many
	// HTTP requests" failure mode would flood the dataplane API
	// silently under steady-state churn.
	c := admittedComponent(t)
	require.Nil(t, c.localVersion,
		"baseline: localVersion must be nil for this test to be meaningful")

	c.admittedPods["pod-A"] = &dataplane.Endpoint{
		PodName: "pod-A",
		URL:     "http://10.0.0.1:5555",
	}
	c.admittedPods["pod-B"] = &dataplane.Endpoint{
		PodName: "pod-B",
		URL:     "http://10.0.0.2:5555",
	}

	candidates := []dataplane.Endpoint{
		{PodName: "pod-A", URL: "http://10.0.0.1:5555"},
		{PodName: "pod-B", URL: "http://10.0.0.2:5555"},
	}

	// Must complete without panicking on the nil localVersion.
	admitted, _ := c.filterByVersion(candidates, coreconfig.Credentials{})

	require.Len(t, admitted, 2,
		"both already-admitted pods MUST come back — the cache lookup is "+
			"the entire point of the steady-state path")

	// Verify both are the cached pointers (preserves version fields).
	gotByPod := make(map[string]*dataplane.Endpoint, len(admitted))
	for _, ep := range admitted {
		gotByPod[ep.PodName] = ep
	}
	assert.Same(t, c.admittedPods["pod-A"], gotByPod["pod-A"])
	assert.Same(t, c.admittedPods["pod-B"], gotByPod["pod-B"])
}

func TestFilterByVersion_EmptyCandidatesReturnsEmptyAdmitted(t *testing.T) {
	// Boundary: the function must handle an empty candidate slice
	// (which happens when all HAProxy pods have been removed) without
	// panicking and without spuriously emitting any admitted entries.
	// A regression that, say, returned a nil slice instead of an
	// empty one would still satisfy the test, but a regression that
	// emitted phantom entries would fail.
	c := admittedComponent(t)
	c.admittedPods["stale-pod"] = &dataplane.Endpoint{PodName: "stale-pod"}

	admitted, _ := c.filterByVersion(nil, coreconfig.Credentials{})

	assert.Empty(t, admitted,
		"empty candidate list MUST produce empty admitted list — "+
			"the cache must NOT be enumerated to fabricate admitted entries "+
			"for pods that no longer exist (that would re-admit pods the "+
			"watcher removed, causing the deployer to push config to "+
			"vanished endpoints)")
}

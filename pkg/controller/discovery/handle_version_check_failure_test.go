// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// handleVersionCheckFailure has THREE load-bearing state-management
// contracts that aren't directly tested:
//
//  1. New-pod first failure must ADD an entry to pendingRetries
//     with retryCount=1. A regression that left the map untouched
//     (or used = instead of ++) would silently drop pods from retry
//     tracking — they'd never be retried and never get admitted to
//     the deployment set.
//
//  2. Existing-pod failure must INCREMENT retryCount, not reset it.
//     The exponential-backoff schedule depends on the count growing
//     monotonically; a regression that reset to 1 each call would
//     lock retries at the initial 5s interval forever and hammer
//     unhealthy pods.
//
//  3. Every failure must update lastAttempt to time.Now(). The retry
//     timer uses lastAttempt+interval to decide WHEN to retry. A
//     regression that left lastAttempt stale would either fire
//     retries immediately (causing a busy loop) or never fire them
//     at all.
//
// These tests work directly on the Component struct, bypassing the
// haproxy-binary requirement that gates the integration-style tests
// in component_test.go (no version detection or retry timer needed
// for state-management testing).

// newTestComponentWithoutHAProxy builds a Component instance with
// just the fields handleVersionCheckFailure touches (plus the embedded
// component.Base the handlers log through). This avoids the New()
// constructor's local HAProxy version detection so the test runs in
// environments without the haproxy binary.
func newTestComponentWithoutHAProxy(t *testing.T) *Component {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	c := &Component{
		admissionProofs:   make(map[endpointIdentity]versionAdmissionProof),
		versionRejections: make(map[endpointIdentity]string),
		pendingRetries:    make(map[endpointIdentity]*retryState),
	}
	c.Base = component.New(&component.Config{
		EventBus:   bus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: 1,
		Handler:    c,
	})
	return c
}

func testEndpointIdentity(podName string) endpointIdentity {
	return endpointIdentity{podNamespace: "default", podName: podName, podUID: podName + "-uid", url: "http://127.0.0.1:5555/v3"}
}

func TestHandleVersionCheckFailure_NewPodAddsEntryWithCountOne(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	require.Empty(t, c.pendingRetries,
		"baseline: pendingRetries must start empty for this test to be meaningful")

	before := time.Now()
	identity := testEndpointIdentity("new-pod")
	c.handleVersionCheckFailure(&identity, errors.New("connect: connection refused"))
	after := time.Now()

	require.Len(t, c.pendingRetries, 1,
		"first failure for a new pod MUST add one entry to pendingRetries — "+
			"a regression that left the map untouched would silently drop the "+
			"pod from retry tracking, so it never gets retried and never reaches "+
			"the deployment set")

	retry, ok := c.pendingRetries[identity]
	require.True(t, ok, "the new entry must be keyed by the endpoint identity")
	assert.Equal(t, 1, retry.retryCount,
		"retryCount must be 1 after the first failure — a regression that "+
			"started at 0 (forgetting to ++) would skew every backoff calculation")
	// lastAttempt must be set to roughly time.Now() — bracket the
	// observed window with the before/after timestamps captured around
	// the call.
	assert.False(t, retry.lastAttempt.Before(before),
		"lastAttempt must be >= the time captured BEFORE the call — "+
			"otherwise the retry timer would fire too early")
	assert.False(t, retry.lastAttempt.After(after),
		"lastAttempt must be <= the time captured AFTER the call — "+
			"otherwise the retry timer would never fire")
}

func TestHandleVersionCheckFailure_ExistingPodIncrementsCount(t *testing.T) {
	// Pre-populate with a pod that's already failed twice. A subsequent
	// failure must increment to 3, NOT reset to 1.
	c := newTestComponentWithoutHAProxy(t)
	originalAttempt := time.Now().Add(-10 * time.Minute) // stale
	identity := testEndpointIdentity("existing-pod")
	c.pendingRetries[identity] = &retryState{
		retryCount:  2,
		lastAttempt: originalAttempt,
	}

	c.handleVersionCheckFailure(&identity, errors.New("timeout"))

	retry := c.pendingRetries[identity]
	assert.Equal(t, 3, retry.retryCount,
		"existing-pod retryCount MUST INCREMENT from 2 to 3 — "+
			"a regression that reset the count would lock the exponential-"+
			"backoff schedule at the initial 5s interval forever and hammer "+
			"unhealthy pods")
	assert.True(t, retry.lastAttempt.After(originalAttempt),
		"lastAttempt MUST be refreshed on every failure — a regression that "+
			"left the original timestamp would either fire the retry timer "+
			"immediately (busy loop) or never fire it at all")
}

func TestHandleVersionCheckFailure_MultipleConsecutiveFailuresStackMonotonically(t *testing.T) {
	// Loop-style failure pattern: simulate three failures in a row for
	// the same new pod. The retryCount must reach 3, not stay at 1
	// (the bug shape from the previous test) and not skip values.
	c := newTestComponentWithoutHAProxy(t)
	identity := testEndpointIdentity("flaky-pod")

	for i := 1; i <= 3; i++ {
		c.handleVersionCheckFailure(&identity, errors.New("transient"))
		retry := c.pendingRetries[identity]
		assert.Equal(t, i, retry.retryCount,
			"after %d failures, retryCount MUST be %d (monotonic increment) — "+
				"a regression that double-counted, skipped, or reset would break "+
				"the documented exponential backoff schedule",
			i, i)
	}

	// A retry sequence for one endpoint identity shares one entry.
	require.Len(t, c.pendingRetries, 1,
		"multiple failures for the same endpoint identity MUST share one entry — "+
			"a regression that created a new entry per failure would leak "+
			"map memory unboundedly during a flaky-pod outage")
}

func TestHandleVersionCheckFailure_DistinctPodsGetSeparateEntries(t *testing.T) {
	// Two different pods failing must produce two independent entries
	// — never collapse into one. A regression that miskeyed the map
	// (e.g. used a constant string) would let one pod's failures
	// silently increment another pod's backoff.
	c := newTestComponentWithoutHAProxy(t)
	podA := testEndpointIdentity("pod-a")
	podB := testEndpointIdentity("pod-b")
	c.handleVersionCheckFailure(&podA, errors.New("err-a"))
	c.handleVersionCheckFailure(&podB, errors.New("err-b"))
	c.handleVersionCheckFailure(&podA, errors.New("err-a-again"))

	require.Len(t, c.pendingRetries, 2,
		"distinct pods MUST produce distinct entries — pinning this catches "+
			"a regression that miskeyed the map and silently merged unrelated "+
			"pods' retry state")
	assert.Equal(t, 2, c.pendingRetries[podA].retryCount,
		"pod-a saw two failures so its retryCount must be 2")
	assert.Equal(t, 1, c.pendingRetries[podB].retryCount,
		"pod-b saw one failure so its retryCount must be 1 — independent "+
			"of pod-a's count")
}

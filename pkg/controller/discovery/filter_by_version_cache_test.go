// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func infoServerWithProbeCount(t *testing.T, apiVersion string, probes *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v3/info":
			probes.Add(1)
			_, _ = w.Write([]byte(`{"api":{"version":"` + apiVersion + `"}}`))
		case "/v3/services/haproxy/runtime/info":
			_, _ = w.Write([]byte(`{"info":{"version":"3.4.2"}}`))
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRetryTimerKeepsEarlierArmedDeadline(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	defer c.stopRetryTimer()
	c.pendingRetries[testEndpointIdentity("haproxy-0")] = &retryState{
		lastAttempt: time.Now().Add(-initialRetryInterval),
		retryCount:  1,
	}

	c.mu.Lock()
	c.scheduleRetryTimerLocked()
	c.mu.Unlock()
	c.retryTimerMu.Lock()
	firstTimer := c.retryTimer
	firstDeadline := c.retryTimerAt
	c.retryTimerMu.Unlock()
	require.NotNil(t, firstTimer)

	c.mu.Lock()
	c.scheduleRetryTimerLocked()
	c.mu.Unlock()
	c.retryTimerMu.Lock()
	defer c.retryTimerMu.Unlock()
	assert.Same(t, firstTimer, c.retryTimer)
	assert.Equal(t, firstDeadline, c.retryTimerAt)
}

func TestFilterByVersion_CacheHitUsesFreshConnectionAuthority(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	candidate := dataplane.Endpoint{
		URL:          "http://10.0.0.1:5555/v3",
		Username:     "rotated-user",
		Password:     "rotated-password",
		PodName:      "haproxy-0",
		PodNamespace: "haptic",
		PodUID:       "uid-1",
	}
	c.admissionProofs[endpointIdentityOf(&candidate)] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.5 8467a253"},
		haproxy:      dataplane.Version{Major: 3, Minor: 4, Full: "3.4.2"},
	}

	admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})

	require.Empty(t, rejections)
	require.Len(t, admitted, 1)
	assert.Equal(t, candidate.URL, admitted[0].URL)
	assert.Equal(t, candidate.Username, admitted[0].Username)
	assert.Equal(t, candidate.Password, admitted[0].Password)
	assert.Equal(t, candidate.PodUID, admitted[0].PodUID)
	assert.Equal(t, 3, admitted[0].DetectedMajorVersion)
	assert.Equal(t, 3, admitted[0].DetectedMinorVersion)
	assert.Equal(t, "v3.3.5 8467a253", admitted[0].DetectedFullVersion)
}

func TestFilterByVersion_EndpointIdentityControlsVersionProof(t *testing.T) {
	var firstServerProbes atomic.Int32
	firstServer := infoServerWithProbeCount(t, "v3.3.5 first", &firstServerProbes)
	var secondServerProbes atomic.Int32
	secondServer := infoServerWithProbeCount(t, "v3.3.6 second", &secondServerProbes)

	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}
	base := dataplane.Endpoint{
		URL:          firstServer.URL,
		Username:     "initial-user",
		Password:     "initial-password",
		PodName:      "haproxy-0",
		PodNamespace: "haptic",
		PodUID:       "uid-1",
		PodRuntimeID: "runtime-1",
	}

	admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{base})
	require.Empty(t, rejections)
	require.Len(t, admitted, 1)
	assert.Equal(t, int32(1), firstServerProbes.Load())
	proof := c.admissionProofs[endpointIdentityOf(&base)]
	assert.Equal(t, "v3.3.5 first", proof.dataPlaneAPI.Full)
	assert.Equal(t, "3.4.2", proof.haproxy.Full)

	credentialRotation := base
	credentialRotation.Username = "rotated-user"
	credentialRotation.Password = "rotated-password"
	admitted, rejections = c.filterByVersion(t.Context(), []dataplane.Endpoint{credentialRotation})
	require.Empty(t, rejections)
	require.Len(t, admitted, 1)
	assert.Equal(t, int32(1), firstServerProbes.Load(), "credential changes reuse the version proof")
	assert.Equal(t, "rotated-user", admitted[0].Username)
	assert.Equal(t, "rotated-password", admitted[0].Password)

	imageChanged := credentialRotation
	imageChanged.PodRuntimeID = "runtime-2"
	_, rejections = c.filterByVersion(t.Context(), []dataplane.Endpoint{imageChanged})
	require.Empty(t, rejections)
	assert.Equal(t, int32(2), firstServerProbes.Load(), "a new container runtime epoch requires a new probe")

	replacement := imageChanged
	replacement.PodUID = "uid-2"
	_, rejections = c.filterByVersion(t.Context(), []dataplane.Endpoint{replacement})
	require.Empty(t, rejections)
	assert.Equal(t, int32(3), firstServerProbes.Load(), "a new pod UID requires a new probe")

	moved := replacement
	moved.URL = secondServer.URL
	admitted, rejections = c.filterByVersion(t.Context(), []dataplane.Endpoint{moved})
	require.Empty(t, rejections)
	require.Len(t, admitted, 1)
	assert.Equal(t, int32(1), secondServerProbes.Load(), "a new endpoint URL requires a new probe")
	assert.Equal(t, "v3.3.6 second", admitted[0].DetectedFullVersion)
}

func TestFilterByVersion_PermanentMismatchIsNotReprobed(t *testing.T) {
	var probes atomic.Int32
	server := infoServerWithProbeCount(t, "v4.0.0", &probes)
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}
	candidate := dataplane.Endpoint{
		URL:          server.URL,
		Username:     "admin",
		Password:     "password",
		PodName:      "haproxy-0",
		PodNamespace: "haptic",
		PodUID:       "uid-1",
	}
	identity := endpointIdentityOf(&candidate)
	c.pendingRetries[identity] = &retryState{
		lastAttempt: time.Now().Add(-initialRetryInterval),
		retryCount:  1,
	}

	for range 2 {
		admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})
		assert.Empty(t, admitted)
		require.Len(t, rejections, 1)
		assert.Equal(t, "version_mismatch_newer", rejections[0].reason)
	}

	assert.Equal(t, int32(1), probes.Load())
	assert.Empty(t, c.pendingRetries)
}

func TestFilterByVersion_PendingProbeWaitsForItsBackoff(t *testing.T) {
	var probes atomic.Int32
	server := infoServerWithProbeCount(t, "v3.3.5", &probes)
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}
	candidate := dataplane.Endpoint{
		URL:          server.URL,
		Username:     "admin",
		Password:     "password",
		PodName:      "haproxy-0",
		PodNamespace: "haptic",
		PodUID:       "uid-1",
	}
	identity := endpointIdentityOf(&candidate)
	c.pendingRetries[identity] = &retryState{lastAttempt: time.Now(), retryCount: 1}

	admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})
	assert.Empty(t, admitted)
	assert.Empty(t, rejections)
	assert.Zero(t, probes.Load())

	c.pendingRetries[identity].lastAttempt = time.Now().Add(-initialRetryInterval)
	admitted, rejections = c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})
	require.Len(t, admitted, 1)
	assert.Empty(t, rejections)
	assert.Equal(t, int32(1), probes.Load())
	assert.NotContains(t, c.pendingRetries, identity)
}

func TestFilterByVersion_EmptyCandidatesReturnsEmptyAdmitted(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.admissionProofs[testEndpointIdentity("stale-pod")] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3},
	}

	admitted, rejections := c.filterByVersion(t.Context(), nil)

	assert.Empty(t, admitted)
	assert.Empty(t, rejections)
}

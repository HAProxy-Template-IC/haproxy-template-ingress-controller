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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// infoServer returns an httptest server whose /v3/info reports the given
// DataPlane API version string (the shape client.DetectVersion parses).
// httptest binds to loopback, so no Windows Firewall prompt is triggered.
func infoServer(t *testing.T, apiVersion string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api":{"version":"` + apiVersion + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A controller built against HAProxy 3.4 (localVersion 3.4.x) MUST admit a pod
// whose DataPlane API reports v3.3: the HAProxy 3.4 image ships DataPlane API
// v3.3, so the binary version (3.4) and the DataPlane API version (3.3) diverge
// at the minor level. Discovery gates on the MAJOR version only — a strict
// major.minor match deadlocked a correctly-paired 3.4 fleet (every pod rejected
// → no endpoints → nothing ever deployed). Regression guard for the
// binary/DataPlane-API version decoupling.
func TestFilterByVersion_Admits34BinaryAgainst33DataPlaneAPI(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.admittedPods = make(map[string]*dataplane.Endpoint)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0-64a335366"}

	srv := infoServer(t, "v3.3.5 8467a253")
	candidate := dataplane.Endpoint{URL: srv.URL, PodName: "haproxy-0", PodNamespace: "haptic"}

	admitted, rejections := c.filterByVersion(
		t.Context(),
		[]dataplane.Endpoint{candidate},
		coreconfig.Credentials{DataplaneUsername: "admin", DataplanePassword: "pw"},
	)

	require.Empty(t, rejections, "a v3.3 DataPlane API pod must not be rejected by a 3.4 controller")
	require.Len(t, admitted, 1)
	assert.Equal(t, 3, admitted[0].DetectedMajorVersion)
	assert.Equal(t, 3, admitted[0].DetectedMinorVersion,
		"records the detected DataPlane API minor (3), not the controller binary's (4)")
}

// A genuinely unsupported MAJOR (v2) is still permanently rejected — the gate
// loosened to major-only, not removed.
func TestFilterByVersion_RejectsUnsupportedMajor(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.admittedPods = make(map[string]*dataplane.Endpoint)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}

	srv := infoServer(t, "v2.9.0 deadbeef")
	candidate := dataplane.Endpoint{URL: srv.URL, PodName: "haproxy-legacy", PodNamespace: "haptic"}

	admitted, rejections := c.filterByVersion(
		t.Context(),
		[]dataplane.Endpoint{candidate},
		coreconfig.Credentials{DataplaneUsername: "admin", DataplanePassword: "pw"},
	)

	require.Empty(t, admitted, "a v2 DataPlane API pod is unsupported and must be rejected")
	require.Len(t, rejections, 1)
	assert.Equal(t, "version_mismatch_older", rejections[0].reason)
}

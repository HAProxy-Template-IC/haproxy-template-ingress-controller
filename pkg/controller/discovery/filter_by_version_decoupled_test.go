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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func infoServer(t *testing.T, apiVersion, haproxyVersion string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/info":
			_, _ = w.Write([]byte(`{"api":{"version":"` + apiVersion + `"}}`))
		case "/v3/services/haproxy/runtime/info":
			_, _ = w.Write([]byte(`{"info":{"version":"` + haproxyVersion + `"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// DataPlane API and HAProxy minor versions are independent compatibility axes.
func TestFilterByVersion_Admits34BinaryAgainst33DataPlaneAPI(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0-64a335366"}

	srv := infoServer(t, "v3.3.5 8467a253", "3.4.2-1a2b3c4d")
	candidate := dataplane.Endpoint{URL: srv.URL, Username: "admin", Password: "pw", PodName: "haproxy-0", PodNamespace: "haptic"}

	admitted, rejections := c.filterByVersion(
		t.Context(),
		[]dataplane.Endpoint{candidate},
	)

	require.Empty(t, rejections, "a v3.3 DataPlane API pod must not be rejected by a 3.4 controller")
	require.Len(t, admitted, 1)
	assert.Equal(t, 3, admitted[0].DetectedMajorVersion)
	assert.Equal(t, 3, admitted[0].DetectedMinorVersion,
		"records the detected DataPlane API minor (3), not the controller binary's (4)")
}

func TestFilterByVersion_RejectsUnsupportedMajor(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}

	srv := infoServer(t, "v2.9.0 deadbeef", "")
	candidate := dataplane.Endpoint{URL: srv.URL, Username: "admin", Password: "pw", PodName: "haproxy-legacy", PodNamespace: "haptic"}

	admitted, rejections := c.filterByVersion(
		t.Context(),
		[]dataplane.Endpoint{candidate},
	)

	require.Empty(t, admitted, "a v2 DataPlane API pod is unsupported and must be rejected")
	require.Len(t, rejections, 1)
	assert.Equal(t, "version_mismatch_older", rejections[0].reason)
}

func TestFilterByVersion_DataPlaneAPISupportIsIndependentOfLocalHAProxyMajor(t *testing.T) {
	t.Run("supported API admits matching future HAProxy series", func(t *testing.T) {
		c := newTestComponentWithoutHAProxy(t)
		c.localVersion = &dataplane.Version{Major: 4, Minor: 0, Full: "4.0.0"}

		srv := infoServer(t, "v3.3.5", "4.0.1")
		candidate := dataplane.Endpoint{
			URL: srv.URL, Username: "admin", Password: "pw",
			PodName: "haproxy-future", PodNamespace: "haptic", PodUID: "uid-1",
		}

		admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})

		require.Empty(t, rejections)
		require.Len(t, admitted, 1)
		assert.Equal(t, 3, admitted[0].DetectedMajorVersion)
	})

	t.Run("unsupported API is rejected even when it matches HAProxy major", func(t *testing.T) {
		c := newTestComponentWithoutHAProxy(t)
		c.localVersion = &dataplane.Version{Major: 4, Minor: 0, Full: "4.0.0"}

		srv := infoServer(t, "v4.0.1", "")
		candidate := dataplane.Endpoint{
			URL: srv.URL, Username: "admin", Password: "pw",
			PodName: "unsupported-api", PodNamespace: "haptic", PodUID: "uid-1",
		}

		admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})

		require.Empty(t, admitted)
		require.Len(t, rejections, 1)
		assert.Equal(t, "version_mismatch_newer", rejections[0].reason)
	})
}

func TestFilterByVersion_RejectsMismatchedHAProxySeries(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 4, Full: "3.4.0"}

	srv := infoServer(t, "v3.3.5 8467a253", "3.2.10")
	candidate := dataplane.Endpoint{
		URL: srv.URL, Username: "admin", Password: "pw",
		PodName: "haproxy-legacy", PodNamespace: "haptic", PodUID: "uid-1",
	}

	admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})

	require.Empty(t, admitted)
	require.Len(t, rejections, 1)
	assert.Equal(t, "version_mismatch_older", rejections[0].reason)
}

func TestFilterByVersion_AdmitsEnterpriseRuntimeRevision(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	c.localVersion = &dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"}

	srv := infoServer(t, "v3.2.15-ee1", "3.2r1")
	candidate := dataplane.Endpoint{
		URL: srv.URL, Username: "admin", Password: "pw",
		PodName: "haproxy-enterprise", PodNamespace: "haptic", PodUID: "uid-1",
	}

	admitted, rejections := c.filterByVersion(t.Context(), []dataplane.Endpoint{candidate})

	require.Empty(t, rejections)
	require.Len(t, admitted, 1)
	assert.Equal(t, "v3.2.15-ee1", admitted[0].DetectedFullVersion)
}

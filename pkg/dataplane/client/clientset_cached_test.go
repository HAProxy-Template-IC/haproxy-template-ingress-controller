// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewClientset has three branches the existing TestNewClientset doesn't
// cover, each with its own silent failure mode:
//
//  1. Cached version short-circuit (HasCachedVersion=true): the cache
//     was added specifically to AVOID a /v3/info HTTP round-trip on
//     every clientset construction. Discovery populates the cache
//     once per pod and reuses it. A regression that ignored the cache
//     would silently re-add an HTTP round-trip per clientset call,
//     visible only as latency degradation under load.
//
//  2. Unsupported major version: the function MUST reject anything
//     other than v3 with a clear error message naming the rejected
//     version. A regression that wrongly accepted v2 or v4 would
//     construct a clientset against an incompatible API and fail
//     much later with confusing errors from the underlying generated
//     clients.
//
//  3. ParseVersion failure fallback: a server returning a
//     non-parseable version string (e.g. dev builds with weird
//     formats) must NOT fail clientset construction outright — the
//     function falls back to v3.0 with a logged warning. A regression
//     that errored out instead would prevent the controller from
//     working with any HAProxy build whose version string the parser
//     doesn't recognize.

func TestNewClientset_CachedVersionSkipsHTTPCall(t *testing.T) {
	// Track how many requests reach the server. With a populated
	// cache, the count must stay ZERO — no /v3/info call.
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoint := &Endpoint{
		URL:      server.URL,
		Username: "admin",
		Password: "password",
		// Cache populated — must short-circuit version detection.
		CachedMajorVersion: 3,
		CachedMinorVersion: 2,
		CachedFullVersion:  "v3.2.6 cached-by-discovery",
		CachedIsEnterprise: false,
	}

	clientset, err := NewClientset(context.Background(), endpoint, nil)
	require.NoError(t, err)
	require.NotNil(t, clientset)

	// (1) No HTTP request was made — the cache supplied everything.
	assert.Equal(t, int32(0), atomic.LoadInt32(&requestCount),
		"populated cache MUST short-circuit the /v3/info call; a regression "+
			"that ignored the cache would silently re-add an HTTP round-trip per "+
			"clientset construction (visible only as latency degradation under load)")

	// (2) The cached values flow through to the constructed clientset.
	assert.Equal(t, 3, clientset.MajorVersion(),
		"cached major version must be honored")
	assert.Equal(t, 2, clientset.MinorVersion(),
		"cached minor version must be honored")
	assert.Equal(t, "v3.2.6 cached-by-discovery", clientset.DetectedVersion(),
		"cached full version string must propagate (used in observability/log fields)")

	// (3) Capabilities are derived from the cached major+minor — a
	// regression that built capabilities from a default major=0/minor=0
	// would silently disable every feature flag.
	assert.True(t, clientset.Capabilities().SupportsCrtList,
		"v3.2 capabilities must be derived from the cached version, not from "+
			"a zero-default; a regression that silently disabled every feature "+
			"flag would manifest as 'feature not supported' errors much later")
}

func TestNewClientset_CachedVersionPreservesEnterpriseEdition(t *testing.T) {
	endpoint := &Endpoint{
		URL:                "http://does-not-need-to-exist",
		Username:           "admin",
		Password:           "password",
		CachedMajorVersion: 3,
		CachedMinorVersion: 2,
		CachedFullVersion:  "v3.2.6-ee1",
	}

	clientset, err := NewClientset(t.Context(), endpoint, nil)
	require.NoError(t, err)
	assert.True(t, clientset.IsEnterprise())
	assert.True(t, clientset.Capabilities().SupportsWAF)
}

func TestNewClientset_UnsupportedMajorVersionIsRejected(t *testing.T) {
	tests := []struct {
		name          string
		cachedMajor   int
		cachedMinor   int
		wantErrSubstr []string
	}{
		{
			name:          "v2 is rejected",
			cachedMajor:   2,
			cachedMinor:   8,
			wantErrSubstr: []string{"unsupported", "2"},
		},
		{
			name:          "v4 is rejected",
			cachedMajor:   4,
			cachedMinor:   0,
			wantErrSubstr: []string{"unsupported", "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use cache to avoid mocking the HTTP server — we're
			// testing the major-version validation, not detection.
			endpoint := &Endpoint{
				URL:                "http://does-not-need-to-exist",
				Username:           "admin",
				Password:           "password",
				CachedMajorVersion: tt.cachedMajor,
				CachedMinorVersion: tt.cachedMinor,
				CachedFullVersion:  "vX.Y",
			}

			clientset, err := NewClientset(context.Background(), endpoint, nil)

			require.Error(t, err,
				"only v3.x is supported; a regression that wrongly accepted "+
					"v%d would construct a clientset against an incompatible API "+
					"and fail much later with confusing generated-client errors",
				tt.cachedMajor)
			assert.Nil(t, clientset, "no clientset must be returned on error")
			for _, substr := range tt.wantErrSubstr {
				assert.Contains(t, err.Error(), substr,
					"error must name the rejected version so the operator knows "+
						"which DataPlane API version is incompatible")
			}
		})
	}
}

func TestNewClientset_UnparseableVersionFallsBackToV30(t *testing.T) {
	// A server returning a non-parseable version string must NOT fail
	// construction — the function logs a warning and falls back to
	// v3.0. This is the contract that lets the controller work with
	// HAProxy dev builds whose version strings don't match the
	// expected "vX.Y.Z ..." format.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			// Deliberately unparseable — no "vX.Y.Z" prefix.
			_, _ = w.Write([]byte(`{"api":{"version":"weird-build-tag-2025"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	endpoint := &Endpoint{
		URL:      server.URL,
		Username: "admin",
		Password: "password",
	}

	clientset, err := NewClientset(context.Background(), endpoint, nil)

	require.NoError(t, err,
		"unparseable version strings must NOT fail clientset construction; "+
			"a regression that errored out instead would prevent the controller "+
			"from working with any HAProxy build whose version string the parser "+
			"doesn't recognize (e.g. dev builds, custom forks)")
	require.NotNil(t, clientset)
	assert.Equal(t, 3, clientset.MajorVersion(),
		"parse-failure fallback must select major v3")
	assert.Equal(t, 0, clientset.MinorVersion(),
		"parse-failure fallback must select minor 0 (the safest baseline — "+
			"v3.0 capabilities are a strict subset of v3.1+ so we never claim "+
			"features the server might not support)")
	assert.Equal(t, "weird-build-tag-2025", clientset.DetectedVersion(),
		"the original unparseable version string must still be retained for "+
			"observability/logs even after the fallback to 3.0")
}

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// New() installs a CheckRedirect callback on the underlying http.Client
// that breaks redirect chains longer than 10 hops. The function body
// has zero direct test coverage despite being load-bearing for memory
// safety: without the cap, a malicious or misconfigured server that
// always returns a 302 to itself would cause http.Client to follow
// the chain forever, allocating a *Request on every hop until OOM or
// stack overflow.
//
// Two contracts to pin:
//
//  1. Legitimate single-hop redirects MUST be followed transparently.
//     If the cap were too tight (e.g. 1 instead of 10) every CDN URL
//     that 302s once to a real backend would fail with a useless
//     "too many redirects" error. The 10-hop limit is generous enough
//     for any realistic CDN chain.
//
//  2. Pathological redirect loops MUST be broken with a clear error.
//     Without the cap an attacker (or a misconfigured S3-style bucket
//     with a self-pointing 302) could exhaust controller memory.
//
// Both branches are tested through Fetch() against an httptest server
// because CheckRedirect isn't exposed publicly — Fetch is the only
// observable surface.

func TestHTTPStore_FetchFollowsLegitimateRedirect(t *testing.T) {
	// Server: /start 302→/end, /end serves "the-content".
	// A regression that disabled redirect-following entirely (e.g.
	// removing CheckRedirect and replacing it with one that always
	// errors) would surface here as a fetch failure.
	mux := http.NewServeMux()
	mux.HandleFunc("/end", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the-content"))
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/end", http.StatusFound)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	store := New(nil, 0)

	content, err := store.Fetch(
		context.Background(),
		server.URL+"/start",
		FetchOptions{Critical: true, Timeout: 5 * time.Second},
		nil,
	)

	require.NoError(t, err,
		"a single 302 redirect to a valid endpoint must be followed transparently; "+
			"a regression that broke redirect-following would fail every CDN-backed "+
			"HTTP fetch")
	assert.Equal(t, "the-content", content,
		"the body served at the redirect target must be returned verbatim")
}

func TestHTTPStore_FetchBreaksRedirectLoop(t *testing.T) {
	// Server: /loop ALWAYS 302s back to /loop. Without the
	// CheckRedirect cap, http.Client would follow this forever.
	// We assert two things:
	//   * Fetch returns an error (with Critical=true)
	//   * The handler is invoked at most ~10-12 times (allowing some
	//     slop for the original request + 10 redirects); a regression
	//     that disabled the cap would invoke it FAR more times before
	//     the test timed out.
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()

	store := New(nil, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := store.Fetch(
		ctx,
		server.URL+"/loop",
		FetchOptions{Critical: true, Timeout: 2 * time.Second, Retries: 1, RetryDelay: 1 * time.Millisecond},
		nil,
	)

	require.Error(t, err,
		"an infinite-redirect loop must surface as a fetch error rather than "+
			"running forever; without the CheckRedirect cap the controller would "+
			"exhaust memory following redirects until OOM")

	// The 10-hop cap means at most 11 hits per fetch attempt (initial
	// + 10 follows). Retries=1 in this code path actually maps to
	// "1 retry after initial" = 2 attempts total = up to 22 hits.
	// Allow a small safety margin (50) in case the underlying
	// http.Client implementation, retry semantics, or hop accounting
	// changes slightly. The point is that the cap WORKS — without
	// it the server would be hit thousands of times before the
	// 5-second context timeout.
	finalHits := hits.Load()
	assert.Less(t, finalHits, int32(50),
		"redirect-loop must be broken after a small bounded number of hops "+
			"(the cap is 10 per attempt); a regression that lifted the cap "+
			"would let the server be hit hundreds or thousands of times "+
			"before any timeout — got %d hits", finalHits)
	// And we DID make at least one redirect follow (otherwise the
	// test wouldn't be exercising the cap at all).
	assert.GreaterOrEqual(t, finalHits, int32(2),
		"the test must actually trigger at least one redirect follow to "+
			"meaningfully exercise the cap; got %d hits", finalHits)
}

func TestHTTPStore_NewWithNilLoggerUsesDefault(t *testing.T) {
	// Already covered by TestHTTPStore_NewWithNilLogger but adds an
	// extra assertion that the constructed store's Fetch call still
	// works end-to-end with the defaulted logger — guards against a
	// regression where slog.Default() returned a logger whose .With()
	// chain produced a panicking sub-logger (rare but has happened
	// during slog Beta).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	store := New(nil, 0) // nil logger → must default

	content, err := store.Fetch(
		context.Background(),
		server.URL,
		FetchOptions{Critical: true, Timeout: 2 * time.Second},
		nil,
	)

	require.NoError(t, err,
		"a store constructed with a nil logger must still successfully fetch — "+
			"the nil-logger fallback to slog.Default() is on the hot path of "+
			"every fetch via the .With('component', 'httpstore') chain")
	assert.Equal(t, "ok", content)
}

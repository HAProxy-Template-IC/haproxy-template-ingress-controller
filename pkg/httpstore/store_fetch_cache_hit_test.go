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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HTTPStore.Fetch's cache-hit branch (entry exists with non-empty
// AcceptedContent) is the steady-state hot path: every subsequent
// Fetch for an already-cached URL must short-circuit without an
// HTTP request. Existing tests in store_test.go exercise the
// cache-miss + happy/error paths but never trigger the cache-hit
// branch via a second Fetch call. Two load-bearing contracts pinned:
//
//  1. Second Fetch returns the cached content WITHOUT making
//     another HTTP request. Without this branch every template
//     render would re-fetch every URL, defeating the cache and
//     swamping upstream services with traffic proportional to
//     reconciliation rate × URL count.
//
//  2. Cache-hit updates LastAccessTime. The eviction logic
//     (EvictUnused) keys on time-since-last-access to decide which
//     entries to drop. A regression that didn't update the
//     timestamp would let actively-used entries get evicted as
//     "stale", forcing a re-fetch on the next reconciliation.

func TestHTTPStore_Fetch_SecondCallReturnsFromCacheWithoutHTTPRequest(t *testing.T) {
	const wantContent = "cached-content-payload"
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_, _ = w.Write([]byte(wantContent))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)
	ctx := context.Background()

	// First Fetch: cache miss → real HTTP request.
	got1, err := store.Fetch(ctx, server.URL, FetchOptions{}, nil)
	require.NoError(t, err)
	require.Equal(t, wantContent, got1)
	require.Equal(t, int32(1), atomic.LoadInt32(&requestCount),
		"sanity: first Fetch must make exactly one HTTP request to populate the cache")

	// Second Fetch: MUST hit the cache-hit branch and return
	// without incrementing requestCount.
	got2, err := store.Fetch(ctx, server.URL, FetchOptions{}, nil)
	require.NoError(t, err)
	assert.Equal(t, wantContent, got2,
		"second Fetch MUST return the cached content unchanged")
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount),
		"second Fetch MUST NOT make an HTTP request — without the cache-hit "+
			"branch every template render would re-fetch every URL, defeating "+
			"the cache and swamping upstream services with traffic "+
			"proportional to reconciliation rate × URL count")
}

func TestHTTPStore_Fetch_CacheHitUpdatesLastAccessTime(t *testing.T) {
	const wantContent = "cached-content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(wantContent))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)
	ctx := context.Background()

	// Populate cache.
	_, err := store.Fetch(ctx, server.URL, FetchOptions{}, nil)
	require.NoError(t, err)

	// Capture the post-fetch access time.
	store.mu.Lock()
	firstAccessTime := store.cache[server.URL].LastAccessTime
	store.mu.Unlock()
	require.False(t, firstAccessTime.IsZero(),
		"sanity: LastAccessTime must be populated after the initial Fetch")

	// Wait long enough that a NEW timestamp will be observably later.
	time.Sleep(5 * time.Millisecond)

	// Second Fetch: cache hit MUST refresh LastAccessTime.
	_, err = store.Fetch(ctx, server.URL, FetchOptions{}, nil)
	require.NoError(t, err)

	store.mu.Lock()
	secondAccessTime := store.cache[server.URL].LastAccessTime
	store.mu.Unlock()
	assert.True(t, secondAccessTime.After(firstAccessTime),
		"cache-hit MUST refresh LastAccessTime — the eviction logic "+
			"(EvictUnused) keys on time-since-last-access to decide which "+
			"entries to drop. A regression that didn't update the timestamp "+
			"would let actively-used entries get evicted as 'stale', forcing "+
			"a re-fetch on the next reconciliation cycle (got %s, expected after %s)",
		secondAccessTime, firstAccessTime)
}

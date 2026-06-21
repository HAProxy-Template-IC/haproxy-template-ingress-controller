// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

// NewCachedStore has THREE silent-defaulting branches that the existing
// TestNewCachedStore (happy path) and TestNewCachedStore_Errors (missing
// required fields) do NOT cover. Each silently substitutes a documented
// default value; a regression that dropped any default would either:
//
//  1. CacheTTL=0 → without the default substitution (2m10s), every
//     cache entry would expire immediately and every Get() would
//     hit the API. Production performance would crater silently.
//
//  2. MaxCacheSize<=0 → without the default substitution (256), the
//     hashicorp lru.New constructor errors on size 0, breaking every
//     caller that doesn't explicitly pass a size. Worse: zero-size
//     was the original behaviour, so the test bar moved silently.
//
//  3. Logger=nil → without the default (slog.Default), the very
//     first log call inside the store would nil-deref and panic.
//
// All three default-substitution branches are at the top of
// NewCachedStore — pin them so a refactor that "tightened" the
// constructor (e.g., made these required) would surface as a test
// failure rather than a runtime regression.

func TestNewCachedStore_DefaultCacheTTL(t *testing.T) {
	// Pass CacheTTL=0 (NOT explicit) — the constructor must substitute
	// the documented default (2m10s = 130s = 130_000ms).
	cfg := &CachedStoreConfig{
		NumKeys:  1,
		CacheTTL: 0, // <-- the regression we're guarding against
		Client:   fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:  createTestIndexer(),
	}

	store, err := NewCachedStore(cfg)
	require.NoError(t, err,
		"NewCachedStore must accept CacheTTL=0 (treated as 'use default'); "+
			"a regression that errored here would force every caller to "+
			"explicitly choose a TTL")
	require.NotNil(t, store)

	// Pin the documented default — 2 minutes plus 10 seconds. Without
	// this, a refactor that quietly shortened the default to (say) 30s
	// would silently change production cache hit rates without breaking
	// any existing test.
	assert.Equal(t, 2*time.Minute+10*time.Second, store.cacheTTL,
		"CacheTTL=0 MUST default to 2m10s — the documented production "+
			"value tuned for the controller's reconciliation cadence; "+
			"a regression would silently shift cache hit rates and "+
			"controller-vs-API request volume")
}

func TestNewCachedStore_ExplicitNonZeroCacheTTLNotOverridden(t *testing.T) {
	// Negative path: when the caller PASSES a non-zero CacheTTL, the
	// constructor must NOT overwrite it with the default. Pin this so
	// a regression that always defaulted (regardless of input) would
	// surface — that bug would silently ignore production tuning.
	const customTTL = 7 * time.Second
	cfg := &CachedStoreConfig{
		NumKeys:  1,
		CacheTTL: customTTL,
		Client:   fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:  createTestIndexer(),
	}

	store, err := NewCachedStore(cfg)
	require.NoError(t, err)
	assert.Equal(t, customTTL, store.cacheTTL,
		"NewCachedStore MUST preserve a non-zero CacheTTL — a regression "+
			"that always overwrote with the default would silently ignore "+
			"per-store tuning (e.g., shorter TTL for fast-changing resources)")
}

func TestNewCachedStore_DefaultMaxCacheSize(t *testing.T) {
	// Pass MaxCacheSize=0 (NOT explicit) — the constructor must
	// substitute DefaultMaxCacheSize. Without this branch the call
	// would fail at lru.New[string, *cacheEntry](0) inside the
	// constructor (hashicorp/lru rejects size 0).
	cfg := &CachedStoreConfig{
		NumKeys:      1,
		CacheTTL:     time.Minute, // explicit so we isolate the MaxCacheSize default
		MaxCacheSize: 0,           // <-- the regression we're guarding against
		Client:       fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:          schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:      createTestIndexer(),
	}

	store, err := NewCachedStore(cfg)
	require.NoError(t, err,
		"NewCachedStore must accept MaxCacheSize=0 — without the default "+
			"substitution to %d, lru.New would fail and every caller "+
			"that didn't explicitly set MaxCacheSize would error",
		DefaultMaxCacheSize)
	require.NotNil(t, store)
	require.NotNil(t, store.cache,
		"the LRU cache must be successfully constructed via the default "+
			"size — a nil cache would nil-deref on every Get/Add")
}

func TestNewCachedStore_NegativeMaxCacheSizeAlsoUsesDefault(t *testing.T) {
	// The branch is `<= 0`, not `== 0`. A negative value (which a
	// future refactor might pass via int subtraction) MUST also fall
	// through to the default. Pin both halves of the branch.
	cfg := &CachedStoreConfig{
		NumKeys:      1,
		CacheTTL:     time.Minute,
		MaxCacheSize: -5,
		Client:       fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:          schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:      createTestIndexer(),
	}

	store, err := NewCachedStore(cfg)
	require.NoError(t, err,
		"NewCachedStore must treat negative MaxCacheSize as 'use default' — "+
			"a regression that flipped the comparison to `< 0` would let "+
			"MaxCacheSize=0 fall through to lru.New(0) and crash")
	require.NotNil(t, store.cache)
}

func TestNewCachedStore_NilLoggerDefaultsToSlog(t *testing.T) {
	// Pass Logger=nil — the constructor must substitute slog.Default().
	// Without this, the very first log call inside the store would
	// nil-deref and panic.
	cfg := &CachedStoreConfig{
		NumKeys:  1,
		CacheTTL: time.Minute,
		Client:   fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:  createTestIndexer(),
		Logger:   nil, // <-- the regression we're guarding against
	}

	store, err := NewCachedStore(cfg)
	require.NoError(t, err,
		"NewCachedStore MUST accept Logger=nil and substitute slog.Default — "+
			"a regression that required Logger would force every caller to "+
			"construct one even when they don't care about diagnostic output")
	require.NotNil(t, store)
	assert.NotNil(t, store.logger,
		"after substitution, the logger field MUST be non-nil so the first "+
			"log call doesn't nil-deref and crash the controller")
}

// Cache TTL semantics: cached entries return before ANY API call, but
// each successful Get RESETS the TTL via cacheResource. That means a
// hot resource (accessed more often than the TTL) effectively never
// expires. Pin this contract — it's load-bearing for "frequently
// accessed Secrets shouldn't keep hitting the API".
func TestCachedStore_GetRefreshesTTLOnHit(t *testing.T) {
	scheme := runtime.NewScheme()
	resource := createTestResource("default", "hot-secret")
	client := fake.NewSimpleDynamicClient(scheme, resource)

	cfg := &CachedStoreConfig{
		NumKeys:  2,
		CacheTTL: 100 * time.Millisecond, // short so we can observe TTL effects
		Client:   client,
		GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
		Indexer:  createTestIndexer(),
	}
	store, err := NewCachedStore(cfg)
	require.NoError(t, err)

	// Add caches the resource immediately.
	require.NoError(t, store.Add(resource, []string{"default", "hot-secret"}))
	require.Equal(t, 1, cacheLen(store),
		"baseline: Add must populate the cache")

	// Tick repeatedly at < TTL intervals. After the test span (which is
	// much longer than the original TTL), the entry must STILL be
	// cached because each Get refreshed the TTL.
	deadline := time.Now().Add(300 * time.Millisecond) // 3x TTL
	for time.Now().Before(deadline) {
		_, err := store.Get("default", "hot-secret")
		require.NoError(t, err)
		time.Sleep(30 * time.Millisecond) // < cache TTL
	}

	// Even though wall-clock time exceeded the original TTL, the entry
	// must still be present because the Get path refreshes TTL on hit.
	assert.Equal(t, 1, cacheLen(store),
		"hot resources (accessed more often than CacheTTL) MUST remain "+
			"in cache; a regression that didn't refresh TTL on hit would "+
			"force every Get to re-fetch from the API on TTL boundaries, "+
			"producing exactly the API-pressure spikes the cache exists "+
			"to prevent")

	// Cross-check: the cached entry's expiresAt must be in the future,
	// because the most recent Get refreshed it. The production read path
	// (fetchResourceByRef) treats a future expiresAt as a cache hit, so a
	// regression that didn't refresh expiresAt during Get would have left
	// it in the past and forced an API re-fetch.
	store.mu.RLock()
	entry, ok := store.cache.Peek("default/hot-secret")
	store.mu.RUnlock()
	require.True(t, ok, "the hot entry must still be present in the cache")
	assert.True(t, time.Now().Before(entry.expiresAt),
		"after a hot Get loop, the entry's expiresAt must be in the future — "+
			"a regression that didn't refresh expiresAt during Get would "+
			"see the entry as expired even though it was just accessed")
}

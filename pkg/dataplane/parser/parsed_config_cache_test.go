// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parsedConfigCache is the LRU cache fronting Parse() — every parsed
// HAProxy configuration is keyed on a SHA-256 hash of its source and
// reused on the next request with the same source. This is on the
// reconciliation hot path: every reconciliation that produces an
// identical render must hit the cache rather than reparse, which
// would cost milliseconds per call.
//
// The cache has zero direct test coverage despite three load-bearing
// invariants:
//
//  1. LRU eviction: when full, the LEAST recently used entry must
//     be evicted, not a random one or the most-recent one. A
//     regression that evicted the wrong entry would tank the cache
//     hit rate for hot configurations and silently slow down
//     reconciliation under churn.
//
//  2. Move-to-end on hit: a Get() that finds an entry MUST move it
//     to the end of the LRU order so subsequent eviction decisions
//     keep treating it as recent. A regression that left the order
//     untouched on hit would cause hot entries to age out and be
//     evicted while cold entries stayed in cache.
//
//  3. Update-in-place on Set with existing key: setting an existing
//     hash must REPLACE the value in-place AND move the entry to
//     the end (treating the update as a fresh access), NOT add a
//     duplicate entry to the order slice. A regression that
//     appended a duplicate would drift the order/entries maps and
//     either over-evict (one slot used by two order entries) or
//     under-evict (entries slot map outgrows the order slice).
//
// Tests construct their own parsedConfigCache instances so they
// don't interact with the package-level configCache used in
// production paths.

// newTestCache returns a fresh cache with the given maxSize. Using a
// helper keeps the test bodies focused on the contract being pinned
// rather than struct literal noise.
func newTestCache(maxSize int) *parsedConfigCache {
	return &parsedConfigCache{
		entries: make(map[string]*cacheSlot, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

// stubConfig returns a non-nil sentinel config — get() treats a
// nil config as a miss, so we need a real (but unused) value.
func stubConfig() *StructuredConfig {
	return &StructuredConfig{}
}

func TestParsedConfigCache_GetMissReturnsNil(t *testing.T) {
	c := newTestCache(2)

	got := c.get("never-set")

	assert.Nil(t, got, "missing hash must return nil")
	// hitCount must NOT increment on a miss — a regression that
	// counted misses as hits would over-report the hit ratio in
	// metrics and mislead capacity planning.
	assert.Equal(t, int64(0), c.hitCount.Load(),
		"hitCount must NOT increment on miss")
}

func TestParsedConfigCache_GetHitMovesToEnd(t *testing.T) {
	c := newTestCache(3)
	cfgA, cfgB, cfgC := stubConfig(), stubConfig(), stubConfig()

	c.set("A", cfgA)
	c.set("B", cfgB)
	c.set("C", cfgC)
	require.Equal(t, []string{"A", "B", "C"}, c.order)

	// Hit on "A" should make it the MOST recently used (move to end).
	got := c.get("A")
	require.NotNil(t, got)
	assert.Equal(t, []string{"B", "C", "A"}, c.order,
		"a Get() hit must move the hash to the END of the LRU order so "+
			"subsequent eviction treats it as recent; a regression that left "+
			"the order untouched would cause hot entries to age out while "+
			"cold entries stayed in cache, tanking the hit rate for "+
			"steady-state reconciliation")

	assert.Equal(t, int64(1), c.hitCount.Load(), "hitCount must increment on hit")
}

func TestParsedConfigCache_SetEvictsLeastRecentlyUsed(t *testing.T) {
	c := newTestCache(2)
	cfgA, cfgB, cfgC := stubConfig(), stubConfig(), stubConfig()

	c.set("A", cfgA)
	c.set("B", cfgB)
	require.Equal(t, []string{"A", "B"}, c.order)

	// Adding C with the cache full must evict A (the LRU). NOT B
	// (most recent) and NOT a random entry.
	c.set("C", cfgC)

	assert.NotContains(t, c.entries, "A",
		"the least recently used entry (A) MUST be evicted when the cache "+
			"is full; a regression that evicted a different entry (e.g. the "+
			"most-recent or a random one) would silently degrade cache hit "+
			"rate under churn")
	assert.Contains(t, c.entries, "B")
	assert.Contains(t, c.entries, "C")
	assert.Equal(t, []string{"B", "C"}, c.order,
		"order must reflect the eviction: A removed, C appended")
}

func TestParsedConfigCache_SetUpdatesExistingInPlace(t *testing.T) {
	c := newTestCache(2)
	cfgA1 := stubConfig()
	cfgA2 := stubConfig() // distinct pointer — must replace cfgA1
	cfgB := stubConfig()

	c.set("A", cfgA1)
	c.set("B", cfgB)
	require.Equal(t, []string{"A", "B"}, c.order)

	// Re-set "A" with a different config. The contract:
	//   * The slot's config pointer must update to the new value
	//   * "A" must move to the END of the order (treated as fresh
	//     access)
	//   * The order slice must NOT grow — no duplicate "A" entry
	c.set("A", cfgA2)

	assert.Same(t, cfgA2, c.entries["A"].config,
		"setting an existing hash must REPLACE the stored config in-place; "+
			"a regression that left the old value would mean updates from "+
			"reparsing (e.g. after a normalize-fix) silently fail to take effect")
	assert.Equal(t, []string{"B", "A"}, c.order,
		"updating an existing hash must move it to the END of the order — "+
			"the update is treated as a fresh access. A regression that "+
			"appended a DUPLICATE 'A' to the order would drift order vs entries: "+
			"the order slice would outgrow entries, and the next eviction would "+
			"either skip a real eviction or remove a still-mapped entry")
	assert.Len(t, c.order, 2,
		"the order slice MUST NOT grow on an update — exactly one entry per hash")
}

func TestParsedConfigCache_FullCycleEvictionRespectsRecency(t *testing.T) {
	// End-to-end LRU semantics: a Get() refreshes recency, so the
	// next eviction must skip the refreshed entry. This is the
	// scenario most affected by the move-to-end contract.
	c := newTestCache(3)
	cfgA, cfgB, cfgC, cfgD := stubConfig(), stubConfig(), stubConfig(), stubConfig()

	c.set("A", cfgA)
	c.set("B", cfgB)
	c.set("C", cfgC)
	require.Equal(t, []string{"A", "B", "C"}, c.order)

	// Touch A — this should make B the LRU.
	c.get("A")
	require.Equal(t, []string{"B", "C", "A"}, c.order)

	// Add D — must evict B (now LRU), NOT A.
	c.set("D", cfgD)
	assert.Contains(t, c.entries, "A",
		"a recently-touched entry (A) MUST survive the next eviction; "+
			"this is the whole point of move-to-end on Get")
	assert.NotContains(t, c.entries, "B",
		"the entry that aged out via the touch (B) MUST be evicted")
	assert.Equal(t, []string{"C", "A", "D"}, c.order)
}

func TestParsedConfigCache_GetTreatsNilConfigAsMiss(t *testing.T) {
	// The get() implementation specifically checks `entry.config == nil`
	// in addition to the map miss. Pin this branch: a slot whose
	// config got nilified (defensive — currently never happens but
	// the code allows for it) must report as a miss, not return nil
	// silently and have callers crash on nil dereference.
	c := newTestCache(2)
	c.entries["empty-slot"] = &cacheSlot{hash: "empty-slot", config: nil}
	c.order = append(c.order, "empty-slot")

	got := c.get("empty-slot")

	assert.Nil(t, got,
		"a slot with nil config must be reported as a miss — without this "+
			"guard, callers that null-check the return would still receive "+
			"a wrapped nil and crash on dereference")
	assert.Equal(t, int64(0), c.hitCount.Load(),
		"a nil-slot miss must NOT count as a hit")
}

func TestHashConfig_DeterministicAndDistinct(t *testing.T) {
	// hashConfig is the cache-key derivation; two contracts:
	//   1. Same input → same output (deterministic — otherwise
	//      reconciliation never hits the cache)
	//   2. Different input → different output (distinct — otherwise
	//      different configs collide and one silently overwrites the
	//      other in the cache)
	const cfg1 = "global\n    daemon\n"
	const cfg2 = "global\n    nbthread 2\n"

	h1a := hashConfig(cfg1)
	h1b := hashConfig(cfg1)
	h2 := hashConfig(cfg2)

	assert.Equal(t, h1a, h1b,
		"hashConfig must be deterministic — without this, identical configs "+
			"produce different cache keys and never hit the cache, defeating "+
			"the entire LRU optimization")
	assert.NotEqual(t, h1a, h2,
		"different configs must hash to different keys — a collision would "+
			"let one config silently overwrite another in the cache")

	// Sanity: the hash is a 64-char hex string (SHA-256).
	assert.Len(t, h1a, 64,
		"sha256 hex output must be 64 chars — a regression to a shorter "+
			"hash function would dramatically increase collision risk")
}

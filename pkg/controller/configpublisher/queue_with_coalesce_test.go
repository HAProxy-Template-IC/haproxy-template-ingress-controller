// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// queueWithCoalesce is the channel-full coalescing helper that BOTH
// publish and validation-failed worker queues funnel through. Three
// load-bearing contracts protect against publish-side bugs:
//
//  1. Empty channel → workItem sent immediately, NO drain attempted.
//     A regression that always drained would unnecessarily clear
//     unrelated entries from renderedConfigs and produce noisy logs.
//
//  2. Full channel → the OLD pending item is drained, its cached
//     renderedConfigs entry is dropped via discardCachedConfig, and
//     the NEW item replaces it. A regression that forgot the discard
//     would leak renderedConfigs entries indefinitely under publish
//     pressure (each coalesced item leaves a stale entry behind).
//
//  3. Coalescing key alignment: discardCachedConfig is called with
//     the OLD item's correlation ID (extracted via correlationOf),
//     NOT the new item's. A regression that flipped these would
//     drop the cached state for the item we're about to publish,
//     causing the worker to fail with "missing cached state".

// stubWorkItem is a minimal struct that simulates publishWorkItem /
// validationFailedWorkItem for the purposes of exercising
// queueWithCoalesce without depending on the full work-item shapes.
type stubWorkItem struct {
	correlationID string
}

func TestQueueWithCoalesce_EmptyChannelSendsWithoutDraining(t *testing.T) {
	logger := testutil.NewTestLogger()
	c := &Component{
		logger: logger,
		// Pre-populate one cached entry so we can verify it's NOT
		// touched by an empty-channel push.
		renderedConfigs: map[string]*renderedConfigEntry{
			"corr-existing": {config: "untouched"},
		},
	}
	ch := make(chan stubWorkItem, 1)

	queueWithCoalesce(c, ch, stubWorkItem{correlationID: "corr-new"},
		"publish", "corr-new",
		func(w stubWorkItem) string { return w.correlationID })

	// Channel must contain the new item.
	require.Len(t, ch, 1,
		"empty-channel queue must successfully send on the first try — "+
			"a regression that hit the drain branch unnecessarily would still "+
			"end up with the item queued, but would have side-effected the "+
			"renderedConfigs map and emitted spurious coalesce logs")

	got := <-ch
	assert.Equal(t, "corr-new", got.correlationID)

	// The pre-existing renderedConfigs entry must be untouched —
	// proving the empty-channel path did NOT call discardCachedConfig.
	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.Contains(t, c.renderedConfigs, "corr-existing",
		"empty-channel queue MUST NOT touch renderedConfigs — a regression "+
			"that always drained would clear the unrelated cached entry")
}

func TestQueueWithCoalesce_FullChannelDrainsAndDiscardsOldCachedEntry(t *testing.T) {
	// The load-bearing contract: when the channel is full, the OLD
	// item is drained, its cached renderedConfigs entry is dropped
	// via discardCachedConfig, and the NEW item replaces it.
	logger := testutil.NewTestLogger()
	c := &Component{
		logger: logger,
		renderedConfigs: map[string]*renderedConfigEntry{
			"corr-old": {config: "stale"},
			"corr-new": {config: "fresh"},
		},
	}
	ch := make(chan stubWorkItem, 1)

	// Pre-fill the channel so the next push hits the coalesce path.
	ch <- stubWorkItem{correlationID: "corr-old"}

	queueWithCoalesce(c, ch, stubWorkItem{correlationID: "corr-new"},
		"publish", "corr-new",
		func(w stubWorkItem) string { return w.correlationID })

	// The new item replaced the old one in the channel.
	require.Len(t, ch, 1,
		"full-channel coalesce must end with exactly ONE item in the channel "+
			"(the new one) — a regression that double-pushed would corrupt "+
			"the publish-side queue ordering")
	got := <-ch
	assert.Equal(t, "corr-new", got.correlationID,
		"the queued item MUST be the new one — coalescing is 'latest wins'")

	// The OLD entry's renderedConfigs cache MUST be discarded.
	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.NotContains(t, c.renderedConfigs, "corr-old",
		"full-channel coalesce MUST drop the OLD item's cached renderedConfigs "+
			"entry — without this, every coalesced item leaves a stale entry "+
			"behind and the map grows unbounded under publish pressure")
	assert.Contains(t, c.renderedConfigs, "corr-new",
		"the NEW item's cached entry MUST survive (we're about to publish it)")
}

func TestQueueWithCoalesce_DiscardsOldKeyNotNewKey(t *testing.T) {
	// Crucial alignment contract: discardCachedConfig is called with
	// the OLD item's correlation ID extracted via correlationOf, NOT
	// the new item's correlation ID passed as the third argument.
	// A regression that flipped these would drop the cached state for
	// the item we're about to publish, causing the worker to fail
	// with "missing cached state".
	logger := testutil.NewTestLogger()
	c := &Component{
		logger: logger,
		renderedConfigs: map[string]*renderedConfigEntry{
			"corr-OLD": {config: "old"},
			"corr-NEW": {config: "new"}, // we're about to publish this
		},
	}
	ch := make(chan stubWorkItem, 1)
	ch <- stubWorkItem{correlationID: "corr-OLD"}

	queueWithCoalesce(c, ch, stubWorkItem{correlationID: "corr-NEW"},
		"publish", "corr-NEW",
		func(w stubWorkItem) string { return w.correlationID })

	c.mu.RLock()
	defer c.mu.RUnlock()
	// The CRITICAL assertion: corr-NEW must STILL be cached. A
	// regression that called discardCachedConfig("corr-NEW") instead
	// of "corr-OLD" would leave the worker without the cached
	// rendered config it needs.
	assert.Contains(t, c.renderedConfigs, "corr-NEW",
		"the NEW correlation ID's cache entry MUST remain — discard targets "+
			"the OLD item via correlationOf, NOT the new item passed as "+
			"correlationID. A regression here would discard the entry the "+
			"worker is about to publish, causing 'missing cached state' errors")
	assert.NotContains(t, c.renderedConfigs, "corr-OLD",
		"and the OLD item's entry MUST be gone (already covered by previous "+
			"test, but cross-check the alignment)")
}

func TestQueueWithCoalesce_FullChannelWithMissingOldEntryIsTolerant(t *testing.T) {
	// Robustness: discardCachedConfig is called on the drained item's
	// correlation ID, but that ID may not be in renderedConfigs (e.g.,
	// already cleaned up by another path). Verify queueWithCoalesce
	// doesn't panic and still ends up with the new item queued.
	logger := testutil.NewTestLogger()
	c := &Component{
		logger:          logger,
		renderedConfigs: map[string]*renderedConfigEntry{}, // empty!
	}
	ch := make(chan stubWorkItem, 1)
	ch <- stubWorkItem{correlationID: "corr-already-gone"}

	require.NotPanics(t, func() {
		queueWithCoalesce(c, ch, stubWorkItem{correlationID: "corr-new"},
			"publish", "corr-new",
			func(w stubWorkItem) string { return w.correlationID })
	}, "queueWithCoalesce must tolerate a missing cached entry for the "+
		"drained item — discardCachedConfig is itself idempotent, so this "+
		"is just a cross-test that the call site doesn't add its own panic")

	require.Len(t, ch, 1)
	assert.Equal(t, "corr-new", (<-ch).correlationID)
}

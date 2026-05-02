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

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// flushPendingPublish is the throttle-timer callback that drains
// the buffered work item set by processPublishWork during the
// refractory window. Two early-return branches are load-bearing
// safety guards that prevent double-publishing or nil-deref under
// race conditions, and were uncovered:
//
//  1. nil pendingPublish → silent no-op. The throttle timer can
//     fire AFTER the pending work has already been drained by
//     another path (e.g. shutdown, or a dedup-skip in
//     processPublishWork that flipped the buffer to nil between
//     the timer's scheduling and its firing). Without this guard
//     the function would nil-deref reading work.entry / work.correlationID.
//
//  2. content checksum already published → early return AND drop
//     the cached entry. The whole reason flushPendingPublish does
//     a SECOND skipIfAlreadyPublished check is that something else
//     (e.g. a non-throttled path on the same content) may have
//     published the content while this one was buffered. Without
//     this re-check, the throttle timer would write an identical
//     duplicate to etcd — undermining the throttle's whole purpose.

// flushTestComponent constructs a Component with the minimum fields
// flushPendingPublish needs for the early-return paths: pendingMu,
// pendingPublish, mu, lastPublishedChecksum, renderedConfigs, logger.
// publisher and eventBus are intentionally nil — the tested paths
// MUST NOT reach them.
func flushTestComponent() *Component {
	return &Component{
		logger:          testutil.NewTestLogger(),
		renderedConfigs: make(map[string]*renderedConfigEntry),
	}
}

func TestFlushPendingPublish_NilPendingIsNoOp(t *testing.T) {
	c := flushTestComponent()
	// pendingPublish is the zero value (nil) — sanity assert.
	c.pendingMu.Lock()
	require.Nil(t, c.pendingPublish, "baseline: pendingPublish must start nil")
	c.pendingMu.Unlock()

	// Pre-seed an unrelated cache entry so we can verify the guard
	// doesn't touch shared state on the no-op path.
	c.renderedConfigs["unrelated"] = &renderedConfigEntry{config: "untouched"}

	require.NotPanics(t, func() { c.flushPendingPublish() },
		"nil pendingPublish must NOT panic — without this guard the "+
			"function would nil-deref reading work.entry / work.correlationID "+
			"when the throttle timer fires after the buffer was drained "+
			"by another path (shutdown, dedup-skip in processPublishWork)")

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.Contains(t, c.renderedConfigs, "unrelated",
		"the no-op path MUST NOT touch shared state — a regression "+
			"that wiped renderedConfigs unconditionally would orphan "+
			"in-flight work for other correlation IDs")
}

func TestFlushPendingPublish_DedupHitSkipsAndDropsCache(t *testing.T) {
	// Set up: pendingPublish carries a work item whose checksum
	// MATCHES lastPublishedChecksum. The second skipIfAlreadyPublished
	// must fire, the publisher MUST NOT be called (it's nil — would
	// panic), and the cached entry for this correlation ID MUST be
	// dropped to keep renderedConfigs from leaking.
	c := flushTestComponent()
	const dupChecksum = "matching-content-checksum"
	const corrID = "throttled-then-superseded"

	cachedEntry := &renderedConfigEntry{
		config:          "rendered-content",
		contentChecksum: dupChecksum,
	}
	c.renderedConfigs[corrID] = cachedEntry

	work := &publishWorkItem{
		correlationID:  corrID,
		entry:          cachedEntry,
		templateConfig: &v1alpha1.HAProxyTemplateConfig{},
	}

	c.pendingMu.Lock()
	c.pendingPublish = work
	c.pendingMu.Unlock()

	// lastPublishedChecksum matches the work's checksum → dedup MUST
	// fire and skip the publish.
	c.mu.Lock()
	c.lastPublishedChecksum = dupChecksum
	c.mu.Unlock()

	require.NotPanics(t, func() { c.flushPendingPublish() },
		"dedup-skip path MUST NOT call executePublish — c.publisher is "+
			"nil here, so reaching executePublish would crash. The whole "+
			"point of the second skipIfAlreadyPublished check is that "+
			"another path may have published the content while this one "+
			"was buffered; without it the throttle timer would write an "+
			"identical duplicate to etcd")

	// Pending must be drained regardless of outcome.
	c.pendingMu.Lock()
	pending := c.pendingPublish
	c.pendingMu.Unlock()
	assert.Nil(t, pending,
		"flushPendingPublish MUST always drain the buffer — leaving the "+
			"pending work in place would let the next timer tick re-process "+
			"the same dup'd work indefinitely")

	// Cache for the dup'd correlation MUST be dropped.
	c.mu.RLock()
	_, stillCached := c.renderedConfigs[corrID]
	c.mu.RUnlock()
	assert.False(t, stillCached,
		"on dedup the cached renderedConfig entry MUST be dropped "+
			"(via skipIfAlreadyPublished's discardCachedConfig side effect) "+
			"— without this the cache leaks one entry per throttled-and-"+
			"deduped publish, which is exactly the steady-state hot path")
}

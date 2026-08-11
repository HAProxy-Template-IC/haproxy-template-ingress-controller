// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/throttle"
)

// processPublishWork is the throttle decision point sitting between
// the work channel and the K8s publisher. Its job is to keep CRD
// write pressure on etcd bounded under endpoint churn (a fresh
// rendered config per reconciliation × multiple replicas would write
// ~500 KB to etcd every few seconds otherwise). Existing tests cover
// the content-dedup short-circuit (skip_if_already_published_test.go)
// but the throttle buffering branch — the actual rate-limit machinery
// — has no direct coverage. This file pins the load-bearing branches:
//
//  1. Within refractory + no previous pending → buffer the work as
//     pendingPublish, do NOT touch the publisher. A regression that
//     called the publisher anyway would defeat the throttle entirely.
//
//  2. Within refractory + previous pending → discard the OLD
//     pending's cached renderedConfig entry (NOT the new one's) and
//     replace pendingPublish with the new work. This is the
//     correlation-alignment contract: a regression that discarded
//     the NEW item's cache would leave the buffered work with no
//     rendered output to publish when the timer fires, silently
//     dropping the publish. The OLD item's cache must always be
//     dropped to prevent renderedConfigs from leaking entries
//     proportional to the throttle rate.
//
// The tests use a minimal Component constructed directly because
// processPublishWork's throttle branch only touches pendingMu,
// pendingPublish, publishInterval, publishThrottle, mu,
// renderedConfigs, lastPublishedChecksum, and logger. No publisher /
// event bus needed for the buffering branches (they don't reach
// executePublish).

// throttleComponent constructs a Component pre-wired for throttle
// branch testing. The publisher is intentionally nil — branches 1
// and 2 must NOT reach it; if a regression invokes the publisher
// the test crashes with a nil-pointer deref, which is the desired
// loud failure mode.
//
// markFired toggles whether the throttle's gate is closed (true =
// "inside refractory") or open (false = "outside refractory" /
// disabled when interval == 0).
func throttleComponent(t *testing.T, publishInterval time.Duration, markFired bool) *Component {
	t.Helper()
	gate := throttle.New(publishInterval)
	t.Cleanup(gate.Stop)
	if markFired {
		gate.MarkFired()
	}
	return &Component{
		logger:                testutil.NewTestLogger(),
		renderedConfigs:       make(map[string]*renderedConfigEntry),
		publishInterval:       publishInterval,
		publishThrottle:       gate,
		lastPublishedChecksum: "",
	}
}

// throttleWork builds a publishWorkItem with an entry pre-cached in
// renderedConfigs so the cache-drop side effect is observable.
func throttleWork(c *Component, correlationID, checksum string) *publishWorkItem {
	entry := &renderedConfigEntry{contentChecksum: checksum, config: "rendered-" + correlationID}
	c.renderedConfigs[correlationID] = entry
	return &publishWorkItem{
		correlationID:  correlationID,
		entry:          entry,
		templateConfig: &v1alpha1.HAProxyTemplateConfig{},
	}
}

func TestProcessPublishWork_BuffersWhenWithinRefractoryAndNoPendingPublish(t *testing.T) {
	// publishInterval=10s, throttle marked fired just now → inside refractory.
	// No previous pendingPublish. The work item MUST be buffered, the
	// publisher MUST NOT be invoked, and the renderedConfigs cache
	// for the work's correlation ID MUST remain intact (the buffered
	// publish will need it when the throttle timer fires).
	c := throttleComponent(t, 10*time.Second, true)
	work := throttleWork(c, "corr-buffered", "fresh-checksum")

	c.processPublishWork(t.Context(), work)

	c.pendingMu.Lock()
	pending := c.pendingPublish
	c.pendingMu.Unlock()
	require.NotNil(t, pending,
		"work inside refractory must be buffered as pendingPublish — "+
			"a regression that fell through to executePublish would "+
			"defeat the throttle and let CRD writes hit etcd at "+
			"reconciliation rate")
	assert.Same(t, work, pending,
		"the buffered pendingPublish MUST be the same work item we sent — "+
			"if a regression replaced it (e.g. with a copy) the timer-fire "+
			"path would publish stale data")

	// Cache for the buffered work MUST remain — flushPendingPublish
	// will look it up by correlation ID when the timer fires.
	c.mu.RLock()
	_, cached := c.renderedConfigs["corr-buffered"]
	c.mu.RUnlock()
	assert.True(t, cached,
		"the buffered work's cached renderedConfig MUST remain in "+
			"renderedConfigs — without it, flushPendingPublish has no "+
			"rendered output to publish when the throttle timer fires")
}

func TestProcessPublishWork_BuffersWhenWithinRefractoryAndDiscardsOldPending(t *testing.T) {
	// The high-value contract: when buffering a NEW item over an
	// existing pendingPublish, the OLD item's cached renderedConfig
	// MUST be dropped and the NEW item's MUST be retained. A regression
	// that flipped these would either:
	//   - Drop the new item's cache → flushPendingPublish has nothing
	//     to publish when the timer fires, silently losing the update.
	//   - Retain the old item's cache → renderedConfigs leaks entries
	//     proportional to the throttle rate (every superseded pending
	//     leaves a stale entry behind).
	c := throttleComponent(t, 10*time.Second, true)
	oldWork := throttleWork(c, "corr-old-pending", "old-checksum")
	newWork := throttleWork(c, "corr-new-pending", "new-checksum")

	// Pre-seed the buffer with the old work to simulate a prior
	// processPublishWork call that landed during the same refractory
	// window. We set the buffer state directly so this test exercises
	// only the supersede branch, not the path that put oldWork there.
	c.pendingMu.Lock()
	c.pendingPublish = oldWork
	c.pendingMu.Unlock()

	require.Contains(t, c.renderedConfigs, "corr-old-pending",
		"sanity: old work's cache must be present before supersede")

	c.processPublishWork(t.Context(), newWork)

	c.pendingMu.Lock()
	pending := c.pendingPublish
	c.pendingMu.Unlock()

	require.NotNil(t, pending)
	assert.Same(t, newWork, pending,
		"pendingPublish MUST be replaced with the newer work — the throttle "+
			"is leading-edge with last-write-wins; a regression that kept "+
			"the OLD item would publish stale config when the timer fires")

	c.mu.RLock()
	_, oldCached := c.renderedConfigs["corr-old-pending"]
	_, newCached := c.renderedConfigs["corr-new-pending"]
	c.mu.RUnlock()

	assert.False(t, oldCached,
		"the SUPERSEDED (old) pending's cached renderedConfig MUST be dropped — "+
			"a regression that left it behind would leak renderedConfigs "+
			"entries proportional to the throttle rate (every superseded "+
			"buffer would orphan a cache entry)")
	assert.True(t, newCached,
		"the NEW pending's cached renderedConfig MUST be retained — a regression "+
			"that called discardCachedConfig with the new correlation ID instead "+
			"of the old one would orphan the buffered publish: when the throttle "+
			"timer fires, flushPendingPublish would have no rendered output to "+
			"publish, silently dropping the update")
}

// TestProcessPublishWork_NoDeadlockWithLostLeadership is a concurrency
// regression test for the mu/pendingMu lock ordering. processPublishWork's
// supersede path discards a cached config (which takes mu) and
// handleLostLeadership takes mu then pendingMu; an earlier revision nested
// pendingMu->mu inside processPublishWork, inverting that order — a latent
// AB-BA deadlock that neither -race (data races only) nor go vet nor Go's
// all-goroutines-blocked deadlock detector can catch. Running both paths
// concurrently under a watchdog hangs pre-fix and completes post-fix.
func TestProcessPublishWork_NoDeadlockWithLostLeadership(t *testing.T) {
	c := throttleComponent(t, time.Hour, true) // gate closed → buffering+supersede branch

	mkWork := func(id string) *publishWorkItem {
		// Seed a cache entry so the supersede path actually calls discardCachedConfig.
		c.mu.Lock()
		c.renderedConfigs[id] = &renderedConfigEntry{config: "cfg", contentChecksum: id}
		c.mu.Unlock()
		return &publishWorkItem{
			correlationID:  id,
			templateConfig: &v1alpha1.HAProxyTemplateConfig{},
			entry:          &renderedConfigEntry{config: "cfg", contentChecksum: id},
		}
	}

	const iters = 2000
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				c.processPublishWork(ctx, mkWork(fmt.Sprintf("c-%d", i)))
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				c.handleLostLeadership(nil)
			}
		}()
		wg.Wait()
	}()

	select {
	case <-done:
		// Completed without deadlock — the two mutexes are never nested in
		// conflicting orders.
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: processPublishWork and handleLostLeadership did not " +
			"complete — mu/pendingMu AB-BA lock inversion")
	}
}

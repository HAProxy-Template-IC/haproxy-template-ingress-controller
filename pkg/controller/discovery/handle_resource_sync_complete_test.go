// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
)

// handleResourceSyncComplete sets the initialSyncComplete flag
// (which gates initial discovery) and trips tryInitialDiscovery.
// The function has THREE branches; the existing component_test.go
// covers the happy path using the fake HAProxy executor installed
// in TestMain. The state-management contracts are additionally
// pinned at the pure unit level here:
//
//  1. Wrong resource type → no-op. ResourceSyncCompleteEvent fires
//     for EVERY watched resource type during startup; without this
//     filter the discovery component would prematurely set
//     initialSyncComplete=true on the first ingress/service sync,
//     letting tryInitialDiscovery proceed before haproxy-pods
//     have actually loaded.
//
//  2. Duplicate event (already complete) → no-op. After leadership
//     transitions or controller restarts, watchers can re-emit
//     ResourceSyncCompleteEvent. Without this dedup, every
//     re-emission would re-call tryInitialDiscovery (which has
//     its own initialDiscoveryDone guard, but the extra mutex
//     acquisition + log line would surface as noise during
//     leadership churn).
//
//  3. First valid event → flip initialSyncComplete to true and
//     enter tryInitialDiscovery (which then enforces the other
//     prerequisites for actual discovery). This is the load-
//     bearing state transition.

func TestHandleResourceSyncComplete_WrongResourceTypeIsNoOp(t *testing.T) {
	c := newTestComponent(t)
	require.False(t, c.initialSyncComplete,
		"baseline: initialSyncComplete must start false")

	// Sync-complete for SERVICES (not haproxy-pods) — must be
	// ignored. Without this filter the next non-haproxy-pods sync
	// would let initial discovery proceed before haproxy-pods are
	// even loaded.
	c.handleResourceSyncComplete(events.NewResourceSyncCompleteEvent("services", 5))

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.initialSyncComplete,
		"non-haproxy-pods ResourceSyncCompleteEvent MUST leave "+
			"initialSyncComplete unchanged — a regression that didn't "+
			"filter would let the discovery component think haproxy-pods "+
			"are synced as soon as ANY watched resource finishes its "+
			"initial sync (typically services or ingresses, which are "+
			"smaller and complete first), causing tryInitialDiscovery "+
			"to fire with an empty pod store")
}

func TestHandleResourceSyncComplete_FirstHAProxyPodsEventFlipsFlag(t *testing.T) {
	c := newTestComponent(t)
	require.False(t, c.initialSyncComplete)

	c.handleResourceSyncComplete(
		events.NewResourceSyncCompleteEvent(names.HAProxyPodsResourceType, 3),
	)

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.initialSyncComplete,
		"first valid haproxy-pods sync-complete MUST flip "+
			"initialSyncComplete to true — this is the state transition "+
			"that gates the entire discovery flow; without it the "+
			"controller can never start discovering pods even after the "+
			"watcher confirms initial sync")
	// initialDiscoveryDone stays false here because credentials and
	// dataplane port aren't set (the test component is minimal). The
	// tryInitialDiscovery call's own guards handle that case — pinned
	// elsewhere.
	assert.False(t, c.initialDiscoveryDone,
		"initialDiscoveryDone MUST remain false here because credentials/"+
			"port/podStore aren't set on the minimal test component — the "+
			"tryInitialDiscovery guards (covered in handlers_initial_discovery_test.go) "+
			"correctly skip in this state")
}

func TestHandleResourceSyncComplete_DuplicateEventIsIdempotent(t *testing.T) {
	c := newTestComponent(t)

	// First call: flips the flag.
	c.handleResourceSyncComplete(
		events.NewResourceSyncCompleteEvent(names.HAProxyPodsResourceType, 3),
	)
	c.mu.RLock()
	require.True(t, c.initialSyncComplete,
		"sanity: first call must flip the flag for the dedup test to be meaningful")
	c.mu.RUnlock()

	// Second call: must be a no-op. Without the dedup branch every
	// re-emission of ResourceSyncCompleteEvent (after leadership
	// transitions or controller restarts) would re-enter
	// tryInitialDiscovery — extra mutex acquisition + log noise that
	// surfaces during leadership churn.
	require.NotPanics(t, func() {
		c.handleResourceSyncComplete(
			events.NewResourceSyncCompleteEvent(names.HAProxyPodsResourceType, 5),
		)
	})

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.initialSyncComplete,
		"duplicate event MUST leave initialSyncComplete true (idempotent — "+
			"the dedup branch must not flip it back)")
}

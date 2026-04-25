// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// cleanupOldEventCorrelation is what keeps the RingBuffer's
// correlationIndex from growing unbounded as the buffer wraps. Unlike
// typeIndex which uses lazy cleanup during reads, correlationIndex
// must be ACTIVELY cleaned because correlation IDs are unique per
// reconciliation cycle — a long-running controller would otherwise
// accumulate one stale map entry per reconciliation forever.
//
// The function has FIVE distinct return-early branches and one
// mutation branch, with no direct test coverage. The most critical
// branches:
//
//  1. Map entry deletion when newIndices is empty: a regression that
//     left empty []int slices in the map would defeat the entire
//     purpose of cleanup — the map would still grow unbounded, just
//     with empty slices instead of populated ones. Test pins this
//     by reading the map size before/after.
//
//  2. Map entry update when newIndices is non-empty: a regression
//     that deleted the entry instead of updating would lose the
//     other still-valid indices, causing FindByCorrelationID to
//     miss events that are still in the buffer.
//
//  3. The unrelated-correlation isolation: cleanup must only touch
//     the OVERWRITTEN event's correlation, never anything else.
//     Tested by interleaving multiple correlation IDs and asserting
//     unrelated ones survive.
//
// Tests exercise the function through Add() (its only call site) by
// filling the buffer past capacity to trigger wraparound. The
// internal correlationIndex map is read-only-inspected via the
// existing FindByCorrelationID public API plus a single white-box
// length assertion that catches the silent map-growth regression.

func TestRingBuffer_CleanupCorrelation_RemovesStaleEntryWhenLastIndexEvicted(t *testing.T) {
	// Capacity 2: each new Add after the first 2 must overwrite an
	// old slot and trigger cleanupOldEventCorrelation.
	rb := NewRingBuffer(2)

	// Add a single correlated event.
	evt1 := ctlevents.NewReconciliationTriggeredEvent("first", true, ctlevents.WithNewCorrelation())
	corrID := evt1.CorrelationID()
	rb.Add(evt1)

	// Sanity: it's findable.
	require.Len(t, rb.FindByCorrelationID(corrID, 0), 1,
		"sanity: the correlated event must be findable after Add")

	// Pre-condition: correlationIndex has exactly one entry.
	rb.mu.RLock()
	mapSizeBefore := len(rb.correlationIndex)
	rb.mu.RUnlock()
	require.Equal(t, 1, mapSizeBefore,
		"sanity: correlationIndex must have one entry for the just-added event")

	// Wrap the buffer twice with non-correlated mockEvents to evict
	// the original. Capacity 2 + 3 adds = at least one wraparound.
	rb.Add(mockEvent{eventType: "filler-a"})
	rb.Add(mockEvent{eventType: "filler-b"})
	rb.Add(mockEvent{eventType: "filler-c"}) // wraps; evicts evt1

	// Post-condition: the correlation entry MUST be deleted from the
	// map (not just emptied). This is the load-bearing memory-leak
	// guard — a regression that left an empty []int slice in the
	// map would still make this assertion fail.
	rb.mu.RLock()
	mapSizeAfter := len(rb.correlationIndex)
	rb.mu.RUnlock()
	assert.Equal(t, 0, mapSizeAfter,
		"correlationIndex MUST be empty after the only correlated event is "+
			"evicted; a regression that left an empty []int slice instead of "+
			"calling delete(map, key) would cause the map to grow by one entry "+
			"per reconciliation forever in long-running controllers")

	// And the event is no longer findable.
	assert.Empty(t, rb.FindByCorrelationID(corrID, 0),
		"the evicted event must not be findable; if it is, the cleanup "+
			"logic incorrectly preserved a stale buffer index")
}

func TestRingBuffer_CleanupCorrelation_PreservesOtherIndicesForSameCorrelation(t *testing.T) {
	// Two events sharing the same correlation ID, only one gets
	// evicted. The remaining index for the same correlation must
	// survive — a regression that deleted the whole map entry
	// instead of removing just the evicted index would lose the
	// surviving event from FindByCorrelationID lookups.
	rb := NewRingBuffer(3)

	evt1 := ctlevents.NewReconciliationTriggeredEvent("a", true, ctlevents.WithNewCorrelation())
	corrID := evt1.CorrelationID()
	evt2 := ctlevents.NewReconciliationStartedEvent("a", ctlevents.WithCorrelation(corrID, evt1.EventID()))

	rb.Add(evt1)
	rb.Add(evt2)
	require.Len(t, rb.FindByCorrelationID(corrID, 0), 2,
		"sanity: both correlated events findable")

	// Add filler to trigger wrap and evict evt1 (oldest).
	rb.Add(mockEvent{eventType: "x"})
	rb.Add(mockEvent{eventType: "y"}) // evicts evt1

	// evt2 must still be findable under the same correlation ID.
	found := rb.FindByCorrelationID(corrID, 0)
	assert.Len(t, found, 1,
		"the surviving correlated event MUST remain findable; a regression "+
			"that deleted the whole map entry on first eviction would lose "+
			"every still-buffered event sharing that correlation ID — "+
			"silently breaking reconciliation-summary correlation in the "+
			"commentator")
}

func TestRingBuffer_CleanupCorrelation_DoesNotAffectUnrelatedCorrelations(t *testing.T) {
	// Two distinct correlation IDs interleaved. Evicting one must
	// NOT touch the other's correlationIndex entry. This catches
	// regressions that would, e.g., clear the entire map on
	// wraparound or use the wrong index in the cleanup logic.
	rb := NewRingBuffer(2)

	evtA := ctlevents.NewReconciliationTriggeredEvent("a", true, ctlevents.WithNewCorrelation())
	corrA := evtA.CorrelationID()
	evtB := ctlevents.NewReconciliationTriggeredEvent("b", true, ctlevents.WithNewCorrelation())
	corrB := evtB.CorrelationID()

	rb.Add(evtA)
	rb.Add(evtB)

	// Add a third correlated event to evict evtA.
	evtC := ctlevents.NewReconciliationTriggeredEvent("c", true, ctlevents.WithNewCorrelation())
	corrC := evtC.CorrelationID()
	rb.Add(evtC) // evicts evtA

	// corrA cleaned up.
	assert.Empty(t, rb.FindByCorrelationID(corrA, 0),
		"corrA's events should be evicted with the buffer slot")
	// corrB and corrC are untouched.
	assert.Len(t, rb.FindByCorrelationID(corrB, 0), 1,
		"corrB MUST survive the eviction of an unrelated correlation; a "+
			"regression that wrote the cleanup loop with the wrong index "+
			"would silently delete unrelated correlations from the map")
	assert.Len(t, rb.FindByCorrelationID(corrC, 0), 1,
		"corrC (just added) MUST be findable")
}

func TestRingBuffer_CleanupCorrelation_TolerantOfNonCorrelatedEviction(t *testing.T) {
	// The early-return branches: when the evicted slot held
	// (a) a nil event, (b) a non-CorrelatedEvent, or (c) a
	// CorrelatedEvent with empty correlation ID, cleanup must
	// silently no-op rather than panic or touch the map.
	//
	// Pin via a buffer that mostly holds non-correlated mockEvents
	// being evicted by other non-correlated mockEvents. The map
	// must stay empty throughout — any leak indicates the early-
	// return branches mistakenly wrote to the map.
	rb := NewRingBuffer(2)

	for i := 0; i < 10; i++ {
		rb.Add(mockEvent{eventType: "filler"})
	}

	rb.mu.RLock()
	mapSize := len(rb.correlationIndex)
	rb.mu.RUnlock()
	assert.Equal(t, 0, mapSize,
		"non-correlated event eviction must NOT touch correlationIndex; a "+
			"regression that fell through the early-return branches and "+
			"wrote a stale entry would surface here as a non-zero map size")
}

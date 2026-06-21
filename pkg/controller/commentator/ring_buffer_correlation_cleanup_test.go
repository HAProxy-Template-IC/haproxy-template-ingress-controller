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

// Correlation lookups must track buffer eviction exactly: once an event's slot
// is overwritten on wraparound it must disappear from FindByCorrelationID, but
// other events — whether they share the correlation ID or belong to a different
// one — must keep being found as long as their slots are still live. The plain
// linear-scan buffer gets this for free (there's no secondary index to drift
// out of sync), so these tests pin the externally observable contract rather
// than any internal bookkeeping.

func TestRingBuffer_CleanupCorrelation_RemovesStaleEntryWhenLastIndexEvicted(t *testing.T) {
	// Capacity 2: each new Add after the first 2 must overwrite an old slot.
	rb := NewRingBuffer(2)

	// Add a single correlated event.
	evt1 := ctlevents.NewReconciliationTriggeredEvent("first", true, ctlevents.WithNewCorrelation())
	corrID := evt1.CorrelationID()
	rb.Add(evt1)

	// Sanity: it's findable.
	require.Len(t, rb.FindByCorrelationID(corrID, 0), 1,
		"sanity: the correlated event must be findable after Add")

	// Wrap the buffer with non-correlated mockEvents to evict the original.
	// Capacity 2 + 3 adds = at least one wraparound.
	rb.Add(mockEvent{eventType: "filler-a"})
	rb.Add(mockEvent{eventType: "filler-b"})
	rb.Add(mockEvent{eventType: "filler-c"}) // wraps; evicts evt1

	// The event is no longer findable once its slot is overwritten.
	assert.Empty(t, rb.FindByCorrelationID(corrID, 0),
		"the evicted event must not be findable; if it is, the buffer is "+
			"returning events from slots that have already been overwritten")
}

func TestRingBuffer_CleanupCorrelation_PreservesOtherIndicesForSameCorrelation(t *testing.T) {
	// Two events sharing the same correlation ID, only one gets evicted. The
	// surviving one must remain findable under that correlation ID.
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
		"the surviving correlated event MUST remain findable; losing it would "+
			"silently break reconciliation-summary correlation in the commentator")
}

func TestRingBuffer_CleanupCorrelation_DoesNotAffectUnrelatedCorrelations(t *testing.T) {
	// Two distinct correlation IDs interleaved. Evicting one must NOT affect
	// the other's lookups.
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

	// corrA evicted with its slot.
	assert.Empty(t, rb.FindByCorrelationID(corrA, 0),
		"corrA's events should be evicted with the buffer slot")
	// corrB and corrC are untouched.
	assert.Len(t, rb.FindByCorrelationID(corrB, 0), 1,
		"corrB MUST survive the eviction of an unrelated correlation")
	assert.Len(t, rb.FindByCorrelationID(corrC, 0), 1,
		"corrC (just added) MUST be findable")
}

func TestRingBuffer_CleanupCorrelation_TolerantOfNonCorrelatedEviction(t *testing.T) {
	// Evicting non-correlated events must never surface a phantom correlation:
	// a buffer full of non-correlated mockEvents has nothing to find.
	rb := NewRingBuffer(2)

	for i := 0; i < 10; i++ {
		rb.Add(mockEvent{eventType: "filler"})
	}

	assert.Empty(t, rb.FindByCorrelationID("any", 0),
		"non-correlated events must never be returned by a correlation lookup")
}

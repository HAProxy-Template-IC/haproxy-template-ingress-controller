// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// TestNewReconciliationFailedEvent_StatusPatchesDefensiveCopy pins the
// outer-slice defensive-copy contract for the failure path. The Coordinator
// passes `c.lastSuccessfulPatches` — a long-lived field updated on every
// successful render. If the constructor shared the backing array, the
// next successful render would mutate every previously-published failure
// event still held by subscribers (commentator ring buffer,
// status-applier failure handler).
func TestNewReconciliationFailedEvent_StatusPatchesDefensiveCopy(t *testing.T) {
	original := []templating.StatusPatch{
		{Name: "first", Kind: "Gateway"},
		{Name: "second", Kind: "HTTPRoute"},
	}
	event := NewReconciliationFailedEvent("err", "render", original)

	original[0] = templating.StatusPatch{Name: "MUTATED", Kind: "Mutant"}

	require.Equal(t, "first", event.StatusPatches[0].Name,
		"event's StatusPatches must not share backing array with caller")
	require.Equal(t, 2, len(event.StatusPatches))
}

// TestNewReconciliationFailedEvent_NilStatusPatchesStaysNil covers the
// early-bootstrap case: failure before any successful render means the
// Coordinator has no lastSuccessfulPatches to forward; the resulting event
// should have a nil (not empty) slice so the StatusApplier's
// `len(patches) == 0` guard cleanly short-circuits.
func TestNewReconciliationFailedEvent_NilStatusPatchesStaysNil(t *testing.T) {
	event := NewReconciliationFailedEvent("err", "render", nil)
	require.Nil(t, event.StatusPatches,
		"nil patches should round-trip as nil (slices.Clone(nil) == nil)")
}

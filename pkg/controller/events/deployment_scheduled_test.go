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

// TestNewDeploymentScheduledEvent_StatusPatchesDefensiveCopy pins the outer-
// slice defensive-copy contract for StatusPatches. The DeploymentScheduler
// passes a slice value it pulls from `s.lastValidatedStatusPatches` (cached
// from TemplateRenderedEvent) — that same slice header is reused across
// reconciliation cycles. If the constructor shared the caller's backing
// array, a subsequent render that overwrote the cache would silently mutate
// every previously-published DeploymentScheduledEvent still held by
// subscribers (commentator ring buffer, the deployer's in-flight handler).
func TestNewDeploymentScheduledEvent_StatusPatchesDefensiveCopy(t *testing.T) {
	original := []templating.StatusPatch{
		{Name: "first", Kind: "Gateway"},
		{Name: "second", Kind: "HTTPRoute"},
	}
	event := NewDeploymentScheduledEvent(
		"haproxy config", nil, nil, nil, "name", "ns", "reason", "checksum",
		nil, "", original, true,
	)

	original[0] = templating.StatusPatch{Name: "MUTATED", Kind: "Mutant"}

	require.Equal(t, "first", event.StatusPatches[0].Name,
		"event's StatusPatches must not share backing array with caller")
	require.Equal(t, 2, len(event.StatusPatches))
}

// TestNewDeploymentScheduledEvent_NilStatusPatchesStaysNil mirrors the
// nil-stays-nil contract documented for OperationBreakdown:
// slices.Clone(nil) returns nil, so subscribers can branch on `len(patches)
// == 0` without false positives from an accidental empty-but-non-nil slice.
func TestNewDeploymentScheduledEvent_NilStatusPatchesStaysNil(t *testing.T) {
	event := NewDeploymentScheduledEvent(
		"", nil, nil, nil, "", "", "", "", nil, "", nil, false,
	)
	require.Nil(t, event.StatusPatches,
		"nil patches should round-trip as nil (slices.Clone(nil) == nil)")
}

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// NewDeploymentCompletedEvent's existing tests cover the scalar
// fields and correlation propagation, but two non-obvious contracts
// are uncovered:
//
//  1. OperationBreakdown is a map and MUST be defensively copied.
//     The constructor builds the map from a result struct that the
//     caller (deployer scheduler) typically reuses across reconciliation
//     cycles. If the constructor stored the caller's map by reference,
//     a subsequent reconciliation cycle that mutated its scratch map
//     would silently corrupt every previously published event still
//     held by subscribers (commentator ring buffer, metrics, debug
//     event buffer).
//
//  2. nil OperationBreakdown must be preserved as nil (not coerced
//     to an empty map). Downstream consumers — the commentator's
//     deploymentInsight() — branches on `breakdown == nil` to
//     decide whether to log the breakdown line at all. Coercing nil
//     to an empty map would produce a noisy "operations: " log line
//     for every no-op deployment.
//
//  3. BackendDiffFields must round-trip verbatim. This string is the
//     pre-formatted "[Field1, Field2] (N backends)" summary that
//     formatBackendDiffFields produces; the deployer feeds it
//     unchanged into the commentator. Any mangling (re-quoting,
//     stripping, etc) would break log scrapers and alert templates
//     that parse this format.

func TestNewDeploymentCompletedEvent_DefensiveCopyOfBreakdown(t *testing.T) {
	// The caller's map; we'll mutate it after construction and verify
	// the event's copy is unaffected.
	original := map[string]int{
		"backend_create": 2,
		"server_update":  5,
	}

	event := NewDeploymentCompletedEvent(&DeploymentResult{
		OperationBreakdown: original,
	})

	require.NotNil(t, event.OperationBreakdown)
	require.Equal(t, 2, event.OperationBreakdown["backend_create"])
	require.Equal(t, 5, event.OperationBreakdown["server_update"])

	// Mutate the caller's map AFTER construction. If the event held
	// a reference instead of a copy, these mutations would now
	// appear inside published events sitting in subscriber buffers.
	original["backend_create"] = 99
	original["new_op"] = 7

	assert.Equal(t, 2, event.OperationBreakdown["backend_create"],
		"event's OperationBreakdown must be a defensive copy; "+
			"caller mutation MUST NOT leak into published events held by commentator/metrics/debug-buffer")
	assert.NotContains(t, event.OperationBreakdown, "new_op",
		"defensive copy must isolate the event from post-construction additions to the caller's map")
}

func TestNewDeploymentCompletedEvent_NilBreakdownStaysNil(t *testing.T) {
	// nil-vs-empty-map matters: the commentator's deploymentInsight
	// branches on `breakdown == nil` to decide whether to log the
	// breakdown line at all. Coercing nil -> empty map would
	// silently log "operations:" for every no-op reconciliation.
	event := NewDeploymentCompletedEvent(&DeploymentResult{
		OperationBreakdown: nil,
	})

	assert.Nil(t, event.OperationBreakdown,
		"nil OperationBreakdown must be preserved as nil; coercing it to an empty map would "+
			"flip the commentator's nil-check and produce noisy 'operations:' lines on every no-op deployment")
}

func TestNewDeploymentCompletedEvent_EmptyBreakdownIsCopiedToEmptyMap(t *testing.T) {
	// Caller passing an explicit empty (but non-nil) map should NOT
	// be coerced back to nil — it's a deliberate "we tried but had
	// zero ops" signal that some downstream consumers may want to
	// distinguish from the nil "we never computed it" signal.
	original := map[string]int{}

	event := NewDeploymentCompletedEvent(&DeploymentResult{
		OperationBreakdown: original,
	})

	require.NotNil(t, event.OperationBreakdown,
		"explicitly-empty map must NOT be coerced to nil; the distinction between 'never computed' (nil) "+
			"and 'computed and empty' (empty map) is preserved by the constructor")
	assert.Empty(t, event.OperationBreakdown)

	// Mutating the caller's empty map must still not affect the event.
	original["after"] = 1
	assert.Empty(t, event.OperationBreakdown,
		"defensive copy applies even to empty maps")
}

func TestNewDeploymentCompletedEvent_BackendDiffFieldsRoundTripsVerbatim(t *testing.T) {
	// BackendDiffFields is the pre-formatted summary string from
	// formatBackendDiffFields. The constructor must NOT modify it —
	// log scrapers and alert templates parse the bracketed
	// "[Field1, Field2] (N backends)" shape exactly.
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty string round-trips", in: ""},
		{name: "single bucket singular noun", in: "[Mode] (1 backend)"},
		{name: "multiple buckets sorted", in: "[Balance] (1 backend), [Mode] (3 backends)"},
		{name: "field name with comma is preserved", in: "[Field, A, B] (2 backends)"},
		{name: "newline-containing string is NOT split", in: "line1\nline2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewDeploymentCompletedEvent(&DeploymentResult{
				BackendDiffFields: tt.in,
			})
			assert.Equal(t, tt.in, event.BackendDiffFields,
				"BackendDiffFields must round-trip verbatim; any mangling breaks log scrapers and alerts")
		})
	}
}

// TestNewDeploymentCompletedEvent_StatusPatchesDefensiveCopy pins the same
// outer-slice defensive-copy contract for StatusPatches that
// deployment_skipped_test.go pins on DeploymentSkippedEvent. The deployer
// passes the patches it received from DeploymentScheduledEvent into
// NewDeploymentCompletedEvent unchanged; if the constructor shared the
// caller's backing array, a subsequent reconciliation cycle that reused the
// same patches slice could mutate every previously-published completion
// event still held by subscribers (commentator ring buffer, status-applier
// handler, debug-event dump).
func TestNewDeploymentCompletedEvent_StatusPatchesDefensiveCopy(t *testing.T) {
	original := []templating.StatusPatch{
		{Name: "first", Kind: "Gateway"},
		{Name: "second", Kind: "HTTPRoute"},
	}
	event := NewDeploymentCompletedEvent(&DeploymentResult{
		Total:         2,
		Succeeded:     2,
		StatusPatches: original,
	})

	original[0] = templating.StatusPatch{Name: "MUTATED", Kind: "Mutant"}

	require.Equal(t, "first", event.StatusPatches[0].Name,
		"event's StatusPatches must not share backing array with caller")
	require.Equal(t, 2, len(event.StatusPatches))
}

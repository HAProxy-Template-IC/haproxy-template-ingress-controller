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

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestNewDeploymentSkippedEvent_StoresAllFields(t *testing.T) {
	event := NewDeploymentSkippedEvent(3, "config_unchanged", "abc123", "def456", nil)

	assert.Equal(t, 3, event.Total)
	assert.Equal(t, "config_unchanged", event.Reason)
	assert.Equal(t, "abc123", event.ConfigHash)
	assert.Equal(t, "def456", event.PodSetHash)
	assert.False(t, event.Timestamp().IsZero(), "timestamp should be set")
}

func TestNewDeploymentSkippedEvent_EventType(t *testing.T) {
	event := NewDeploymentSkippedEvent(0, "", "", "", nil)

	assert.Equal(t, EventTypeDeploymentSkipped, event.EventType())
}

func TestNewDeploymentSkippedEvent_CorrelationPropagation(t *testing.T) {
	// Build a triggering event with a known correlation ID.
	triggering := &DeploymentScheduledEvent{
		Correlation: newCorrelation(WithCorrelation("test-corr-id", "test-causation-id")),
	}

	event := NewDeploymentSkippedEvent(
		1, "config_unchanged", "hash1", "hash2", nil,
		PropagateCorrelation(triggering),
	)

	assert.Equal(t, "test-corr-id", event.CorrelationID(),
		"correlation ID should propagate from triggering event")
}

func TestNewDeploymentSkippedEvent_ZeroValues(t *testing.T) {
	event := NewDeploymentSkippedEvent(0, "", "", "", nil)

	assert.Equal(t, 0, event.Total)
	assert.Equal(t, "", event.Reason)
	assert.Equal(t, "", event.ConfigHash)
	assert.Equal(t, "", event.PodSetHash)
	assert.Nil(t, event.StatusPatches, "nil patches should remain nil (slices.Clone(nil) == nil)")
	assert.NotEmpty(t, event.EventID(), "event ID should always be generated")
	assert.Empty(t, event.CorrelationID(), "correlation ID stays empty without explicit options")
}

// TestNewDeploymentSkippedEvent_StatusPatchesDefensiveCopy pins that the outer
// patches slice is cloned at construction. The caller may reuse its scratch
// slice across reconciliation cycles; sharing the backing array would let a
// later mutation silently corrupt a published event still held by subscribers
// (the commentator's ring buffer, debug-event dumps, etc.).
func TestNewDeploymentSkippedEvent_StatusPatchesDefensiveCopy(t *testing.T) {
	original := []templating.StatusPatch{
		{Name: "first", Kind: "Gateway"},
		{Name: "second", Kind: "HTTPRoute"},
	}
	event := NewDeploymentSkippedEvent(1, "config_unchanged", "h1", "h2", original)

	// Mutate caller's slice after publication.
	original[0] = templating.StatusPatch{Name: "MUTATED", Kind: "Mutant"}

	assert.Equal(t, "first", event.StatusPatches[0].Name, "event's StatusPatches must not share backing array with caller")
	assert.Equal(t, 2, len(event.StatusPatches))
}

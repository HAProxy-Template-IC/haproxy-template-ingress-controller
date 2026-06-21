// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// NewProposalValidationRequestedEvent has three contract clauses that
// are easy to silently regress and have no direct test:
//
//  1. Each call generates a fresh unique RequestID. HTTPStore and
//     Webhook rely on this for response correlation; if two
//     concurrent requests collided their responses would cross.
//  2. The Overlays map is defensively copied. Callers are free to
//     mutate the map they passed in (e.g. release a buffer back to
//     a pool) without polluting the event the bus is carrying.
//  3. EventType is the canonical proposal-request constant
//     (subscribers route on it).
//
// Pin all three so a refactor that swapped the random ID generator for a
// counter, dropped the maps.Copy, or changed the event-type string fails
// loudly.
func TestNewProposalValidationRequestedEvent(t *testing.T) {
	t.Run("generates unique RequestID per call", func(t *testing.T) {
		e1 := NewProposalValidationRequestedEvent(nil, nil, "httpstore", "url-A")
		e2 := NewProposalValidationRequestedEvent(nil, nil, "httpstore", "url-B")
		e3 := NewProposalValidationRequestedEvent(nil, nil, "webhook", "ns/name")

		assert.NotEmpty(t, e1.RequestID(), "RequestID must be non-empty for response correlation")
		assert.NotEqual(t, e1.RequestID(), e2.RequestID(),
			"each call must yield a fresh ID; collisions would cross responses between concurrent requests")
		assert.NotEqual(t, e1.RequestID(), e3.RequestID())
		assert.NotEqual(t, e2.RequestID(), e3.RequestID())
	})

	t.Run("Overlays map is defensively copied (caller mutation does not leak)", func(t *testing.T) {
		// Build a real overlay so the test exercises the actual map
		// type, not just the empty-map shape.
		overlays := map[string]*stores.StoreOverlay{
			"ingresses": stores.NewStoreOverlayForDelete("ns", "old-ingress"),
		}

		event := NewProposalValidationRequestedEvent(overlays, nil, "webhook", "ingresses/ns/old-ingress")

		// Caller now mutates the original map. If the event held a
		// reference (no defensive copy), the new entry would appear
		// in event.Overlays and a downstream subscriber would see a
		// proposal it never received a request for.
		overlays["services"] = stores.NewStoreOverlayForDelete("ns", "leaked")

		require.Contains(t, event.Overlays, "ingresses",
			"original entry must survive into the event")
		assert.NotContains(t, event.Overlays, "services",
			"post-construction caller mutation must not leak into the event (defensive copy)")
	})

	t.Run("EventType returns the canonical proposal-request constant", func(t *testing.T) {
		event := NewProposalValidationRequestedEvent(nil, nil, "any", "any")
		assert.Equal(t, EventTypeProposalValidationRequested, event.EventType(),
			"subscribers route on this constant; changing it silently would orphan all current subscribers")
	})

	t.Run("preserves Source and SourceContext verbatim", func(t *testing.T) {
		event := NewProposalValidationRequestedEvent(nil, nil, "httpstore", "https://example.com/cfg.yaml")
		assert.Equal(t, "httpstore", event.Source)
		assert.Equal(t, "https://example.com/cfg.yaml", event.SourceContext)
	})
}

// The completion-event constructors split into two paths (success vs
// failure) but produce the SAME concrete type — Valid is the
// discriminator. That means every consumer's switch on Valid plus
// "is Phase / Error meaningful?" is implicitly relying on the
// constructor populating the right combination of fields.
//
// Pin both directions plus the load-bearing nil-error handling on
// the failure path: if a caller passes err == nil to the failure
// constructor, the event must still be Valid=false (the caller
// already decided it failed) but Error must be the empty string,
// not the literal text "<nil>" from a naive err.Error() call.
func TestNewProposalValidationCompletedEvent(t *testing.T) {
	t.Run("success constructor produces Valid=true with no phase/error", func(t *testing.T) {
		event := NewProposalValidationCompletedEvent("req-123", 42)

		assert.Equal(t, "req-123", event.RequestID)
		assert.True(t, event.Valid, "success constructor must set Valid=true")
		assert.Empty(t, event.Phase, "success path must not carry a failure phase")
		assert.Empty(t, event.Error, "success path must not carry an error message")
		assert.Equal(t, int64(42), event.DurationMs)
		assert.Equal(t, EventTypeProposalValidationCompleted, event.EventType())
	})
}

func TestNewProposalValidationFailedEvent(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
		phase     string
		err       error
		want      string
	}{
		{
			name:      "non-nil error serialises to err.Error()",
			requestID: "req-A",
			phase:     "syntax",
			err:       errors.New("missing 'global' section"),
			want:      "missing 'global' section",
		},
		{
			name:      "nil error becomes empty string (no '<nil>' literal)",
			requestID: "req-B",
			phase:     "render",
			err:       nil,
			want:      "",
		},
		{
			name:      "wrapped error: only the .Error() text is captured",
			requestID: "req-C",
			phase:     "semantic",
			err:       errors.New("haproxy validation failed: parsing line 42"),
			want:      "haproxy validation failed: parsing line 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewProposalValidationFailedEvent(tt.requestID, tt.phase, tt.err, 99)

			assert.Equal(t, tt.requestID, event.RequestID)
			assert.False(t, event.Valid,
				"failure constructor must always set Valid=false, even when caller passes nil err")
			assert.Equal(t, tt.phase, event.Phase, "phase must round-trip verbatim for downstream classification")
			assert.Equal(t, tt.want, event.Error)
			assert.Equal(t, int64(99), event.DurationMs)
			assert.Equal(t, EventTypeProposalValidationCompleted, event.EventType(),
				"success and failure constructors must produce the SAME event type so consumers can subscribe once")
		})
	}
}

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package parser

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// normalizeGlobalMetadata is the per-section helper for the global section.
// It iterates LogTargetList and runs NormalizeMetadata on each entry's
// Metadata map. Pin the contracts:
//   - nil global is a safe no-op (defensive guard)
//   - empty LogTargetList leaves the section untouched
//   - nested {"value": X} metadata is flattened in-place
//   - already-flat metadata passes through unchanged
//   - all entries in LogTargetList are visited (not just the first)
//
// These pin the same observable behaviour the public NormalizeConfigMetadata
// relies on for the global section.
func TestNormalizeGlobalMetadata(t *testing.T) {
	t.Run("nil global is a no-op (defensive guard against partial parse)", func(t *testing.T) {
		assert.NotPanics(t, func() {
			normalizeGlobalMetadata(nil)
		})
	})

	t.Run("empty LogTargetList leaves the section untouched", func(t *testing.T) {
		g := &models.Global{}
		normalizeGlobalMetadata(g)
		assert.Empty(t, g.LogTargetList)
	})

	t.Run("nested metadata is flattened in-place across every log target", func(t *testing.T) {
		g := &models.Global{
			LogTargetList: models.LogTargets{
				&models.LogTarget{
					Metadata: map[string]any{
						"comment": map[string]any{"value": "syslog target"},
					},
				},
				&models.LogTarget{
					Metadata: map[string]any{
						"region": map[string]any{"value": "us-east"},
					},
				},
			},
		}

		normalizeGlobalMetadata(g)

		assert.Equal(t, "syslog target", g.LogTargetList[0].Metadata["comment"],
			"first log target's nested metadata must be flattened")
		assert.Equal(t, "us-east", g.LogTargetList[1].Metadata["region"],
			"second log target's nested metadata must also be flattened — every entry visited")
	})

	t.Run("already-flat metadata passes through unchanged", func(t *testing.T) {
		g := &models.Global{
			LogTargetList: models.LogTargets{
				&models.LogTarget{
					Metadata: map[string]any{
						"comment": "Pod: echo-server",
						"custom":  "foo",
					},
				},
			},
		}

		normalizeGlobalMetadata(g)

		assert.Equal(t, "Pod: echo-server", g.LogTargetList[0].Metadata["comment"])
		assert.Equal(t, "foo", g.LogTargetList[0].Metadata["custom"])
	})

	t.Run("nil metadata on a log target is normalized to nil (the documented contract)", func(t *testing.T) {
		// NormalizeMetadata returns nil for nil/empty input; the helper just
		// reassigns whatever it gets back. Pin that the result is nil rather
		// than an empty-map allocation.
		g := &models.Global{
			LogTargetList: models.LogTargets{
				&models.LogTarget{Metadata: nil},
			},
		}

		normalizeGlobalMetadata(g)

		assert.Nil(t, g.LogTargetList[0].Metadata)
	})

	t.Run("mixed flat/nested entries are normalized independently", func(t *testing.T) {
		g := &models.Global{
			LogTargetList: models.LogTargets{
				&models.LogTarget{
					Metadata: map[string]any{
						"flat":   "value-a",
						"nested": map[string]any{"value": "value-b"},
					},
				},
			},
		}

		normalizeGlobalMetadata(g)

		assert.Equal(t, "value-a", g.LogTargetList[0].Metadata["flat"])
		assert.Equal(t, "value-b", g.LogTargetList[0].Metadata["nested"])
	})
}

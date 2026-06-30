// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package parser

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeCrtStoresMetadata is the most complex of the per-section
// normalize helpers because it descends into a NESTED collection
// (CrtLoads) AND that collection is a `map[string]CrtLoad` of VALUES.
// Two contracts are load-bearing here that the simpler one-level
// helpers don't have:
//
//  1. The nested CrtLoad metadata is normalized — *and the result is
//     reassigned back into the map*. Because Go maps return value-type
//     elements by COPY, a regression that drops the
//     `crtStore.CrtLoads[k] = crtLoad` reassignment would silently leave
//     the map's CrtLoad entries with un-normalized metadata while making
//     the function look like it ran successfully (a local copy was
//     mutated in vain). This is the kind of subtle bug a coverage tool
//     can't catch without a behavioural check.
//
//  2. The CrtStore-level metadata is normalized BEFORE descending.
//     Reversing the order, or skipping the outer normalization, would
//     leave outer metadata in nested form.
//
// We pin both contracts in a single fixture per case so a regression
// affecting just the nested branch is caught even if the outer branch
// still works.

func TestNormalizeCrtStoresMetadata(t *testing.T) {
	t.Run("nil slice and empty slice are no-ops (defensive)", func(t *testing.T) {
		assert.NotPanics(t, func() {
			normalizeCrtStoresMetadata(nil)
			normalizeCrtStoresMetadata([]*models.CrtStore{})
		})
	})

	t.Run("nil entries inside the slice are skipped (defensive)", func(t *testing.T) {
		stores := []*models.CrtStore{nil, nil}
		assert.NotPanics(t, func() {
			normalizeCrtStoresMetadata(stores)
		}, "nil pointer must be silently skipped — a regression that "+
			"dereferenced before nil-checking would crash the parser on "+
			"partially-built fixtures")
	})

	t.Run("CrtStore-level metadata is flattened in place", func(t *testing.T) {
		stores := []*models.CrtStore{
			{
				CrtStoreBase: models.CrtStoreBase{
					Name:     "store-a",
					Metadata: map[string]any{"comment": map[string]any{"value": "outer-a"}},
				},
			},
		}

		normalizeCrtStoresMetadata(stores)

		assert.Equal(t, "outer-a", stores[0].Metadata["comment"],
			"outer CrtStore.Metadata must be flattened — without this, the "+
				"comparator sees nested vs. flat shapes from different sources "+
				"and emits spurious updates")
	})

	t.Run("nested CrtLoad metadata is flattened AND written back to the map", func(t *testing.T) {
		// This is the high-value branch — the map-value-copy gotcha.
		// Build a map of CrtLoad VALUES (not pointers) so we hit the
		// same path the parser produces.
		stores := []*models.CrtStore{
			{
				CrtStoreBase: models.CrtStoreBase{Name: "store-a"},
				CrtLoads: map[string]models.CrtLoad{
					"primary": {
						Certificate: "primary.pem",
						Metadata:    map[string]any{"region": map[string]any{"value": "us-east"}},
					},
					"secondary": {
						Certificate: "secondary.pem",
						Metadata:    map[string]any{"tier": map[string]any{"value": "fallback"}},
					},
				},
			},
		}

		normalizeCrtStoresMetadata(stores)

		// Re-read from the map (NOT from a pre-existing reference) — this
		// catches the regression where the local copy was normalized but
		// the map entry was never reassigned.
		require.Contains(t, stores[0].CrtLoads, "primary")
		require.Contains(t, stores[0].CrtLoads, "secondary")
		assert.Equal(t, "us-east", stores[0].CrtLoads["primary"].Metadata["region"],
			"nested CrtLoad metadata must be flattened AND the normalized "+
				"copy must be written back into the map; dropping the map "+
				"reassignment would leave metadata in nested form even though "+
				"the local copy looked normalized")
		assert.Equal(t, "fallback", stores[0].CrtLoads["secondary"].Metadata["tier"],
			"every CrtLoad entry must be visited, not just the first")
	})

	t.Run("outer and nested metadata are normalized together", func(t *testing.T) {
		// Composite check: a single fixture exercises both branches at
		// once, so a regression that drops one but keeps the other is
		// still caught by the assertion on the dropped branch.
		stores := []*models.CrtStore{
			{
				CrtStoreBase: models.CrtStoreBase{
					Name:     "store-a",
					Metadata: map[string]any{"owner": map[string]any{"value": "platform-team"}},
				},
				CrtLoads: map[string]models.CrtLoad{
					"primary": {
						Certificate: "primary.pem",
						Metadata:    map[string]any{"sla": map[string]any{"value": "99.9"}},
					},
				},
			},
		}

		normalizeCrtStoresMetadata(stores)

		assert.Equal(t, "platform-team", stores[0].Metadata["owner"])
		assert.Equal(t, "99.9", stores[0].CrtLoads["primary"].Metadata["sla"])
	})

	t.Run("multiple CrtStores are all visited", func(t *testing.T) {
		// A regression that broke the outer for-loop early (e.g. accidental
		// `return` inside the loop) would only normalize the first store.
		stores := []*models.CrtStore{
			{
				CrtStoreBase: models.CrtStoreBase{
					Name:     "store-a",
					Metadata: map[string]any{"k": map[string]any{"value": "va"}},
				},
			},
			{
				CrtStoreBase: models.CrtStoreBase{
					Name:     "store-b",
					Metadata: map[string]any{"k": map[string]any{"value": "vb"}},
				},
			},
		}

		normalizeCrtStoresMetadata(stores)

		assert.Equal(t, "va", stores[0].Metadata["k"])
		assert.Equal(t, "vb", stores[1].Metadata["k"],
			"second store must also be visited — a regression that "+
				"broke the outer loop after the first iteration would silently "+
				"leave later stores un-normalized")
	})
}

// normalizeAcmeProvidersMetadata is the simpler one-level cousin —
// no nested collection, just iterate-and-normalize. Its branches are
// the same generic shape shared with normalizeRingsMetadata,
// normalizeFCGIAppsMetadata, and
// normalizeCachesMetadata, so the cases here also implicitly document
// the contract those siblings adhere to. The contract is:
//
//   - nil/empty slice → no-op (no panic)
//   - nil pointer entries → skipped (no panic, no false write)
//   - non-nil entries have their Metadata flattened in place
//   - every entry is visited (no early break)

func TestNormalizeAcmeProvidersMetadata(t *testing.T) {
	t.Run("nil and empty slice are no-ops", func(t *testing.T) {
		assert.NotPanics(t, func() {
			normalizeAcmeProvidersMetadata(nil)
			normalizeAcmeProvidersMetadata([]*models.AcmeProvider{})
		})
	})

	t.Run("nil pointer entries are skipped without panic", func(t *testing.T) {
		assert.NotPanics(t, func() {
			normalizeAcmeProvidersMetadata([]*models.AcmeProvider{nil, nil})
		})
	})

	t.Run("nested metadata flattened across every provider", func(t *testing.T) {
		providers := []*models.AcmeProvider{
			{
				Name:     "letsencrypt",
				Metadata: map[string]any{"env": map[string]any{"value": "prod"}},
			},
			nil, // mid-slice nil must not stop iteration
			{
				Name:     "zerossl",
				Metadata: map[string]any{"env": map[string]any{"value": "staging"}},
			},
		}

		normalizeAcmeProvidersMetadata(providers)

		assert.Equal(t, "prod", providers[0].Metadata["env"])
		assert.Equal(t, "staging", providers[2].Metadata["env"],
			"a nil entry in the middle of the slice must NOT short-circuit "+
				"iteration — later entries must still be normalized")
	})

	t.Run("already-flat metadata passes through unchanged", func(t *testing.T) {
		providers := []*models.AcmeProvider{
			{
				Name:     "letsencrypt",
				Metadata: map[string]any{"env": "prod", "tier": "primary"},
			},
		}

		normalizeAcmeProvidersMetadata(providers)

		assert.Equal(t, "prod", providers[0].Metadata["env"])
		assert.Equal(t, "primary", providers[0].Metadata["tier"])
	})
}

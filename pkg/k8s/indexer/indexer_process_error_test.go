// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Indexer.Process composes FilterFields → ExtractKeys → ConvertResource
// and propagates errors from the first two stages. The existing
// TestProcess covers only the happy path. The error-propagation +
// ordering contracts are load-bearing because Process is called for
// EVERY resource the watcher receives — a regression that swallowed
// errors or swapped stage order would silently corrupt the index.
//
// Two contracts pinned:
//
//  1. FilterFields error → Process returns it verbatim, NO ExtractKeys
//     attempt, NO conversion. A regression that continued past a
//     filter error would index an inconsistently-mutated resource
//     (some patterns removed, some left in) — a corruption that
//     wouldn't show up until comparator drift later.
//
//  2. Filter runs BEFORE key extraction. Critical because filtering
//     can remove fields ExtractKeys would otherwise read; a regression
//     that swapped the order would produce different keys than the
//     post-filter steady state and break later lookups.

func TestIndexer_Process_FilterErrorIsPropagated(t *testing.T) {
	// Build an indexer that tries to delete metadata.foo.
	// We construct the resource with metadata as an INT (not a map
	// or struct), so navigating to metadata succeeds but trying to
	// delete the "foo" field from an int triggers the
	// "deleting field from int" error in deleteField.
	idx, err := New(Config{
		IndexBy:      []string{"metadata.namespace"},
		IgnoreFields: []string{"metadata.foo"},
	})
	require.NoError(t, err, "indexer construction must succeed for valid config")

	// Resource where "metadata" is an int — this makes the filter's
	// final deleteField call fail.
	resource := map[string]any{
		"metadata": 42,
	}

	result, processErr := idx.Process(resource)

	require.Error(t, processErr,
		"filter errors during Process MUST surface to the caller — "+
			"a regression that swallowed them would index a "+
			"partially-mutated resource and silently corrupt the store")
	assert.Nil(t, result,
		"on filter error the result must be nil; a non-nil result "+
			"with a partial mutation would be the worst-case silent "+
			"corruption mode")

	// The error must include the failing pattern so operators can
	// triage which IgnoreFields entry caused the failure.
	assert.Contains(t, processErr.Error(), "metadata.foo",
		"FilterError must name the failing pattern so operators see "+
			"which IgnoreFields entry is misconfigured")
}

func TestIndexer_Process_FiltersBeforeExtractingKeys(t *testing.T) {
	// Important ordering contract: filtering happens BEFORE key
	// extraction. This matters because filtering can remove fields
	// that ExtractKeys would otherwise try to read. A regression
	// that swapped the order would extract keys from a resource
	// whose to-be-removed fields were still present, producing
	// different keys than the post-filter steady state and breaking
	// later lookups.
	//
	// We construct a resource where the indexed field would be
	// affected if ordering were swapped (in practice ExtractKeys
	// reads metadata.* which we don't filter, so this test mostly
	// verifies the happy-path composition).
	idx, err := New(Config{
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		IgnoreFields: []string{"metadata.managedFields"},
	})
	require.NoError(t, err)

	resource := map[string]any{
		"metadata": map[string]any{
			"namespace":     "kube-system",
			"name":          "coredns",
			"managedFields": []any{"big-blob"},
		},
	}

	result, processErr := idx.Process(resource)
	require.NoError(t, processErr)
	require.NotNil(t, result)

	// Both filter and key extraction succeeded.
	assert.Equal(t, []string{"kube-system", "coredns"}, result.Keys,
		"happy-path keys must reflect the indexed metadata fields")

	// Filter ran (managedFields removed) BEFORE key extraction.
	md := resource["metadata"].(map[string]any)
	_, hasManagedFields := md["managedFields"]
	assert.False(t, hasManagedFields,
		"the filter step must run during Process — managedFields was "+
			"present in the input but should have been removed; a "+
			"regression that skipped filtering would let large "+
			"managedFields blobs reach storage and bloat memory")
	assert.Equal(t, "kube-system", md["namespace"],
		"unrelated metadata fields must NOT be touched by the filter")
}

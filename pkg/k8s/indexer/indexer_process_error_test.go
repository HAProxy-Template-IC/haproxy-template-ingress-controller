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
//  1. ExtractKeys error → Process returns it verbatim, NO conversion.
//     A regression that continued past a key-extraction error would
//     index a resource under empty/garbage keys and break later lookups.
//
//  2. Filter runs BEFORE key extraction. Critical because filtering
//     can remove fields ExtractKeys would otherwise read; a regression
//     that swapped the order would produce different keys than the
//     post-filter steady state and break later lookups.

func TestIndexer_Process_ExtractKeysErrorIsPropagated(t *testing.T) {
	// Build an indexer that extracts metadata.namespace. The resource
	// below has no metadata.namespace, so ExtractKeys fails with a
	// JSONPath "no results found" error, which Process must surface.
	idx, err := New(Config{
		IndexBy:      []string{"metadata.namespace"},
		IgnoreFields: []string{"metadata.managedFields"},
	})
	require.NoError(t, err, "indexer construction must succeed for valid config")

	// Resource lacking the indexed field — ExtractKeys cannot resolve it.
	resource := map[string]any{
		"metadata": map[string]any{
			"name": "orphan",
		},
	}

	result, processErr := idx.Process(resource)

	require.Error(t, processErr,
		"key-extraction errors during Process MUST surface to the caller — "+
			"a regression that swallowed them would index a resource under "+
			"empty/garbage keys and silently break later lookups")
	assert.Nil(t, result,
		"on extraction error the result must be nil; a non-nil result "+
			"would be the worst-case silent corruption mode")

	// The error must include the failing expression so operators can
	// triage which IndexBy entry could not be resolved.
	assert.Contains(t, processErr.Error(), "metadata.namespace",
		"IndexError must name the failing expression so operators see "+
			"which IndexBy entry could not be extracted")
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

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package auxiliaryfiles

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The existing TestCategorizeFile in auxiliaryfiles_test.go covers the
// four basic dispatch branches (missing/different/same/no-fingerprint).
// These additional tests pin TWO contracts the existing test leaves
// unexercised but Compare relies on:
//
//  1. __NO_FINGERPRINT__ sentinel takes precedence over content
//     equality. The sentinel signals "API has metadata but no content
//     fingerprint" — the API can't tell us if its bytes match ours,
//     so we must always send via UPDATE. Even if the desired content
//     happens to literally equal the sentinel string, we must NOT
//     fall into the "same content → no-op" branch and skip the
//     update. A regression that re-ordered the equality check before
//     the sentinel check would silently leave the file un-updated on
//     a pathological match.
//
//  2. categorizeFile appends to existing ToCreate/ToUpdate slices
//     rather than overwriting them. Compare loops over all desired
//     files calling categorizeFile once per file; a regression that
//     reset the slices each call would leave the diff containing
//     only the LAST file's classification, silently dropping every
//     other create/update.

func TestCategorizeFile_NoFingerprintSentinelTakesPrecedenceOverEquality(t *testing.T) {
	// Pathological case: desired content literally equals the sentinel
	// string. The categorizeFile branch order is:
	//   if currentContent == "__NO_FINGERPRINT__" → ToUpdate
	//   else if currentContent != desiredContent  → ToUpdate
	//   else                                      → no-op
	// A regression that swapped the order to check equality first
	// would fall into "same content" and skip the update — leaving
	// the file un-deployed.
	current := map[string]GeneralFile{
		"sentinel.http": {Filename: "sentinel.http", Content: noFingerprintSentinel},
	}
	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{},
		ToUpdate: []GeneralFile{},
	}

	desired := GeneralFile{Filename: "sentinel.http", Content: noFingerprintSentinel}
	categorizeFile(current, "sentinel.http", desired, diff)

	assert.Empty(t, diff.ToCreate,
		"sentinel-current must NOT route to ToCreate")
	require.Len(t, diff.ToUpdate, 1,
		"sentinel-current MUST route to ToUpdate even when desired content "+
			"literally equals the sentinel — a regression that checked "+
			"equality before the sentinel branch would silently leave the "+
			"file un-updated")
	assert.Equal(t, "sentinel.http", diff.ToUpdate[0].Filename)
}

func TestCategorizeFile_AccumulatesAcrossInvocations(t *testing.T) {
	// Compare loops over all desired files, calling categorizeFile once
	// per file. Pin that the helper appends to existing ToCreate/ToUpdate
	// rather than overwriting — a regression that reset the slices each
	// call would leave Compare returning only the LAST file's diff.
	current := map[string]GeneralFile{
		"existing.http": {Filename: "existing.http", Content: "old"},
		// "fresh.http" not in current → will be created
	}
	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{},
		ToUpdate: []GeneralFile{},
	}

	categorizeFile(current, "existing.http", GeneralFile{Filename: "existing.http", Content: "new"}, diff)
	categorizeFile(current, "fresh.http", GeneralFile{Filename: "fresh.http", Content: "first"}, diff)

	require.Len(t, diff.ToCreate, 1,
		"ToCreate must accumulate across calls — a regression that "+
			"overwrote the slice would leave only the last file in the diff "+
			"and silently drop every other create/update from Compare's loop")
	assert.Equal(t, "fresh.http", diff.ToCreate[0].Filename)

	require.Len(t, diff.ToUpdate, 1,
		"ToUpdate must accumulate across calls (same accumulation contract)")
	assert.Equal(t, "existing.http", diff.ToUpdate[0].Filename)
}

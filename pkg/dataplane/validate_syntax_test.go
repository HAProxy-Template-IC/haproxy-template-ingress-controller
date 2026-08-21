// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getCachedValidatorForVersion is the read-side dispatcher that picks a
// per-HAProxy-version CachedValidator from one of four lazily-initialised
// slots (v3.0, v3.1, v3.2, v3.3). The mapping rule is non-obvious in two
// places:
//
//   - Unknown / nil version conservatively falls back to the v3.0 slot
//     (most restrictive schema).
//   - Versions NEWER than v3.3 (e.g. v3.4, v4.0) intentionally clamp to
//     the v3.3 validator because v3.3 is "the newest schema currently
//     bundled". This is a documented design choice — a refactor that
//     started routing v3.4+ to a non-existent v3.4 slot, or that crashed
//     on unknown versions, would be a regression.
//
// Pin every branch (nil, < v3.0, exact v3.0/v3.1/v3.2/v3.3, > v3.3 by
// minor, > v3.x by major) plus the slot reuse / lazy-init contract on
// repeated calls.
func TestGetCachedValidatorForVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  *Version
		wantSlot *cachedValidatorSlot
	}{
		{
			name:     "nil version falls back to v3.0 (most restrictive default)",
			version:  nil,
			wantSlot: validatorSlotV30,
		},
		{
			name:     "Major 0 (zero value) falls back to v3.0",
			version:  &Version{Major: 0, Minor: 0},
			wantSlot: validatorSlotV30,
		},
		{
			name:     "Major < 3 (e.g. legacy v2.x) falls back to v3.0",
			version:  &Version{Major: 2, Minor: 8},
			wantSlot: validatorSlotV30,
		},
		{
			name:     "v3.0 exact maps to v30 slot",
			version:  &Version{Major: 3, Minor: 0},
			wantSlot: validatorSlotV30,
		},
		{
			name:     "v3.1 exact maps to v31 slot",
			version:  &Version{Major: 3, Minor: 1},
			wantSlot: validatorSlotV31,
		},
		{
			name:     "v3.2 exact maps to v32 slot",
			version:  &Version{Major: 3, Minor: 2},
			wantSlot: validatorSlotV32,
		},
		{
			name:     "v3.3 exact maps to v33 slot",
			version:  &Version{Major: 3, Minor: 3},
			wantSlot: validatorSlotV33,
		},
		{
			name:     "v3.4 (newer minor than bundled) clamps to v33 (newest bundled schema)",
			version:  &Version{Major: 3, Minor: 4},
			wantSlot: validatorSlotV33,
		},
		{
			name:     "v3.99 (far-future minor) still clamps to v33",
			version:  &Version{Major: 3, Minor: 99},
			wantSlot: validatorSlotV33,
		},
		{
			name:     "Major > 3 (e.g. v4.x) clamps to v33 (no v4 slot exists)",
			version:  &Version{Major: 4, Minor: 0},
			wantSlot: validatorSlotV33,
		},
		{
			name:     "Major > 3 with high minor still clamps to v33",
			version:  &Version{Major: 5, Minor: 7},
			wantSlot: validatorSlotV33,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCachedValidatorForVersion(tt.version)
			require.NotNil(t, got, "getCachedValidatorForVersion must never return nil; nil-version callers expect a working default validator")
			assert.Same(t, tt.wantSlot.get(), got,
				"version %v must route to the %d.%d validator slot", tt.version, tt.wantSlot.major, tt.wantSlot.minor)
		})
	}
}

// The per-version slots use sync.Once so that the first call lazily
// constructs the CachedValidator and every subsequent call returns the
// same instance. This matters because:
//
//   - A new CachedValidator builds an empty Cache, so a freshly-built
//     validator has zero memoisation across reconciliations.
//   - Multiple validator instances would defeat the cache and quietly
//     regress validation throughput under load.
//
// Pin the slot identity contract: repeated calls for the same version
// must return the SAME *CachedValidator, and different versions must
// return different *CachedValidators.
func TestGetCachedValidatorForVersion_SlotReuse(t *testing.T) {
	v30a := getCachedValidatorForVersion(&Version{Major: 3, Minor: 0})
	v30b := getCachedValidatorForVersion(&Version{Major: 3, Minor: 0})
	v30nil := getCachedValidatorForVersion(nil)
	v31a := getCachedValidatorForVersion(&Version{Major: 3, Minor: 1})
	v31b := getCachedValidatorForVersion(&Version{Major: 3, Minor: 1})
	v32a := getCachedValidatorForVersion(&Version{Major: 3, Minor: 2})
	v32b := getCachedValidatorForVersion(&Version{Major: 3, Minor: 2})
	v33a := getCachedValidatorForVersion(&Version{Major: 3, Minor: 3})
	v33b := getCachedValidatorForVersion(&Version{Major: 3, Minor: 3})
	v34 := getCachedValidatorForVersion(&Version{Major: 3, Minor: 4})

	// Same-version calls must return the SAME pointer (sync.Once
	// guarantees one CachedValidator per slot).
	assert.Same(t, v30a, v30b, "repeated v3.0 lookups must return the same cached validator")
	assert.Same(t, v30a, v30nil, "nil-version fallback must reuse the v3.0 slot, not allocate fresh")
	assert.Same(t, v31a, v31b, "repeated v3.1 lookups must return the same cached validator")
	assert.Same(t, v32a, v32b, "repeated v3.2 lookups must return the same cached validator")
	assert.Same(t, v33a, v33b, "repeated v3.3 lookups must return the same cached validator")
	assert.Same(t, v33a, v34, "v3.4 clamp must reuse the v3.3 slot, not allocate fresh")

	// Different versions must return DIFFERENT pointers (slot
	// segregation: a v3.0 cache must not pollute v3.1 / v3.2 / v3.3).
	assert.NotSame(t, v30a, v31a, "v3.0 and v3.1 must use distinct slots / caches")
	assert.NotSame(t, v30a, v32a, "v3.0 and v3.2 must use distinct slots / caches")
	assert.NotSame(t, v31a, v32a, "v3.1 and v3.2 must use distinct slots / caches")
	assert.NotSame(t, v32a, v33a, "v3.2 and v3.3 must use distinct slots / caches")
}

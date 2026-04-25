// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getCachedValidatorForVersion is the read-side dispatcher that picks a
// per-HAProxy-version CachedValidator from one of three lazily-initialised
// slots (v3.0, v3.1, v3.2). The mapping rule is non-obvious in two
// places:
//
//   - Unknown / nil version conservatively falls back to the v3.0 slot
//     (most restrictive schema).
//   - Versions NEWER than v3.2 (e.g. v3.3, v4.0) intentionally clamp to
//     the v3.2 validator because v3.2 is "the newest schema currently
//     bundled". This is a documented design choice — a refactor that
//     started routing v3.3+ to a non-existent v3.3 slot, or that crashed
//     on unknown versions, would be a regression.
//
// Pin every branch (nil, < v3.0, exact v3.0/v3.1/v3.2, > v3.2 by minor,
// > v3.x by major) plus the slot reuse / lazy-init contract on
// repeated calls.
func TestGetCachedValidatorForVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     *Version
		wantVersion string // ValidatorSet().Version() returns "v30" / "v31" / "v32"
	}{
		{
			name:        "nil version falls back to v3.0 (most restrictive default)",
			version:     nil,
			wantVersion: "v30",
		},
		{
			name:        "Major 0 (zero value) falls back to v3.0",
			version:     &Version{Major: 0, Minor: 0},
			wantVersion: "v30",
		},
		{
			name:        "Major < 3 (e.g. legacy v2.x) falls back to v3.0",
			version:     &Version{Major: 2, Minor: 8},
			wantVersion: "v30",
		},
		{
			name:        "v3.0 exact maps to v30 slot",
			version:     &Version{Major: 3, Minor: 0},
			wantVersion: "v30",
		},
		{
			name:        "v3.1 exact maps to v31 slot",
			version:     &Version{Major: 3, Minor: 1},
			wantVersion: "v31",
		},
		{
			name:        "v3.2 exact maps to v32 slot",
			version:     &Version{Major: 3, Minor: 2},
			wantVersion: "v32",
		},
		{
			name:        "v3.3 (newer minor than bundled) clamps to v32 (newest bundled schema)",
			version:     &Version{Major: 3, Minor: 3},
			wantVersion: "v32",
		},
		{
			name:        "v3.99 (far-future minor) still clamps to v32",
			version:     &Version{Major: 3, Minor: 99},
			wantVersion: "v32",
		},
		{
			name:        "Major > 3 (e.g. v4.x) clamps to v32 (no v4 slot exists)",
			version:     &Version{Major: 4, Minor: 0},
			wantVersion: "v32",
		},
		{
			name:        "Major > 3 with high minor still clamps to v32",
			version:     &Version{Major: 5, Minor: 7},
			wantVersion: "v32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCachedValidatorForVersion(tt.version)
			// Use require.NotNil so a nil return halts the test
			// instead of falling through to a nil-pointer panic on
			// got.ValidatorSet().
			require.NotNil(t, got, "getCachedValidatorForVersion must never return nil; nil-version callers expect a working default validator")
			assert.Equal(t, tt.wantVersion, got.ValidatorSet().Version(),
				"version %v must route to the %s validator slot", tt.version, tt.wantVersion)
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
	v33 := getCachedValidatorForVersion(&Version{Major: 3, Minor: 3})

	// Same-version calls must return the SAME pointer (sync.Once
	// guarantees one CachedValidator per slot).
	assert.Same(t, v30a, v30b, "repeated v3.0 lookups must return the same cached validator")
	assert.Same(t, v30a, v30nil, "nil-version fallback must reuse the v3.0 slot, not allocate fresh")
	assert.Same(t, v31a, v31b, "repeated v3.1 lookups must return the same cached validator")
	assert.Same(t, v32a, v32b, "repeated v3.2 lookups must return the same cached validator")
	assert.Same(t, v32a, v33, "v3.3 clamp must reuse the v3.2 slot, not allocate fresh")

	// Different versions must return DIFFERENT pointers (slot
	// segregation: a v3.0 cache must not pollute v3.1 / v3.2).
	assert.NotSame(t, v30a, v31a, "v3.0 and v3.1 must use distinct slots / caches")
	assert.NotSame(t, v30a, v32a, "v3.0 and v3.2 must use distinct slots / caches")
	assert.NotSame(t, v31a, v32a, "v3.1 and v3.2 must use distinct slots / caches")
}

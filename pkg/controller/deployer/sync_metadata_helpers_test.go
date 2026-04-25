// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// formatBackendDiffFields produces the human-readable string that the
// deployer puts on commentator events / Prometheus labels to explain
// WHICH BackendBase fields drove a backend update. The format is load-
// bearing — log scrapers and alert templates parse the bracketed
// "[Field1, Field2] (N backends)" shape, and pluralisation follows the
// usual English rule.
//
// Pin the contract:
//   - empty input -> empty string (no brackets at all)
//   - single backend in a group -> "[fields] (1 backend)" (singular noun)
//   - multiple backends sharing a field signature -> "(N backends)" (plural)
//   - fields inside one bucket are sorted alphabetically (signature
//     stability across reconciliations)
//   - groups themselves are sorted alphabetically (output stability)
//   - distinct field signatures produce distinct buckets
func TestFormatBackendDiffFields(t *testing.T) {
	tests := []struct {
		name string
		in   map[string][]string
		want string
	}{
		{
			name: "empty input yields empty string (no brackets)",
			in:   map[string][]string{},
			want: "",
		},
		{
			name: "nil input yields empty string",
			in:   nil,
			want: "",
		},
		{
			name: "single backend uses singular 'backend'",
			in: map[string][]string{
				"backend-a": {"Balance", "Mode"},
			},
			want: "[Balance, Mode] (1 backend)",
		},
		{
			name: "multiple backends sharing one field signature use plural 'backends'",
			in: map[string][]string{
				"backend-a": {"Mode"},
				"backend-b": {"Mode"},
				"backend-c": {"Mode"},
			},
			want: "[Mode] (3 backends)",
		},
		{
			name: "fields inside a bucket are sorted alphabetically (signature stability)",
			in: map[string][]string{
				// Caller hands us fields in any order. Output must
				// canonicalise so two reconciliations producing the
				// same set always emit the same string.
				"backend-a": {"Mode", "Balance", "AdvCheck"},
			},
			want: "[AdvCheck, Balance, Mode] (1 backend)",
		},
		{
			name: "distinct field signatures produce distinct buckets, joined and sorted",
			in: map[string][]string{
				"backend-a": {"Mode"},
				"backend-b": {"Balance"},
			},
			want: "[Balance] (1 backend), [Mode] (1 backend)",
		},
		{
			name: "mixed: some buckets singular, some plural, all alphabetised",
			in: map[string][]string{
				"backend-a": {"Mode"},
				"backend-b": {"Balance", "AdvCheck"},
				"backend-c": {"Balance", "AdvCheck"},
				"backend-d": {"Mode"},
				"backend-e": {"Mode"},
			},
			want: "[AdvCheck, Balance] (2 backends), [Mode] (3 backends)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBackendDiffFields(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// safeIntToInt32 is the bounds-clamping helper used wherever the
// deployer pushes counts into int32 fields (Kubernetes Status replicas,
// Prometheus gauges typed as int32, etc.). The clamping behaviour is
// deliberately silent — callers expect MaxInt32/MinInt32 instead of an
// overflow panic — so any refactor that swapped clamping for wrapping
// or for an error return would be a silent regression.
//
// Pin every numeric boundary:
//   - 0, positive, negative passthrough in range
//   - MaxInt32 and MinInt32 are returned exactly (boundary inclusive)
//   - MaxInt32+1 clamps to MaxInt32 (no wraparound to negative)
//   - MinInt32-1 clamps to MinInt32 (no wraparound to positive)
//   - max int / min int (platform-dependent ints) clamp to MaxInt32 /
//     MinInt32 respectively
func TestSafeIntToInt32(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int32
	}{
		{name: "zero passes through", in: 0, want: 0},
		{name: "small positive passes through", in: 42, want: 42},
		{name: "small negative passes through", in: -42, want: -42},
		{name: "MaxInt32 boundary returned exactly", in: math.MaxInt32, want: math.MaxInt32},
		{name: "MinInt32 boundary returned exactly", in: math.MinInt32, want: math.MinInt32},
		{name: "MaxInt32+1 clamps to MaxInt32 (no overflow wraparound)", in: math.MaxInt32 + 1, want: math.MaxInt32},
		{name: "MinInt32-1 clamps to MinInt32 (no underflow wraparound)", in: math.MinInt32 - 1, want: math.MinInt32},
		{name: "very large positive clamps to MaxInt32", in: math.MaxInt64, want: math.MaxInt32},
		{name: "very large negative clamps to MinInt32", in: math.MinInt64, want: math.MinInt32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeIntToInt32(tt.in)
			assert.Equal(t, tt.want, got,
				"safeIntToInt32(%d) must clamp to int32 range, never wrap or overflow", tt.in)
		})
	}
}

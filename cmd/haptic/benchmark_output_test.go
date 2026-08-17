// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// truncateString lives in cmd/haptic/benchmark_output.go and is used
// to fit benchmark file names into the fixed-width table. Note that
// pkg/controller/testrunner has a same-named helper with DIFFERENT
// semantics (no max-len underflow guard, "..." appended without
// shrinking the slice prefix). The cmd/haptic version pins:
//
//   - Strings shorter or equal to maxLen pass through verbatim.
//   - Strings longer than maxLen are shrunk to maxLen total bytes,
//     with the LAST 3 bytes replaced by "..." — so the visible width
//     is exactly maxLen, not maxLen+3.
//   - When maxLen <= 3 there isn't room for the ellipsis. The
//     function falls back to a hard truncate without "..." rather
//     than panicking on the negative slice index `s[:maxLen-3]`.
//     This guard is the load-bearing part: a refactor that dropped
//     it would crash the benchmark output formatter on any column
//     narrower than 4 characters.
func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{name: "fits exactly", input: "abc", maxLen: 3, want: "abc"},
		{name: "shorter than maxLen passes through", input: "ab", maxLen: 5, want: "ab"},
		{name: "empty string passes through", input: "", maxLen: 5, want: ""},
		{name: "long string is truncated to maxLen including ellipsis", input: "abcdefghij", maxLen: 6, want: "abc..."},
		{name: "truncated output fits within maxLen exactly", input: "abcdefghij", maxLen: 8, want: "abcde..."},
		{name: "maxLen=4 (smallest size that still permits ellipsis)", input: "abcdef", maxLen: 4, want: "a..."},
		// The maxLen <= 3 guard prevents a negative slice index from
		// `s[:maxLen-3]`. A refactor that dropped this guard would
		// panic with "runtime error: slice bounds out of range" on
		// any narrow column.
		{name: "maxLen=3 hard-truncates without ellipsis (no room for '...')", input: "abcdef", maxLen: 3, want: "abc"},
		{name: "maxLen=2 hard-truncates without ellipsis", input: "abcdef", maxLen: 2, want: "ab"},
		{name: "maxLen=1 hard-truncates without ellipsis", input: "abcdef", maxLen: 1, want: "a"},
		{name: "maxLen=0 returns empty string (no panic)", input: "abcdef", maxLen: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.want, got,
				"truncateString(%q, %d) must respect the maxLen cap and never panic on the maxLen-3 underflow",
				tt.input, tt.maxLen)
			if tt.maxLen >= 0 {
				assert.LessOrEqual(t, len(got), tt.maxLen,
					"output length must be <= maxLen so the table column never overflows")
			}
		})
	}
}

// shortenTestName strips the conventional "benchmark-" prefix from
// validation-test names so the column header in the benchmark table
// stays compact. Two contracts:
//   - Names with the prefix have it removed.
//   - Names without the prefix pass through unchanged (no false-prefix
//     trimming, no panic).
//
// A regression that, e.g., used Trim(name, "benchmark-") instead of
// TrimPrefix would silently strip ALL leading characters that happened
// to be in "benchmark-" — turning "be-aware" into "ware".
func TestShortenTestName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "removes 'benchmark-' prefix", in: "benchmark-render-routes", want: "render-routes"},
		{name: "no prefix passes through unchanged", in: "render-routes", want: "render-routes"},
		{name: "empty string passes through", in: "", want: ""},
		{name: "prefix-only string becomes empty", in: "benchmark-", want: ""},
		{
			// Names that happen to share characters with the prefix
			// must NOT be eaten. A naive Trim instead of TrimPrefix
			// would shrink "be-aware" -> "ware" silently.
			name: "name with shared prefix characters is not eaten",
			in:   "be-aware-of-trim-vs-trimprefix",
			want: "be-aware-of-trim-vs-trimprefix",
		},
		{
			// Inner occurrence is NOT a prefix; do not strip.
			name: "inner 'benchmark-' substring is not removed",
			in:   "test-benchmark-routes",
			want: "test-benchmark-routes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shortenTestName(tt.in))
		})
	}
}

// extractFileNames pulls the file-name list from the first iteration
// of the first benchmark result. It assumes all tests rendered the
// same set of files (the benchmark contract). Pin three behaviours:
//   - Empty Iterations on the first result -> nil (no panic on
//     out-of-range index).
//   - Single iteration with multiple files -> names in order.
//   - Order is preserved (not sorted), because the table column
//     order MUST match the iteration order of FileResults; sorting
//     would put data in the wrong cells.
func TestExtractFileNames(t *testing.T) {
	t.Run("empty iterations returns nil", func(t *testing.T) {
		// A refactor that didn't guard against empty Iterations would
		// panic with "index out of range [0]".
		got := extractFileNames([]*BenchmarkResult{
			{TestName: "no-iterations", Iterations: nil},
		})
		assert.Nil(t, got, "empty Iterations must yield nil, not panic on Iterations[0]")
	})

	t.Run("single iteration returns file names in iteration order", func(t *testing.T) {
		results := []*BenchmarkResult{
			{
				TestName: "primary",
				Iterations: []IterationResult{
					{
						TotalTime: 10 * time.Millisecond,
						FileResults: []FileRenderResult{
							{Name: "haproxy.cfg"},
							{Name: "maps/host.map"},
							{Name: "maps/path.map"},
						},
					},
				},
			},
		}

		got := extractFileNames(results)

		// Order is load-bearing: printTableDataRows pairs file index
		// against fileResults[fileIdx]; if extractFileNames sorted
		// alphabetically, the table would put data in the wrong rows.
		assert.Equal(t, []string{"haproxy.cfg", "maps/host.map", "maps/path.map"}, got,
			"file order must match the iteration order of FileResults; sorting would put data in the wrong table cells")
	})

	t.Run("uses ONLY the first result's first iteration (other results ignored)", func(t *testing.T) {
		// The contract is "all tests render the same files", so the
		// first result is treated as authoritative. A refactor that
		// scanned all results and took the union would produce a
		// table layout that didn't match the headers.
		results := []*BenchmarkResult{
			{
				TestName: "first",
				Iterations: []IterationResult{
					{FileResults: []FileRenderResult{{Name: "first.cfg"}}},
				},
			},
			{
				TestName: "second",
				Iterations: []IterationResult{
					{FileResults: []FileRenderResult{{Name: "second.cfg"}}},
				},
			},
		}

		got := extractFileNames(results)
		assert.Equal(t, []string{"first.cfg"}, got,
			"extractFileNames must read ONLY the first result; the contract is that all results render the same files")
	})
}

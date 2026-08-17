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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// mergeIncludeStat is the per-name aggregator that the include-timing
// profile depends on. The function is small but has three distinct
// behaviours that all matter for the "Top 20 slowest" report users
// see at the end of `controller validate` and `controller benchmark`:
//
//  1. First sighting of a name MUST create a fresh entry copying all
//     four fields verbatim. A regression that initialised AvgMs from
//     the first stat (instead of leaving it 0 for the post-aggregate
//     calculation) would produce wrong averages after the first call.
//  2. Subsequent sightings MUST add Count and TotalMs (cumulative).
//     A refactor that overwrote instead of accumulated would silently
//     under-count repeat-renders in loops.
//  3. MaxMs MUST only update when the incoming value is GREATER
//     (running max, not last-seen). A refactor that always assigned
//     would corrupt the "Max(ms)" column to the last value seen
//     rather than the slowest single render.
func TestMergeIncludeStat(t *testing.T) {
	t.Run("first sighting creates fresh entry with all fields", func(t *testing.T) {
		agg := map[string]*templating.IncludeStats{}
		mergeIncludeStat(agg, templating.IncludeStats{
			Name:    "snippets/header",
			Count:   3,
			TotalMs: 12.5,
			MaxMs:   5.0,
		})

		require.Len(t, agg, 1)
		entry := agg["snippets/header"]
		require.NotNil(t, entry)
		assert.Equal(t, "snippets/header", entry.Name)
		assert.Equal(t, 3, entry.Count)
		assert.InEpsilon(t, 12.5, entry.TotalMs, 0.0001)
		assert.InEpsilon(t, 5.0, entry.MaxMs, 0.0001)
		assert.Zero(t, entry.AvgMs,
			"AvgMs must remain 0 on first sighting; it's computed in aggregateIncludeStatsFromSlices "+
				"AFTER all merges complete, not by mergeIncludeStat itself")
	})

	t.Run("subsequent sightings accumulate Count and TotalMs", func(t *testing.T) {
		agg := map[string]*templating.IncludeStats{}
		mergeIncludeStat(agg, templating.IncludeStats{Name: "x", Count: 2, TotalMs: 4.0, MaxMs: 3.0})
		mergeIncludeStat(agg, templating.IncludeStats{Name: "x", Count: 5, TotalMs: 11.0, MaxMs: 2.0})

		entry := agg["x"]
		assert.Equal(t, 7, entry.Count, "Count must accumulate across merges")
		assert.InEpsilon(t, 15.0, entry.TotalMs, 0.0001, "TotalMs must accumulate across merges")
	})

	t.Run("MaxMs is a running maximum, not last-seen", func(t *testing.T) {
		agg := map[string]*templating.IncludeStats{}

		// Three merges: 3.0, 8.0, 1.0. Max must end at 8.0.
		mergeIncludeStat(agg, templating.IncludeStats{Name: "y", Count: 1, TotalMs: 3.0, MaxMs: 3.0})
		mergeIncludeStat(agg, templating.IncludeStats{Name: "y", Count: 1, TotalMs: 8.0, MaxMs: 8.0})
		mergeIncludeStat(agg, templating.IncludeStats{Name: "y", Count: 1, TotalMs: 1.0, MaxMs: 1.0})

		assert.InEpsilon(t, 8.0, agg["y"].MaxMs, 0.0001,
			"MaxMs must track the highest seen value, not the last-seen value; "+
				"a refactor that always assigned would silently corrupt the slowest-render column")
	})
}

// aggregateIncludeStatsFromSlices is the public-facing aggregator that
// produces the include-timing report. It threads three behaviours that
// each have a load-bearing edge case:
//
//   - Across multiple worker stat slices, names are unioned with
//     cumulative Count/TotalMs and running MaxMs (delegated to
//     mergeIncludeStat, but verified end-to-end here so a refactor
//     that bypassed the helper still has to satisfy the contract).
//   - AvgMs is computed AFTER all merges as TotalMs/Count, ONLY for
//     entries with Count > 0. The Count > 0 guard prevents a divide-
//     by-zero panic on synthesised zero-count entries.
//   - The output slice is sorted by TotalMs descending — the report
//     header says "Top 20 slowest" and assumes [0] is the slowest.
func TestAggregateIncludeStatsFromSlices(t *testing.T) {
	t.Run("empty input yields empty output", func(t *testing.T) {
		got := aggregateIncludeStatsFromSlices(nil)
		assert.Empty(t, got, "nil input must yield empty result, not panic")

		got = aggregateIncludeStatsFromSlices([][]templating.IncludeStats{})
		assert.Empty(t, got, "empty input must yield empty result")
	})

	t.Run("union across multiple worker slices with cumulative totals", func(t *testing.T) {
		got := aggregateIncludeStatsFromSlices([][]templating.IncludeStats{
			{
				{Name: "header", Count: 2, TotalMs: 6.0, MaxMs: 4.0},
				{Name: "footer", Count: 1, TotalMs: 1.0, MaxMs: 1.0},
			},
			{
				{Name: "header", Count: 3, TotalMs: 9.0, MaxMs: 5.0},
				{Name: "body", Count: 10, TotalMs: 50.0, MaxMs: 8.0},
			},
		})

		require.Len(t, got, 3, "three distinct names across two worker slices must produce three entries")

		byName := indexByName(got)

		// header: cumulative Count=5, TotalMs=15, MaxMs=5 (max of 4,5)
		assert.Equal(t, 5, byName["header"].Count)
		assert.InEpsilon(t, 15.0, byName["header"].TotalMs, 0.0001)
		assert.InEpsilon(t, 5.0, byName["header"].MaxMs, 0.0001,
			"MaxMs must be the max across worker slices, not the last-seen value")

		// AvgMs computed at aggregation time: 15.0 / 5 = 3.0
		assert.InEpsilon(t, 3.0, byName["header"].AvgMs, 0.0001,
			"AvgMs must be TotalMs/Count, computed AFTER all merges (not per-merge)")

		// body: only seen once, full passthrough plus AvgMs
		assert.Equal(t, 10, byName["body"].Count)
		assert.InEpsilon(t, 50.0, byName["body"].TotalMs, 0.0001)
		assert.InEpsilon(t, 5.0, byName["body"].AvgMs, 0.0001)
	})

	t.Run("output is sorted by TotalMs descending (slowest first)", func(t *testing.T) {
		got := aggregateIncludeStatsFromSlices([][]templating.IncludeStats{
			{
				{Name: "fast", Count: 1, TotalMs: 1.0},
				{Name: "slowest", Count: 1, TotalMs: 100.0},
				{Name: "medium", Count: 1, TotalMs: 10.0},
			},
		})

		require.Len(t, got, 3)
		// "Top 20 slowest" report assumes [0] is the slowest. A
		// refactor that sorted ascending or alphabetically would
		// produce a "fastest first" report under a "slowest first"
		// header — silent and visually correct.
		assert.Equal(t, "slowest", got[0].Name, "[0] MUST be the slowest entry; the report header says 'Top N slowest'")
		assert.Equal(t, "medium", got[1].Name)
		assert.Equal(t, "fast", got[2].Name)
	})

	t.Run("zero-count entry does NOT divide-by-zero", func(t *testing.T) {
		// A worker that registered a name but never actually
		// rendered it could produce an entry with Count=0. The
		// Count > 0 guard inside aggregateIncludeStatsFromSlices
		// prevents the AvgMs computation from panicking on the
		// integer division. A refactor that dropped the guard
		// would crash the controller validate / benchmark report.
		require.NotPanics(t, func() {
			got := aggregateIncludeStatsFromSlices([][]templating.IncludeStats{
				{
					{Name: "registered-but-never-ran", Count: 0, TotalMs: 0, MaxMs: 0},
				},
			})
			require.Len(t, got, 1)
			assert.Zero(t, got[0].AvgMs,
				"zero-count entries must keep AvgMs=0 (the Count>0 guard skips the division)")
		})
	})
}

// sortedKeys is a tiny generic that the formatted output paths use
// to render maps deterministically. The contract is just "ascending
// alphabetical" — pin it so a refactor that swapped to
// maps.Keys (unsorted) would break test reproducibility.
func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]int{
		"zeta":  1,
		"alpha": 2,
		"mu":    3,
	})
	assert.Equal(t, []string{"alpha", "mu", "zeta"}, got,
		"sortedKeys must return ascending alphabetical order; "+
			"swapping to unsorted map iteration would make report output non-deterministic")
}

// indexByName is a tiny test helper to dereference the result of
// aggregateIncludeStatsFromSlices into a map keyed by Name. Used by
// multiple subtests above.
func indexByName(stats []templating.IncludeStats) map[string]templating.IncludeStats {
	out := make(map[string]templating.IncludeStats, len(stats))
	for _, s := range stats {
		out[s.Name] = s
	}
	return out
}

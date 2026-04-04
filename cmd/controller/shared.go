// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// separatorDouble is a double-line separator for major sections.
	separatorDouble = "================================================================================"

	// separatorSingle is a single-line separator for subsections.
	separatorSingle = "--------------------------------------------------------------------------------"
)

// errNoValidationTests is returned when a config has no validation tests defined.
var errNoValidationTests = errors.New("no validation tests found in config")

// sortedKeys returns the keys of a map sorted in ascending order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// mergeIncludeStat merges a single include stat into the aggregation map.
func mergeIncludeStat(aggregated map[string]*templating.IncludeStats, stat templating.IncludeStats) {
	if existing, ok := aggregated[stat.Name]; ok {
		existing.Count += stat.Count
		existing.TotalMs += stat.TotalMs
		if stat.MaxMs > existing.MaxMs {
			existing.MaxMs = stat.MaxMs
		}
	} else {
		aggregated[stat.Name] = &templating.IncludeStats{
			Name:    stat.Name,
			Count:   stat.Count,
			TotalMs: stat.TotalMs,
			MaxMs:   stat.MaxMs,
		}
	}
}

// sortIncludeStatsByTotalTime sorts include stats by total time (slowest first).
func sortIncludeStatsByTotalTime(stats []templating.IncludeStats) {
	slices.SortFunc(stats, func(a, b templating.IncludeStats) int {
		return cmp.Compare(b.TotalMs, a.TotalMs)
	})
}

// aggregateIncludeStatsFromSlices collects and aggregates include statistics from multiple stat slices.
func aggregateIncludeStatsFromSlices(statSlices [][]templating.IncludeStats) []templating.IncludeStats {
	aggregated := make(map[string]*templating.IncludeStats)

	for _, stats := range statSlices {
		for _, stat := range stats {
			mergeIncludeStat(aggregated, stat)
		}
	}

	// Convert to slice and calculate averages
	result := make([]templating.IncludeStats, 0, len(aggregated))
	for _, stat := range aggregated {
		if stat.Count > 0 {
			stat.AvgMs = stat.TotalMs / float64(stat.Count)
		}
		result = append(result, *stat)
	}

	sortIncludeStatsByTotalTime(result)

	return result
}

// printIncludeProfile prints the formatted include timing profile.
func printIncludeProfile(stats []templating.IncludeStats) {
	fmt.Println("\n" + separatorDouble)
	fmt.Println("INCLUDE TIMING PROFILE (Top 20 slowest)")
	fmt.Println(separatorDouble)
	fmt.Printf("%-45s %8s %10s %10s %10s\n", "Include", "Count", "Total(ms)", "Avg(ms)", "Max(ms)")
	fmt.Println(separatorSingle)

	limit := min(len(stats), 20)

	for i := 0; i < limit; i++ {
		stat := stats[i]
		fmt.Printf("%-45s %8d %10.2f %10.2f %10.2f\n",
			stat.Name, stat.Count, stat.TotalMs, stat.AvgMs, stat.MaxMs)
	}

	// Summary
	var totalTime float64
	var totalCalls int
	for _, stat := range stats {
		totalTime += stat.TotalMs
		totalCalls += stat.Count
	}
	fmt.Println(separatorSingle)
	fmt.Printf("%-45s %8d %10.2f\n", "TOTAL", totalCalls, totalTime)
}

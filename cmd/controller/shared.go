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
	"maps"
	"slices"
	"sort"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

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
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].TotalMs > stats[j].TotalMs
	})
}

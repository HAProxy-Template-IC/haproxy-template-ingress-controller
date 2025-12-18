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

package templating

import (
	"sort"
	"time"

	"github.com/open2b/scriggo"
)

// ProfilingEntry represents timing for a single template include/render.
// This type provides a stable API for consumers while using Scriggo's
// built-in profiling under the hood.
type ProfilingEntry struct {
	Name     string        // Template name or path
	Path     string        // Source file path
	Duration time.Duration // Execution time
}

// aggregateScriggoProfile converts Scriggo profile data into aggregated
// IncludeStats format for easier consumption.
func aggregateScriggoProfile(profile *scriggo.Profile) []IncludeStats {
	if profile == nil || len(profile.Calls) == 0 {
		return nil
	}

	// Aggregate by template name, only counting include/render operations
	type aggregate struct {
		count   int
		total   time.Duration
		maxTime time.Duration
	}

	byName := make(map[string]*aggregate)
	for _, call := range profile.Calls {
		// Only aggregate macro/render calls
		// Note: In Scriggo, `render` statements compile to CallKindMacro
		if call.Kind != scriggo.CallKindMacro {
			continue
		}

		name := call.TemplatePath
		if name == "" {
			name = call.Name
		}

		agg, exists := byName[name]
		if !exists {
			agg = &aggregate{}
			byName[name] = agg
		}
		agg.count++
		agg.total += call.Duration
		if call.Duration > agg.maxTime {
			agg.maxTime = call.Duration
		}
	}

	if len(byName) == 0 {
		return nil
	}

	// Convert to IncludeStats slice
	stats := make([]IncludeStats, 0, len(byName))
	for name, agg := range byName {
		totalMs := float64(agg.total.Microseconds()) / 1000.0
		avgMs := totalMs / float64(agg.count)
		maxMs := float64(agg.maxTime.Microseconds()) / 1000.0
		stats = append(stats, IncludeStats{
			Name:    name,
			Count:   agg.count,
			TotalMs: totalMs,
			AvgMs:   avgMs,
			MaxMs:   maxMs,
		})
	}

	// Sort by total time descending
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].TotalMs > stats[j].TotalMs
	})

	return stats
}

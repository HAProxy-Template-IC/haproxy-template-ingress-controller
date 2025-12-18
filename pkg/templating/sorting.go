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
	"fmt"
	"log/slog"
	"strings"
)

// SortDebugger provides debug logging capability for sort operations.
// This interface decouples sorting logic from engine-specific tracing implementations.
type SortDebugger interface {
	// IsFilterDebugEnabled returns true if filter debug logging is enabled.
	IsFilterDebugEnabled() bool
}

// sortableItems provides efficient multi-criteria sorting with pre-computed keys.
// This optimization evaluates each criterion expression once per item (O(n))
// rather than during each comparison (O(n log n) comparisons × k criteria).
// For 40 items with 8 criteria, this reduces evaluations from ~1700 to 320.
//
// This type is used by the Scriggo template engine.
type sortableItems struct {
	items      []interface{}
	criteria   []string
	debugger   SortDebugger // For filter debug logging (can be nil)
	cachedKeys [][]sortKey  // Pre-computed sort keys: cachedKeys[itemIndex][criterionIndex]
	descending []bool       // Per-criterion descending flag
}

// sortKey holds a pre-computed value for sorting with its type preserved.
type sortKey struct {
	value   interface{} // The evaluated and transformed value (after length/exists operators)
	isExist bool        // True if this was an :exists check (value is bool)
}

// Sort modifier constants.
const (
	sortModifierDesc   = "desc"
	sortModifierExists = "exists"
)

// precomputeKeys evaluates all criteria for all items once before sorting.
func (s *sortableItems) precomputeKeys() {
	s.cachedKeys = make([][]sortKey, len(s.items))
	s.descending = make([]bool, len(s.criteria))

	// Parse criteria once to extract descending flags
	cleanCriteria := make([]string, len(s.criteria))
	checkExists := make([]bool, len(s.criteria))
	checkLength := make([]bool, len(s.criteria))

	for ci, criterion := range s.criteria {
		parts := strings.Split(criterion, ":")
		expr := parts[0]

		for pi := 1; pi < len(parts); pi++ {
			modifier := strings.TrimSpace(parts[pi])
			switch modifier {
			case sortModifierDesc:
				s.descending[ci] = true
			case sortModifierExists:
				checkExists[ci] = true
			}
		}

		// Check for length operator in expression
		if strings.Contains(expr, " | length") {
			checkLength[ci] = true
			expr = strings.Replace(expr, " | length", "", 1)
		}

		cleanCriteria[ci] = strings.TrimSpace(expr)
	}

	// Pre-compute all keys
	for i, item := range s.items {
		s.cachedKeys[i] = make([]sortKey, len(s.criteria))
		for ci := range s.criteria {
			value := evaluateExpression(item, cleanCriteria[ci])

			if checkExists[ci] {
				// Convert to boolean existence check
				s.cachedKeys[i][ci] = sortKey{value: value != nil, isExist: true}
			} else if checkLength[ci] {
				// Convert to length
				s.cachedKeys[i][ci] = sortKey{value: getLength(value), isExist: false}
			} else {
				s.cachedKeys[i][ci] = sortKey{value: value, isExist: false}
			}
		}
	}
}

func (s *sortableItems) Len() int {
	return len(s.items)
}

func (s *sortableItems) Less(i, j int) bool {
	for ci := range s.criteria {
		cmp := s.comparePrecomputedKeys(s.cachedKeys[i][ci], s.cachedKeys[j][ci], s.criteria[ci])
		if cmp != 0 {
			if s.descending[ci] {
				return cmp > 0
			}
			return cmp < 0
		}
	}
	return false
}

func (s *sortableItems) Swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
	s.cachedKeys[i], s.cachedKeys[j] = s.cachedKeys[j], s.cachedKeys[i]
}

// comparePrecomputedKeys compares two pre-computed sort keys.
func (s *sortableItems) comparePrecomputedKeys(a, b sortKey, criterion string) int {
	// Handle debug logging if enabled
	debugEnabled := s.debugger != nil && s.debugger.IsFilterDebugEnabled()

	if debugEnabled {
		result := compareValues(a.value, b.value)
		slog.Info("SORT comparison",
			"criterion", criterion,
			"valA", a.value,
			"valA_type", fmt.Sprintf("%T", a.value),
			"valB", b.value,
			"valB_type", fmt.Sprintf("%T", b.value),
			"result", result,
		)
		return result
	}

	return compareValues(a.value, b.value)
}

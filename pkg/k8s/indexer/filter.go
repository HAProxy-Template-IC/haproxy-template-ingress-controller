package indexer

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FieldFilter removes fields from Kubernetes resources based on JSONPath expressions.
//
// This is used to reduce memory usage by removing unnecessary fields before storing
// resources in the index (e.g., metadata.managedFields).
type FieldFilter struct {
	patterns []string
}

// For example: "metadata.managedFields", "metadata.annotations".
func NewFieldFilter(patterns []string) *FieldFilter {
	return &FieldFilter{
		patterns: patterns,
	}
}

// Filter removes matching fields from the resource.
//
// The resource is modified in-place for efficiency.
// Returns an error if filtering fails.
//
// Example:
//
//	filter := NewFieldFilter([]string{"metadata.managedFields"})
//	err := filter.Filter(resource)
func (f *FieldFilter) Filter(resource any) error {
	if len(f.patterns) == 0 {
		return nil
	}

	// Production only ever feeds *unstructured.Unstructured, which unwraps to
	// the underlying map[string]any. Anything that doesn't resolve to a map
	// (e.g. a nil pointer) has no fields to remove — treat it as a no-op.
	data, ok := unwrapUnstructured(resource).(map[string]any)
	if !ok {
		return nil
	}

	// Apply each pattern.
	for _, pattern := range f.patterns {
		// Split pattern into segments.
		// Example: "metadata.labels['app']" -> ["metadata", "labels", "app"]
		segments := parseJSONPathPattern(pattern)
		if len(segments) == 0 {
			return &FilterError{
				Pattern: pattern,
				Cause:   errors.New("empty pattern"),
			}
		}

		removeField(data, segments)
	}

	return nil
}

// removeField deletes the field at segments below data. A "*" segment fans
// out over every array element. Missing or differently shaped intermediates
// are silent no-ops: missing fields are not errors during filtering.
func removeField(data map[string]any, segments []string) {
	wildcard := slices.Index(segments, "*")
	if wildcard < 0 {
		unstructured.RemoveNestedField(data, segments...)
		return
	}
	items, found, err := unstructured.NestedFieldNoCopy(data, segments[:wildcard]...)
	if err != nil || !found {
		return
	}
	elements, ok := items.([]any)
	if !ok {
		return
	}
	for _, element := range elements {
		if child, ok := element.(map[string]any); ok {
			removeField(child, segments[wildcard+1:])
		}
	}
}

// parseJSONPathPattern parses a JSONPath pattern into segments.
//
// Examples:
//   - "metadata.name" -> ["metadata", "name"]
//   - "metadata.labels['app']" -> ["metadata", "labels", "app"]
//   - "spec.rules[0].host" -> ["spec", "rules", "0", "host"]
func parseJSONPathPattern(pattern string) []string {
	var segments []string

	// Remove leading dot if present
	pattern = strings.TrimPrefix(pattern, ".")

	// Split by dots, but handle brackets specially
	current := ""
	inBracket := false

	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]

		switch ch {
		case '.':
			if !inBracket && current != "" {
				segments = append(segments, current)
				current = ""
			} else if !inBracket {
				// Skip leading dots
			} else {
				current += string(ch)
			}

		case '[':
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
			inBracket = true

		case ']':
			if inBracket && current != "" {
				// Remove quotes if present
				current = strings.Trim(current, "'\"")
				segments = append(segments, current)
				current = ""
			}
			inBracket = false

		default:
			current += string(ch)
		}
	}

	// Add remaining segment
	if current != "" {
		segments = append(segments, current)
	}

	return segments
}

// RootField returns the top-level (first) field segment of a JSONPath
// expression, or "" if the pattern is empty. It is resource-agnostic — it
// only parses the JSONPath string — and is used to compute the set of
// top-level object fields a projection must retain so that index-key
// extraction and field-selector evaluation still work on the projected
// object.
//
// Examples:
//   - "metadata.name"                          -> "metadata"
//   - "metadata.labels['kubernetes.io/x']"     -> "metadata"
//   - "spec.rules[0].host"                     -> "spec"
//   - "status"                                 -> "status"
func RootField(pattern string) string {
	segments := parseJSONPathPattern(pattern)
	if len(segments) == 0 {
		return ""
	}
	return segments[0]
}

// FilterError represents an error during field filtering.
type FilterError struct {
	Pattern string
	Cause   error
}

func (e *FilterError) Error() string {
	return fmt.Sprintf("filter error for pattern '%s': %v", e.Pattern, e.Cause)
}

func (e *FilterError) Unwrap() error {
	return e.Cause
}

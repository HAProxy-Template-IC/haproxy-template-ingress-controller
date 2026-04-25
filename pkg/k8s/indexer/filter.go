package indexer

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
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

	// Unwrap unstructured.Unstructured to get the underlying map
	// This allows us to modify the actual data
	data := unwrapUnstructured(resource)

	// Get reflect.Value for the unwrapped data and follow pointers/interfaces
	// to the concrete map/struct we want to mutate.
	rv, ok := derefForFilter(reflect.ValueOf(data))
	if !ok {
		return nil
	}

	// Apply each pattern
	for _, pattern := range f.patterns {
		if err := f.removeField(rv, pattern); err != nil {
			return &FilterError{
				Pattern: pattern,
				Cause:   err,
			}
		}
	}

	return nil
}

// removeField removes a field from the resource based on a JSONPath expression.
func (f *FieldFilter) removeField(rv reflect.Value, pattern string) error {
	// Split pattern into segments
	// Example: "metadata.labels['app']" -> ["metadata", "labels", "app"]
	segments := parseJSONPathPattern(pattern)
	if len(segments) == 0 {
		return errors.New("empty pattern")
	}

	// Navigate to parent of target field
	parent := rv
	for i := 0; i < len(segments)-1; i++ {
		var navigateErr error
		parent, navigateErr = f.navigateToField(parent, segments[i])
		if navigateErr != nil {
			// Field doesn't exist, nothing to remove - this is not an error
			// Intentionally return nil (not navigateErr) since missing fields are acceptable
			return nil // Missing fields are not errors during filtering
		}
	}

	// Remove the target field
	targetField := segments[len(segments)-1]
	return f.deleteField(parent, targetField)
}

// derefForFilter follows pointers and interfaces until reaching a concrete value.
// Returns ok=false if a nil pointer/interface is encountered along the way, in
// which case callers typically treat the field as "already absent".
func derefForFilter(rv reflect.Value) (reflect.Value, bool) {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}, false
		}
		rv = rv.Elem()
	}
	return rv, true
}

// findStructField returns the field on rv whose name matches fieldName, trying
// an exact match first and falling back to a case-insensitive match (common
// when navigating Kubernetes API objects whose JSON tags differ in case from
// their Go field names). The returned value is invalid when no match exists.
func findStructField(rv reflect.Value, fieldName string) reflect.Value {
	if value := rv.FieldByName(fieldName); value.IsValid() {
		return value
	}
	for i := 0; i < rv.NumField(); i++ {
		if strings.EqualFold(rv.Type().Field(i).Name, fieldName) {
			return rv.Field(i)
		}
	}
	return reflect.Value{}
}

// navigateToField navigates to a field in the resource structure.
func (f *FieldFilter) navigateToField(rv reflect.Value, fieldName string) (reflect.Value, error) {
	rv, ok := derefForFilter(rv)
	if !ok {
		return reflect.Value{}, errors.New("nil pointer")
	}

	switch rv.Kind() {
	case reflect.Map:
		// Map field access
		key := reflect.ValueOf(fieldName)
		value := rv.MapIndex(key)
		if !value.IsValid() {
			return reflect.Value{}, fmt.Errorf("field not found: %s", fieldName)
		}
		return value, nil

	case reflect.Struct:
		value := findStructField(rv, fieldName)
		if !value.IsValid() {
			return reflect.Value{}, fmt.Errorf("field not found: %s", fieldName)
		}
		return value, nil

	default:
		return reflect.Value{}, fmt.Errorf("navigating into %s", rv.Kind())
	}
}

// deleteField removes a field from a map or struct.
func (f *FieldFilter) deleteField(parent reflect.Value, fieldName string) error {
	parent, ok := derefForFilter(parent)
	if !ok {
		return nil
	}

	switch parent.Kind() {
	case reflect.Map:
		// Delete from map
		key := reflect.ValueOf(fieldName)
		if parent.MapIndex(key).IsValid() {
			parent.SetMapIndex(key, reflect.Value{})
		}
		return nil

	case reflect.Struct:
		// Cannot delete struct fields, can only zero them
		value := findStructField(parent, fieldName)
		if value.IsValid() && value.CanSet() {
			value.Set(reflect.Zero(value.Type()))
		}
		return nil

	default:
		return fmt.Errorf("deleting field from %s", parent.Kind())
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

package indexer

import (
	"fmt"
	"strings"
)

// FieldSelectorMatcher filters resources by evaluating a field selector expression.
// It parses expressions in the format "field.path=value" and uses client-side
// JSONPath evaluation to determine if a resource matches.
type FieldSelectorMatcher struct {
	evaluator     *JSONPathEvaluator
	expectedValue string
	expression    string
}

// NewFieldSelectorMatcher creates a new matcher for the given field selector expression.
//
// The expression must be in the format "field.path=value" where:
//   - field.path is a JSONPath expression (e.g., "spec.ingressClassName")
//   - value is the expected string value (e.g., "haproxy-internal")
//
// Example: "spec.ingressClassName=haproxy-internal"
//
// Returns an error if the expression format is invalid or the JSONPath is malformed.
func NewFieldSelectorMatcher(expression string) (*FieldSelectorMatcher, error) {
	if expression == "" {
		return nil, fmt.Errorf("empty field selector expression")
	}

	// Split on '=' to get field path and expected value
	parts := strings.SplitN(expression, "=", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid field selector format: expected 'field.path=value', got %q", expression)
	}

	fieldPath := strings.TrimSpace(parts[0])
	expectedValue := strings.TrimSpace(parts[1])

	if fieldPath == "" {
		return nil, fmt.Errorf("invalid field selector: empty field path in %q", expression)
	}

	// Create JSONPath evaluator for the field path
	evaluator, err := NewJSONPathEvaluator(fieldPath)
	if err != nil {
		return nil, fmt.Errorf("invalid field selector path %q: %w", fieldPath, err)
	}

	return &FieldSelectorMatcher{
		evaluator:     evaluator,
		expectedValue: expectedValue,
		expression:    expression,
	}, nil
}

// Matches returns true if the resource matches the field selector expression.
//
// Returns:
//   - (true, nil) if the field exists and its value matches the expected value
//   - (false, nil) if the field doesn't exist or the value doesn't match
//   - (false, error) only for unexpected evaluation errors
//
// A missing field is treated as a non-match (not an error), allowing resources
// without the field to be filtered out gracefully.
func (m *FieldSelectorMatcher) Matches(resource interface{}) (bool, error) {
	actualValue, found := m.tryEvaluate(resource)
	if !found {
		// Missing field - treat as non-match.
		// This is expected for resources that don't have the field.
		return false, nil
	}

	return actualValue == m.expectedValue, nil
}

// tryEvaluate attempts to evaluate the field path and returns (value, found).
// If the field doesn't exist or evaluation fails, returns ("", false).
func (m *FieldSelectorMatcher) tryEvaluate(resource interface{}) (string, bool) {
	actualValue, err := m.evaluator.Evaluate(resource)
	if err != nil {
		return "", false
	}
	return actualValue, true
}

// Expression returns the original field selector expression.
func (m *FieldSelectorMatcher) Expression() string {
	return m.expression
}

// ExpectedValue returns the expected value portion of the expression.
func (m *FieldSelectorMatcher) ExpectedValue() string {
	return m.expectedValue
}

// FieldPath returns the field path portion of the expression.
func (m *FieldSelectorMatcher) FieldPath() string {
	return m.evaluator.Expression()
}

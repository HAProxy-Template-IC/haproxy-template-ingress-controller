package indexer

// ValidateJSONPath validates a JSONPath expression syntax without executing it.
//
// This is a generic validation function that can be used anywhere JSONPath
// validation is needed. It only checks syntax correctness and does not
// require a resource to evaluate against.
//
// The expression should follow JSONPath syntax without the surrounding braces.
// For example: "metadata.namespace" or "metadata.labels['app']"
//
// Parameters:
//   - expr: The JSONPath expression to validate
//
// Returns:
//   - An error if the expression syntax is invalid
//   - nil if the expression is valid
//
// Example:
//
//	err := indexer.ValidateJSONPath("metadata.namespace")
//	if err != nil {
//	    slog.Error("Invalid JSONPath", "error", err)
//	}
func ValidateJSONPath(expr string) error {
	// Try to create a JSONPath evaluator - this validates the syntax
	_, err := NewJSONPathEvaluator(expr)
	return err
}

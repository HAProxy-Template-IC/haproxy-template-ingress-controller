// Package indexer provides functionality for extracting index keys from
// Kubernetes resources and filtering fields based on JSONPath expressions.
//
// This package is used by the store implementations to:
// - Extract index keys for O(1) lookups
// - Remove unnecessary fields to reduce memory usage
package indexer

import (
	"fmt"
)

// Indexer extracts index keys from Kubernetes resources and filters fields.
//
// It combines JSONPath evaluation for key extraction with field filtering
// for memory optimization.
type Indexer struct {
	evaluators []*JSONPathEvaluator
	filter     *FieldFilter
}

// Config configures the indexer behavior.
type Config struct {
	// IndexBy specifies JSONPath expressions for extracting index keys.
	// At least one expression is required.
	IndexBy []string

	// IgnoreFields specifies JSONPath patterns for fields to remove.
	// These fields are removed from resources before storage.
	IgnoreFields []string
}

// New creates a new Indexer with the provided configuration.
//
// Returns an error if:
//   - IndexBy is empty
//   - Any JSONPath expression is invalid (fail-fast)
//
// Example:
//
//	indexer, err := indexer.New(indexer.Config{
//	    IndexBy: []string{
//	        "metadata.namespace",
//	        "metadata.name",
//	    },
//	    IgnoreFields: []string{
//	        "metadata.managedFields",
//	    },
//	})
func New(cfg Config) (*Indexer, error) {
	if len(cfg.IndexBy) == 0 {
		return nil, fmt.Errorf("at least one index expression is required")
	}

	// Create JSONPath evaluators for index keys (fail-fast validation)
	evaluators := make([]*JSONPathEvaluator, len(cfg.IndexBy))
	for i, expr := range cfg.IndexBy {
		eval, err := NewJSONPathEvaluator(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid index expression at position %d: %w", i, err)
		}
		evaluators[i] = eval
	}

	// Create field filter
	filter := NewFieldFilter(cfg.IgnoreFields)

	return &Indexer{
		evaluators: evaluators,
		filter:     filter,
	}, nil
}

// ExtractKeys extracts index keys from the resource using configured JSONPath expressions.
//
// Returns:
//   - A slice of string keys in the order of IndexBy configuration
//   - An error if key extraction fails
//
// Example:
//
//	// For IndexBy: ["metadata.namespace", "metadata.name"]
//	keys, err := indexer.ExtractKeys(ingress)
//	// keys = ["default", "my-ingress"]
func (idx *Indexer) ExtractKeys(resource interface{}) ([]string, error) {
	keys := make([]string, len(idx.evaluators))

	for i, eval := range idx.evaluators {
		key, err := eval.Evaluate(resource)
		if err != nil {
			return nil, &IndexError{
				Expression: eval.Expression(),
				Position:   i,
				Cause:      err,
			}
		}
		keys[i] = key
	}

	return keys, nil
}

// FilterFields removes fields from the resource based on IgnoreFields configuration.
//
// The resource is modified in-place for efficiency.
// Returns an error if filtering fails.
//
// Example:
//
//	err := indexer.FilterFields(ingress)
//	// ingress.Metadata.ManagedFields is now removed
func (idx *Indexer) FilterFields(resource interface{}) error {
	return idx.filter.Filter(resource)
}

// Process is a convenience method that filters fields and extracts keys in one call.
//
// This is the most common usage pattern: filter the resource to reduce memory,
// then extract keys for indexing.
//
// Returns:
//   - Extracted index keys
//   - An error if either operation fails
//
// The resource is modified in-place by field filtering.
//
// Example:
//
//	keys, err := indexer.Process(ingress)
//	if err != nil {
//	    return err
//	}
//	store.Add(ingress, keys)
func (idx *Indexer) Process(resource interface{}) ([]string, error) {
	// Filter fields first to reduce memory
	if err := idx.FilterFields(resource); err != nil {
		return nil, err
	}

	// Extract index keys
	return idx.ExtractKeys(resource)
}

// NumKeys returns the number of index keys this indexer extracts.
func (idx *Indexer) NumKeys() int {
	return len(idx.evaluators)
}

// IndexExpressions returns the JSONPath expressions used for key extraction.
func (idx *Indexer) IndexExpressions() []string {
	exprs := make([]string, len(idx.evaluators))
	for i, eval := range idx.evaluators {
		exprs[i] = eval.Expression()
	}
	return exprs
}

// IndexError represents an error during index key extraction.
type IndexError struct {
	Expression string
	Position   int
	Cause      error
}

func (e *IndexError) Error() string {
	return fmt.Sprintf("index error at position %d for expression '%s': %v",
		e.Position, e.Expression, e.Cause)
}

func (e *IndexError) Unwrap() error {
	return e.Cause
}

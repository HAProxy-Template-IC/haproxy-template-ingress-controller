// Package store provides storage implementations for indexed Kubernetes resources.
//
// This package offers two store types:
// - Memory store: Fast in-memory storage with complete resources
// - Cached store: Memory-efficient storage with API-backed retrieval and caching
package store

import (
	"errors"
	"fmt"
	"strings"
)

var errResourceNameRequired = errors.New("resource name is required")

const opDelete = "delete"

type resourceIdentity struct {
	namespace string
	name      string
}

// makeKeyString creates a composite key from multiple key parts.
//
// Example:
//
//	makeKeyString("default", "my-ingress") -> "default/my-ingress"
func makeKeyString(keys []string) string {
	return strings.Join(keys, "/")
}

// StoreError represents a generic store operation error.
type StoreError struct {
	Operation string
	Keys      []string
	Cause     error
}

func (e *StoreError) Error() string {
	keyStr := strings.Join(e.Keys, "/")
	if keyStr == "" {
		return fmt.Sprintf("store error during %s: %v", e.Operation, e.Cause)
	}
	return fmt.Sprintf("store error during %s for key '%s': %v", e.Operation, keyStr, e.Cause)
}

func (e *StoreError) Unwrap() error {
	return e.Cause
}

// validateKeyCount returns a StoreError when the supplied keys don't match the
// store's expected key count. Used by Add/Update/Delete on both store types to
// enforce the index-key contract.
func validateKeyCount(operation string, keys []string, want int) error {
	if len(keys) == want {
		return nil
	}
	return &StoreError{
		Operation: operation,
		Keys:      keys,
		Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), want),
	}
}

func validateDeleteName(name string, keys []string) error {
	if name != "" {
		return nil
	}
	return &StoreError{
		Operation: opDelete,
		Keys:      keys,
		Cause:     errResourceNameRequired,
	}
}

// extractNamespaceName extracts namespace and name from a Kubernetes resource.
// Returns empty strings if the resource doesn't have metadata.namespace or metadata.name.
func extractNamespaceName(resource any) (namespace, name string) {
	// Try to extract from unstructured.Unstructured or any type with GetNamespace/GetName methods
	type metadataGetter interface {
		GetNamespace() string
		GetName() string
	}

	if mg, ok := resource.(metadataGetter); ok {
		return mg.GetNamespace(), mg.GetName()
	}

	// Fallback: try to access as map
	if m, ok := resource.(map[string]any); ok {
		if metadata, ok := m["metadata"].(map[string]any); ok {
			ns, _ := metadata["namespace"].(string)
			name, _ := metadata["name"].(string)
			return ns, name
		}
	}

	return "", ""
}

func identifyResource(resource any) (resourceIdentity, bool) {
	namespace, name := extractNamespaceName(resource)
	return resourceIdentity{namespace: namespace, name: name}, name != ""
}

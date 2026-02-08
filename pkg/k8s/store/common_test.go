package store

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestStoreError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *StoreError
		contains []string
	}{
		{
			name: "with keys",
			err: &StoreError{
				Operation: "add",
				Keys:      []string{"default", "my-resource"},
				Cause:     errors.New("invalid key count"),
			},
			contains: []string{"add", "default/my-resource", "invalid key count"},
		},
		{
			name: "without keys",
			err: &StoreError{
				Operation: "list",
				Keys:      nil,
				Cause:     errors.New("store empty"),
			},
			contains: []string{"list", "store empty"},
		},
		{
			name: "with empty keys slice",
			err: &StoreError{
				Operation: "delete",
				Keys:      []string{},
				Cause:     errors.New("no keys provided"),
			},
			contains: []string{"delete", "no keys provided"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := tt.err.Error()

			for _, substring := range tt.contains {
				if !contains(errMsg, substring) {
					t.Errorf("expected error message to contain %q, got: %q", substring, errMsg)
				}
			}
		})
	}
}

func TestStoreError_Unwrap(t *testing.T) {
	baseErr := errors.New("base error")
	storeErr := &StoreError{
		Operation: "add",
		Keys:      []string{"default"},
		Cause:     baseErr,
	}

	unwrapped := storeErr.Unwrap()
	if unwrapped != baseErr {
		t.Errorf("expected Unwrap() to return base error, got: %v", unwrapped)
	}

	// Test errors.Is with wrapped error
	if !errors.Is(storeErr, baseErr) {
		t.Error("errors.Is should match the wrapped error")
	}
}

func TestMakeKeyString(t *testing.T) {
	tests := []struct {
		name     string
		keys     []string
		expected string
	}{
		{
			name:     "two keys",
			keys:     []string{"default", "my-resource"},
			expected: "default/my-resource",
		},
		{
			name:     "single key",
			keys:     []string{"default"},
			expected: "default",
		},
		{
			name:     "empty keys",
			keys:     []string{},
			expected: "",
		},
		{
			name:     "three keys",
			keys:     []string{"ns", "name", "label"},
			expected: "ns/name/label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := makeKeyString(tt.keys)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractNamespaceName(t *testing.T) {
	tests := []struct {
		name              string
		resource          interface{}
		expectedNamespace string
		expectedName      string
	}{
		{
			name: "unstructured resource",
			resource: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"namespace": "default",
						"name":      "my-resource",
					},
				},
			},
			expectedNamespace: "default",
			expectedName:      "my-resource",
		},
		{
			name: "map resource",
			resource: map[string]interface{}{
				"metadata": map[string]interface{}{
					"namespace": "kube-system",
					"name":      "config",
				},
			},
			expectedNamespace: "kube-system",
			expectedName:      "config",
		},
		{
			name: "map without metadata",
			resource: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			expectedNamespace: "",
			expectedName:      "",
		},
		{
			name:              "unsupported type",
			resource:          "string",
			expectedNamespace: "",
			expectedName:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, name := extractNamespaceName(tt.resource)
			if ns != tt.expectedNamespace {
				t.Errorf("expected namespace %q, got %q", tt.expectedNamespace, ns)
			}
			if name != tt.expectedName {
				t.Errorf("expected name %q, got %q", tt.expectedName, name)
			}
		})
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s != "" && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// These helpers test common Store interface behaviors across different
// implementations (MemoryStore, CachedStore) to ensure consistent behavior.

// storeWithSize is an interface for stores that support Size().
// Both MemoryStore and CachedStore implement this.
type storeWithSize interface {
	Size() int
}

// RunUpdateToNewKeyTest verifies that Update creates a new entry when the resource doesn't exist.
// This is a common behavior that both MemoryStore and CachedStore should support.
//
// Parameters:
//   - t: test context
//   - store: the Store implementation to test (must also implement Size())
//   - resource: a resource appropriate for the store type
//   - keys: the index keys for the resource
func RunUpdateToNewKeyTest(t *testing.T, store interface {
	Update(resource interface{}, keys []string) error
	Get(keys ...string) ([]interface{}, error)
}, sizeGetter storeWithSize, resource interface{}, keys []string) {
	t.Helper()

	// Update a resource that doesn't exist (should create it)
	err := store.Update(resource, keys)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify the resource was created
	if sizeGetter.Size() != 1 {
		t.Errorf("expected size 1, got %d", sizeGetter.Size())
	}

	// Verify it can be retrieved
	results, err := store.Get(keys...)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// RunDeleteNonExistentTest verifies that deleting a non-existent resource is a no-op.
// This is a common behavior that both MemoryStore and CachedStore should support.
//
// Parameters:
//   - t: test context
//   - store: the Store implementation to test (must also implement Size())
//   - existingResource: a resource to add first
//   - existingKeys: keys for the existing resource
//   - nonExistentKeys: keys for a resource that doesn't exist
func RunDeleteNonExistentTest(t *testing.T, store interface {
	Add(resource interface{}, keys []string) error
	Delete(keys ...string) error
}, sizeGetter storeWithSize, existingResource interface{}, existingKeys []string, nonExistentKeys []string) {
	t.Helper()

	// Add a resource first
	if err := store.Add(existingResource, existingKeys); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Delete a non-existent resource (should not error)
	err := store.Delete(nonExistentKeys...)
	if err != nil {
		t.Errorf("Delete of non-existent resource should not error: %v", err)
	}

	// Original resource should still exist
	if sizeGetter.Size() != 1 {
		t.Errorf("expected size 1, got %d", sizeGetter.Size())
	}
}

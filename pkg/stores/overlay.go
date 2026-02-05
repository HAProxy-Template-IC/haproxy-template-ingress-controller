// Package stores provides abstractions for accessing and composing resource stores.
package stores

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktypes "k8s.io/apimachinery/pkg/types"
)

// Compile-time assertion: StoreOverlay implements ContentOverlay.
var _ ContentOverlay = (*StoreOverlay)(nil)

// StoreOverlay represents proposed changes to a store.
//
// This enables "what if" scenarios: validate configurations with proposed
// changes without modifying the actual store state.
//
// Resources are pre-converted to template-friendly format (floats to ints)
// at construction time to avoid repeated conversion during Get()/List() calls.
type StoreOverlay struct {
	// Additions are new resources to add to the store.
	Additions []runtime.Object

	// Modifications are existing resources with updated content.
	Modifications []runtime.Object

	// Deletions are keys identifying resources to remove.
	// Uses NamespacedName for consistent key format.
	Deletions []ktypes.NamespacedName

	// Pre-converted resources for template use (computed once at construction).
	// These are the template-friendly representations with floats converted to ints.
	convertedAdditions     []interface{}
	convertedModifications []interface{}
}

// NewStoreOverlay creates a new empty StoreOverlay.
func NewStoreOverlay() *StoreOverlay {
	return &StoreOverlay{
		Additions:              make([]runtime.Object, 0),
		Modifications:          make([]runtime.Object, 0),
		Deletions:              make([]ktypes.NamespacedName, 0),
		convertedAdditions:     make([]interface{}, 0),
		convertedModifications: make([]interface{}, 0),
	}
}

// NewStoreOverlayForCreate creates a StoreOverlay for a CREATE operation.
// The resource is pre-converted to template-friendly format.
func NewStoreOverlayForCreate(obj runtime.Object) *StoreOverlay {
	converted := convertOverlayResource(obj)
	return &StoreOverlay{
		Additions:              []runtime.Object{obj},
		Modifications:          make([]runtime.Object, 0),
		Deletions:              make([]ktypes.NamespacedName, 0),
		convertedAdditions:     []interface{}{converted},
		convertedModifications: make([]interface{}, 0),
	}
}

// NewStoreOverlayForUpdate creates a StoreOverlay for an UPDATE operation.
// The resource is pre-converted to template-friendly format.
func NewStoreOverlayForUpdate(obj runtime.Object) *StoreOverlay {
	converted := convertOverlayResource(obj)
	return &StoreOverlay{
		Additions:              make([]runtime.Object, 0),
		Modifications:          []runtime.Object{obj},
		Deletions:              make([]ktypes.NamespacedName, 0),
		convertedAdditions:     make([]interface{}, 0),
		convertedModifications: []interface{}{converted},
	}
}

// NewStoreOverlayForDelete creates a StoreOverlay for a DELETE operation.
func NewStoreOverlayForDelete(namespace, name string) *StoreOverlay {
	return &StoreOverlay{
		Additions:              make([]runtime.Object, 0),
		Modifications:          make([]runtime.Object, 0),
		Deletions:              []ktypes.NamespacedName{{Namespace: namespace, Name: name}},
		convertedAdditions:     make([]interface{}, 0),
		convertedModifications: make([]interface{}, 0),
	}
}

// IsEmpty returns true if the overlay contains no changes.
func (o *StoreOverlay) IsEmpty() bool {
	return len(o.Additions) == 0 && len(o.Modifications) == 0 && len(o.Deletions) == 0
}

// AddAddition adds a resource to the additions list and pre-converts it.
// Use this method instead of directly modifying the Additions field to ensure
// the pre-converted slice stays in sync.
func (o *StoreOverlay) AddAddition(obj runtime.Object) {
	o.Additions = append(o.Additions, obj)
	o.convertedAdditions = append(o.convertedAdditions, convertOverlayResource(obj))
}

// AddModification adds a resource to the modifications list and pre-converts it.
// Use this method instead of directly modifying the Modifications field to ensure
// the pre-converted slice stays in sync.
func (o *StoreOverlay) AddModification(obj runtime.Object) {
	o.Modifications = append(o.Modifications, obj)
	o.convertedModifications = append(o.convertedModifications, convertOverlayResource(obj))
}

// CompositeStore wraps a base store with an overlay, providing a unified view
// that reflects both the actual state and proposed changes.
//
// Read operations (Get, List) return the merged view.
// Write operations are not supported and will return an error.
//
// # Thread Safety
//
// CompositeStore itself is not thread-safe, but this is by design:
//   - A new CompositeStore is created for each validation request (not shared across goroutines)
//   - The base store must be thread-safe (k8s.Store uses sync.RWMutex internally)
//   - The overlay must not be modified after construction (immutable during validation)
//
// This design avoids synchronization overhead since each request gets its own instance.
type CompositeStore struct {
	base    Store
	overlay *StoreOverlay

	// keyExtractor extracts index keys from a resource.
	// This is needed to properly index additions and modifications.
	keyExtractor KeyExtractor
}

// KeyExtractor extracts index keys from a resource.
// The keys are used to index resources in the store.
type KeyExtractor func(resource interface{}) ([]string, error)

// NewCompositeStore creates a new CompositeStore.
//
// Parameters:
//   - base: The underlying store with actual state
//   - overlay: The proposed changes to apply on top
func NewCompositeStore(base Store, overlay *StoreOverlay) *CompositeStore {
	return &CompositeStore{
		base:    base,
		overlay: overlay,
	}
}

// NewCompositeStoreWithKeyExtractor creates a new CompositeStore with a key extractor.
//
// The key extractor is needed for stores that use multi-key indexing.
// Without it, additions and modifications cannot be properly matched with Get() queries.
func NewCompositeStoreWithKeyExtractor(base Store, overlay *StoreOverlay, keyExtractor KeyExtractor) *CompositeStore {
	return &CompositeStore{
		base:         base,
		overlay:      overlay,
		keyExtractor: keyExtractor,
	}
}

// Get returns resources matching the given keys.
//
// The result includes:
//   - Resources from the base store (excluding deletions)
//   - Modifications (replacing base resources with same keys)
//   - Additions that match the keys
func (s *CompositeStore) Get(keys ...string) ([]interface{}, error) {
	// Get base results
	baseResults, err := s.base.Get(keys...)
	if err != nil {
		return nil, err
	}

	// Filter out deleted and modified resources
	filtered := make([]interface{}, 0, len(baseResults))
	for _, res := range baseResults {
		if !s.isDeleted(res) && !s.isModified(res) {
			filtered = append(filtered, res)
		}
	}

	// Add modifications that match the keys (use pre-converted resources)
	for i, mod := range s.overlay.Modifications {
		if s.matchesKeys(mod, keys) {
			filtered = append(filtered, s.overlay.convertedModifications[i])
		}
	}

	// Add additions that match the keys (use pre-converted resources)
	for i, add := range s.overlay.Additions {
		if s.matchesKeys(add, keys) {
			filtered = append(filtered, s.overlay.convertedAdditions[i])
		}
	}

	return filtered, nil
}

// List returns all resources from the merged view.
func (s *CompositeStore) List() ([]interface{}, error) {
	// Get all base resources
	baseResults, err := s.base.List()
	if err != nil {
		return nil, err
	}

	// Filter out deleted and modified resources
	result := make([]interface{}, 0, len(baseResults)+len(s.overlay.Additions))
	for _, res := range baseResults {
		if !s.isDeleted(res) && !s.isModified(res) {
			result = append(result, res)
		}
	}

	// Add all modifications (use pre-converted resources)
	result = append(result, s.overlay.convertedModifications...)

	// Add all additions (use pre-converted resources)
	result = append(result, s.overlay.convertedAdditions...)

	return result, nil
}

// Add is not supported on CompositeStore.
// CompositeStore is read-only; modifications should be made through the overlay.
func (s *CompositeStore) Add(_ interface{}, _ []string) error {
	return &ReadOnlyStoreError{Operation: "Add"}
}

// Update is not supported on CompositeStore.
// CompositeStore is read-only; modifications should be made through the overlay.
func (s *CompositeStore) Update(_ interface{}, _ []string) error {
	return &ReadOnlyStoreError{Operation: "Update"}
}

// Delete is not supported on CompositeStore.
// CompositeStore is read-only; modifications should be made through the overlay.
func (s *CompositeStore) Delete(_ ...string) error {
	return &ReadOnlyStoreError{Operation: "Delete"}
}

// Clear is not supported on CompositeStore.
// CompositeStore is read-only; modifications should be made through the overlay.
func (s *CompositeStore) Clear() error {
	return &ReadOnlyStoreError{Operation: "Clear"}
}

// ReadOnlyStoreError indicates an attempt to modify a read-only store.
type ReadOnlyStoreError struct {
	Operation string
}

func (e *ReadOnlyStoreError) Error() string {
	return "composite store is read-only: " + e.Operation + " not supported"
}

// isDeleted checks if a resource is marked for deletion in the overlay.
func (s *CompositeStore) isDeleted(resource interface{}) bool {
	resKey := s.getResourceKey(resource)
	if resKey == nil {
		return false
	}

	for _, del := range s.overlay.Deletions {
		if del.Namespace == resKey.Namespace && del.Name == resKey.Name {
			return true
		}
	}
	return false
}

// isModified checks if a resource has a modification in the overlay.
func (s *CompositeStore) isModified(resource interface{}) bool {
	resKey := s.getResourceKey(resource)
	if resKey == nil {
		return false
	}

	for _, mod := range s.overlay.Modifications {
		modKey := s.getResourceKey(mod)
		if modKey != nil && modKey.Namespace == resKey.Namespace && modKey.Name == resKey.Name {
			return true
		}
	}
	return false
}

// matchesKeys checks if a resource matches the given lookup keys.
func (s *CompositeStore) matchesKeys(resource interface{}, keys []string) bool {
	if s.keyExtractor == nil {
		// Without a key extractor, we can only do basic namespace/name matching
		resKey := s.getResourceKey(resource)
		if resKey == nil || len(keys) == 0 {
			return false
		}

		// Match based on keys length:
		// 1 key = namespace match
		// 2 keys = namespace + name match
		if len(keys) >= 1 && keys[0] != resKey.Namespace {
			return false
		}
		if len(keys) >= 2 && keys[1] != resKey.Name {
			return false
		}
		return true
	}

	// Use key extractor for full key matching
	resourceKeys, err := s.keyExtractor(resource)
	if err != nil || len(resourceKeys) < len(keys) {
		return false
	}

	// Check if provided keys match the resource keys (prefix match)
	for i, key := range keys {
		if resourceKeys[i] != key {
			return false
		}
	}
	return true
}

// getResourceKey extracts namespace/name from a resource.
func (s *CompositeStore) getResourceKey(resource interface{}) *ktypes.NamespacedName {
	// Try to get metadata via accessor
	if accessor, ok := resource.(interface {
		GetNamespace() string
		GetName() string
	}); ok {
		return &ktypes.NamespacedName{
			Namespace: accessor.GetNamespace(),
			Name:      accessor.GetName(),
		}
	}
	return nil
}

// convertOverlayResource converts a runtime.Object to a map for template use.
// This ensures overlay resources have the same format as pre-converted maps in stores.
//
// The conversion extracts the underlying map from unstructured resources and
// converts float64 values to int64 where they have no fractional part.
func convertOverlayResource(obj runtime.Object) interface{} {
	// Extract map from unstructured
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return convertFloatsToInts(u.Object)
	}
	return obj
}

// convertFloatsToInts recursively converts float64 values to int64 where they
// have no fractional part. Mutation is performed in-place since overlay resources
// are freshly deserialized from admission webhooks and owned by us.
//
// This is necessary because JSON unmarshaling converts all numbers to float64
// when the target type is interface{}. For Kubernetes resources, this causes
// integer fields like ports (80) to appear as floats (80.0) in templates.
// HAProxy configuration syntax requires integers (port 80), not floats (port 80.0).
func convertFloatsToInts(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		// Mutate map values in-place (safe: resources are freshly deserialized and owned by us)
		for k, val := range v {
			v[k] = convertFloatsToInts(val)
		}
		return v

	case []interface{}:
		// Mutate slice elements in-place
		for i, val := range v {
			v[i] = convertFloatsToInts(val)
		}
		return v

	case float64:
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v

	default:
		return v
	}
}

package store

import (
	"fmt"
	"maps"
	"reflect"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ImmutableResource owns a resource graph shared by informer and store snapshots.
type ImmutableResource struct {
	value atomic.Pointer[unstructured.Unstructured]
}

// NewImmutableResource detaches the caller's graph before allowing shared ownership.
func NewImmutableResource(resource map[string]any) (*ImmutableResource, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource is nil: %w", stores.ErrSnapshotUnsupported)
	}
	owned, err := typegen.CloneImmutableJSON(resource)
	if err != nil {
		return nil, err
	}
	result := &ImmutableResource{}
	result.value.Store(&unstructured.Unstructured{Object: owned.(map[string]any)})
	return result, nil
}

// GetObjectMeta gives client-go detached metadata for indexing and version checks.
func (r *ImmutableResource) GetObjectMeta() metav1.Object {
	metadata, err := typegen.CloneImmutableJSON(r.resource().Object["metadata"])
	if err != nil {
		return nil
	}
	return &unstructured.Unstructured{Object: map[string]any{"metadata": metadata}}
}

// GetNamespace returns the resource's namespace.
func (r *ImmutableResource) GetNamespace() string { return r.resource().GetNamespace() }

// GetName returns the resource's name.
func (r *ImmutableResource) GetName() string { return r.resource().GetName() }

// GetResourceVersion returns the resource's watch version.
func (r *ImmutableResource) GetResourceVersion() string { return r.resource().GetResourceVersion() }

// ExtractKeys evaluates configured index expressions without exposing the graph.
func (r *ImmutableResource) ExtractKeys(idx *indexer.Indexer) ([]string, error) {
	return idx.ExtractKeys(r.resource())
}

// Matches evaluates a configured field selector without exposing the graph.
func (r *ImmutableResource) Matches(matcher *indexer.FieldSelectorMatcher) (bool, error) {
	return matcher.Matches(r.resource().Object)
}

// ShareUnchanged reuses the previous value or its body, preserving this watch version.
func (r *ImmutableResource) ShareUnchanged(previous *ImmutableResource) *ImmutableResource {
	if r == nil || previous == nil || r.value.Load() == nil || previous.value.Load() == nil {
		return r
	}
	previousObject := previous.resource().Object
	sameMetadata := reflect.DeepEqual(r.resource().Object["metadata"], previousObject["metadata"])
	if !r.shareUnchanged(previousObject) {
		return r
	}
	if sameMetadata {
		return previous
	}
	return r
}

func (r *ImmutableResource) resource() *unstructured.Unstructured {
	if value := r.value.Load(); value != nil {
		return value
	}
	return &unstructured.Unstructured{}
}

// Retain the watch version while sharing a semantically unchanged store body.
func (r *ImmutableResource) shareUnchanged(current any) bool {
	incoming := r.resource().Object
	if !equalIgnoringResourceVersion(current, incoming) {
		return false
	}
	object, ok := current.(map[string]any)
	if !ok {
		return false
	}
	previousMetadata, ok := object["metadata"].(map[string]any)
	incomingMetadata, incomingHasMetadata := incoming["metadata"].(map[string]any)
	if ok && incomingHasMetadata && !reflect.DeepEqual(previousMetadata["resourceVersion"], incomingMetadata["resourceVersion"]) {
		object = maps.Clone(object)
		metadata := maps.Clone(previousMetadata)
		metadata["resourceVersion"] = incomingMetadata["resourceVersion"]
		object["metadata"] = metadata
	}
	r.value.Store(&unstructured.Unstructured{Object: object})
	return true
}

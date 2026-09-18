package watcher

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type watchedResource interface {
	GetName() string
	GetNamespace() string
	GetResourceVersion() string
}

func watchResource(obj any) watchedResource {
	switch value := obj.(type) {
	case *unstructured.Unstructured:
		if value != nil {
			return value
		}
	case *store.ImmutableResource:
		if value != nil {
			return value
		}
	}
	return nil
}

func storedResourceValue(resource watchedResource) any {
	if value, ok := resource.(*unstructured.Unstructured); ok {
		return value.Object
	}
	return resource
}

func (w *Watcher) extractResourceKeys(resource watchedResource) ([]string, error) {
	switch value := resource.(type) {
	case *unstructured.Unstructured:
		return w.indexer.ExtractKeys(value)
	case *store.ImmutableResource:
		return value.ExtractKeys(w.indexer)
	default:
		return nil, errors.New("unsupported watched resource")
	}
}

func (w *Watcher) resourceMatchesSelector(resource watchedResource) (bool, error) {
	switch value := resource.(type) {
	case *unstructured.Unstructured:
		return w.fieldSelectorMatcher.Matches(value.Object)
	case *store.ImmutableResource:
		return value.Matches(w.fieldSelectorMatcher)
	default:
		return false, errors.New("unsupported watched resource")
	}
}

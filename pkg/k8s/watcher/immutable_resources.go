package watcher

import (
	"runtime"
	"sync"
	"weak"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

type immutableResourceIdentity struct {
	namespace string
	name      string
}

type immutableResources struct {
	mu      sync.Mutex
	indexer *indexer.Indexer
	values  map[immutableResourceIdentity]weak.Pointer[store.ImmutableResource]
}

func newImmutableResources(idx *indexer.Indexer) *immutableResources {
	return &immutableResources{indexer: idx, values: make(map[immutableResourceIdentity]weak.Pointer[store.ImmutableResource])}
}

func (r *immutableResources) transform(obj any) (any, error) {
	resource, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}
	normalizeInPlace(resource, r.indexer)
	owned, err := store.NewImmutableResource(resource.Object)
	if err != nil {
		return nil, err
	}
	identity := immutableResourceIdentity{namespace: owned.GetNamespace(), name: owned.GetName()}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Client-go can replace equal-version objects without notifying our handler.
	previous := r.values[identity].Value()
	owned = owned.ShareUnchanged(previous)
	if owned == previous {
		return owned, nil
	}
	reference := weak.Make(owned)
	r.values[identity] = reference
	runtime.AddCleanup(owned, cleanImmutableResource, immutableResourceCleanup{owner: r, identity: identity, reference: reference})
	return owned, nil
}

type immutableResourceCleanup struct {
	owner     *immutableResources
	identity  immutableResourceIdentity
	reference weak.Pointer[store.ImmutableResource]
}

func cleanImmutableResource(cleanup immutableResourceCleanup) {
	cleanup.owner.mu.Lock()
	defer cleanup.owner.mu.Unlock()
	if cleanup.owner.values[cleanup.identity] == cleanup.reference {
		delete(cleanup.owner.values, cleanup.identity)
	}
}

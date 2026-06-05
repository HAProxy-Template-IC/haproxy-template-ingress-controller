package store

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

// newProjectedTestIndexer builds a real indexer so the live-GET → Process path
// (which projected-mode reads always take) works. The empty createTestIndexer()
// stub is only safe for cache-hit paths.
func newProjectedTestIndexer(t *testing.T) *indexer.Indexer {
	t.Helper()
	idx, err := indexer.New(indexer.Config{IndexBy: []string{"metadata.namespace", "metadata.name"}})
	if err != nil {
		t.Fatalf("indexer.New failed: %v", err)
	}
	return idx
}

// In projected mode the informer delivers a body-stripped object, so the
// CachedStore must NOT cache it on Add/Update (it would serve a husk to a warm
// render read). The full body is fetched live on demand instead; Update must
// invalidate any stale full body so the next read re-fetches.
func TestCachedStore_ProjectedMode(t *testing.T) {
	scheme := runtime.NewScheme()
	resource := createTestResource("default", "test-cm")
	client := fake.NewSimpleDynamicClient(scheme, resource)
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:   2,
		CacheTTL:  5 * time.Minute,
		Client:    client,
		GVR:       gvr,
		Indexer:   newProjectedTestIndexer(t),
		Projected: true,
	})
	if err != nil {
		t.Fatalf("NewCachedStore failed: %v", err)
	}

	// Add must NOT populate the value cache in projected mode.
	if err := store.Add(resource, []string{"default", "test-cm"}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if store.Size() != 1 {
		t.Errorf("expected ref size 1, got %d", store.Size())
	}
	if store.CacheSize() != 0 {
		t.Errorf("projected Add must not cache the body; CacheSize = %d, want 0", store.CacheSize())
	}

	// A read fetches the FULL body live and caches it.
	results, err := store.Get("default", "test-cm")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if store.CacheSize() != 1 {
		t.Errorf("expected full body cached after Get; CacheSize = %d, want 1", store.CacheSize())
	}

	// Update must INVALIDATE the stale cached body so the next read re-fetches.
	if err := store.Update(resource, []string{"default", "test-cm"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if store.CacheSize() != 0 {
		t.Errorf("projected Update must invalidate the cached body; CacheSize = %d, want 0", store.CacheSize())
	}
}

// Default (non-projected) mode keeps the existing behavior: Add caches the body.
func TestCachedStore_NonProjectedStillCachesOnAdd(t *testing.T) {
	scheme := runtime.NewScheme()
	resource := createTestResource("default", "test-cm")
	client := fake.NewSimpleDynamicClient(scheme, resource)
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:  2,
		CacheTTL: 5 * time.Minute,
		Client:   client,
		GVR:      gvr,
		Indexer:  createTestIndexer(),
		// Projected defaults to false.
	})
	if err != nil {
		t.Fatalf("NewCachedStore failed: %v", err)
	}

	if err := store.Add(resource, []string{"default", "test-cm"}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if store.CacheSize() != 1 {
		t.Errorf("non-projected Add should cache the body; CacheSize = %d, want 1", store.CacheSize())
	}
}

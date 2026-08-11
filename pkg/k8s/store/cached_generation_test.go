// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type cacheGenerationDynamicClient struct {
	resource *cacheGenerationResourceClient
}

func (c *cacheGenerationDynamicClient) Resource(schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return c.resource
}

type cacheGenerationResourceClient struct {
	dynamic.ResourceInterface
	firstStarted chan struct{}
	releaseFirst chan struct{}
	first        *unstructured.Unstructured
	later        *unstructured.Unstructured
	calls        atomic.Int32
}

func (c *cacheGenerationResourceClient) Namespace(string) dynamic.ResourceInterface {
	return c
}

func (c *cacheGenerationResourceClient) Get(
	ctx context.Context,
	_ string,
	_ metav1.GetOptions,
	_ ...string,
) (*unstructured.Unstructured, error) {
	if c.calls.Add(1) == 1 {
		close(c.firstStarted)
		select {
		case <-c.releaseFirst:
			return c.first.DeepCopy(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.later.DeepCopy(), nil
}

func TestCachedStoreStaleHitCannotRenewAcrossInformerMutation(t *testing.T) {
	for _, projected := range []bool{false, true} {
		for _, mutation := range cacheGenerationMutations() {
			name := mutation + "/projected=" + strconv.FormatBool(projected)
			t.Run(name, func(t *testing.T) {
				oldResource := cacheGenerationResource("old")
				store := newGenerationTestStore(t, oldResource, projected)
				keys := []string{"default", "test-cm"}
				require.NoError(t, store.Add(oldResource, keys))
				if projected {
					resources, err := store.Get(keys...)
					require.NoError(t, err)
					require.Len(t, resources, 1)
				}

				staleRef := store.matchingRefs(keys)[0]
				applyCacheGenerationMutation(t, store, mutation, keys)

				_, hit := store.loadCachedResource(staleRef)
				assert.False(t, hit)
				assertCacheMatchesMutation(t, store, mutation, projected)
			})
		}
	}
}

func TestCachedStoreStaleFetchCannotCommitAcrossInformerMutation(t *testing.T) {
	for _, projected := range []bool{false, true} {
		for _, mutation := range cacheGenerationMutations() {
			name := mutation + "/projected=" + strconv.FormatBool(projected)
			t.Run(name, func(t *testing.T) {
				runStaleFetchMutationTest(t, projected, mutation)
			})
		}
	}
}

func TestCachedStoreStaleFetchCannotRestoreRetiredIndexLocation(t *testing.T) {
	for _, mutation := range []string{"move", "delete and recreate"} {
		t.Run(mutation, func(t *testing.T) {
			oldResource := cacheGenerationResource("old")
			newResource := cacheGenerationResource("new")
			resourceClient := &cacheGenerationResourceClient{
				firstStarted: make(chan struct{}),
				releaseFirst: make(chan struct{}),
				first:        oldResource,
				later:        newResource,
			}
			store := newGenerationStore(t, &cacheGenerationDynamicClient{resource: resourceClient}, true)
			oldKeys := []string{"default", "old"}
			newKeys := []string{"default", "new"}
			require.NoError(t, store.Add(oldResource, oldKeys))

			readDone := make(chan error, 1)
			go func() {
				_, err := store.GetContext(t.Context(), oldKeys...)
				readDone <- err
			}()

			select {
			case <-resourceClient.firstStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("API fetch did not start")
			}

			if mutation == "delete and recreate" {
				require.NoError(t, store.Delete("default", "test-cm", oldKeys))
				require.NoError(t, store.Add(newResource, newKeys))
			} else {
				require.NoError(t, store.Update(newResource, newKeys))
			}
			close(resourceClient.releaseFirst)

			select {
			case err := <-readDone:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("stale cache miss did not finish")
			}

			oldBucket, err := store.Get(oldKeys...)
			require.NoError(t, err)
			assert.Empty(t, oldBucket)

			newBucket, err := store.Get(newKeys...)
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			assert.Equal(t, "new", cacheGenerationRevision(t, newBucket[0]))
		})
	}
}

func runStaleFetchMutationTest(t *testing.T, projected bool, mutation string) {
	t.Helper()
	oldResource := cacheGenerationResource("old")
	newResource := cacheGenerationResource("new")
	resourceClient := &cacheGenerationResourceClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		first:        oldResource,
		later:        newResource,
	}
	store := newGenerationStore(t, &cacheGenerationDynamicClient{resource: resourceClient}, projected)
	keys := []string{"default", "test-cm"}
	require.NoError(t, store.Add(oldResource, keys))

	store.mu.Lock()
	store.cache.Purge()
	store.mu.Unlock()

	readDone := make(chan error, 1)
	go func() {
		_, err := store.GetContext(t.Context(), keys...)
		readDone <- err
	}()

	select {
	case <-resourceClient.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("API fetch did not start")
	}

	applyCacheGenerationMutation(t, store, mutation, keys)
	close(resourceClient.releaseFirst)
	select {
	case err := <-readDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cache miss did not finish")
	}

	assertCacheMatchesMutation(t, store, mutation, projected)
	resources, err := store.Get(keys...)
	require.NoError(t, err)
	if !cacheGenerationMutationKeepsResource(mutation) {
		assert.Empty(t, resources)
		return
	}
	require.Len(t, resources, 1)
	assert.Equal(t, "new", cacheGenerationRevision(t, resources[0]))
}

func newGenerationTestStore(t *testing.T, resource *unstructured.Unstructured, projected bool) *CachedStore {
	t.Helper()
	client := &cacheGenerationResourceClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		first:        resource,
		later:        resource,
	}
	close(client.releaseFirst)
	return newGenerationStore(t, &cacheGenerationDynamicClient{resource: client}, projected)
}

func newGenerationStore(t *testing.T, client dynamic.Interface, projected bool) *CachedStore {
	t.Helper()
	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:   2,
		CacheTTL:  5 * time.Minute,
		Client:    client,
		GVR:       configMapGVR,
		Indexer:   newProjectedTestIndexer(t),
		Projected: projected,
	})
	require.NoError(t, err)
	return store
}

func cacheGenerationResource(revision string) *unstructured.Unstructured {
	resource := createTestResource("default", "test-cm")
	resource.Object["revision"] = revision
	return resource
}

func applyCacheGenerationMutation(t *testing.T, store *CachedStore, mutation string, keys []string) {
	t.Helper()
	switch mutation {
	case "delete":
		require.NoError(t, store.Delete("default", "test-cm", keys))
	case "clear":
		require.NoError(t, store.Clear())
	case "delete and recreate":
		require.NoError(t, store.Delete("default", "test-cm", keys))
		require.NoError(t, store.Add(cacheGenerationResource("new"), keys))
	case "clear and recreate":
		require.NoError(t, store.Clear())
		require.NoError(t, store.Add(cacheGenerationResource("new"), keys))
	default:
		require.NoError(t, store.Update(cacheGenerationResource("new"), keys))
	}
}

func assertCacheMatchesMutation(t *testing.T, store *CachedStore, mutation string, projected bool) {
	t.Helper()
	store.mu.RLock()
	defer store.mu.RUnlock()

	entry, cached := store.cache.Peek(resourceCacheKey("default", "test-cm"))
	if !cacheGenerationMutationKeepsResource(mutation) || projected {
		assert.False(t, cached)
		return
	}

	require.True(t, cached)
	assert.Equal(t, "new", cacheGenerationRevision(t, entry.resource))
	assert.Equal(t, store.refGenerations[resourceCacheKey("default", "test-cm")], entry.generation)
}

func cacheGenerationMutations() []string {
	return []string{"update", "delete", "clear", "delete and recreate", "clear and recreate"}
}

func cacheGenerationMutationKeepsResource(mutation string) bool {
	return mutation != "delete" && mutation != "clear"
}

func cacheGenerationRevision(t *testing.T, resource any) string {
	t.Helper()
	var revision any
	switch typed := resource.(type) {
	case *unstructured.Unstructured:
		revision = typed.Object["revision"]
	case map[string]any:
		revision = typed["revision"]
	default:
		t.Fatalf("unexpected cached resource type %T", resource)
		return ""
	}
	value, ok := revision.(string)
	require.True(t, ok, "revision has type %T", revision)
	return value
}

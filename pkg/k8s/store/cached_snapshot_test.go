// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestCachedStorePinDoesNotFetchBodies(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store := newProjectedSnapshotStore(t, client)

	for index := range 1000 {
		name := fmt.Sprintf("item-%04d", index)
		resource := cachedSnapshotResource("default", name, fmt.Sprintf("%d", index+1), "value")
		require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", name}))
	}

	snapshot, err := store.Pin()
	require.NoError(t, err)
	items, err := snapshot.Get("default", "missing")
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NotEmpty(t, snapshot.GetRevision("default", "missing"))
	assert.Zero(t, gets.Load())
}

func TestCachedStoreImmutableReadKeepsOwnedValuePrivate(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "original")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	store := newTestCachedStore(t, client, newProjectedTestIndexer(t), 2, time.Minute)
	require.NoError(t, store.Add(resource, []string{"default", "target"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)
	immutable := snapshot.(*cachedReadSnapshot)

	owned, err := immutable.getImmutable(t.Context(), "default", "target")
	require.NoError(t, err)
	public, err := snapshot.Get("default", "target")
	require.NoError(t, err)
	setCachedSnapshotValue(t, public[0], "poison")
	again, err := immutable.getImmutable(t.Context(), "default", "target")
	require.NoError(t, err)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, again[0]))
	assert.Equal(t, reflect.ValueOf(owned[0]).Pointer(), reflect.ValueOf(again[0]).Pointer())
}

func TestCachedStoreOwnsFetchedValueBeforeImmutableRead(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	store := newProjectedSnapshotStore(t, client)
	source := cachedSnapshotResource("default", "target", "7", "original")
	require.NoError(t, store.Add(cachedSnapshotRef(source), []string{"default", "target"}))
	ref, found := store.readRoot.Load().locations.Get([]byte(resourceCacheKey("default", "target")))
	require.True(t, found)
	owned, err := store.cacheFetchedResource(&ref, source.Object, "7")
	require.NoError(t, err)
	setCachedSnapshotValue(t, source, "poison")

	snapshot, err := store.Pin()
	require.NoError(t, err)
	items, err := snapshot.(*cachedReadSnapshot).getImmutable(t.Context(), "default", "target")
	require.NoError(t, err)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, reflect.ValueOf(owned).Pointer(), reflect.ValueOf(items[0]).Pointer())
}

func TestCachedStoreSnapshotReadIsLazyStableAndDetached(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "original")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store := newProjectedSnapshotStore(t, client)
	require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", "target"}))

	snapshot, err := store.Pin()
	require.NoError(t, err)
	items, err := snapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	setCachedSnapshotValue(t, items[0], "poison")

	again, err := snapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, again[0]))
	assert.Equal(t, int32(1), gets.Load())

	live, err := store.Get("default", "target")
	require.NoError(t, err)
	setCachedSnapshotValue(t, live[0], "live poison")
	live, err = store.Get("default", "target")
	require.NoError(t, err)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, live[0]))
	assert.Equal(t, int32(1), gets.Load())
}

func TestCachedStoreSnapshotRejectsRootAPIDisagreement(t *testing.T) {
	tests := map[string]struct {
		objects []runtime.Object
		want    error
	}{
		"newer resourceVersion": {
			objects: []runtime.Object{cachedSnapshotResource("default", "target", "8", "newer")},
			want:    stores.ErrSnapshotChanged,
		},
		"pinned object missing": {
			want: stores.ErrSnapshotChanged,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), test.objects...)
			store := newProjectedSnapshotStore(t, client)
			pinned := cachedSnapshotResource("default", "target", "7", "pinned")
			require.NoError(t, store.Add(cachedSnapshotRef(pinned), []string{"default", "target"}))
			snapshot, err := store.Pin()
			require.NoError(t, err)

			items, err := snapshot.Get("default", "target")
			assert.Nil(t, items)
			assert.ErrorIs(t, err, test.want)
			assert.Zero(t, cacheLen(store))
		})
	}
}

func TestCachedStoreSnapshotRejectsInformerMutationDuringFetch(t *testing.T) {
	oldResource := cachedSnapshotResource("default", "target", "7", "old")
	newResource := cachedSnapshotResource("default", "target", "8", "new")
	resourceClient := &cacheGenerationResourceClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		first:        oldResource,
		later:        newResource,
	}
	store := newGenerationStore(t, &cacheGenerationDynamicClient{resource: resourceClient}, true)
	require.NoError(t, store.Add(cachedSnapshotRef(oldResource), []string{"default", "target"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, getErr := snapshot.Get("default", "target")
		result <- getErr
	}()
	select {
	case <-resourceClient.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot API fetch did not start")
	}
	require.NoError(t, store.Update(cachedSnapshotRef(newResource), []string{"default", "target"}))
	close(resourceClient.releaseFirst)
	select {
	case err := <-result:
		assert.ErrorIs(t, err, stores.ErrSnapshotChanged)
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot API fetch did not finish")
	}
	assert.Zero(t, cacheLen(store))
}

func TestCachedStoreSnapshotCanceledLoadCanRetry(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "value")
	resourceClient := &cacheGenerationResourceClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		first:        resource,
		later:        resource,
	}
	store := newGenerationStore(t, &cacheGenerationDynamicClient{resource: resourceClient}, true)
	require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	snapshotValue, err := store.Pin()
	require.NoError(t, err)
	snapshot := snapshotValue.(stores.ContextReadSnapshot)

	ctx, cancel := context.WithCancel(t.Context())
	firstResult := make(chan error, 1)
	go func() {
		_, getErr := snapshot.GetContext(ctx, "default", "target")
		firstResult <- getErr
	}()

	select {
	case <-resourceClient.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot API fetch did not start")
	}
	cancel()
	select {
	case getErr := <-firstResult:
		require.ErrorIs(t, getErr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled snapshot API fetch did not finish")
	}

	items, err := snapshot.GetContext(t.Context(), "default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "value", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, int32(2), resourceClient.calls.Load())
}

func TestCachedStoreSnapshotRejectsMovedIndexProof(t *testing.T) {
	oldResource := cachedSnapshotIndexedResource("default", "target", "7", "old", "A")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), oldResource)
	store := newIndexedProjectedSnapshotStore(t, client)
	require.NoError(t, store.Add(cachedSnapshotRef(oldResource), []string{"default", "A"}))
	oldSnapshot, err := store.Pin()
	require.NoError(t, err)
	oldRevision := oldSnapshot.GetRevision("default", "A")

	moved := cachedSnapshotIndexedResource("default", "target", "7", "moved", "B")
	_, err = client.Resource(configMapGVR).Namespace("default").Update(
		t.Context(), moved, metav1.UpdateOptions{},
	)
	require.NoError(t, err)

	items, err := oldSnapshot.Get("default", "A")
	assert.Nil(t, items)
	assert.ErrorIs(t, err, stores.ErrSnapshotChanged)

	require.NoError(t, store.Update(cachedSnapshotRef(moved), []string{"default", "B"}))
	newSnapshot, err := store.Pin()
	require.NoError(t, err)
	assert.NotEqual(t, oldRevision, newSnapshot.GetRevision("default", "A"))
	items, err = newSnapshot.Get("default", "A")
	require.NoError(t, err)
	assert.Empty(t, items)
	items, err = newSnapshot.Get("default", "B")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "moved", cachedSnapshotDataValue(t, items[0]))
}

func TestCachedStoreSnapshotRejectsDeleteRecreateABA(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "same")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	store := newProjectedSnapshotStore(t, client)
	require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	oldSnapshot, err := store.Pin()
	require.NoError(t, err)
	oldRevision := oldSnapshot.GetRevision("default", "target")
	oldIdentityRevision := oldSnapshot.IdentityRevision("default", "target")

	require.NoError(t, client.Resource(configMapGVR).Namespace("default").Delete(
		t.Context(), "target", metav1.DeleteOptions{},
	))
	require.NoError(t, store.Delete("default", "target", []string{"default", "target"}))
	recreated := resource.DeepCopy()
	_, err = client.Resource(configMapGVR).Namespace("default").Create(
		t.Context(), recreated, metav1.CreateOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, store.Add(cachedSnapshotRef(recreated), []string{"default", "target"}))

	items, err := oldSnapshot.Get("default", "target")
	assert.Nil(t, items)
	assert.ErrorIs(t, err, stores.ErrSnapshotChanged)

	newSnapshot, err := store.Pin()
	require.NoError(t, err)
	assert.NotEqual(t, oldRevision, newSnapshot.GetRevision("default", "target"))
	assert.NotEqual(t, oldIdentityRevision, newSnapshot.IdentityRevision("default", "target"))
	items, err = newSnapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "same", cachedSnapshotDataValue(t, items[0]))
}

func TestCachedStoreSnapshotRejectsUpdateAwayAndBackABA(t *testing.T) {
	original := cachedSnapshotResource("default", "target", "7", "original")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), original)
	store := newProjectedSnapshotStore(t, client)
	require.NoError(t, store.Add(cachedSnapshotRef(original), []string{"default", "target"}))
	oldSnapshot, err := store.Pin()
	require.NoError(t, err)
	oldRevision := oldSnapshot.GetRevision("default", "target")

	away := cachedSnapshotResource("default", "target", "8", "away")
	_, err = client.Resource(configMapGVR).Namespace("default").Update(
		t.Context(), away, metav1.UpdateOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, store.Update(cachedSnapshotRef(away), []string{"default", "target"}))

	back := cachedSnapshotResource("default", "target", "9", "original")
	_, err = client.Resource(configMapGVR).Namespace("default").Update(
		t.Context(), back, metav1.UpdateOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, store.Update(cachedSnapshotRef(back), []string{"default", "target"}))

	items, err := oldSnapshot.Get("default", "target")
	assert.Nil(t, items)
	assert.ErrorIs(t, err, stores.ErrSnapshotChanged)

	newSnapshot, err := store.Pin()
	require.NoError(t, err)
	assert.NotEqual(t, oldRevision, newSnapshot.GetRevision("default", "target"))
	items, err = newSnapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, items[0]))
}

func TestCachedStoreSnapshotConcurrentReadersRejectMutation(t *testing.T) {
	oldResource := cachedSnapshotResource("default", "target", "7", "old")
	newResource := cachedSnapshotResource("default", "target", "8", "new")
	resourceClient := &cacheGenerationResourceClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		first:        oldResource,
		later:        newResource,
	}
	store := newGenerationStore(t, &cacheGenerationDynamicClient{resource: resourceClient}, true)
	require.NoError(t, store.Add(cachedSnapshotRef(oldResource), []string{"default", "target"}))
	oldSnapshot, err := store.Pin()
	require.NoError(t, err)

	const readers = 32
	start := make(chan struct{})
	results := make(chan error, readers)
	var ready sync.WaitGroup
	ready.Add(readers)
	for range readers {
		go func() {
			ready.Done()
			<-start
			_, getErr := oldSnapshot.Get("default", "target")
			results <- getErr
		}()
	}
	ready.Wait()
	close(start)
	select {
	case <-resourceClient.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot API fetch did not start")
	}
	require.NoError(t, store.Update(cachedSnapshotRef(newResource), []string{"default", "target"}))
	close(resourceClient.releaseFirst)

	for range readers {
		select {
		case getErr := <-results:
			assert.ErrorIs(t, getErr, stores.ErrSnapshotChanged)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent snapshot reader did not finish")
		}
	}
	assert.Equal(t, int32(1), resourceClient.calls.Load())

	newSnapshot, err := store.Pin()
	require.NoError(t, err)
	items, err := newSnapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "new", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, int32(2), resourceClient.calls.Load())
}

func TestCachedStoreSnapshotBindsNegativeReads(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "value")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	store := newProjectedSnapshotStore(t, client)

	before, err := store.Pin()
	require.NoError(t, err)
	negativeRevision := before.GetRevision("default", "target")
	items, err := before.Get("default", "target")
	require.NoError(t, err)
	assert.Empty(t, items)

	require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	after, err := store.Pin()
	require.NoError(t, err)
	assert.NotEqual(t, negativeRevision, after.GetRevision("default", "target"))
	items, err = before.Get("default", "target")
	require.NoError(t, err)
	assert.Empty(t, items)
	items, err = after.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)

	current, changes, complete := store.ChangesSince(before.Sequence())
	assert.True(t, complete)
	assert.Equal(t, after.Sequence(), current)
	require.Len(t, changes, 1)
	assert.Equal(t, []string{"default", "target"}, changes[0].NewKeys)
}

func TestCachedStoreSnapshotListIsExplicitlyComplete(t *testing.T) {
	objects := make([]runtime.Object, 0, 3)
	storeObjects := make([]*unstructured.Unstructured, 0, 3)
	for index, name := range []string{"charlie", "alpha", "bravo"} {
		resource := cachedSnapshotResource("default", name, fmt.Sprintf("%d", index+1), name)
		objects = append(objects, resource)
		storeObjects = append(storeObjects, resource)
	}
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store := newProjectedSnapshotStore(t, client)
	for _, resource := range storeObjects {
		require.NoError(t, store.Add(
			cachedSnapshotRef(resource),
			[]string{resource.GetNamespace(), resource.GetName()},
		))
	}

	snapshotValue, err := store.Pin()
	require.NoError(t, err)
	snapshot := snapshotValue.(*cachedReadSnapshot)
	warm, err := snapshot.ListWarm()
	require.NoError(t, err)
	assert.Empty(t, warm)
	assert.Zero(t, gets.Load())

	items, err := snapshot.Get("default", "alpha")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int32(1), gets.Load())
	warm, err = snapshot.ListWarm()
	require.NoError(t, err)
	assert.Empty(t, warm, "the warm list is fixed at Pin")

	items, err = snapshot.List()
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, cachedSnapshotNames(t, items))
	assert.Equal(t, int32(3), gets.Load())

	nextValue, err := store.Pin()
	require.NoError(t, err)
	nextWarm, err := nextValue.(*cachedReadSnapshot).ListWarm()
	require.NoError(t, err)
	require.Len(t, nextWarm, 3)
	assert.Equal(t, int32(3), gets.Load())
}

func TestCachedStoreSnapshotSurvivesCacheEviction(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "original")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	store := newTestCachedStore(t, client, newProjectedTestIndexer(t), 2, time.Minute)
	require.NoError(t, store.Add(resource, []string{"default", "target"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)
	sequence := snapshot.Sequence()
	revision := snapshot.GetRevision("default", "target")

	store.mu.Lock()
	store.cache.Remove(resourceCacheKey("default", "target"))
	store.mu.Unlock()
	resource.Object["data"].(map[string]any)["key"] = "caller mutation"

	items, err := snapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, sequence, snapshot.Sequence())
	assert.Equal(t, revision, snapshot.GetRevision("default", "target"))
}

func TestCachedStoreWarmRootVersionsReplacement(t *testing.T) {
	original := cachedSnapshotResource("default", "target", "7", "original")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), original)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store := newTestCachedStore(t, client, newProjectedTestIndexer(t), 2, time.Minute)
	require.NoError(t, store.Add(original, []string{"default", "target"}))

	originalSnapshotValue, err := store.Pin()
	require.NoError(t, err)
	originalSnapshot := originalSnapshotValue.(*cachedReadSnapshot)
	originalRoot := originalSnapshot.root

	updated := cachedSnapshotResource("default", "target", "8", "updated")
	require.NoError(t, store.Update(updated, []string{"default", "target"}))
	updatedSnapshotValue, err := store.Pin()
	require.NoError(t, err)
	updatedSnapshot := updatedSnapshotValue.(*cachedReadSnapshot)
	assert.NotSame(t, originalRoot, updatedSnapshot.root)
	assert.NotSame(t, originalRoot.warm, updatedSnapshot.root.warm)

	originalItems, err := originalSnapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, originalItems, 1)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, originalItems[0]))
	setCachedSnapshotValue(t, originalItems[0], "caller mutation")
	originalItems, err = originalSnapshot.Get("default", "target")
	require.NoError(t, err)
	assert.Equal(t, "original", cachedSnapshotDataValue(t, originalItems[0]))

	updatedItems, err := updatedSnapshot.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, updatedItems, 1)
	assert.Equal(t, "updated", cachedSnapshotDataValue(t, updatedItems[0]))
	assert.Zero(t, gets.Load())
}

func TestCachedStoreWarmRootTracksCapacityEviction(t *testing.T) {
	first := cachedSnapshotResource("default", "first", "7", "first")
	second := cachedSnapshotResource("default", "second", "8", "second")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), first, second)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:      2,
		CacheTTL:     time.Minute,
		MaxCacheSize: 1,
		Client:       client,
		GVR:          configMapGVR,
		Indexer:      newProjectedTestIndexer(t),
	})
	require.NoError(t, err)
	require.NoError(t, store.Add(first, []string{"default", "first"}))

	beforeEvictionValue, err := store.Pin()
	require.NoError(t, err)
	beforeEviction := beforeEvictionValue.(*cachedReadSnapshot)
	require.NoError(t, store.Add(second, []string{"default", "second"}))
	afterEvictionValue, err := store.Pin()
	require.NoError(t, err)
	afterEviction := afterEvictionValue.(*cachedReadSnapshot)

	assert.Equal(t, 1, beforeEviction.root.warm.Len())
	assert.Equal(t, 1, afterEviction.root.warm.Len())
	_, firstStillWarm := beforeEviction.root.warm.Get([]byte(resourceCacheKey("default", "first")))
	assert.True(t, firstStillWarm)
	_, firstWarmAfterEviction := afterEviction.root.warm.Get([]byte(resourceCacheKey("default", "first")))
	assert.False(t, firstWarmAfterEviction)
	_, secondWarmAfterEviction := afterEviction.root.warm.Get([]byte(resourceCacheKey("default", "second")))
	assert.True(t, secondWarmAfterEviction)

	items, err := beforeEviction.Get("default", "first")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "first", cachedSnapshotDataValue(t, items[0]))
	assert.Zero(t, gets.Load())

	warm, err := afterEviction.ListWarm()
	require.NoError(t, err)
	require.Len(t, warm, 1)
	_, warmName := extractNamespaceName(warm[0])
	assert.Equal(t, "second", warmName)
	items, err = afterEviction.Get("default", "first")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "first", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, int32(1), gets.Load())
}

func TestCachedStoreWarmRootTracksTTLRenewal(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "7", "value")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	store := newTestCachedStore(t, client, newProjectedTestIndexer(t), 2, time.Minute)
	require.NoError(t, store.Add(resource, []string{"default", "target"}))
	cacheKey := []byte(resourceCacheKey("default", "target"))
	before, found := store.readRoot.Load().warm.Get(cacheKey)
	require.True(t, found)

	store.mu.Lock()
	store.cacheTTL = time.Hour
	store.mu.Unlock()
	items, err := store.Get("default", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	after, found := store.readRoot.Load().warm.Get(cacheKey)
	require.True(t, found)
	assert.Greater(t, after.expiresAt.Sub(before.expiresAt), 30*time.Minute)

	snapshotValue, err := store.Pin()
	require.NoError(t, err)
	warm, err := snapshotValue.(*cachedReadSnapshot).ListWarm()
	require.NoError(t, err)
	require.Len(t, warm, 1)
	assert.Equal(t, "value", cachedSnapshotDataValue(t, warm[0]))
}

func TestCachedStoreSnapshotRequiresResourceVersionForLazyBody(t *testing.T) {
	resource := createTestResource("default", "target")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	store := newProjectedSnapshotStore(t, client)
	require.NoError(t, store.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)

	items, err := snapshot.Get("default", "target")
	assert.Nil(t, items)
	assert.ErrorIs(t, err, stores.ErrSnapshotUnsupported)
	assert.Zero(t, gets.Load())
}

func newProjectedSnapshotStore(t *testing.T, client *fake.FakeDynamicClient) *CachedStore {
	t.Helper()
	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:   2,
		CacheTTL:  time.Minute,
		Client:    client,
		GVR:       schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		Indexer:   newProjectedTestIndexer(t),
		Projected: true,
	})
	require.NoError(t, err)
	return store
}

func newIndexedProjectedSnapshotStore(t *testing.T, client *fake.FakeDynamicClient) *CachedStore {
	t.Helper()
	idx, err := indexer.New(indexer.Config{
		IndexBy: []string{"metadata.namespace", "metadata.labels.bucket"},
	})
	require.NoError(t, err)
	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:   2,
		CacheTTL:  time.Minute,
		Client:    client,
		GVR:       configMapGVR,
		Indexer:   idx,
		Projected: true,
	})
	require.NoError(t, err)
	return store
}

func countCachedSnapshotGets(counter *atomic.Int32) k8stesting.ReactionFunc {
	return func(k8stesting.Action) (bool, runtime.Object, error) {
		counter.Add(1)
		return false, nil, nil
	}
}

func cachedSnapshotResource(namespace, name, resourceVersion, value string) *unstructured.Unstructured {
	resource := createTestResource(namespace, name)
	resource.SetResourceVersion(resourceVersion)
	resource.Object["data"] = map[string]any{"key": value}
	return resource
}

func cachedSnapshotIndexedResource(
	namespace, name, resourceVersion, value, bucket string,
) *unstructured.Unstructured {
	resource := cachedSnapshotResource(namespace, name, resourceVersion, value)
	resource.SetLabels(map[string]string{"bucket": bucket})
	return resource
}

func cachedSnapshotRef(resource *unstructured.Unstructured) *unstructured.Unstructured {
	ref := resource.DeepCopy()
	delete(ref.Object, "data")
	return ref
}

func cachedSnapshotDataValue(t *testing.T, resource any) string {
	t.Helper()
	object, ok := resource.(map[string]any)
	if !ok {
		unstructuredResource, isUnstructured := resource.(*unstructured.Unstructured)
		require.True(t, isUnstructured, "resource has type %T", resource)
		object = unstructuredResource.Object
	}
	data, ok := object["data"].(map[string]any)
	require.True(t, ok, "resource data has type %T", object["data"])
	value, ok := data["key"].(string)
	require.True(t, ok, "resource value has type %T", data["key"])
	return value
}

func setCachedSnapshotValue(t *testing.T, resource any, value string) {
	t.Helper()
	object, ok := resource.(map[string]any)
	if !ok {
		unstructuredResource, isUnstructured := resource.(*unstructured.Unstructured)
		require.True(t, isUnstructured, "resource has type %T", resource)
		object = unstructuredResource.Object
	}
	object["data"].(map[string]any)["key"] = value
}

func cachedSnapshotNames(t *testing.T, resources []any) []string {
	t.Helper()
	names := make([]string, len(resources))
	for index, resource := range resources {
		namespace, name := extractNamespaceName(resource)
		require.Equal(t, "default", namespace)
		names[index] = name
	}
	return names
}

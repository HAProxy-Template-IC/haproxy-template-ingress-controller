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

package renderer

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestColdCarrierResolverReadsDistinctResourceInputsConcurrently(t *testing.T) {
	const resourceCount = 512
	store := k8sstore.NewMemoryStore(2)
	for index := range resourceCount {
		name := fmt.Sprintf("route-%04d", index)
		err := store.Add(map[string]any{
			"apiVersion": "cache.test/v1",
			"kind":       "Route",
			"metadata": map[string]any{
				"namespace": "default",
				"name":      name,
			},
		}, []string{"default", name})
		require.NoError(t, err)
	}
	snapshot, err := store.Pin()
	require.NoError(t, err)
	session := &incrementalRenderSession{
		state: &incrementalRenderState{
			config:    &config.Config{},
			httpSpecs: map[uint64]httpInputSpec{},
		},
		bindingPlan: &incrementalBindingPlan{
			props:  map[string][]byte{},
			owners: map[string]incrementalComponent{},
		},
		renderSnapshots:         map[string]stores.ReadSnapshot{"routes": snapshot},
		cursors:                 map[string]incrementalStoreCursor{},
		httpObserved:            map[incremental.InputKey]incremental.Input{},
		cachePublicationEnabled: true,
	}
	session.resetCatalog(nil)

	start := make(chan struct{})
	errors := make(chan error, resourceCount)
	var group sync.WaitGroup
	for index := range resourceCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			name := fmt.Sprintf("route-%04d", index)
			spec := resourceInputSpec{
				resourceType: "routes",
				scope:        resourceInputGet,
				keys:         []string{"default", name},
			}
			input, resolveErr := session.resolveInput(t.Context(), resourceInputKey(&spec))
			if resolveErr != nil {
				errors <- resolveErr
				return
			}
			if !input.Found {
				errors <- fmt.Errorf("resource %s was absent", name)
			}
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for resolveErr := range errors {
		require.NoError(t, resolveErr)
	}
	committed, err := session.catalogCommit()
	require.NoError(t, err)
	assert.Equal(t, resourceCount, committed.Len())
}

func TestIncrementalResourceCatalogRejectsPoisonedEntry(t *testing.T) {
	wanted := resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputGet,
		keys:         []string{"default", "route"},
	}
	key := resourceInputKey(&wanted)
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	shard := session.catalog.shard(key)
	shard.changes[key] = incrementalResourceCatalogMutation{
		owner: session.catalog, generation: session.catalog.generation,
		key:   incremental.NewInputKey(key.Opaque() + "-poison"),
		state: incrementalResourceCatalogPresent,
	}

	err := session.catalogLoadOrStore(key, &wanted)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalResourceCatalogShardCollisionKeepsExactKeys(t *testing.T) {
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	type candidate struct {
		key  incremental.InputKey
		spec resourceInputSpec
	}
	seen := map[*incrementalResourceCatalogShard]candidate{}
	var first, second candidate
	for index := range incrementalResourceCatalogShardCount + 1 {
		spec := resourceCatalogTestSpec(index)
		key := resourceInputKey(&spec)
		shard := session.catalog.shard(key)
		if previous, exists := seen[shard]; exists {
			first, second = previous, candidate{key: key, spec: spec}
			break
		}
		seen[shard] = candidate{key: key, spec: spec}
	}
	require.NotEmpty(t, first.key.Opaque())
	require.NotEqual(t, first.key, second.key)
	require.Same(t, session.catalog.shard(first.key), session.catalog.shard(second.key))
	err := session.catalogLoadOrStore(first.key, &first.spec)
	require.NoError(t, err)
	err = session.catalogLoadOrStore(second.key, &second.spec)
	require.NoError(t, err)

	opened, exists, err := session.catalogGet(first.key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, first.spec, opened)
	opened, exists, err = session.catalogGet(second.key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, second.spec, opened)

	shard := session.catalog.shard(first.key)
	firstMutation := shard.changes[first.key]
	shard.changes[first.key] = shard.changes[second.key]
	_, _, err = session.catalogGet(first.key)
	require.ErrorContains(t, err, "invalid provenance")
	shard.changes[first.key] = firstMutation

	committed, err := session.catalogCommit()
	require.NoError(t, err)
	assert.Equal(t, 2, committed.Len())
}

func TestIncrementalResourceCatalogRejectsCandidateAndMutationTampering(t *testing.T) {
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	spec := resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputGet,
		keys:         []string{"default", "route"},
	}
	key := resourceInputKey(&spec)
	err := session.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)

	spec.keys[1] = "tampered"
	opened, exists, err := session.catalogGet(key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, []string{"default", "route"}, opened.keys)
	err = session.catalogLoadOrStore(key, &spec)
	require.ErrorContains(t, err, "invalid provenance")

	shard := session.catalog.shard(key)
	mutation := shard.changes[key]
	mutation.state = 0
	shard.changes[key] = mutation
	_, _, err = session.catalogGet(key)
	require.ErrorContains(t, err, "invalid provenance")
	_, err = session.catalogCommit()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalResourceCatalogRejectsCopiedAndForeignOwnership(t *testing.T) {
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	spec := resourceCatalogTestSpec(1)
	key := resourceInputKey(&spec)
	err := session.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)

	copied := *session.catalog
	original := session.catalog
	session.catalog = &copied
	_, _, err = session.catalogGet(key)
	require.ErrorContains(t, err, "invalid ownership")
	session.catalog = original

	foreignSession := &incrementalRenderSession{}
	foreignSession.resetCatalog(nil)
	err = foreignSession.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)
	foreignMutation := foreignSession.catalog.shard(key).changes[key]
	shard := session.catalog.shard(key)
	shard.changes[key] = foreignMutation
	_, _, err = session.catalogGet(key)
	require.ErrorContains(t, err, "invalid provenance")

	session.catalog = foreignSession.catalog
	_, _, err = session.catalogGet(key)
	require.ErrorContains(t, err, "invalid ownership")
}

func TestIncrementalResourceCatalogRejectsAwayAndBackMutationReplay(t *testing.T) {
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	spec := resourceCatalogTestSpec(1)
	key := resourceInputKey(&spec)
	err := session.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)
	stale := session.catalog.shard(key).changes[key]

	session.resetCatalog(nil)
	err = session.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)
	shard := session.catalog.shard(key)
	current := shard.changes[key]
	require.NotEqual(t, stale.generation, current.generation)
	opened, exists, err := session.catalogGet(key)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, spec, opened)

	shard.changes[key] = stale
	_, _, err = session.catalogGet(key)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalResourceCatalogConcurrentExactAccess(t *testing.T) {
	const (
		keyCount    = 256
		workerCount = 32
		iterations  = 1000
	)
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	specs := make([]resourceInputSpec, keyCount)
	keys := make([]incremental.InputKey, keyCount)
	for index := range keyCount {
		specs[index] = resourceCatalogTestSpec(index)
		keys[index] = resourceInputKey(&specs[index])
	}

	start := make(chan struct{})
	errors := make(chan error, workerCount)
	var workers sync.WaitGroup
	for worker := range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := range iterations {
				index := (worker + iteration) % keyCount
				var err error
				switch iteration % 3 {
				case 0:
					err = session.catalogLoadOrStore(keys[index], &specs[index])
				case 1:
					_, _, err = session.catalogGet(keys[index])
				case 2:
					err = session.catalogDelete(keys[index])
				}
				if err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	for index := range keyCount {
		require.NoError(t, session.catalogInsert(keys[index], &specs[index]))
	}
	committed, err := session.catalogCommit()
	require.NoError(t, err)
	assert.Equal(t, keyCount, committed.Len())
}

func TestIncrementalResourceCatalogRejectsCopiedSnapshot(t *testing.T) {
	session := &incrementalRenderSession{}
	session.resetCatalog(nil)
	spec := resourceCatalogTestSpec(1)
	key := resourceInputKey(&spec)
	err := session.catalogLoadOrStore(key, &spec)
	require.NoError(t, err)
	snapshot, err := session.catalogCommit()
	require.NoError(t, err)
	copied := *snapshot

	session.resetCatalog(&copied)
	_, exists, err := session.catalogGet(key)
	require.NoError(t, err)
	require.False(t, exists)
}

func resourceCatalogTestSpec(index int) resourceInputSpec {
	return resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputIdentity,
		namespace:    "default",
		name:         fmt.Sprintf("route-%06d", index),
	}
}

type legacyIncrementalResourceCatalog struct {
	mu      sync.RWMutex
	entries *iradix.Txn[resourceInputSpec]
}

func (c *legacyIncrementalResourceCatalog) loadOrStore(
	key incremental.InputKey,
	candidate *resourceInputSpec,
) resourceInputSpec {
	if resourceInputKey(candidate) != key {
		panic("invalid legacy catalog candidate")
	}
	rawKey := []byte(key.Opaque())
	c.mu.RLock()
	known, exists := c.entries.Get(rawKey)
	c.mu.RUnlock()
	if exists {
		if resourceInputKey(&known) != key {
			panic("invalid legacy catalog entry")
		}
		return known
	}
	c.mu.Lock()
	if known, exists = c.entries.Get(rawKey); !exists {
		c.entries.Insert(rawKey, *candidate)
		known = *candidate
	}
	if resourceInputKey(&known) != key {
		panic("invalid legacy catalog entry")
	}
	c.mu.Unlock()
	return known
}

func BenchmarkIncrementalResourceCatalogConcurrentColdStore(b *testing.B) {
	const storeCount = 8192
	specs := make([]resourceInputSpec, storeCount)
	keys := make([]incremental.InputKey, storeCount)
	for index := range storeCount {
		specs[index] = resourceCatalogTestSpec(index)
		keys[index] = resourceInputKey(&specs[index])
	}

	b.Run("legacy-global-lock", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			catalog := &legacyIncrementalResourceCatalog{entries: iradix.New[resourceInputSpec]().Txn()}
			b.StartTimer()
			runParallelCatalogBatch(storeCount, func(index int) {
				catalog.loadOrStore(keys[index], &specs[index])
			})
		}
		b.ReportMetric(float64(b.N*storeCount)/b.Elapsed().Seconds(), "stores/s")
	})
	b.Run("sharded-exact-keys", func(b *testing.B) {
		session := &incrementalRenderSession{}
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			session.resetCatalog(nil)
			b.StartTimer()
			runParallelCatalogBatch(storeCount, func(index int) {
				if err := session.catalogLoadOrStore(keys[index], &specs[index]); err != nil {
					panic(err)
				}
			})
		}
		b.ReportMetric(float64(b.N*storeCount)/b.Elapsed().Seconds(), "stores/s")
	})
}

func runParallelCatalogBatch(count int, operation func(int)) {
	const workerCount = 32
	var next atomic.Uint64
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= count {
					return
				}
				operation(index)
			}
		}()
	}
	workers.Wait()
}

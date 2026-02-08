// Copyright 2025 Philipp Hossner
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

package validators

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_GetAdd(t *testing.T) {
	cache := NewCache()

	// Miss on empty cache
	_, ok := cache.Get(42)
	assert.False(t, ok)

	// Add nil error (valid entry)
	cache.Add(42, nil)
	result, ok := cache.Get(42)
	assert.True(t, ok)
	assert.NoError(t, result)

	// Add non-nil error
	testErr := errors.New("validation failed")
	cache.Add(100, testErr)
	result, ok = cache.Get(100)
	assert.True(t, ok)
	assert.Equal(t, testErr, result)
}

func TestCache_LRUEviction(t *testing.T) {
	cache := NewCache()

	// All entries go to shard 0 (hash % 64 == 0)
	// Fill shard beyond capacity
	for i := 0; i < ShardSize+10; i++ {
		hash := uint64(i * NumShards) // All map to shard 0
		cache.Add(hash, nil)
	}

	// Shard 0 should be capped at ShardSize
	shard := cache.getShard(0)
	assert.Equal(t, ShardSize, shard.Len())

	// Early entries should be evicted
	_, ok := cache.Get(0)
	assert.False(t, ok, "earliest entry should be evicted")

	// Recent entries should still exist
	recentHash := uint64(ShardSize * NumShards)
	_, ok = cache.Get(recentHash)
	assert.True(t, ok, "recent entry should exist")
}

func TestCache_ConcurrentAccess(t *testing.T) {
	cache := NewCache()

	var wg sync.WaitGroup
	goroutines := 100
	opsPerGoroutine := 1000

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				hash := uint64(id*opsPerGoroutine + i)
				cache.Add(hash, nil)
				cache.Get(hash)
			}
		}(g)
	}

	wg.Wait()

	// Should not panic and cache should have entries
	assert.Greater(t, cache.Len(), 0)
}

func TestCache_Purge(t *testing.T) {
	cache := NewCache()

	// Add entries across multiple shards
	for i := uint64(0); i < 100; i++ {
		cache.Add(i, nil)
	}
	require.Greater(t, cache.Len(), 0)

	// Purge and verify
	cache.Purge()
	assert.Equal(t, 0, cache.Len())

	// Verify entries are gone
	_, ok := cache.Get(0)
	assert.False(t, ok)
}

func TestCache_Stats(t *testing.T) {
	cache := NewCache()

	stats := cache.Stats()
	assert.Equal(t, 0, stats.Entries)
	assert.Equal(t, NumShards, stats.Shards)

	// Add some entries
	for i := uint64(0); i < 50; i++ {
		cache.Add(i, nil)
	}

	stats = cache.Stats()
	assert.Equal(t, 50, stats.Entries)
	assert.Equal(t, NumShards, stats.Shards)
}

func TestCache_Len(t *testing.T) {
	cache := NewCache()

	assert.Equal(t, 0, cache.Len())

	// Add entries that go to different shards
	for i := uint64(0); i < 200; i++ {
		cache.Add(i, nil)
	}

	assert.Equal(t, 200, cache.Len())
}

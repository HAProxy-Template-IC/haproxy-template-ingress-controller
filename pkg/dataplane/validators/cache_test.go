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
)

// cacheLen counts entries across all shards (test-only observability).
func cacheLen(c *Cache) int {
	total := 0
	for i := range c.shards {
		total += c.shards[i].Len()
	}
	return total
}

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
	for i := range ShardSize + 10 {
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
	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			for i := range opsPerGoroutine {
				hash := uint64(id*opsPerGoroutine + i)
				cache.Add(hash, nil)
				cache.Get(hash)
			}
		}(g)
	}

	wg.Wait()

	// Should not panic and cache should have entries
	assert.Greater(t, cacheLen(cache), 0)
}

func TestCache_Len(t *testing.T) {
	cache := NewCache()

	assert.Equal(t, 0, cacheLen(cache))

	// Add entries that go to different shards
	for i := range uint64(200) {
		cache.Add(i, nil)
	}

	assert.Equal(t, 200, cacheLen(cache))
}

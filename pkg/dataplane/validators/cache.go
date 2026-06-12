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
	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	// NumShards is the number of cache shards for reduced lock contention.
	NumShards = 64
	// ShardSize is the maximum entries per shard (64 * 1024 = 65536 total).
	ShardSize = 1024
)

// Cache provides a sharded LRU cache for validation results.
// It uses content-based hashing to achieve high cache hit rates for
// template-driven configurations that produce repetitive models.
//
// Thread-safe: hashicorp/golang-lru is internally synchronized.
type Cache struct {
	shards [NumShards]*lru.Cache[uint64, error]
}

// NewCache creates a new validation cache with sharded LRU caches.
func NewCache() *Cache {
	c := &Cache{}
	for i := range c.shards {
		// Ignore error - lru.New only fails with size <= 0
		c.shards[i], _ = lru.New[uint64, error](ShardSize)
	}
	return c
}

// getShard returns the appropriate shard for a hash value.
func (c *Cache) getShard(hash uint64) *lru.Cache[uint64, error] {
	return c.shards[hash%NumShards]
}

// Get retrieves a cached validation result.
func (c *Cache) Get(hash uint64) (error, bool) {
	return c.getShard(hash).Get(hash)
}

// Add stores a validation result in the cache.
func (c *Cache) Add(hash uint64, result error) {
	c.getShard(hash).Add(hash, result)
}

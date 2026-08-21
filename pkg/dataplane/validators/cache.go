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

//go:build playground

package validators

import (
	lru "github.com/hashicorp/golang-lru/v2"
)

// CacheSize is the maximum number of entries the validation cache holds.
const CacheSize = 65536

// Cache provides an LRU cache for validation results.
// It uses content-based hashing to achieve high cache hit rates for
// template-driven configurations that produce repetitive models.
//
// Thread-safe: hashicorp/golang-lru is internally synchronized, so a single
// cache needs no extra locking or sharding.
type Cache struct {
	lru *lru.Cache[uint64, error]
}

// NewCache creates a new validation cache.
func NewCache() *Cache {
	// Ignore error - lru.New only fails with size <= 0.
	c, _ := lru.New[uint64, error](CacheSize)
	return &Cache{lru: c}
}

// Get retrieves a cached validation result.
func (c *Cache) Get(hash uint64) (error, bool) {
	return c.lru.Get(hash)
}

// Add stores a validation result in the cache.
func (c *Cache) Add(hash uint64, result error) {
	c.lru.Add(hash, result)
}

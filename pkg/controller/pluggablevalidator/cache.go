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

package pluggablevalidator

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// DefaultCacheCapacity is the default LRU cache size. Sized for a healthy
// reconciliation churn — one entry per (validator, distinct-content) pair.
const DefaultCacheCapacity = 256

// CacheKey identifies a cache entry. The validator name keeps entries from
// different validators isolated even when their request bodies happen to
// match.
type CacheKey struct {
	ValidatorName string
	ContentSHA256 string // hex-encoded sha256 of the request body
}

// HashContent returns the hex-encoded sha256 of the given content. Used to
// build CacheKeys without keeping the full payload around. Stable across
// goroutines.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// NewCacheKey builds a CacheKey for a given validator + raw request body.
func NewCacheKey(validatorName string, content []byte) CacheKey {
	return CacheKey{ValidatorName: validatorName, ContentSHA256: HashContent(content)}
}

// ResultCache is a process-local LRU cache mapping CacheKey to *Response.
// Concurrent access is serialised via an internal mutex; the cache is safe
// for use by multiple goroutines.
type ResultCache struct {
	capacity int
	mu       sync.Mutex
	entries  map[CacheKey]*list.Element
	order    *list.List // front = most recent, back = least recent
}

type cacheEntry struct {
	key      CacheKey
	response *Response
}

// NewResultCache returns a cache with the given capacity. A capacity <= 0
// falls back to DefaultCacheCapacity.
func NewResultCache(capacity int) *ResultCache {
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	return &ResultCache{
		capacity: capacity,
		entries:  make(map[CacheKey]*list.Element, capacity),
		order:    list.New(),
	}
}

// Get returns the cached Response for a key, or (nil, false) on miss. A hit
// promotes the entry to most-recently-used.
func (c *ResultCache) Get(key CacheKey) (*Response, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).response, true
}

// Put records a Response under the key. If the cache is at capacity, the
// least-recently-used entry is evicted first.
//
// The Response is stored by reference; callers MUST NOT mutate it after
// caching.
func (c *ResultCache) Put(key CacheKey, response *Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		// Update existing entry: replace response, promote to front.
		el.Value.(*cacheEntry).response = response
		c.order.MoveToFront(el)
		return
	}
	if c.order.Len() >= c.capacity {
		// Evict LRU before inserting.
		oldest := c.order.Back()
		if oldest != nil {
			delete(c.entries, oldest.Value.(*cacheEntry).key)
			c.order.Remove(oldest)
		}
	}
	el := c.order.PushFront(&cacheEntry{key: key, response: response})
	c.entries[key] = el
}

// Len returns the number of cached entries. For tests and metrics.
func (c *ResultCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

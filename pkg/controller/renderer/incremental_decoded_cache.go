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
	"errors"
	"sync"
)

const (
	incrementalDecodedCacheShardCount = 128
	incrementalDecodedCacheHashOffset = uint64(14695981039346656037)
	incrementalDecodedCacheHashPrime  = uint64(1099511628211)
)

var (
	errIncrementalDecodedCacheProvenance = errors.New("incremental decoded cache has invalid provenance")
	errIncrementalDecodedCacheBuildPanic = errors.New("incremental decoded cache construction panicked")
)

type incrementalDecodedCache[K comparable, V any] struct {
	shards [incrementalDecodedCacheShardCount]incrementalDecodedCacheShard[K, V]
}

type incrementalDecodedCacheShard[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]*incrementalDecodedCacheEntry[K, V]
}

type incrementalDecodedCacheEntry[K comparable, V any] struct {
	key   K
	hash  uint64
	ready chan struct{}
	value V
	err   error
	seal  *incrementalDecodedCacheEntry[K, V]
}

func (c *incrementalDecodedCache[K, V]) load(key K, hash uint64) (value V, exists bool, err error) {
	shard := &c.shards[hash&(incrementalDecodedCacheShardCount-1)]
	shard.mu.Lock()
	entry := shard.entries[key]
	if entry == nil {
		shard.mu.Unlock()
		var zero V
		return zero, false, nil
	}
	if !incrementalDecodedCacheEntryMatches(entry, key, hash) {
		shard.mu.Unlock()
		var zero V
		return zero, false, errIncrementalDecodedCacheProvenance
	}
	shard.mu.Unlock()
	value, err = awaitIncrementalDecodedCacheEntry(entry, key, hash)
	if err != nil {
		var zero V
		return zero, false, err
	}
	return value, true, nil
}

func (c *incrementalDecodedCache[K, V]) loadOrCompute(
	key K,
	hash uint64,
	compute func() (V, error),
) (V, error) {
	shard := &c.shards[hash&(incrementalDecodedCacheShardCount-1)]
	shard.mu.Lock()
	if entry := shard.entries[key]; entry != nil {
		if !incrementalDecodedCacheEntryMatches(entry, key, hash) {
			shard.mu.Unlock()
			var zero V
			return zero, errIncrementalDecodedCacheProvenance
		}
		shard.mu.Unlock()
		return awaitIncrementalDecodedCacheEntry(entry, key, hash)
	}
	if shard.entries == nil {
		shard.entries = make(map[K]*incrementalDecodedCacheEntry[K, V])
	}
	entry := &incrementalDecodedCacheEntry[K, V]{
		key:   key,
		hash:  hash,
		ready: make(chan struct{}),
	}
	entry.seal = entry
	shard.entries[key] = entry
	shard.mu.Unlock()

	value, panicValue, err := incrementalDecodedCacheCompute(compute)
	entry.value = value
	entry.err = err
	close(entry.ready)
	if err != nil {
		shard.mu.Lock()
		if shard.entries[key] == entry {
			delete(shard.entries, key)
		}
		shard.mu.Unlock()
	}
	if panicValue != nil {
		panic(panicValue)
	}
	return value, err
}

func incrementalDecodedCacheCompute[V any](compute func() (V, error)) (value V, panicValue any, err error) {
	func() {
		defer func() {
			panicValue = recover()
		}()
		value, err = compute()
	}()
	if panicValue != nil {
		err = errIncrementalDecodedCacheBuildPanic
	}
	return value, panicValue, err
}

func incrementalDecodedCacheEntryMatches[K comparable, V any](
	entry *incrementalDecodedCacheEntry[K, V],
	key K,
	hash uint64,
) bool {
	return entry != nil && entry.seal == entry && entry.key == key && entry.hash == hash && entry.ready != nil
}

func awaitIncrementalDecodedCacheEntry[K comparable, V any](
	entry *incrementalDecodedCacheEntry[K, V],
	key K,
	hash uint64,
) (V, error) {
	if !incrementalDecodedCacheEntryMatches(entry, key, hash) {
		var zero V
		return zero, errIncrementalDecodedCacheProvenance
	}
	<-entry.ready
	if !incrementalDecodedCacheEntryMatches(entry, key, hash) {
		var zero V
		return zero, errIncrementalDecodedCacheProvenance
	}
	return entry.value, entry.err
}

func (c *incrementalDecodedCache[K, V]) reset() {
	for index := range c.shards {
		shard := &c.shards[index]
		shard.mu.Lock()
		shard.entries = nil
		shard.mu.Unlock()
	}
}

func (c *incrementalDecodedCache[K, V]) len() int {
	total := 0
	for index := range c.shards {
		shard := &c.shards[index]
		shard.mu.Lock()
		total += len(shard.entries)
		shard.mu.Unlock()
	}
	return total
}

func incrementalDecodedCacheStringHash(value string) uint64 {
	hash := incrementalDecodedCacheHashOffset
	for index := 0; index < len(value); index++ {
		hash ^= uint64(value[index])
		hash *= incrementalDecodedCacheHashPrime
	}
	return hash
}

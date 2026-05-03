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
	"sync"
	"testing"
)

func TestHashContent_Stable(t *testing.T) {
	a := HashContent([]byte("[hub]\nlisten = \"0.0.0.0:9000\""))
	b := HashContent([]byte("[hub]\nlisten = \"0.0.0.0:9000\""))
	if a != b {
		t.Fatalf("HashContent should be deterministic; got %q vs %q", a, b)
	}
	c := HashContent([]byte("[hub]\nlisten = \"0.0.0.0:9001\""))
	if a == c {
		t.Fatal("differing content must hash differently")
	}
}

func TestHashContent_Concurrent(t *testing.T) {
	const n = 100
	content := []byte("identical input")
	want := HashContent(content)

	results := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = HashContent(content)
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got != want {
			t.Fatalf("goroutine %d returned %q, want %q", i, got, want)
		}
	}
}

func TestResultCache_HitAndMiss(t *testing.T) {
	cache := NewResultCache(8)
	key := NewCacheKey("coraza", []byte("config-a"))

	if _, ok := cache.Get(key); ok {
		t.Fatal("empty cache reported hit")
	}
	resp := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}
	cache.Put(key, resp)
	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("Put then Get reported miss")
	}
	if got != resp {
		t.Fatal("returned Response is not the cached pointer (must be byte-identical)")
	}
}

func TestResultCache_DifferentValidators(t *testing.T) {
	cache := NewResultCache(8)
	contentA := []byte("identical-content")
	keyForA := NewCacheKey("coraza", contentA)
	keyForB := NewCacheKey("otel", contentA)
	if keyForA == keyForB {
		t.Fatal("keys must differ when validator names differ even with identical content")
	}

	respCoraza := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}
	respOtel := &Response{ProtocolVersion: ProtocolVersion, Result: ResultError}
	cache.Put(keyForA, respCoraza)
	cache.Put(keyForB, respOtel)

	gotCoraza, _ := cache.Get(keyForA)
	gotOtel, _ := cache.Get(keyForB)
	if gotCoraza == gotOtel {
		t.Fatal("validators with the same content key must NOT share cache entries")
	}
}

func TestResultCache_CapacityEviction(t *testing.T) {
	cache := NewResultCache(2)
	keyA := NewCacheKey("v", []byte("a"))
	keyB := NewCacheKey("v", []byte("b"))
	keyC := NewCacheKey("v", []byte("c"))
	resp := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}

	cache.Put(keyA, resp)
	cache.Put(keyB, resp)
	cache.Put(keyC, resp) // should evict A (LRU)

	if _, ok := cache.Get(keyA); ok {
		t.Fatal("A should have been evicted by capacity-bounded eviction")
	}
	if _, ok := cache.Get(keyB); !ok {
		t.Fatal("B should still be in cache")
	}
	if _, ok := cache.Get(keyC); !ok {
		t.Fatal("C should still be in cache")
	}
	if cache.Len() != 2 {
		t.Fatalf("cache.Len() = %d, want 2", cache.Len())
	}
}

func TestResultCache_AccessPromotesEntry(t *testing.T) {
	cache := NewResultCache(2)
	keyA := NewCacheKey("v", []byte("a"))
	keyB := NewCacheKey("v", []byte("b"))
	keyC := NewCacheKey("v", []byte("c"))
	resp := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}

	cache.Put(keyA, resp)
	cache.Put(keyB, resp)
	// Touch A so B becomes the LRU candidate.
	if _, ok := cache.Get(keyA); !ok {
		t.Fatal("expected A to be in cache before promotion")
	}
	cache.Put(keyC, resp) // must evict B (now LRU), not A

	if _, ok := cache.Get(keyA); !ok {
		t.Fatal("A was promoted before C was inserted; should still be cached")
	}
	if _, ok := cache.Get(keyB); ok {
		t.Fatal("B should have been evicted after being demoted to LRU")
	}
}

func TestResultCache_ZeroCapacityDefaults(t *testing.T) {
	cache := NewResultCache(0)
	// Should not panic and should accept entries up to the default cap.
	for i := range DefaultCacheCapacity + 1 {
		key := NewCacheKey("v", []byte{byte(i)})
		cache.Put(key, &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid})
	}
	if cache.Len() != DefaultCacheCapacity {
		t.Fatalf("cache.Len() = %d, want %d", cache.Len(), DefaultCacheCapacity)
	}
}

func TestResultCache_PutOverwrite(t *testing.T) {
	cache := NewResultCache(8)
	key := NewCacheKey("v", []byte("a"))
	resp1 := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}
	resp2 := &Response{ProtocolVersion: ProtocolVersion, Result: ResultError}

	cache.Put(key, resp1)
	cache.Put(key, resp2)

	got, _ := cache.Get(key)
	if got.Result != ResultError {
		t.Fatalf("overwrite did not take effect; got %q want %q", got.Result, ResultError)
	}
	if cache.Len() != 1 {
		t.Fatalf("overwrite duplicated entry; len=%d want 1", cache.Len())
	}
}

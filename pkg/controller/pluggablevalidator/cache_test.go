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
	key := NewCacheKey("coraza", "/etc/x/config.toml", []byte("config-a"))

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
	path := "/etc/x/config.toml"
	contentA := []byte("identical-content")
	keyForA := NewCacheKey("coraza", path, contentA)
	keyForB := NewCacheKey("otel", path, contentA)
	if keyForA == keyForB {
		t.Fatal("keys must differ when validator names differ even with identical (path, content)")
	}

	respCoraza := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}
	respOtel := &Response{ProtocolVersion: ProtocolVersion, Result: ResultError}
	cache.Put(keyForA, respCoraza)
	cache.Put(keyForB, respOtel)

	gotCoraza, _ := cache.Get(keyForA)
	gotOtel, _ := cache.Get(keyForB)
	if gotCoraza == gotOtel {
		t.Fatal("validators with the same (path, content) key must NOT share cache entries")
	}
}

func TestResultCache_DifferentPaths(t *testing.T) {
	cache := NewResultCache(8)
	content := []byte("identical-content")
	keyA := NewCacheKey("v", "/etc/a.toml", content)
	keyB := NewCacheKey("v", "/etc/b.toml", content)
	if keyA == keyB {
		t.Fatal("keys must differ when paths differ even with identical content")
	}

	respA := &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid}
	respB := &Response{ProtocolVersion: ProtocolVersion, Result: ResultError}
	cache.Put(keyA, respA)
	cache.Put(keyB, respB)

	gotA, _ := cache.Get(keyA)
	gotB, _ := cache.Get(keyB)
	if gotA == gotB {
		t.Fatal("paths with same content must NOT share cache entries — wire response carries the path")
	}
}

func TestResultCache_CapacityEviction(t *testing.T) {
	cache := NewResultCache(2)
	keyA := NewCacheKey("v", "/p", []byte("a"))
	keyB := NewCacheKey("v", "/p", []byte("b"))
	keyC := NewCacheKey("v", "/p", []byte("c"))
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
	keyA := NewCacheKey("v", "/p", []byte("a"))
	keyB := NewCacheKey("v", "/p", []byte("b"))
	keyC := NewCacheKey("v", "/p", []byte("c"))
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
		key := NewCacheKey("v", "/p", []byte{byte(i)})
		cache.Put(key, &Response{ProtocolVersion: ProtocolVersion, Result: ResultValid})
	}
	if cache.Len() != DefaultCacheCapacity {
		t.Fatalf("cache.Len() = %d, want %d", cache.Len(), DefaultCacheCapacity)
	}
}

func TestResultCache_PutOverwrite(t *testing.T) {
	cache := NewResultCache(8)
	key := NewCacheKey("v", "/p", []byte("a"))
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

// The data files are part of the input, so they must be part of the key.
// Keying on the config file alone would serve the previous verdict for a hub
// config whose bytes did not change while the ruleset it Includes did — which
// is exactly the change the data files exist to check, and the one where a
// stale "valid" costs the most.
func TestNewCacheKey_DataFilesChangeTheKey(t *testing.T) {
	const validator, path, content = "coraza", "/etc/haproxy/general/config.toml", "[hub]"

	base := NewCacheKey(validator, path, []byte(content))
	withData := NewCacheKey(validator, path, []byte(content),
		File{Path: "/rules.conf", Content: "SecAction id:1"})
	changedData := NewCacheKey(validator, path, []byte(content),
		File{Path: "/rules.conf", Content: "SecAction id:2"})

	if base == withData {
		t.Fatal("attaching data files must change the key")
	}
	if withData == changedData {
		t.Fatal("changing a data file's content must change the key")
	}
}

// Dispatch order must not affect the key, or an unchanged input would miss the
// cache at random depending on map iteration order upstream.
func TestNewCacheKey_DataFileOrderIsIrrelevant(t *testing.T) {
	a := File{Path: "/a.conf", Content: "A"}
	b := File{Path: "/b.conf", Content: "B"}

	ab := NewCacheKey("v", "/c.toml", []byte("x"), a, b)
	ba := NewCacheKey("v", "/c.toml", []byte("x"), b, a)

	if ab != ba {
		t.Fatal("key must not depend on the order data files were collected in")
	}
}

// Length-prefixing keeps concatenations distinct: without it ("ab","c") and
// ("a","bc") hash identically, so two different rule sets would share a verdict.
func TestNewCacheKey_NoConcatenationCollisions(t *testing.T) {
	one := NewCacheKey("v", "/c.toml", []byte("x"),
		File{Path: "/ab", Content: "c"})
	two := NewCacheKey("v", "/c.toml", []byte("x"),
		File{Path: "/a", Content: "bc"})

	if one == two {
		t.Fatal("paths and contents must not be able to collide across the boundary")
	}
}

// A request with no data files must key exactly as before, so the change does
// not invalidate every cached verdict on upgrade.
func TestNewCacheKey_NoDataFilesMatchesLegacyKey(t *testing.T) {
	withNone := NewCacheKey("v", "/c.toml", []byte("x"))
	if withNone.ContentSHA256 != HashContent([]byte("x")) {
		t.Fatal("a request without data files must keep the plain content hash")
	}
}

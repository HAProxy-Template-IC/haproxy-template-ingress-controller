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

package queryidentity

import (
	"fmt"
	"hash/maphash"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestAuthorityRejectsCopiedSubstitutedAndForeignState(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	key := incremental.NewQueryKey("component.Y29tcG9uZW50.cm91dGVz.ZGVmYXVsdA.cm91dGU")
	fields := Fields{Component: "component", Source: "routes", Namespace: "default", Name: "route"}
	require.True(t, authority.Register(owner, key, fields))
	opened, ok := authority.Lookup(owner, key)
	require.True(t, ok)
	require.Equal(t, fields, opened)
	wrongOwner := new(int)
	_, ok = authority.Lookup(wrongOwner, key)
	require.False(t, ok)
	require.False(t, authority.Register(wrongOwner, key, fields))

	current := currentRoot(authority, key)
	copied := *current
	replaceRoot(authority, key, &copied)
	_, ok = authority.Lookup(owner, key)
	require.False(t, ok)
	replaceRoot(authority, key, current)

	otherKey := incremental.NewQueryKey(key.Opaque() + "A")
	_, ok = authority.Lookup(owner, otherKey)
	require.False(t, ok)

	foreignOwner := new(int)
	foreign := NewAuthority(foreignOwner)
	require.True(t, foreign.Register(foreignOwner, key, fields))
	replaceRoot(authority, key, currentRoot(foreign, key))
	_, ok = authority.Lookup(owner, key)
	require.False(t, ok)
	replaceRoot(authority, key, current)

	originalRoots := authority.roots
	authority.roots = foreign.roots
	_, ok = authority.Lookup(owner, key)
	require.False(t, ok)
	authority.roots = originalRoots

	originalSeed := authority.roots.seed
	authority.roots.seed = maphash.Seed{}
	_, ok = authority.Lookup(owner, key)
	require.False(t, ok)
	require.False(t, authority.Register(owner, key, fields))
	authority.roots.seed = originalSeed

	_, ok = authority.Lookup(owner, incremental.QueryKey{})
	require.False(t, ok)
}

func TestAuthorityKeepsAwayAndBackGenerationsDistinct(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	key := incremental.NewQueryKey("component.identity")
	require.True(t, authority.Register(owner, key, Fields{Component: "A"}))
	a := currentRoot(authority, key)
	require.True(t, authority.Register(owner, key, Fields{Component: "B"}))
	b := currentRoot(authority, key)
	require.True(t, authority.Register(owner, key, Fields{Component: "A"}))
	aAgain := currentRoot(authority, key)

	require.NotSame(t, a, b)
	require.NotSame(t, a, aAgain)
	opened, ok := authority.Lookup(owner, key)
	require.True(t, ok)
	require.Equal(t, "A", opened.Component)
}

func TestAuthorityHashCollisionRetainsExactKeys(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	seen := make(map[*rootShard[*int]]incremental.QueryKey, rootShardCount)
	var first, second incremental.QueryKey
	for index := range rootShardCount + 1 {
		key := incremental.NewQueryKey(fmt.Sprintf("component.collision.%d", index))
		shard := authority.roots.shard(key)
		if previous, exists := seen[shard]; exists {
			first, second = previous, key
			break
		}
		seen[shard] = key
	}
	require.NotEmpty(t, first.Opaque())
	require.NotEqual(t, first, second)
	require.Same(t, authority.roots.shard(first), authority.roots.shard(second))

	firstFields := Fields{Component: "first", Name: first.Opaque()}
	secondFields := Fields{Component: "second", Name: second.Opaque()}
	require.True(t, authority.Register(owner, first, firstFields))
	require.True(t, authority.Register(owner, second, secondFields))
	opened, ok := authority.Lookup(owner, first)
	require.True(t, ok)
	require.Equal(t, firstFields, opened)
	opened, ok = authority.Lookup(owner, second)
	require.True(t, ok)
	require.Equal(t, secondFields, opened)

	firstRoot := currentRoot(authority, first)
	replaceRoot(authority, first, currentRoot(authority, second))
	_, ok = authority.Lookup(owner, first)
	require.False(t, ok)
	replaceRoot(authority, first, firstRoot)
}

func TestCopiedAuthorityCannotRegisterOrLookup(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	key := incremental.NewQueryKey("component.identity")
	require.True(t, authority.Register(owner, key, Fields{Component: "component"}))
	copied := *authority

	_, ok := copied.Lookup(owner, key)
	require.False(t, ok)
	require.False(t, copied.Register(owner, key, Fields{}))
}

func TestAuthorityConcurrentLookupAndReplacement(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	key := incremental.NewQueryKey("component.identity")
	require.True(t, authority.Register(owner, key, Fields{Component: "initial"}))

	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for generation := range 1000 {
				if worker == 0 {
					if !authority.Register(owner, key, Fields{Component: fmt.Sprintf("%d", generation)}) {
						t.Errorf("Register() rejected generation %d", generation)
						return
					}
				}
				_, ok := authority.Lookup(owner, key)
				if !ok {
					t.Error("Lookup() rejected the current generation")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func registerQueryIdentityGenerations(
	t *testing.T,
	authority *Authority[*int],
	owner *int,
	key incremental.QueryKey,
	generations int,
) {
	t.Helper()
	for generation := range generations {
		if !authority.Register(owner, key, Fields{
			Component: fmt.Sprintf("generation-%d", generation),
			Name:      key.Opaque(),
		}) {
			t.Errorf("Register() rejected %s", key.Opaque())
			return
		}
	}
}

func lookupQueryIdentities(
	t *testing.T,
	authority *Authority[*int],
	owner *int,
	keys []incremental.QueryKey,
	reader, readCount int,
) {
	t.Helper()
	for iteration := range readCount {
		key := keys[(reader+iteration)%len(keys)]
		fields, ok := authority.Lookup(owner, key)
		if !ok || fields.Name != key.Opaque() {
			t.Errorf("Lookup() returned foreign or invalid state for %s", key.Opaque())
			return
		}
	}
}

func TestAuthorityConcurrentIndependentKeys(t *testing.T) {
	const (
		keyCount    = 128
		generations = 500
		readerCount = 16
		readCount   = 10_000
	)
	owner := new(int)
	authority := NewAuthority(owner)
	keys := make([]incremental.QueryKey, keyCount)
	for index := range keys {
		key := incremental.NewQueryKey(fmt.Sprintf("component.identity.%d", index))
		keys[index] = key
		require.True(t, authority.Register(owner, key, Fields{Component: "initial", Name: key.Opaque()}))
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, key := range keys {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			registerQueryIdentityGenerations(t, authority, owner, key, generations)
		}()
	}
	for reader := range readerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			lookupQueryIdentities(t, authority, owner, keys, reader, readCount)
		}()
	}
	close(start)
	workers.Wait()

	for _, key := range keys {
		fields, ok := authority.Lookup(owner, key)
		require.True(t, ok)
		require.Equal(t, Fields{Component: "generation-499", Name: key.Opaque()}, fields)
	}
}

func TestLookupDoesNotAllocate(t *testing.T) {
	owner := new(int)
	authority := NewAuthority(owner)
	part := strings.Repeat("x", 8192)
	key := incremental.NewQueryKey("component." + part)
	require.True(t, authority.Register(owner, key, Fields{
		Component: part, Source: part, Namespace: part, Name: part,
	}))

	allocations := testing.AllocsPerRun(1000, func() {
		if _, ok := authority.Lookup(owner, key); !ok {
			panic("query identity did not resolve")
		}
	})
	require.Zero(t, allocations)
}

func BenchmarkLookup(b *testing.B) {
	for _, size := range []int{32, 8192} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			owner := new(int)
			authority := NewAuthority(owner)
			part := strings.Repeat("x", size)
			key := incremental.NewQueryKey("component." + part)
			authority.Register(owner, key, Fields{Component: part, Source: part, Namespace: part, Name: part})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, ok := authority.Lookup(owner, key); !ok {
					b.Fatal("query identity did not resolve")
				}
			}
		})
	}
}

func BenchmarkConcurrentMultiKeyLookup(b *testing.B) {
	owner := new(int)
	authority := NewAuthority(owner)
	keys := make([]incremental.QueryKey, 256)
	for index := range keys {
		key := incremental.NewQueryKey(fmt.Sprintf("component.identity.%d", index))
		keys[index] = key
		authority.Register(owner, key, Fields{Component: key.Opaque()})
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		index := 0
		for pb.Next() {
			if _, ok := authority.Lookup(owner, keys[index&(len(keys)-1)]); !ok {
				b.Fatal("query identity did not resolve")
			}
			index++
		}
	})
}

func BenchmarkConcurrentMultiKeyReplacement(b *testing.B) {
	owner := new(int)
	authority := NewAuthority(owner)
	keys := make([]incremental.QueryKey, 256)
	for index := range keys {
		key := incremental.NewQueryKey(fmt.Sprintf("component.identity.%d", index))
		keys[index] = key
		authority.Register(owner, key, Fields{Component: key.Opaque()})
	}

	var nextWorker atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := keys[(nextWorker.Add(1)-1)%uint64(len(keys))]
		fields := Fields{Component: key.Opaque()}
		for pb.Next() {
			if !authority.Register(owner, key, fields) {
				b.Fatal("query identity was not registered")
			}
		}
	})
}

func currentRoot[O comparable](authority *Authority[O], key incremental.QueryKey) *root[O] {
	shard := authority.roots.shard(key)
	shard.mu.RLock()
	value := shard.current[key]
	shard.mu.RUnlock()
	return value
}

func replaceRoot[O comparable](authority *Authority[O], key incremental.QueryKey, value *root[O]) {
	shard := authority.roots.shard(key)
	shard.mu.Lock()
	if shard.current == nil {
		shard.current = make(map[incremental.QueryKey]*root[O])
	}
	shard.current[key] = value
	shard.mu.Unlock()
}

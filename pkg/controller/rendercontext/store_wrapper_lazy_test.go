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

package rendercontext

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// cachedTestStore records calls to its three relevant methods so the
// lazy-mode invariants can be asserted without relying on the real
// CachedStore. It exposes `ListCached` (the optional `cachedLister`
// interface) so StoreWrapper picks it up for snapshot priming, while
// `List` and `Get` count whether the wrapper crossed into the
// "would have triggered API fetches" branches.
type cachedTestStore struct {
	cached []any
	byKey  map[string][]any

	listCachedCalls atomic.Int32
	listCalls       atomic.Int32
	getCalls        atomic.Int32
	lastGetKeys     [][]string
}

func (s *cachedTestStore) Get(keys ...string) ([]any, error) {
	s.getCalls.Add(1)
	dup := append([]string{}, keys...)
	s.lastGetKeys = append(s.lastGetKeys, dup)
	if s.byKey == nil {
		return nil, nil
	}
	composite := keys[0]
	for _, k := range keys[1:] {
		composite += "/" + k
	}
	return s.byKey[composite], nil
}

func (s *cachedTestStore) List() ([]any, error) {
	s.listCalls.Add(1)
	all := make([]any, 0, len(s.cached))
	for _, k := range orderedKeys(s.byKey) {
		all = append(all, s.byKey[k]...)
	}
	return all, nil
}

func (s *cachedTestStore) ListCached() ([]any, error) {
	s.listCachedCalls.Add(1)
	return append([]any{}, s.cached...), nil
}

func (s *cachedTestStore) Add(_ any, _ []string) error    { return nil }
func (s *cachedTestStore) Update(_ any, _ []string) error { return nil }
func (s *cachedTestStore) Delete(_ ...string) error       { return nil }
func (s *cachedTestStore) Clear() error                   { return nil }

var (
	_ stores.Store = (*cachedTestStore)(nil)
	_ cachedLister = (*cachedTestStore)(nil)
)

func orderedKeys(m map[string][]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Sort isn't strictly needed for correctness here — tests assert
	// counts and presence, not order — but a stable iteration is
	// nicer for debugging when an assertion fails.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

func secret(name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
		},
	}
}

// TestStoreWrapper_LazyMode_PrimedFromCachedList is the load-bearing
// invariant for the `store: on-demand` use case: the wrapper must
// NEVER call Store.List() during normal operation. Constructing the
// wrapper + reading a key that's already in cache must come at zero
// full-list cost.
func TestStoreWrapper_LazyMode_PrimedFromCachedList(t *testing.T) {
	primedSecret := secret("warm")
	store := &cachedTestStore{
		cached: []any{primedSecret},
		byKey: map[string][]any{
			"default/warm":  {primedSecret},
			"default/cold":  {secret("cold")},
			"default/never": {secret("never-touched")},
		},
	}

	w := &StoreWrapper{
		Store:        store,
		ResourceType: "secrets",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		LazySnapshot: true,
	}

	// Reading the warm key never falls through to Store.Get — the
	// snapshot prime already populated it.
	got := w.GetSingle("default", "warm")
	require.NotNil(t, got)
	assert.Equal(t, primedSecret, got)

	assert.Equal(t, int32(1), store.listCachedCalls.Load(), "primed snapshot once")
	assert.Equal(t, int32(0), store.listCalls.Load(), "lazy mode must never call Store.List()")
	assert.Equal(t, int32(0), store.getCalls.Load(), "warm-cached read served from snapshot index, not Store.Get")
}

// TestStoreWrapper_LazyMode_FetchOnMissAndGrow verifies the
// fetch-and-fold behaviour: a key missing from the primed snapshot
// triggers exactly one Store.Get for that key, the result lands in
// the snapshot, and a later List() includes it alongside the
// originally-primed items.
func TestStoreWrapper_LazyMode_FetchOnMissAndGrow(t *testing.T) {
	primedSecret := secret("warm")
	coldSecret := secret("cold")
	store := &cachedTestStore{
		cached: []any{primedSecret},
		byKey: map[string][]any{
			"default/warm": {primedSecret},
			"default/cold": {coldSecret},
		},
	}

	w := &StoreWrapper{
		Store:        store,
		ResourceType: "secrets",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		LazySnapshot: true,
	}

	// Miss path: cold isn't in the primed cache, so one Store.Get fires.
	got := w.GetSingle("default", "cold")
	require.NotNil(t, got)
	assert.Equal(t, coldSecret, got)
	assert.Equal(t, int32(1), store.getCalls.Load(), "miss path fetches once")
	assert.Equal(t, [][]string{{"default", "cold"}}, store.lastGetKeys,
		"Store.Get is called for exactly the requested key, not the full list")

	// Second read of the same cold key must be served from the
	// snapshot — no further Store.Get.
	got = w.GetSingle("default", "cold")
	require.NotNil(t, got)
	assert.Equal(t, int32(1), store.getCalls.Load(), "repeat read is snapshot-served")

	// List() returns BOTH the primed warm item AND the cold item the
	// render fetched after priming. That's the hybrid contract: the
	// snapshot grows during the render and List() reflects what was
	// actually touched.
	all := w.List()
	assert.Len(t, all, 2)
	assert.Contains(t, all, primedSecret)
	assert.Contains(t, all, coldSecret)

	// List() must NOT call Store.List() in lazy mode.
	assert.Equal(t, int32(0), store.listCalls.Load(),
		"lazy mode List() returns the grown snapshot — never falls back to full Store.List()")
}

// TestStoreWrapper_LazyMode_AbsentKeyDoesNotPoisonSnapshot covers
// the GetSingle-miss-on-truly-absent-key case: the wrapper calls
// Store.Get (which returns empty), adds nothing to the flat
// snapshot, and a subsequent List() still returns just the primed
// entries. The negative-cache check is in
// TestStoreWrapper_LazyMode_AbsentKeyNegativeCached.
func TestStoreWrapper_LazyMode_AbsentKeyDoesNotPoisonSnapshot(t *testing.T) {
	primedSecret := secret("warm")
	store := &cachedTestStore{
		cached: []any{primedSecret},
		byKey: map[string][]any{
			"default/warm": {primedSecret},
		},
	}

	w := &StoreWrapper{
		Store:        store,
		ResourceType: "secrets",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		LazySnapshot: true,
	}

	got := w.GetSingle("default", "missing")
	assert.Nil(t, got)

	all := w.List()
	assert.Len(t, all, 1)
	assert.Equal(t, primedSecret, all[0])
}

// TestStoreWrapper_LazyMode_AbsentKeyNegativeCached pins the
// negative-cache invariant: a render that asks N times for the
// same absent key (e.g. a template iterating Ingresses that all
// carry a dangling auth-tls-secret ref) fires Store.Get exactly
// ONCE, not N times. The first miss caches an empty bucket in
// snapshotByKey; subsequent lookups are served from the snapshot
// at no API cost.
//
// Without this, a template scanning ~50 Ingresses with the same
// missing secret would issue 50 API calls per render — exactly
// the "redundant API calls" pattern LazySnapshot was added to
// eliminate.
func TestStoreWrapper_LazyMode_AbsentKeyNegativeCached(t *testing.T) {
	store := &cachedTestStore{
		cached: nil,
		byKey:  map[string][]any{}, // no entries at all
	}

	w := &StoreWrapper{
		Store:        store,
		ResourceType: "secrets",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		LazySnapshot: true,
	}

	// First lookup — miss, fetches once, gets empty.
	got := w.GetSingle("default", "missing")
	assert.Nil(t, got)
	assert.Equal(t, int32(1), store.getCalls.Load(),
		"first lookup of an absent key must fetch exactly once")

	// 9 more lookups of the SAME absent key — all served from
	// the negative cache, zero further Store.Get calls.
	for i := 0; i < 9; i++ {
		got := w.GetSingle("default", "missing")
		assert.Nil(t, got, "repeat absent-key lookup must stay nil")
	}
	assert.Equal(t, int32(1), store.getCalls.Load(),
		"repeat lookups of the same absent key MUST be negative-cached — "+
			"a regression here re-introduces the per-iteration-of-template "+
			"API-fetch storm Gitar review flagged on !1015")
}

// TestStoreWrapper_EagerMode_StillCallsStoreList pins the
// backward-compat invariant: when LazySnapshot is false (the chart
// default for `store: full` resources), behaviour is unchanged —
// loadSnapshot still calls Store.List() once on first access.
func TestStoreWrapper_EagerMode_StillCallsStoreList(t *testing.T) {
	primed := secret("warm")
	store := &cachedTestStore{
		cached: []any{primed}, // ignored in eager mode
		byKey: map[string][]any{
			"default/warm": {primed},
		},
	}

	w := &StoreWrapper{
		Store:        store,
		ResourceType: "secrets",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		// LazySnapshot left zero → eager
	}

	_ = w.GetSingle("default", "warm")

	assert.Equal(t, int32(1), store.listCalls.Load(),
		"eager mode loads the full snapshot via Store.List on first access")
	assert.Equal(t, int32(0), store.listCachedCalls.Load(),
		"eager mode must not consult ListCached")
}

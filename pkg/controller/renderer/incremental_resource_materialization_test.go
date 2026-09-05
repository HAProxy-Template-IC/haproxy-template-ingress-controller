// Copyright 2026 Philipp Hossner
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

package renderer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type countedMaterializationSnapshot struct {
	stores.ReadSnapshot
	getCalls  atomic.Int64
	listCalls atomic.Int64
}

func (s *countedMaterializationSnapshot) Get(keys ...string) ([]any, error) {
	s.getCalls.Add(1)
	return s.ReadSnapshot.Get(keys...)
}

func (s *countedMaterializationSnapshot) List() ([]any, error) {
	s.listCalls.Add(1)
	return s.ReadSnapshot.List()
}

func TestColdResourceMaterializationReusesTypedSnapshotValue(t *testing.T) {
	store := k8sstore.NewMemoryStore(2)
	source := resourceMaterializationResource("old")
	require.NoError(t, store.Add(source, []string{"default", "route"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)
	counted := &countedMaterializationSnapshot{ReadSnapshot: snapshot}
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	renderSession := newResourceMaterializationSession(counted)
	query := incremental.NewQueryKey("resource-list")
	var returned []any
	graph, err := incremental.New(incremental.Definition{
		Key: query,
		Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
			items, _, decodeErr := renderSession.decodeResourceInput(reader, &spec)
			returned = items
			if decodeErr != nil {
				return nil, decodeErr
			}
			return []byte(resourceMaterializationValue(items)), nil
		},
	})
	require.NoError(t, err)
	graphSession, err := graph.BeginColdResetWithConcurrentResolver(renderSession.resolveInput)
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)

	result, err := graphSession.Evaluate(t.Context(), query)
	require.NoError(t, err)
	assert.Equal(t, "old", string(result))
	assert.Equal(t, int64(1), counted.listCalls.Load())
	assert.Zero(t, counted.getCalls.Load())

	key := resourceInputKey(&spec)
	entry := requireResourceMaterialization(t, renderSession.resourceMaterializations, key)
	cached := requireDecodedResourceInput(t, renderSession, key)
	require.NotNil(t, cached.value.materialization)
	assert.Same(t, entry, cached.value.materialization)
	assert.Nil(t, cached.value.certificate)
	raw := entry.raw.value.Load()
	require.NotNil(t, raw)
	certificate := raw.certificate.Load()
	require.NotNil(t, certificate)
	assert.True(t, certificate.Guards(raw.items))
	assert.Equal(t, reflect.ValueOf(raw.items).Pointer(), reflect.ValueOf(returned).Pointer())
	assert.Nil(t, cached.value.items)
	require.NoError(t, entry.authenticate(renderSession.resourceMaterializations, counted, &spec))

	source["spec"].(map[string]any)["value"] = "source-poison"
	detached, err := snapshot.List()
	require.NoError(t, err)
	detached[0].(map[string]any)["spec"].(map[string]any)["value"] = "detached-poison"
	require.NoError(t, store.Update(resourceMaterializationResource("new"), []string{"default", "route"}))
	assert.Equal(t, "old", resourceMaterializationValue(raw.items))

	mutableInput := entry.input()
	mutableInput.Value[0] = 'X'
	assert.NotEqual(t, "X", entry.encoded[:1])
	require.NoError(t, entry.authenticate(renderSession.resourceMaterializations, counted, &spec))
}

func TestResourceMaterializationAuthenticatesNegativeRead(t *testing.T) {
	store := k8sstore.NewMemoryStore(2)
	snapshot, err := store.Pin()
	require.NoError(t, err)
	spec := resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputGet,
		keys:         []string{"default", "missing"},
	}
	arena := newIncrementalResourceMaterializationArena()
	entry, supported, err := arena.ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	assert.False(t, entry.found)
	assert.Empty(t, entry.encoded)
	assert.Zero(t, entry.itemCount)
	assert.Nil(t, entry.raw.value.Load())
	require.NoError(t, entry.authenticate(arena, snapshot, &spec))

	forged := entry.input()
	forged.Value = []byte(`[]`)
	_, matched, err := arena.matching(forged)
	require.NoError(t, err)
	assert.False(t, matched)
}

func TestResourceMaterializationSingleFlightOwnsOneDetachedRead(t *testing.T) {
	_, snapshot := newResourceMaterializationStore(t, "old")
	counted := &countedMaterializationSnapshot{ReadSnapshot: snapshot}
	arena := newIncrementalResourceMaterializationArena()
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	const goroutineCount = 128
	entries := make([]*incrementalResourceMaterialization, goroutineCount)
	errs := make([]error, goroutineCount)
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(goroutineCount)
	for index := range goroutineCount {
		go func() {
			defer workers.Done()
			<-start
			entries[index], _, errs[index] = arena.ensure(t.Context(), counted, &spec)
		}()
	}
	close(start)
	workers.Wait()

	for index := range goroutineCount {
		require.NoError(t, errs[index])
		assert.Same(t, entries[0], entries[index])
	}
	assert.Equal(t, int64(1), counted.listCalls.Load())
	assert.Zero(t, counted.getCalls.Load())
	require.NoError(t, entries[0].authenticate(arena, counted, &spec))
}

func TestResourceMaterializationFailsClosedOnPoison(t *testing.T) {
	tests := map[string]func(*incrementalResourceMaterializationArena, *incrementalResourceMaterialization){
		"arena seal": func(arena *incrementalResourceMaterializationArena, _ *incrementalResourceMaterialization) {
			arena.seal = nil
		},
		"authority": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.authority = &incrementalResourceMaterializationAuthority{}
			entry.authority.seal.Store(entry.authority)
		},
		"entry seal": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.seal = nil
		},
		"proof seal": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.proof.seal = nil
		},
		"key": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.key = incremental.NewInputKey("resource-poison")
		},
		"revision": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.revision = incremental.NewRevision("revision-poison")
		},
		"encoded hash": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.encodedHash++
		},
		"exact bytes": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.encoded += " "
			entry.encodedHash = incrementalDecodedCacheStringHash(entry.encoded)
		},
		"found": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.found = false
		},
		"raw items": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.raw.value.Load().items = slices.Clone(entry.raw.value.Load().items)
		},
		"certificate": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.raw.value.Load().certificate.Store(templating.CertifyIncrementalImmutableInputs([]any{}))
		},
		"raw state": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.raw.seal = nil
		},
		"projected state": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.projected.seal = nil
		},
		"store projection": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.storeValue = nil
		},
		"source": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.source++
		},
		"sequence": func(_ *incrementalResourceMaterializationArena, entry *incrementalResourceMaterialization) {
			entry.sequence++
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			_, snapshot := newResourceMaterializationStore(t, "old")
			arena := newIncrementalResourceMaterializationArena()
			spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
			entry, supported, err := arena.ensure(t.Context(), snapshot, &spec)
			require.NoError(t, err)
			require.True(t, supported)
			_, err = entry.immutableCertificate()
			require.NoError(t, err)
			poison(arena, entry)

			_, _, err = arena.ensure(t.Context(), snapshot, &spec)
			require.Error(t, err)
		})
	}
}

func TestResourceMaterializationRejectsSameRevisionDifferentBytes(t *testing.T) {
	_, snapshot := newResourceMaterializationStore(t, "old")
	arena := newIncrementalResourceMaterializationArena()
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	entry, supported, err := arena.ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)

	poison := entry.input()
	poison.Value = []byte(`[{}]`)
	matched, found, err := arena.matching(poison)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, matched)
}

func TestResourceMaterializationCannotCrossGeneration(t *testing.T) {
	store, firstSnapshot := newResourceMaterializationStore(t, "old")
	firstArena := newIncrementalResourceMaterializationArena()
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	first, supported, err := firstArena.ensure(t.Context(), firstSnapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)

	require.NoError(t, store.Update(resourceMaterializationResource("new"), []string{"default", "route"}))
	secondSnapshot, err := store.Pin()
	require.NoError(t, err)
	secondArena := newIncrementalResourceMaterializationArena()
	second, supported, err := secondArena.ensure(t.Context(), secondSnapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	assert.NotSame(t, first, second)
	firstItems, err := first.rawItems()
	require.NoError(t, err)
	secondItems, err := second.rawItems()
	require.NoError(t, err)
	assert.Equal(t, "old", resourceMaterializationValue(firstItems))
	assert.Equal(t, "new", resourceMaterializationValue(secondItems))
	require.Error(t, first.authenticateIdentity(secondArena))
	require.Error(t, first.authenticate(firstArena, secondSnapshot, &spec))

	firstArena.revoke()
	require.Error(t, first.authenticateDetached())
	_, _, err = firstArena.ensure(t.Context(), firstSnapshot, &spec)
	require.Error(t, err)
}

func TestNormalizeOwnedResourceMaterializationMatchesCodec(t *testing.T) {
	values := []any{
		nil, false, "value",
		int(-1), int8(-2), int16(-3), int32(-4), int64(-5),
		uint(1), uint8(2), uint16(3), uint32(4), uint64(math.MaxInt64), uint64(math.MaxUint64),
		float32(0.1), float32(1), math.Copysign(0, -1), float64(1), float64(1.5), 1e-7, 1e20, 1e21,
		map[string]any{"nested": []any{float32(2), uint64(math.MaxUint64)}},
	}
	for index, value := range values {
		encoded, err := encodeResourceValue(value)
		require.NoError(t, err, "case %d", index)
		want, err := decodeResourceValue(encoded)
		require.NoError(t, err, "case %d", index)
		owned := value
		if index == len(values)-1 {
			owned = map[string]any{"nested": []any{float32(2), uint64(math.MaxUint64)}}
		}
		visits := incrementalResourceMaterializationVisitSet{}
		got, err := normalizeOwnedResourceMaterialization(owned, &visits, 0)
		require.NoError(t, err, "case %d", index)
		assert.True(t, reflect.DeepEqual(want, got), "case %d: want %#v (%T), got %#v (%T)", index, want, want, got, got)
	}
}

func TestResourceMaterializationBreaksSharedMutableAliases(t *testing.T) {
	snapshot := &sharedAliasResourceMaterializationSnapshot{}
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	entry, supported, err := newIncrementalResourceMaterializationArena().ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	items, err := entry.rawItems()
	require.NoError(t, err)
	item := items[0].(map[string]any)
	left := item["left"].(map[string]any)
	right := item["right"].(map[string]any)
	require.NotEqual(t, reflect.ValueOf(left).Pointer(), reflect.ValueOf(right).Pointer())
	left["value"] = int64(2)
	assert.Equal(t, int64(1), right["value"])
}

func TestResourceMaterializationOwnsForeignSnapshotValue(t *testing.T) {
	source := []any{resourceMaterializationResource("old")}
	snapshot := &retainedResourceMaterializationSnapshot{items: source}
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	entry, supported, err := newIncrementalResourceMaterializationArena().ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	items, err := entry.rawItems()
	require.NoError(t, err)
	require.NotEqual(t, reflect.ValueOf(source[0]).Pointer(), reflect.ValueOf(items[0]).Pointer())

	source[0].(map[string]any)["spec"].(map[string]any)["value"] = "poison"
	assert.Equal(t, "old", resourceMaterializationValue(items))
}

func TestMaterializedResourceInputDefersImmutableCertificate(t *testing.T) {
	_, snapshot := newResourceMaterializationStore(t, "old")
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	renderSession := newResourceMaterializationSession(snapshot)
	query := incremental.NewQueryKey("materialized-resource")
	graph, err := incremental.New(incremental.Definition{
		Key: query,
		Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
			items, materialization, decodeErr := renderSession.decodeMaterializedResourceInput(reader, &spec)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if materialization == nil {
				return nil, errors.New("resource materialization is unavailable")
			}
			assert.Nil(t, items)
			return []byte("materialized"), nil
		},
	})
	require.NoError(t, err)
	graphSession, err := graph.BeginColdResetWithConcurrentResolver(renderSession.resolveInput)
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)
	result, err := graphSession.Evaluate(t.Context(), query)
	require.NoError(t, err)
	assert.Equal(t, "materialized", string(result))

	entry := requireResourceMaterialization(t, renderSession.resourceMaterializations, resourceInputKey(&spec))
	assert.Nil(t, entry.raw.value.Load())
	const workerCount = 64
	certificates := make(chan *templating.IncrementalImmutableCertificate, workerCount)
	errs := make(chan error, workerCount)
	var group sync.WaitGroup
	for range workerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			certificate, certificateErr := entry.immutableCertificate()
			certificates <- certificate
			errs <- certificateErr
		}()
	}
	group.Wait()
	close(certificates)
	close(errs)
	for certificateErr := range errs {
		require.NoError(t, certificateErr)
	}
	raw := entry.raw.value.Load()
	require.NotNil(t, raw)
	want := raw.certificate.Load()
	require.NotNil(t, want)
	for certificate := range certificates {
		assert.Same(t, want, certificate)
	}
}

func TestResourceMaterializationUsesAuthenticatedStoreProjection(t *testing.T) {
	store, snapshot := newResourceMaterializationStore(t, "old")
	projection, supported, err := k8sstore.ProjectImmutableSnapshotList(t.Context(), snapshot)
	require.NoError(t, err)
	require.True(t, supported)
	before, err := projection.Encode()
	require.NoError(t, err)

	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	entry, supported, err := newIncrementalResourceMaterializationArena().ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, before, []byte(entry.encoded))
	assert.Nil(t, entry.raw.value.Load())

	public, err := store.List()
	require.NoError(t, err)
	public[0].(map[string]any)["spec"].(map[string]any)["value"] = "poison"
	items, err := entry.rawItems()
	require.NoError(t, err)
	assert.Equal(t, "old", resourceMaterializationValue(items))
	after, err := projection.Encode()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestResourceMaterializationCanonicalizesAuthenticatedStoreValueWithoutMutation(t *testing.T) {
	store := k8sstore.NewMemoryStore(2)
	resource := resourceMaterializationResource("old")
	resource["spec"].(map[string]any)["number"] = int(7)
	require.NoError(t, store.Add(resource, []string{"default", "route"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	entry, supported, err := newIncrementalResourceMaterializationArena().ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	assert.Nil(t, entry.raw.value.Load())
	items, err := entry.rawItems()
	require.NoError(t, err)
	assert.Equal(t, int64(7), items[0].(map[string]any)["spec"].(map[string]any)["number"])

	owned, err := snapshot.List()
	require.NoError(t, err)
	assert.Equal(t, int(7), owned[0].(map[string]any)["spec"].(map[string]any)["number"])
}

func BenchmarkResourceMaterializationArenaHit(b *testing.B) {
	store := k8sstore.NewMemoryStore(2)
	if err := store.Add(resourceMaterializationResource("value"), []string{"default", "route"}); err != nil {
		b.Fatal(err)
	}
	snapshot, err := store.Pin()
	if err != nil {
		b.Fatal(err)
	}
	arena := newIncrementalResourceMaterializationArena()
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	if _, _, err := arena.ensure(context.Background(), snapshot, &spec); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := arena.ensure(context.Background(), snapshot, &spec); err != nil {
			b.Fatal(err)
		}
	}
}

func newResourceMaterializationStore(
	t *testing.T,
	value string,
) (*k8sstore.MemoryStore, stores.ReadSnapshot) {
	t.Helper()
	store := k8sstore.NewMemoryStore(2)
	require.NoError(t, store.Add(resourceMaterializationResource(value), []string{"default", "route"}))
	snapshot, err := store.Pin()
	require.NoError(t, err)
	return store, snapshot
}

func resourceMaterializationResource(value string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "route"},
		"spec":     map[string]any{"value": value},
	}
}

func resourceMaterializationValue(items []any) string {
	return items[0].(map[string]any)["spec"].(map[string]any)["value"].(string)
}

func newResourceMaterializationSession(snapshot stores.ReadSnapshot) *incrementalRenderSession {
	session := &incrementalRenderSession{
		state:                    &incrementalRenderState{},
		renderSnapshots:          map[string]stores.ReadSnapshot{"routes": snapshot},
		cursors:                  map[string]incrementalStoreCursor{},
		httpObserved:             map[incremental.InputKey]incremental.Input{},
		resourceProofs:           map[incremental.InputKey]incremental.Input{},
		cachePublicationEnabled:  true,
		resourceMaterializations: newIncrementalResourceMaterializationArena(),
	}
	session.resetCatalog(nil)
	return session
}

func requireResourceMaterialization(
	t *testing.T,
	arena *incrementalResourceMaterializationArena,
	key incremental.InputKey,
) *incrementalResourceMaterialization {
	t.Helper()
	entry, found, err := arena.entries.load(key, incrementalDecodedCacheStringHash(key.Opaque()))
	require.NoError(t, err)
	require.True(t, found)
	return entry
}

type sharedAliasResourceMaterializationSnapshot struct{}

type retainedResourceMaterializationSnapshot struct {
	items []any
}

func (*sharedAliasResourceMaterializationSnapshot) RevisionSource() stores.RevisionSource {
	return 17
}

func (*sharedAliasResourceMaterializationSnapshot) Sequence() uint64 {
	return 4
}

func (*sharedAliasResourceMaterializationSnapshot) ListRevision() stores.Revision {
	return "list-revision"
}

func (*sharedAliasResourceMaterializationSnapshot) GetRevision(...string) stores.Revision {
	return "get-revision"
}

func (*sharedAliasResourceMaterializationSnapshot) IdentityRevision(string, string) stores.Revision {
	return "identity-revision"
}

func (*sharedAliasResourceMaterializationSnapshot) Get(...string) ([]any, error) {
	return nil, nil
}

func (*sharedAliasResourceMaterializationSnapshot) List() ([]any, error) {
	shared := map[string]any{"value": int64(1)}
	return []any{map[string]any{"left": shared, "right": shared}}, nil
}

func (*sharedAliasResourceMaterializationSnapshot) GetIdentity(string, string) (value any, found bool, err error) {
	return nil, false, nil
}

func (*retainedResourceMaterializationSnapshot) RevisionSource() stores.RevisionSource {
	return 18
}

func (*retainedResourceMaterializationSnapshot) Sequence() uint64 {
	return 5
}

func (*retainedResourceMaterializationSnapshot) ListRevision() stores.Revision {
	return "list-revision"
}

func (*retainedResourceMaterializationSnapshot) GetRevision(...string) stores.Revision {
	return "get-revision"
}

func (*retainedResourceMaterializationSnapshot) IdentityRevision(string, string) stores.Revision {
	return "identity-revision"
}

func (*retainedResourceMaterializationSnapshot) Get(...string) ([]any, error) {
	return nil, nil
}

func (s *retainedResourceMaterializationSnapshot) List() ([]any, error) {
	return s.items, nil
}

func (*retainedResourceMaterializationSnapshot) GetIdentity(string, string) (value any, found bool, err error) {
	return nil, false, nil
}

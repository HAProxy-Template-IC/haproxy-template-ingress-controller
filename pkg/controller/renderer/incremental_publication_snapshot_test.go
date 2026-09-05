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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalPublicationSnapshotTestReader struct {
	mu    sync.RWMutex
	input incremental.Input
	calls atomic.Int64
}

func (r *incrementalPublicationSnapshotTestReader) Input(key incremental.InputKey) (value []byte, found bool, err error) {
	input, err := r.ExactInput(key)
	return input.Value, input.Found, err
}

func (r *incrementalPublicationSnapshotTestReader) ExactInput(incremental.InputKey) (incremental.Input, error) {
	r.calls.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	input := r.input
	input.Value = bytes.Clone(input.Value)
	return input, nil
}

func (*incrementalPublicationSnapshotTestReader) Query(
	context.Context,
	incremental.QueryKey,
) ([]byte, error) {
	return nil, errors.New("publication snapshot test reader has no queries")
}

func (r *incrementalPublicationSnapshotTestReader) replace(input incremental.Input) {
	r.mu.Lock()
	r.input = input
	r.mu.Unlock()
}

type incrementalPublicationSnapshotTestValue struct {
	cell  string
	key   string
	rank  string
	value any
}

type incrementalPublicationSnapshotTestFixture struct {
	group      string
	owner      incrementalGroupInstanceID
	generation *incrementalPublicationSnapshotGeneration
	authority  *incrementalPublicationSnapshotAuthority
	result     incrementalComponentResult
	index      *incrementalGroupIndex
	session    *incrementalRenderSession
}

func newIncrementalPublicationSnapshotTestFixture(
	tb testing.TB,
	values ...incrementalPublicationSnapshotTestValue,
) *incrementalPublicationSnapshotTestFixture {
	tb.Helper()
	generation, authority := newIncrementalPublicationSnapshotGeneration()
	owner := incrementalGroupInstanceID{
		component: "publisher", source: "routes", namespace: "default", name: "route",
	}
	recorder := &incrementalRecorder{
		publicationGeneration: generation,
		publicationGroup:      "routing",
		publicationOwner:      owner,
	}
	for index := range values {
		value := &values[index]
		if value.rank == "" {
			recorder.Publish(value.cell, value.key, value.value)
			continue
		}
		recorder.PublishRanked(value.cell, value.key, value.rank, value.value)
	}
	result, err := recorder.result("")
	require.NoError(tb, err)
	index, err := newIncrementalGroupIndex().replace(&incrementalInstanceResult{
		component: owner.component,
		source:    owner.source,
		namespace: owner.namespace,
		name:      owner.name,
		result:    result,
	}, nil)
	require.NoError(tb, err)
	return &incrementalPublicationSnapshotTestFixture{
		group:      "routing",
		owner:      owner,
		generation: generation,
		authority:  authority,
		result:     result,
		index:      index,
		session: &incrementalRenderSession{
			publicationGeneration: generation,
			publicationAuthority:  authority,
			groupIndexes: map[string]*incrementalGroupIndex{
				"routing": index,
			},
		},
	}
}

func (f *incrementalPublicationSnapshotTestFixture) selectorReader(
	tb testing.TB,
	key string,
) (*incrementalPublicationSnapshotTestReader, incremental.InputKey) {
	tb.Helper()
	const cell = "backends"
	input, err := incrementalSelectorInput(f.index, f.group, cell, key)
	require.NoError(tb, err)
	return &incrementalPublicationSnapshotTestReader{input: input}, input.Key
}

func cloneIncrementalPublicationSnapshotTestValue(tb testing.TB, value any) any {
	tb.Helper()
	detached, err := templating.NewIncrementalDetachedValue(value)
	require.NoError(tb, err)
	cloned, err := templating.ConsumeIncrementalDetachedValue(detached)
	require.NoError(tb, err)
	return cloned
}

func legacyIncrementalPublicationNormalization(tb testing.TB, value any) (normalized any, canonical []byte) {
	tb.Helper()
	encoded, err := encodeResourceValue(value)
	require.NoError(tb, err)
	normalized, err = decodeResourceValue(encoded)
	require.NoError(tb, err)
	canonical, err = encodeResourceValue(normalized)
	require.NoError(tb, err)
	return normalized, canonical
}

func TestIncrementalPublicationSnapshotNormalizationParity(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	values := map[string]any{
		"nil":                nil,
		"bool":               true,
		"string":             "route-ü",
		"int":                int(-17),
		"int8":               int8(math.MinInt8),
		"int16":              int16(math.MaxInt16),
		"int32":              int32(math.MinInt32),
		"int64 min":          int64(math.MinInt64),
		"int64 max":          int64(math.MaxInt64),
		"uint":               uint(17),
		"uint8":              uint8(math.MaxUint8),
		"uint16":             uint16(math.MaxUint16),
		"uint32":             uint32(math.MaxUint32),
		"uint max int64":     uint64(math.MaxInt64),
		"uint above int64":   uint64(math.MaxInt64) + 1,
		"uint64 max":         uint64(math.MaxUint64),
		"float32 integer":    float32(17),
		"float32 fraction":   float32(1.25),
		"float32 exponent":   float32(1e-7),
		"float64 zero":       float64(0),
		"float64 negative 0": negativeZero,
		"float64 integer":    float64(17),
		"float64 fraction":   1.25,
		"float64 1e-7":       1e-7,
		"float64 1e-6":       1e-6,
		"float64 1e20":       1e20,
		"float64 1e21":       1e21,
		"float64 smallest":   math.SmallestNonzeroFloat64,
		"float64 max":        math.MaxFloat64,
		"nil map":            map[string]any(nil),
		"nil list":           []any(nil),
		"nested": map[string]any{
			"list": []any{int8(-2), uint64(math.MaxInt64) + 1, float32(1e-7), nil},
			"map":  map[string]any{"number": 2.5, "string": "value"},
		},
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			current := cloneIncrementalPublicationSnapshotTestValue(t, value)
			legacy := cloneIncrementalPublicationSnapshotTestValue(t, value)
			got, gotEncoded, err := normalizeIncrementalPublicationValue(current)
			require.NoError(t, err)
			want, wantEncoded := legacyIncrementalPublicationNormalization(t, legacy)
			assert.Equal(t, want, got)
			assert.Equal(t, string(wantEncoded), string(gotEncoded))
		})
	}
}

func TestIncrementalPublicationSnapshotRandomizedNormalizationParity(t *testing.T) {
	random := rand.New(rand.NewSource(187))
	for iteration := range 1_000 {
		value := randomIncrementalPublicationSnapshotValue(random, 0)
		current := cloneIncrementalPublicationSnapshotTestValue(t, value)
		legacy := cloneIncrementalPublicationSnapshotTestValue(t, value)
		got, gotEncoded, err := normalizeIncrementalPublicationValue(current)
		require.NoError(t, err, "iteration %d", iteration)
		want, wantEncoded := legacyIncrementalPublicationNormalization(t, legacy)
		assert.Equal(t, want, got, "iteration %d", iteration)
		assert.Equal(t, string(wantEncoded), string(gotEncoded), "iteration %d", iteration)
	}
}

func randomIncrementalPublicationSnapshotValue(random *rand.Rand, depth int) any {
	choiceCount := 11
	if depth >= 3 {
		choiceCount = 9
	}
	switch random.Intn(choiceCount) {
	case 0:
		return nil
	case 1:
		return random.Intn(2) == 0
	case 2:
		return fmt.Sprintf("value-%016x", random.Uint64())
	case 3:
		return int(random.Int63())
	case 4:
		return int64(random.Uint64())
	case 5:
		return random.Uint64()
	case 6:
		value := math.Float32frombits(random.Uint32())
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return float32(0)
		}
		return value
	case 7:
		value := math.Float64frombits(random.Uint64())
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return float64(0)
		}
		return value
	case 8:
		return []any(nil)
	case 9:
		length := random.Intn(5)
		values := make([]any, length)
		for index := range values {
			values[index] = randomIncrementalPublicationSnapshotValue(random, depth+1)
		}
		return values
	default:
		length := random.Intn(5)
		values := make(map[string]any, length)
		for index := range length {
			values[fmt.Sprintf("key-%d", index)] = randomIncrementalPublicationSnapshotValue(random, depth+1)
		}
		return values
	}
}

func TestIncrementalPublicationSnapshotNormalizationErrorParity(t *testing.T) {
	cycleMap := map[string]any{}
	cycleMap["self"] = cycleMap
	cycleList := make([]any, 1)
	cycleList[0] = cycleList
	values := map[string]any{
		"map cycle":       cycleMap,
		"list cycle":      cycleList,
		"invalid string":  string([]byte{0xff}),
		"invalid map key": map[string]any{string([]byte{0xff}): "value"},
		"nan":             math.NaN(),
		"positive inf":    math.Inf(1),
		"negative inf":    math.Inf(-1),
		"typed map":       map[string]string{"key": "value"},
		"typed list":      []string{"value"},
		"struct":          struct{ Value string }{Value: "value"},
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			_, _, gotErr := normalizeIncrementalPublicationValue(value)
			_, wantErr := encodeResourceValue(value)
			require.Error(t, gotErr)
			require.Error(t, wantErr)
			assert.Equal(t, wantErr.Error(), gotErr.Error())
		})
	}
}

func TestIncrementalPublicationSnapshotOwnsPublishedValue(t *testing.T) {
	nested := map[string]any{"number": int(7)}
	list := []any{nested, "tail"}
	value := map[string]any{"list": list}
	fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
		cell: "backends", key: "route", value: value,
	})

	nested["number"] = int(99)
	list[0] = "replaced"
	value["new"] = "poison"

	reader, key := fixture.selectorReader(t, "route")
	got, certificate, found, err := fixture.session.publicationInput(reader, key)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, certificate)
	assert.Equal(t, map[string]any{
		"list": []any{map[string]any{"number": int64(7)}, "tail"},
	}, got)
	assert.True(t, certificate.Guards(got))
	assert.Equal(t, `{"list":[{"number":7},"tail"]}`, string(fixture.result.Published[0].Value))
}

func TestIncrementalPublicationSnapshotObservesExactInputEveryRead(t *testing.T) {
	fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
		cell: "backends", key: "route", value: map[string]any{"port": 8080},
	})
	reader, key := fixture.selectorReader(t, "route")
	for range 3 {
		value, certificate, found, err := fixture.session.publicationInput(reader, key)
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, certificate)
		assert.Equal(t, map[string]any{"port": int64(8080)}, value)
	}
	assert.Equal(t, int64(3), reader.calls.Load())
}

func TestIncrementalPublicationSnapshotRejectsPoisonedObservation(t *testing.T) {
	fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
		cell: "backends", key: "route", value: map[string]any{"port": 8080},
	})
	reader, key := fixture.selectorReader(t, "route")
	original := reader.input
	tests := map[string]func(*incremental.Input){
		"key": func(input *incremental.Input) {
			input.Key = incremental.NewInputKey("poison")
		},
		"revision": func(input *incremental.Input) {
			input.Revision = incremental.NewRevision("poison")
		},
		"found": func(input *incremental.Input) {
			input.Found = false
		},
		"value": func(input *incremental.Input) {
			input.Value = []byte(`{"port":8081}`)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			input := original
			input.Value = bytes.Clone(original.Value)
			poison(&input)
			reader.replace(input)
			_, _, _, err := fixture.session.publicationInput(reader, key)
			require.ErrorIs(t, err, incremental.ErrRevisionConflict)
		})
	}
}

func TestIncrementalPublicationSnapshotRejectsPoisonReplayAndRevocation(t *testing.T) {
	tests := map[string]func(*incrementalPublicationSnapshotTestFixture){
		"copied snapshot": func(fixture *incrementalPublicationSnapshotTestFixture) {
			copied := *fixture.result.Published[0].snapshot
			fixture.result.Published[0].snapshot = &copied
		},
		"binding": func(fixture *incrementalPublicationSnapshotTestFixture) {
			fixture.result.Published[0].snapshot.binding.revision = incremental.NewRevision("poison")
		},
		"value": func(fixture *incrementalPublicationSnapshotTestFixture) {
			fixture.result.Published[0].snapshot.value = map[string]any{"port": int64(8081)}
		},
		"proof": func(fixture *incrementalPublicationSnapshotTestFixture) {
			fixture.result.Published[0].snapshot.proof.binding.revision = incremental.NewRevision("poison")
		},
		"generation replay": func(fixture *incrementalPublicationSnapshotTestFixture) {
			generation, _ := newIncrementalPublicationSnapshotGeneration()
			fixture.result.publicationGeneration = generation
		},
		"copied generation": func(fixture *incrementalPublicationSnapshotTestFixture) {
			copied := *fixture.result.publicationGeneration
			fixture.result.publicationGeneration = &copied
		},
		"copied shards": func(fixture *incrementalPublicationSnapshotTestFixture) {
			copied := *fixture.generation.shards
			fixture.generation.shards = &copied
		},
		"cleared source shard": func(fixture *incrementalPublicationSnapshotTestFixture) {
			location := string(incrementalGroupLocationKey(fixture.owner, 0))
			shard := &fixture.generation.shards.sources[incrementalPublicationSourceShardIndex(location)]
			shard.mu.Lock()
			shard.values = nil
			shard.mu.Unlock()
		},
		"revoked": func(fixture *incrementalPublicationSnapshotTestFixture) {
			fixture.generation.revoke()
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
				cell: "backends", key: "route", value: map[string]any{"port": 8080},
			})
			poison(fixture)
			err := validateIncrementalInstanceResult(&fixture.result)
			require.Error(t, err)
		})
	}

	fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
		cell: "backends", key: "route", value: map[string]any{"port": 8080},
	})
	reader, key := fixture.selectorReader(t, "route")
	_, foreignAuthority := newIncrementalPublicationSnapshotGeneration()
	fixture.session.publicationAuthority = foreignAuthority
	value, certificate, found, err := fixture.session.publicationInput(reader, key)
	require.ErrorContains(t, err, "invalid ownership")
	require.Nil(t, value)
	require.Nil(t, certificate)
	require.False(t, found)
}

func TestIncrementalPublicationSnapshotSeparatesIdenticalCanonicalBytes(t *testing.T) {
	fixture := newIncrementalPublicationSnapshotTestFixture(t,
		incrementalPublicationSnapshotTestValue{
			cell: "backends", key: "route-a", value: map[string]any{"port": 8080},
		},
		incrementalPublicationSnapshotTestValue{
			cell: "backends", key: "route-b", value: map[string]any{"port": 8080},
		},
	)
	require.NotSame(t, fixture.result.Published[0].snapshot, fixture.result.Published[1].snapshot)
	require.NotEqual(t,
		fixture.result.Published[0].snapshot.binding.key,
		fixture.result.Published[1].snapshot.binding.key,
	)
	for _, key := range []string{"route-a", "route-b"} {
		reader, inputKey := fixture.selectorReader(t, key)
		value, _, found, err := fixture.session.publicationInput(reader, inputKey)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, map[string]any{"port": int64(8080)}, value)
	}

	poisoned := cloneIncrementalComponentResult(&fixture.result)
	poisoned.Published[0].snapshot = poisoned.Published[1].snapshot
	require.Error(t, validateIncrementalInstanceResult(&poisoned))
}

func TestIncrementalPublicationSnapshotPersistedFallbackParity(t *testing.T) {
	value := map[string]any{
		"port": uint64(math.MaxInt64) + 1,
		"list": []any{float32(1e-7), int8(-2)},
	}
	encoded, err := encodeResourceValue(value)
	require.NoError(t, err)
	decoded, err := decodeResourceValue(encoded)
	require.NoError(t, err)
	canonical, err := encodeResourceValue(decoded)
	require.NoError(t, err)
	result := incrementalComponentResult{Published: []incrementalPublishedValue{{
		Cell: "backends", Key: "route", Value: canonical,
	}}}
	result.PublishedDigest, err = digestIncrementalPublishedValues(result.Published)
	require.NoError(t, err)
	index, err := newIncrementalGroupIndex().replace(&incrementalInstanceResult{
		component: "publisher", source: "routes", namespace: "default", name: "route", result: result,
	}, nil)
	require.NoError(t, err)
	input, err := incrementalSelectorInput(index, "routing", "backends", "route")
	require.NoError(t, err)
	reader := &incrementalPublicationSnapshotTestReader{input: input}
	session := &incrementalRenderSession{groupIndexes: map[string]*incrementalGroupIndex{"routing": index}}

	got, certificate, found, err := session.publicationInput(reader, input.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, certificate)
	assert.Equal(t, decoded, got)
	assert.True(t, certificate.Guards(got))
	assert.Equal(t, int64(1), reader.calls.Load())
}

func TestIncrementalPublicationSnapshotGenerationHasNoSessionOwnership(t *testing.T) {
	fixture := newIncrementalPublicationSnapshotTestFixture(t, incrementalPublicationSnapshotTestValue{
		cell: "backends", key: "route", value: map[string]any{"port": 8080},
	})
	forbidden := map[reflect.Type]struct{}{
		reflect.TypeOf((*incrementalRenderSession)(nil)):           {},
		reflect.TypeOf((*coldIncrementalRenderer)(nil)):            {},
		reflect.TypeOf((*preparedIncrementalVectorRender)(nil)):    {},
		reflect.TypeOf((*incrementalBatchCapabilities)(nil)):       {},
		reflect.TypeOf((*incrementalPublicationSelector)(nil)):     {},
		reflect.TypeOf((*coldIncrementalPublicationSelector)(nil)): {},
	}
	assertIncrementalPublicationTypeHasNoOwner(t, reflect.TypeOf(fixture.generation), forbidden, map[reflect.Type]struct{}{})
	assertIncrementalPublicationTypeHasNoOwner(t, reflect.TypeOf(fixture.authority), forbidden, map[reflect.Type]struct{}{})

	encoded, err := json.Marshal(&fixture.result)
	require.NoError(t, err)
	committed, err := decodeIncrementalComponentResult(encoded)
	require.NoError(t, err)
	assert.Nil(t, committed.publicationGeneration)
	assert.Empty(t, committed.publicationGroup)
	assert.Equal(t, incrementalGroupInstanceID{}, committed.publicationOwner)
	require.Len(t, committed.Published, 1)
	assert.Nil(t, committed.Published[0].snapshot)

	indexed, found := fixture.index.instances.Root().Get(incrementalGroupInstanceKey(fixture.owner))
	require.True(t, found)
	committed, err = decodeIndexedGroupInstanceResult(&indexed)
	require.NoError(t, err)
	assert.Nil(t, committed.publicationGeneration)
	assert.Nil(t, committed.Published[0].snapshot)

	shards := fixture.generation.shards
	fixture.session.releasePublicationFrames()
	fixture.session.releasePublicationFrames()
	fixture.generation.mu.RLock()
	assert.False(t, fixture.generation.active)
	assert.Nil(t, fixture.generation.shards)
	fixture.generation.mu.RUnlock()
	require.NotNil(t, shards)
	for index := range incrementalPublicationSnapshotShardCount {
		assert.Nil(t, shards.sources[index].values)
		assert.Nil(t, shards.derived[index].values)
	}
}

func assertIncrementalPublicationTypeHasNoOwner(
	tb testing.TB,
	typeOf reflect.Type,
	forbidden map[reflect.Type]struct{},
	seen map[reflect.Type]struct{},
) {
	tb.Helper()
	if typeOf == nil {
		return
	}
	if _, disallowed := forbidden[typeOf]; disallowed {
		tb.Fatalf("publication generation reaches session-owned type %s", typeOf)
	}
	if _, visited := seen[typeOf]; visited {
		return
	}
	seen[typeOf] = struct{}{}
	switch typeOf.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		assertIncrementalPublicationTypeHasNoOwner(tb, typeOf.Elem(), forbidden, seen)
	case reflect.Map:
		assertIncrementalPublicationTypeHasNoOwner(tb, typeOf.Key(), forbidden, seen)
		assertIncrementalPublicationTypeHasNoOwner(tb, typeOf.Elem(), forbidden, seen)
	case reflect.Struct:
		if typeOf.PkgPath() != reflect.TypeOf(incrementalPublicationSnapshotGeneration{}).PkgPath() {
			return
		}
		for index := range typeOf.NumField() {
			field := typeOf.Field(index)
			require.NotEqual(tb, "owner", field.Name)
			assertIncrementalPublicationTypeHasNoOwner(tb, field.Type, forbidden, seen)
		}
	}
}

func TestIncrementalPublicationSnapshotConcurrentSelect(t *testing.T) {
	fixture := newIncrementalPublicationSnapshotTestFixture(t,
		incrementalPublicationSnapshotTestValue{
			cell: "backends", key: "route-a", rank: "010", value: map[string]any{"port": 8080},
		},
		incrementalPublicationSnapshotTestValue{
			cell: "backends", key: "route-b", rank: "020", value: map[string]any{"port": 8081},
		},
	)
	singleInput, err := incrementalSelectorInput(fixture.index, fixture.group, "backends", "route-a")
	require.NoError(t, err)
	valuesInput, err := incrementalSelectorValuesInput(fixture.index, fixture.group, "backends")
	require.NoError(t, err)
	singleReader := &incrementalPublicationSnapshotTestReader{input: singleInput}
	valuesReader := &incrementalPublicationSnapshotTestReader{input: valuesInput}

	const workers = 32
	const repetitions = 20
	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		go func(worker int) {
			defer group.Done()
			for range repetitions {
				if worker%2 == 0 {
					value, certificate, found, selectErr := fixture.session.publicationInput(
						singleReader, singleInput.Key,
					)
					assert.NoError(t, selectErr)
					assert.True(t, found)
					assert.True(t, certificate.Guards(value))
					continue
				}
				value, certificate, found, selectErr := fixture.session.publicationInput(
					valuesReader, valuesInput.Key,
				)
				assert.NoError(t, selectErr)
				assert.True(t, found)
				assert.True(t, certificate.Guards(value))
				assert.Len(t, value, 2)
			}
		}(worker)
	}
	group.Wait()
	assert.Equal(t, int64(workers/2*repetitions), singleReader.calls.Load())
	assert.Equal(t, int64(workers/2*repetitions), valuesReader.calls.Load())
}

func TestIncrementalPublicationSnapshotConcurrentCapture(t *testing.T) {
	generation, _ := newIncrementalPublicationSnapshotGeneration()
	const workers = 32
	const repetitions = 32
	errorsByCall := make(chan error, workers*repetitions)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			owner := incrementalGroupInstanceID{
				component: "publisher", source: "routes", name: fmt.Sprintf("route-%d", worker),
			}
			for index := range repetitions {
				detached, err := templating.NewIncrementalDetachedValue(map[string]any{
					"worker": worker,
					"index":  index,
				})
				if err == nil {
					_, _, err = generation.capture(
						"routing", owner, index, "backends", fmt.Sprintf("key-%d", index), "", detached,
					)
				}
				if err != nil {
					errorsByCall <- err
				}
			}
		}()
	}
	group.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		require.NoError(t, err)
	}

	captured := 0
	generation.mu.RLock()
	require.True(t, generation.validLocked())
	for index := range incrementalPublicationSnapshotShardCount {
		shard := &generation.shards.sources[index]
		shard.mu.RLock()
		captured += len(shard.values)
		shard.mu.RUnlock()
	}
	generation.mu.RUnlock()
	assert.Equal(t, workers*repetitions, captured)
}

func TestIncrementalPublicationSnapshotConcurrentCaptureAndRevoke(t *testing.T) {
	const rounds = 16
	const workers = 32
	for round := range rounds {
		generation, _ := newIncrementalPublicationSnapshotGeneration()
		start := make(chan struct{})
		errorsByCall := make(chan error, workers)
		var group sync.WaitGroup
		group.Add(workers)
		for worker := range workers {
			go func() {
				defer group.Done()
				detached, err := templating.NewIncrementalDetachedValue(map[string]any{
					"round":  round,
					"worker": worker,
				})
				if err != nil {
					errorsByCall <- err
					return
				}
				<-start
				owner := incrementalGroupInstanceID{
					component: "publisher", source: "routes", name: fmt.Sprintf("route-%d", worker),
				}
				_, _, err = generation.capture(
					"routing", owner, worker, "backends", fmt.Sprintf("key-%d", worker), "", detached,
				)
				errorsByCall <- err
			}()
		}
		close(start)
		generation.revoke()
		group.Wait()
		close(errorsByCall)
		for err := range errorsByCall {
			if err != nil {
				require.ErrorContains(t, err, "generation is unavailable")
			}
		}

		detached, err := templating.NewIncrementalDetachedValue(map[string]any{"after": "revoke"})
		require.NoError(t, err)
		_, _, err = generation.capture(
			"routing", incrementalGroupInstanceID{component: "publisher", source: "routes", name: "late"},
			0, "backends", "late", "", detached,
		)
		require.ErrorContains(t, err, "generation is unavailable")
		generation.mu.RLock()
		assert.False(t, generation.active)
		assert.Nil(t, generation.shards)
		generation.mu.RUnlock()
	}
}

var incrementalPublicationSnapshotBenchmarkValue any
var incrementalPublicationSnapshotBenchmarkBytes []byte

func BenchmarkIncrementalPublicationSnapshot39012(b *testing.B) {
	values := make([]map[string]any, 39_012)
	keys := make([]string, len(values))
	for index := range values {
		keys[index] = fmt.Sprintf("route-%06d", index)
		values[index] = map[string]any{
			"namespace": "default",
			"name":      fmt.Sprintf("route-%06d", index%3_000),
			"backend": map[string]any{
				"name": fmt.Sprintf("service-%06d", index%9_002),
				"port": 8080 + index%8,
			},
		}
	}
	b.Run("owned-normalize-encode-snapshot", func(b *testing.B) {
		benchmarkPublicationSnapshotCapture(b, values, keys)
	})
	b.Run("owned-encode-decode-certify", func(b *testing.B) {
		benchmarkPublicationSnapshotRoundTrip(b, values)
	})
}

func benchmarkPublicationSnapshotCapture(b *testing.B, values []map[string]any, keys []string) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		generation, authority := newIncrementalPublicationSnapshotGeneration()
		owner := incrementalGroupInstanceID{component: "publisher", source: "routes"}
		for index := range values {
			detached, err := templating.NewIncrementalDetachedValue(values[index])
			if err != nil {
				b.Fatal(err)
			}
			encoded, snapshot, err := generation.capture(
				"routing", owner, index, "backends", keys[index], "", detached,
			)
			if err != nil {
				b.Fatal(err)
			}
			incrementalPublicationSnapshotBenchmarkBytes = encoded
			incrementalPublicationSnapshotBenchmarkValue = snapshot
		}
		incrementalPublicationSnapshotBenchmarkValue = authority
	}
}

func benchmarkPublicationSnapshotRoundTrip(b *testing.B, values []map[string]any) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		for index := range values {
			benchmarkPublicationSnapshotRoundTripValue(b, values[index])
		}
	}
}

func benchmarkPublicationSnapshotRoundTripValue(b *testing.B, value map[string]any) {
	b.Helper()
	detached, err := templating.NewIncrementalDetachedValue(value)
	if err != nil {
		b.Fatal(err)
	}
	owned, err := templating.ConsumeIncrementalDetachedValue(detached)
	if err != nil {
		b.Fatal(err)
	}
	encoded, err := encodeResourceValue(owned)
	if err != nil {
		b.Fatal(err)
	}
	decoded, err := decodeResourceValue(encoded)
	if err != nil {
		b.Fatal(err)
	}
	certificate := templating.CertifyIncrementalImmutableInputs(decoded)
	if certificate == nil || !certificate.Guards(decoded) {
		b.Fatal("decoded publication has no immutable certificate")
	}
	incrementalPublicationSnapshotBenchmarkBytes = encoded
	incrementalPublicationSnapshotBenchmarkValue = decoded
}

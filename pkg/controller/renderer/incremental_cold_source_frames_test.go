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
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type coldSourceFrameInputReader struct {
	incremental.ExactImmutableInputObserver

	mu           sync.Mutex
	inputs       map[incremental.InputKey]incremental.Input
	exactReads   []incremental.InputKey
	observations []incremental.ImmutableInput
	queries      []incremental.QueryKey
}

func (r *coldSourceFrameInputReader) Input(key incremental.InputKey) (value []byte, found bool, err error) {
	input, err := r.ExactInput(key)
	return input.Value, input.Found, err
}

func (r *coldSourceFrameInputReader) ExactInput(key incremental.InputKey) (incremental.Input, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	input, found := r.inputs[key]
	if !found {
		return incremental.Input{}, fmt.Errorf("unknown input %q", key.Opaque())
	}
	r.exactReads = append(r.exactReads, key)
	input.Value = slices.Clone(input.Value)
	return input, nil
}

func (r *coldSourceFrameInputReader) ObserveExactImmutableInput(expected incremental.ImmutableInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	input, found := r.inputs[expected.Key]
	if !found || input.Key != expected.Key || input.Revision != expected.Revision ||
		input.Found != expected.Found || !stringBytesEqual(expected.Value, input.Value) {
		return incremental.ErrRevisionConflict
	}
	r.observations = append(r.observations, expected)
	return nil
}

func (r *coldSourceFrameInputReader) Query(_ context.Context, key incremental.QueryKey) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, key)
	return nil, nil
}

func (r *coldSourceFrameInputReader) exactReadCount(key incremental.InputKey) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, read := range r.exactReads {
		if read == key {
			count++
		}
	}
	return count
}

type coldSourceFrameFallbackReader struct {
	base *coldSourceFrameInputReader
}

func (r *coldSourceFrameFallbackReader) Input(key incremental.InputKey) (value []byte, found bool, err error) {
	return r.base.Input(key)
}

func (r *coldSourceFrameFallbackReader) ExactInput(key incremental.InputKey) (incremental.Input, error) {
	return r.base.ExactInput(key)
}

func (r *coldSourceFrameFallbackReader) Query(ctx context.Context, key incremental.QueryKey) ([]byte, error) {
	return r.base.Query(ctx, key)
}

func requireColdSourceFrameView(
	tb testing.TB,
	refs *incrementalColdSourceFrameRefs,
	queryKey incremental.QueryKey,
	component *incrementalComponent,
) incrementalColdSourceFrameView {
	tb.Helper()
	const source, namespace, name = "routes", "default", "route"
	view, err := refs.authenticateDetached(queryKey, component, source, namespace, name)
	require.NoError(tb, err)
	return view
}

func TestIncrementalColdSourceFramesObserveEveryQueryExactly(t *testing.T) {
	session := &incrementalRenderSession{}
	components := []*incrementalComponent{{name: "route-a"}, {name: "route-b"}}
	queryKeys := []incremental.QueryKey{
		componentQueryKey(components[0], "routes", "default", "route"),
		componentQueryKey(components[1], "routes", "default", "route"),
	}
	generation, err := newIncrementalColdSourceFrameGeneration(session, 3, len(queryKeys))
	require.NoError(t, err)
	for index, key := range queryKeys {
		require.NoError(t, generation.bind(index, key, components[index], "routes", "default", "route"))
	}
	require.NoError(t, generation.sealGeneration())
	inputs := coldSourceFrameInputs(components[0].name, "route")
	for key, input := range coldSourceFrameInputs(components[1].name, "route") {
		inputs[key] = input
	}

	first := &coldSourceFrameInputReader{inputs: inputs}
	firstRefs, err := generation.refsFor(0, queryKeys[0], components[0], "routes", "default", "route")
	require.NoError(t, err)
	firstView := requireColdSourceFrameView(
		t, firstRefs, queryKeys[0], components[0],
	)
	firstBinding, err := firstView.binding.load(t.Context(), first, generation)
	require.NoError(t, err)
	firstItem, err := firstView.item.load(t.Context(), first, generation)
	require.NoError(t, err)
	firstSubject, err := firstView.renderSubject.load(t.Context(), first, generation)
	require.NoError(t, err)
	assert.Len(t, first.exactReads, 3)
	assert.Empty(t, first.observations)

	second := &coldSourceFrameInputReader{inputs: inputs}
	secondRefs, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
	require.NoError(t, err)
	secondView := requireColdSourceFrameView(
		t, secondRefs, queryKeys[1], components[1],
	)
	secondBinding, err := secondView.binding.load(t.Context(), second, generation)
	require.NoError(t, err)
	secondItem, err := secondView.item.load(t.Context(), second, generation)
	require.NoError(t, err)
	secondSubject, err := secondView.renderSubject.load(t.Context(), second, generation)
	require.NoError(t, err)

	assert.Equal(t, []incremental.InputKey{bindingInputKey(components[1].name, "routes")}, second.exactReads)
	require.Len(t, second.observations, 2)
	assert.Equal(t, resourceInputKey(&resourceInputSpec{
		resourceType: "routes", scope: resourceInputIdentity, namespace: "default", name: "route",
	}), second.observations[0].Key)
	assert.Equal(t, renderSubjectInputKey("routes", "default", "route"), second.observations[1].Key)
	assert.NotSame(t, firstBinding, secondBinding)
	assert.Same(t, firstItem, secondItem)
	assert.Same(t, firstSubject, secondSubject)
}

func TestIncrementalColdSourceFramesPreserveLazyDependencyReads(t *testing.T) {
	tests := []struct {
		name        string
		binding     bool
		item        bool
		wantReadKey []incremental.InputKey
	}{
		{name: "missing binding", binding: false, item: true},
		{name: "missing source", binding: true, item: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := &incrementalComponent{name: "route"}
			queryKey := componentQueryKey(component, "routes", "default", "route")
			snapshot := newIncrementalStateSnapshot()
			session := &incrementalRenderSession{
				state:   &incrementalRenderState{deriveSources: map[string]struct{}{}},
				retired: snapshot.retired.Txn(),
			}
			if !test.binding {
				session.retired.Insert([]byte(queryKey.Opaque()), struct{}{})
			}
			generation, err := newIncrementalColdSourceFrameGeneration(session, 0, 1)
			require.NoError(t, err)
			require.NoError(t, generation.bind(0, queryKey, component, "routes", "default", "route"))
			require.NoError(t, generation.sealGeneration())
			refs, err := generation.refsFor(0, queryKey, component, "routes", "default", "route")
			require.NoError(t, err)
			inputs := coldSourceFrameInputs(component.name, "route")
			bindingKey := bindingInputKey(component.name, "routes")
			itemKey := resourceInputKey(&resourceInputSpec{
				resourceType: "routes", scope: resourceInputIdentity, namespace: "default", name: "route",
			})
			binding := inputs[bindingKey]
			binding.Found = test.binding
			if !binding.Found {
				binding.Value = nil
			}
			inputs[bindingKey] = binding
			item := inputs[itemKey]
			item.Found = test.item
			if !item.Found {
				item.Value = nil
			}
			inputs[itemKey] = item
			reader := &coldSourceFrameInputReader{inputs: inputs}

			prepared, _, _, err := session.prepareComponentInputsDetachedWithSourceFrames(
				t.Context(), reader, component, "routes", "default", "route", refs,
			)
			require.NoError(t, err)
			assert.Nil(t, prepared)
			if test.binding {
				assert.Equal(t, []incremental.InputKey{bindingKey, itemKey}, reader.exactReads)
			} else {
				assert.Equal(t, []incremental.InputKey{bindingKey}, reader.exactReads)
			}
			assert.Empty(t, reader.observations)
		})
	}
}

func TestIncrementalColdSourceFramesRejectPoisonedObservationsAndFrames(t *testing.T) {
	components := []*incrementalComponent{{name: "route-a"}, {name: "route-b"}}
	queryKeys := []incremental.QueryKey{
		componentQueryKey(components[0], "routes", "default", "route"),
		componentQueryKey(components[1], "routes", "default", "route"),
	}
	newFixture := func(t *testing.T) (
		*incrementalColdSourceFrameGeneration,
		*incrementalColdSourceFrameRefs,
		*incrementalColdSourceFrameRefs,
		map[incremental.InputKey]incremental.Input,
	) {
		t.Helper()
		generation, err := newIncrementalColdSourceFrameGeneration(&incrementalRenderSession{}, 0, 3)
		require.NoError(t, err)
		for index, key := range queryKeys {
			require.NoError(t, generation.bind(index, key, components[index], "routes", "default", "route"))
		}
		require.NoError(t, generation.bind(
			2,
			componentQueryKey(components[0], "routes", "default", "other"),
			components[0],
			"routes",
			"default",
			"other",
		))
		require.NoError(t, generation.sealGeneration())
		first, err := generation.refsFor(0, queryKeys[0], components[0], "routes", "default", "route")
		require.NoError(t, err)
		second, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
		require.NoError(t, err)
		inputs := coldSourceFrameInputs(components[0].name, "route")
		for key, input := range coldSourceFrameInputs(components[1].name, "route") {
			inputs[key] = input
		}
		return generation, first, second, inputs
	}

	t.Run("same revision different value", func(t *testing.T) {
		generation, first, second, inputs := newFixture(t)
		firstView := requireColdSourceFrameView(
			t, first, queryKeys[0], components[0],
		)
		_, err := firstView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, generation)
		require.NoError(t, err)
		itemKey := firstView.item.key
		poisoned := inputs[itemKey]
		poisoned.Value = []byte(`{"poisoned":true}`)
		inputs[itemKey] = poisoned
		secondView := requireColdSourceFrameView(
			t, second, queryKeys[1], components[1],
		)
		_, err = secondView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, generation)
		require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	})

	for _, test := range []struct {
		name   string
		poison func(*incrementalColdCertifiedSourceInput)
	}{
		{
			name: "revision",
			poison: func(value *incrementalColdCertifiedSourceInput) {
				value.revision = incremental.NewRevision("poisoned")
			},
		},
		{
			name: "encoded value",
			poison: func(value *incrementalColdCertifiedSourceInput) {
				value.encoded = `{"poisoned":true}`
			},
		},
		{
			name: "decoded value",
			poison: func(value *incrementalColdCertifiedSourceInput) {
				value.value = map[string]any{"poisoned": true}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation, first, second, inputs := newFixture(t)
			firstView := requireColdSourceFrameView(
				t, first, queryKeys[0], components[0],
			)
			value, err := firstView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, generation)
			require.NoError(t, err)
			test.poison(value)
			secondView := requireColdSourceFrameView(
				t, second, queryKeys[1], components[1],
			)
			_, err = secondView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, generation)
			require.ErrorContains(t, err, "invalid")
		})
	}

	t.Run("substituted reference", func(t *testing.T) {
		generation, first, _, _ := newFixture(t)
		generation.refs[1] = *first
		_, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
		require.ErrorContains(t, err, "invalid provenance")
	})

	t.Run("cross-kind reference", func(t *testing.T) {
		generation, _, _, _ := newFixture(t)
		generation.refs[1].item = generation.refs[1].binding
		_, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
		require.ErrorContains(t, err, "invalid provenance")
	})

	t.Run("cross-source reference", func(t *testing.T) {
		generation, _, _, _ := newFixture(t)
		generation.refs[1].item = generation.refs[2].item
		_, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
		require.ErrorContains(t, err, "invalid provenance")
	})

	t.Run("copied reference", func(t *testing.T) {
		generation, first, _, _ := newFixture(t)
		copied := *first
		_, err := copied.authenticateDetached(
			queryKeys[0], components[0], "routes", "default", "route",
		)
		require.ErrorContains(t, err, "invalid provenance")
		generation.revoke()
	})

	t.Run("replayed generation", func(t *testing.T) {
		generation, first, _, inputs := newFixture(t)
		firstView := requireColdSourceFrameView(
			t, first, queryKeys[0], components[0],
		)
		other, err := newIncrementalColdSourceFrameGeneration(&incrementalRenderSession{}, 0, 1)
		require.NoError(t, err)
		require.NoError(t, other.bind(0, queryKeys[0], components[0], "routes", "default", "route"))
		require.NoError(t, other.sealGeneration())
		_, err = firstView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, other)
		require.ErrorContains(t, err, "invalid provenance")
		generation.revoke()
		_, err = firstView.item.load(t.Context(), &coldSourceFrameInputReader{inputs: inputs}, generation)
		require.ErrorContains(t, err, "invalid provenance")
	})
}

func TestIncrementalColdSourceFramesFallbackReaderComparesCompleteValue(t *testing.T) {
	session := &incrementalRenderSession{}
	components := []*incrementalComponent{{name: "route-a"}, {name: "route-b"}}
	queryKeys := []incremental.QueryKey{
		componentQueryKey(components[0], "routes", "default", "route"),
		componentQueryKey(components[1], "routes", "default", "route"),
	}
	generation, err := newIncrementalColdSourceFrameGeneration(session, 0, 2)
	require.NoError(t, err)
	for index, key := range queryKeys {
		require.NoError(t, generation.bind(index, key, components[index], "routes", "default", "route"))
	}
	require.NoError(t, generation.sealGeneration())
	inputs := coldSourceFrameInputs(components[0].name, "route")
	for key, input := range coldSourceFrameInputs(components[1].name, "route") {
		inputs[key] = input
	}
	first, err := generation.refsFor(0, queryKeys[0], components[0], "routes", "default", "route")
	require.NoError(t, err)
	firstView := requireColdSourceFrameView(
		t, first, queryKeys[0], components[0],
	)
	_, err = firstView.item.load(t.Context(), &coldSourceFrameFallbackReader{
		base: &coldSourceFrameInputReader{inputs: inputs},
	}, generation)
	require.NoError(t, err)

	second, err := generation.refsFor(1, queryKeys[1], components[1], "routes", "default", "route")
	require.NoError(t, err)
	secondView := requireColdSourceFrameView(
		t, second, queryKeys[1], components[1],
	)
	poisoned := coldSourceFrameInputs(components[1].name, "route")
	item := poisoned[secondView.item.key]
	item.Value = []byte(`{"poisoned":true}`)
	poisoned[secondView.item.key] = item
	reader := &coldSourceFrameInputReader{inputs: poisoned}
	_, err = secondView.item.load(t.Context(), &coldSourceFrameFallbackReader{base: reader}, generation)
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	assert.Equal(t, 1, reader.exactReadCount(secondView.item.key))
}

func TestIncrementalColdSourceFramesKeepDerivedOwnerReadsPerQuery(t *testing.T) {
	const source = "routes"
	owner := incrementalComponent{name: "governance", deriveResource: true}
	components := []*incrementalComponent{{name: "first"}, {name: "second"}}
	plan := newIncrementalBindingPlan()
	plan.owners[source] = owner
	plan.props[string(bindingKey(owner.name, source))] = nil
	session := &incrementalRenderSession{
		state: &incrementalRenderState{
			components: map[string]incrementalComponent{owner.name: owner},
			deriveSources: map[string]struct{}{
				source: {},
			},
		},
		bindingPlan: plan,
		retired:     newIncrementalStateSnapshot().retired.Txn(),
	}
	generation, err := newIncrementalColdSourceFrameGeneration(session, 0, len(components))
	require.NoError(t, err)
	queryKeys := make([]incremental.QueryKey, len(components))
	inputs := map[incremental.InputKey]incremental.Input{}
	for index, component := range components {
		queryKeys[index] = componentQueryKey(component, source, "default", "route")
		require.NoError(t, generation.bind(
			index, queryKeys[index], component, source, "default", "route",
		))
		for key, input := range coldSourceFrameInputs(component.name, "route") {
			inputs[key] = input
		}
	}
	ownerInput := deriveOwnerInput(source, &owner, true)
	inputs[ownerInput.Key] = ownerInput
	require.NoError(t, generation.sealGeneration())

	for index, component := range components {
		refs, err := generation.refsFor(
			index, queryKeys[index], component, source, "default", "route",
		)
		require.NoError(t, err)
		reader := &coldSourceFrameInputReader{inputs: inputs}
		prepared, _, _, err := session.prepareComponentInputsDetachedWithSourceFrames(
			t.Context(), reader, component, source, "default", "route", refs,
		)
		require.NoError(t, err)
		require.NotNil(t, prepared)
		assert.Positive(t, reader.exactReadCount(ownerInput.Key))
		assert.NotEmpty(t, reader.queries)
	}
}

func TestIncrementalColdSourceFramesRandomizedPreparationParity(t *testing.T) {
	const (
		componentCount = 4
		resourceCount  = 64
		queryCount     = componentCount * resourceCount
	)
	components := make([]*incrementalComponent, componentCount)
	for index := range components {
		components[index] = &incrementalComponent{name: fmt.Sprintf("component-%02d", index)}
	}
	session := &incrementalRenderSession{
		state:   &incrementalRenderState{deriveSources: map[string]struct{}{}},
		retired: newIncrementalStateSnapshot().retired.Txn(),
	}
	generation, err := newIncrementalColdSourceFrameGeneration(session, 7, queryCount)
	require.NoError(t, err)
	inputs := map[incremental.InputKey]incremental.Input{}
	queryKeys := make([]incremental.QueryKey, queryCount)
	for index := range queryCount {
		component := components[index%componentCount]
		name := fmt.Sprintf("route-%03d", index/componentCount)
		queryKeys[index] = componentQueryKey(component, "routes", "default", name)
		require.NoError(t, generation.bind(
			index, queryKeys[index], component, "routes", "default", name,
		))
		for key, input := range coldSourceFrameInputs(component.name, name) {
			inputs[key] = input
		}
	}
	require.NoError(t, generation.sealGeneration())

	order := rand.New(rand.NewPCG(187, 23)).Perm(queryCount)
	for _, index := range order {
		component := components[index%componentCount]
		name := fmt.Sprintf("route-%03d", index/componentCount)
		legacyReader := &coldSourceFrameFallbackReader{
			base: &coldSourceFrameInputReader{inputs: inputs},
		}
		legacy, legacyImmediate, legacyExecuted, legacyErr := session.prepareComponentInputsDetached(
			t.Context(), legacyReader, component, "routes", "default", name,
		)
		require.NoError(t, legacyErr)
		refs, err := generation.refsFor(
			index, queryKeys[index], component, "routes", "default", name,
		)
		require.NoError(t, err)
		framed, framedImmediate, framedExecuted, framedErr := session.prepareComponentInputsDetachedWithSourceFrames(
			t.Context(), &coldSourceFrameInputReader{inputs: inputs}, component,
			"routes", "default", name, refs,
		)
		require.NoError(t, framedErr)
		assert.Equal(t, legacyImmediate, framedImmediate)
		assert.Equal(t, legacyExecuted, framedExecuted)
		require.NotNil(t, legacy)
		require.NotNil(t, framed)
		assert.Equal(t, legacy.queryKey, framed.queryKey)
		assert.Equal(t, legacy.source, framed.source)
		assert.Equal(t, legacy.namespace, framed.namespace)
		assert.Equal(t, legacy.name, framed.name)
		assert.Equal(t, legacy.item, framed.item)
		assert.Equal(t, legacy.props, framed.props)
		assert.Equal(t, legacy.renderSubject, framed.renderSubject)
		assert.Equal(t, legacy.itemBytes, framed.itemBytes)
		assert.True(t, framed.itemCertificate.Guards(framed.item))
		assert.True(t, framed.propsCertificate.Guards(framed.props))
		assert.True(t, framed.subjectCertificate.Guards(framed.renderSubject))
	}
}

func TestIncrementalColdSourceFramesConcurrentMaterialization(t *testing.T) {
	const workers = 128
	session := &incrementalRenderSession{}
	components := make([]*incrementalComponent, workers)
	generation, err := newIncrementalColdSourceFrameGeneration(session, 0, workers)
	require.NoError(t, err)
	inputs := map[incremental.InputKey]incremental.Input{}
	for index := range workers {
		components[index] = &incrementalComponent{name: fmt.Sprintf("component-%03d", index)}
		queryKey := componentQueryKey(components[index], "routes", "default", "route")
		require.NoError(t, generation.bind(
			index,
			queryKey,
			components[index],
			"routes",
			"default",
			"route",
		))
		for key, input := range coldSourceFrameInputs(components[index].name, "route") {
			inputs[key] = input
		}
	}
	require.NoError(t, generation.sealGeneration())
	readers := make([]*coldSourceFrameInputReader, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for index := range workers {
		go func() {
			defer group.Done()
			queryKey := componentQueryKey(components[index], "routes", "default", "route")
			refs, refsErr := generation.refsFor(
				index,
				queryKey,
				components[index],
				"routes",
				"default",
				"route",
			)
			if refsErr != nil {
				errs[index] = refsErr
				return
			}
			view, viewErr := refs.authenticateDetached(
				queryKey,
				components[index],
				"routes",
				"default",
				"route",
			)
			if viewErr != nil {
				errs[index] = viewErr
				return
			}
			readers[index] = &coldSourceFrameInputReader{inputs: inputs}
			_, errs[index] = view.item.load(t.Context(), readers[index], generation)
		}()
	}
	group.Wait()
	require.NoError(t, errors.Join(errs...))
	exactReads := 0
	observations := 0
	for _, reader := range readers {
		exactReads += len(reader.exactReads)
		observations += len(reader.observations)
	}
	assert.Equal(t, 1, exactReads)
	assert.Equal(t, workers-1, observations)
	generation.revoke()
}

var incrementalColdSourceFrameBenchmarkSink *incrementalColdCertifiedSourceInput

type coldSourceFrameBenchmarkReader struct {
	incremental.ExactImmutableInputObserver
	inputs map[incremental.InputKey]incremental.Input
}

func (r *coldSourceFrameBenchmarkReader) Input(key incremental.InputKey) (value []byte, found bool, err error) {
	input, err := r.ExactInput(key)
	return input.Value, input.Found, err
}

func (r *coldSourceFrameBenchmarkReader) ExactInput(key incremental.InputKey) (incremental.Input, error) {
	input, found := r.inputs[key]
	if !found {
		return incremental.Input{}, fmt.Errorf("unknown input %q", key.Opaque())
	}
	return input, nil
}

func (r *coldSourceFrameBenchmarkReader) ObserveExactImmutableInput(expected incremental.ImmutableInput) error {
	input, found := r.inputs[expected.Key]
	if !found || input.Revision != expected.Revision || input.Found != expected.Found ||
		!stringBytesEqual(expected.Value, input.Value) {
		return incremental.ErrRevisionConflict
	}
	return nil
}

func (*coldSourceFrameBenchmarkReader) Query(context.Context, incremental.QueryKey) ([]byte, error) {
	return nil, nil
}

type coldSourceFrameBenchmarkItem struct {
	component *incrementalComponent
	name      string
	queryKey  incremental.QueryKey
}

func BenchmarkIncrementalColdSourceFramesHTTPRoute3000Shape(b *testing.B) {
	const (
		resourceCount  = 3000
		componentCount = 13
		queryCount     = resourceCount*componentCount + 12
	)
	components := make([]*incrementalComponent, componentCount+1)
	for index := range components {
		components[index] = &incrementalComponent{name: fmt.Sprintf("component-%02d", index)}
	}
	inputs := map[incremental.InputKey]incremental.Input{}
	items := make([]coldSourceFrameBenchmarkItem, queryCount)
	for resourceIndex := range resourceCount {
		name := fmt.Sprintf("route-%04d", resourceIndex)
		for componentIndex := range components {
			for key, input := range coldSourceFrameInputs(
				components[componentIndex].name, name,
			) {
				inputs[key] = input
			}
		}
	}
	for queryIndex := range queryCount {
		componentIndex := queryIndex / resourceCount
		resourceIndex := queryIndex % resourceCount
		if queryIndex >= resourceCount*componentCount {
			componentIndex = componentCount
			resourceIndex = queryIndex - resourceCount*componentCount
		}
		component := components[componentIndex]
		name := fmt.Sprintf("route-%04d", resourceIndex)
		items[queryIndex] = coldSourceFrameBenchmarkItem{
			component: component,
			name:      name,
			queryKey:  componentQueryKey(component, "routes", "default", name),
		}
	}
	reader := &coldSourceFrameBenchmarkReader{inputs: inputs}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkColdSourceFrameGeneration(b, items, reader)
	}
	b.ReportMetric(float64(unsafe.Sizeof(incrementalColdSourceFrameRefs{})), "ref-B")
}

func benchmarkColdSourceFrameGeneration(
	b *testing.B,
	items []coldSourceFrameBenchmarkItem,
	reader *coldSourceFrameBenchmarkReader,
) {
	b.Helper()
	session := &incrementalRenderSession{}
	generation, err := newIncrementalColdSourceFrameGeneration(session, 0, len(items))
	if err != nil {
		b.Fatal(err)
	}
	for queryIndex := range items {
		item := &items[queryIndex]
		if err := generation.bind(
			queryIndex, item.queryKey, item.component, "routes", "default", item.name,
		); err != nil {
			b.Fatal(err)
		}
	}
	if err := generation.sealGeneration(); err != nil {
		b.Fatal(err)
	}
	for queryIndex := range items {
		item := &items[queryIndex]
		refs := &generation.refs[queryIndex]
		view, err := refs.authenticateDetached(
			item.queryKey, item.component, "routes", "default", item.name,
		)
		if err != nil {
			b.Fatal(err)
		}
		if incrementalColdSourceFrameBenchmarkSink, err = view.binding.load(b.Context(), reader, generation); err != nil {
			b.Fatal(err)
		}
		if incrementalColdSourceFrameBenchmarkSink, err = view.item.load(b.Context(), reader, generation); err != nil {
			b.Fatal(err)
		}
		if incrementalColdSourceFrameBenchmarkSink, err = view.renderSubject.load(b.Context(), reader, generation); err != nil {
			b.Fatal(err)
		}
	}
	generation.revoke()
}

func BenchmarkIncrementalColdSourceFrameLegacyInputsHTTPRoute3000Shape(b *testing.B) {
	const (
		resourceCount  = 3000
		componentCount = 13
		queryCount     = resourceCount*componentCount + 12
	)
	components := make([]*incrementalComponent, componentCount+1)
	for index := range components {
		components[index] = &incrementalComponent{name: fmt.Sprintf("component-%02d", index)}
	}
	inputs := map[incremental.InputKey]incremental.Input{}
	items := make([]coldSourceFrameBenchmarkItem, queryCount)
	for queryIndex := range queryCount {
		componentIndex := queryIndex / resourceCount
		resourceIndex := queryIndex % resourceCount
		if queryIndex >= resourceCount*componentCount {
			componentIndex = componentCount
			resourceIndex = queryIndex - resourceCount*componentCount
		}
		component := components[componentIndex]
		name := fmt.Sprintf("route-%04d", resourceIndex)
		items[queryIndex] = coldSourceFrameBenchmarkItem{component: component, name: name}
		for key, input := range coldSourceFrameInputs(component.name, name) {
			inputs[key] = input
		}
	}
	reader := &coldSourceFrameBenchmarkReader{inputs: inputs}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		session := &incrementalRenderSession{}
		for queryIndex := range queryCount {
			item := &items[queryIndex]
			if _, _, _, _, err := session.decodeComponentInputWithEncoding(
				reader,
				bindingInputKey(item.component.name, "routes"),
				item.component.name,
				"props",
				false,
			); err != nil {
				b.Fatal(err)
			}
			if _, _, _, _, err := session.decodeComponentInputWithEncoding(
				reader,
				resourceInputKey(&resourceInputSpec{
					resourceType: "routes",
					scope:        resourceInputIdentity,
					namespace:    "default",
					name:         item.name,
				}),
				item.component.name,
				"source",
				false,
			); err != nil {
				b.Fatal(err)
			}
			if _, _, _, _, err := session.decodeComponentInputWithEncoding(
				reader,
				renderSubjectInputKey("routes", "default", item.name),
				item.component.name,
				"render subject",
				false,
			); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func coldSourceFrameInputs(
	component, name string,
) map[incremental.InputKey]incremental.Input {
	const source, namespace = "routes", "default"
	bindingKey := bindingInputKey(component, source)
	itemKey := resourceInputKey(&resourceInputSpec{
		resourceType: source,
		scope:        resourceInputIdentity,
		namespace:    namespace,
		name:         name,
	})
	subjectKey := renderSubjectInputKey(source, namespace, name)
	return map[incremental.InputKey]incremental.Input{
		bindingKey: {
			Key: bindingKey, Revision: incremental.NewRevision("binding-" + component + "-" + source), Found: true,
			Value: []byte(fmt.Sprintf(`{"component":%q,"source":%q}`, component, source)),
		},
		itemKey: {
			Key: itemKey, Revision: incremental.NewRevision("item-" + source + "-" + namespace + "-" + name), Found: true,
			Value: []byte(fmt.Sprintf(
				`{"apiVersion":"example.test/v1","kind":"Route","metadata":{"name":%q,"namespace":%q}}`,
				name,
				namespace,
			)),
		},
		subjectKey: {
			Key: subjectKey, Revision: incremental.NewRevision("subject-" + source + "-" + namespace + "-" + name), Found: true,
			Value: []byte(fmt.Sprintf(
				`{"mode":"reconcile","name":%q,"namespace":%q,"source":%q}`,
				name,
				namespace,
				source,
			)),
		},
	}
}

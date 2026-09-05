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

package templating

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalVectorTestLifecycle struct {
	active      int
	begins      []int
	ends        []int
	outputs     []string
	abortIndex  int
	abortCause  error
	abortCalled bool
}

type incrementalVectorEndHookLifecycle struct {
	*incrementalVectorTestLifecycle
	end func(int)
}

func (lifecycle *incrementalVectorEndHookLifecycle) End(index int, output string) error {
	if lifecycle.end != nil {
		lifecycle.end(index)
	}
	return lifecycle.incrementalVectorTestLifecycle.End(index, output)
}

func newIncrementalVectorTestLifecycle(count int) *incrementalVectorTestLifecycle {
	return &incrementalVectorTestLifecycle{
		active:     -1,
		outputs:    make([]string, count),
		abortIndex: -2,
	}
}

func (lifecycle *incrementalVectorTestLifecycle) Begin(index int) error {
	if lifecycle.active >= 0 {
		return fmt.Errorf("item %d is already active", lifecycle.active)
	}
	lifecycle.active = index
	lifecycle.begins = append(lifecycle.begins, index)
	return nil
}

func (lifecycle *incrementalVectorTestLifecycle) End(index int, output string) error {
	if lifecycle.active != index {
		return fmt.Errorf("item %d is not active", index)
	}
	lifecycle.active = -1
	lifecycle.ends = append(lifecycle.ends, index)
	lifecycle.outputs[index] = output
	return nil
}

func (lifecycle *incrementalVectorTestLifecycle) Abort(index int, cause error) {
	lifecycle.abortCalled = true
	lifecycle.abortIndex = index
	lifecycle.abortCause = cause
	lifecycle.active = -1
}

func TestRenderIncrementalComponentVectorAuthenticatesContextsAndOutputs(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ jsonpathGet(item, "$.value") }}:{{ source }}`)
	eligibility, ok := engine.IncrementalComponentVectorEligibility("component")
	require.True(t, ok)
	assert.Equal(t, incrementalVectorBaseBindingNames[:], eligibility.BindingNames)

	input := newIncrementalVectorTestInput(t, engine, 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("source-%d", index)
		values["item"].(map[string]any)["value"] = fmt.Sprintf("item-%d", index)
	})
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)
	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0, 1}, lifecycle.ends)
	assert.Equal(t, []string{"item-0:source-0", "item-1:source-1"}, lifecycle.outputs)
	assert.False(t, lifecycle.abortCalled)
}

func TestRenderIncrementalComponentVectorAbortsActiveItem(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t,
		`{% if item["fail"] == true %}{{ fail(source) }}{% end %}{{ source }}`,
	)
	input := newIncrementalVectorTestInput(t, engine, 3, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("source-%d", index)
		values["item"].(map[string]any)["fail"] = index == 1
	})
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)
	err := engine.RenderIncrementalComponentVector(t.Context(), "component", input)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0}, lifecycle.ends)
	assert.Equal(t, "source-0", lifecycle.outputs[0])
	assert.True(t, lifecycle.abortCalled)
	assert.Equal(t, 1, lifecycle.abortIndex)
	assert.ErrorContains(t, lifecycle.abortCause, "source-1")
	assert.NotErrorIs(t, lifecycle.abortCause, scriggo.ErrVectorGenerationRevoked)
}

func TestRenderIncrementalComponentVectorRejectsForeignItemContextBeforeExecution(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ source }}`)
	input := newIncrementalVectorTestInput(t, engine, 2, nil)
	values := input.Contexts[1].Value(RenderContextContextKey).(map[string]any)
	values["item"] = input.Contexts[0].Value(RenderContextContextKey).(map[string]any)["item"]
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)

	err := engine.RenderIncrementalComponentVector(t.Context(), "component", input)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.Empty(t, lifecycle.begins)
	assert.True(t, lifecycle.abortCalled)
	assert.Equal(t, -1, lifecycle.abortIndex)
}

func TestRenderIncrementalComponentVectorRejectsDeepEqualForeignCapability(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ source }}`)
	input := newIncrementalVectorTestInput(t, engine, 1, nil)
	values := input.Contexts[0].Value(RenderContextContextKey).(map[string]any)
	original := reflect.ValueOf(values["resources"])
	foreign := reflect.New(original.Type().Elem())
	foreign.Elem().Set(original.Elem())
	values["resources"] = foreign.Interface()
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)

	err := engine.RenderIncrementalComponentVector(t.Context(), "component", input)
	require.Error(t, err)
	assert.Empty(t, lifecycle.begins)
	assert.True(t, lifecycle.abortCalled)
	assert.Equal(t, -1, lifecycle.abortIndex)
}

func TestRenderIncrementalComponentVectorOwnsColumnsBeforeExecution(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ source }}`)
	input := newIncrementalVectorTestInput(t, engine, 2, nil)
	base := input.Lifecycle.(*incrementalVectorTestLifecycle)
	sources := input.Bindings["source"].([]string)
	input.Lifecycle = &incrementalVectorMutatingLifecycle{
		incrementalVectorTestLifecycle: base,
		mutate: func(index int) {
			if index == 0 {
				sources[1] = "poison"
			}
		},
	}

	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	assert.Equal(t, []string{"test", "test"}, base.outputs)
}

func TestRenderIncrementalComponentVectorPublishesAfterRevocation(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t,
		`{% var _, _ = http.Fetch(func() string { return source }) %}{{ source }}`,
	)
	fetcher := &incrementalVectorCarrierRetainingFetcher{}
	input := newIncrementalVectorTestInput(t, engine, 1, func(_ int, values map[string]any) {
		values["http"] = fetcher
	})
	base := input.Lifecycle.(*incrementalVectorTestLifecycle)
	var retainedPanic any
	input.Lifecycle = &incrementalVectorEndHookLifecycle{
		incrementalVectorTestLifecycle: base,
		end: func(int) {
			defer func() { retainedPanic = recover() }()
			fetcher.callback()()
		},
	}

	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	require.NotNil(t, retainedPanic)
	assert.ErrorContains(t, fmt.Errorf("%v", retainedPanic), scriggo.ErrVectorGenerationRevoked.Error())
	assert.Equal(t, []int{0}, base.ends)
	assert.Equal(t, []string{"test"}, base.outputs)
	assert.False(t, base.abortCalled)
}

func TestRenderIncrementalComponentVectorRejectsEmptyAndNilPointerColumns(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ source }}`)

	empty := newIncrementalVectorTestInput(t, engine, 1, nil)
	empty.Count = 0
	empty.Contexts = nil
	for name, column := range empty.Bindings {
		empty.Bindings[name] = reflect.MakeSlice(reflect.TypeOf(column), 0, 0).Interface()
	}
	require.ErrorContains(t,
		engine.RenderIncrementalComponentVector(t.Context(), "component", empty),
		"must be positive",
	)

	nilPointer := newIncrementalVectorTestInput(t, engine, 2, nil)
	resources := reflect.ValueOf(nilPointer.Bindings["resources"])
	resources.Index(1).SetZero()
	require.ErrorContains(t,
		engine.RenderIncrementalComponentVector(t.Context(), "component", nilPointer),
		"pointer is nil",
	)
}

func TestIncrementalComponentVectorPreservesInvocationLocalState(t *testing.T) {
	tests := map[string]map[string]string{
		"scalar": {
			"component": `{% var Counter int %}{% Counter++ %}{{ Counter }}`,
		},
		"map": {
			"component": `{% var State = map[string]int{"count": 0} %}{% State["count"]++ %}{{ State["count"] }}`,
		},
		"slice": {
			"component": `{% var State = []int{0} %}{% State[0]++ %}{{ State[0] }}`,
		},
		"imported macro": {
			"component": `{% import "library" for Next %}{{ Next() }}`,
			"library":   `{% var Counter int %}{% macro Next %}{% Counter++ %}{{ Counter }}{% end %}`,
		},
		"capture": {
			"component": `{% var Counter int %}{% var Next = func() int { Counter++; return Counter } %}{{ Next() }}`,
		},
	}
	for name, templates := range tests {
		t.Run(name, func(t *testing.T) {
			engine := newIncrementalVectorTestEngineFromTemplates(t, templates)
			input := newIncrementalVectorTestInput(t, engine, 2, nil)
			lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)
			require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
			assert.Equal(t, []string{"1", "1"}, lifecycle.outputs)
		})
	}
}

func TestIncrementalComponentVectorCompletesAfterImportedSharedMacro(t *testing.T) {
	engine := newIncrementalVectorTestEngineFromTemplates(t, map[string]string{
		"component": `{% import "library" for Contribute %}{{ Contribute() }}`,
		"library":   `{% macro Contribute %}{%% show shared.Unique("cell", "key", source) %%}{% end %}`,
	})
	input := newIncrementalVectorTestInput(t, engine, 2, nil)
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)

	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	assert.Equal(t, []int{0, 1}, lifecycle.ends)
}

func TestIncrementalComponentVectorCompletesAfterNestedImportedResourceMacro(t *testing.T) {
	declaration := (*incrementalResourceBindingTestResources)(nil)
	RegisterIncrementalResourceDeclaration(declaration)
	engine, err := New(map[string]string{
		"component": `{% import "outer" for Lookup %}{{ Lookup() }}`,
		"outer": `{% import "inner" for Service %}
{% macro Lookup() string %}{{ Service() }}{% end %}`,
		"inner": `{% macro Service() string %}{{ tostring(resources.services.GetSingle("default", "service")) }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations:           map[string]any{"resources": declaration},
	})
	require.NoError(t, err)
	input := newIncrementalVectorTestInput(t, engine, 2, func(_ int, values map[string]any) {
		resources := reflect.ValueOf(values["resources"])
		services := reflect.New(resources.Elem().FieldByName("Services").Type().Elem())
		for fieldIndex := range services.Elem().NumField() {
			field := services.Elem().Field(fieldIndex)
			field.Set(reflect.MakeFunc(field.Type(), func(arguments []reflect.Value) []reflect.Value {
				result := reflect.Zero(field.Type().Out(0))
				if field.Type().Out(0) == reflect.TypeFor[any]() {
					env := arguments[0].Interface().(native.Env)
					itemValues := env.Context().Value(RenderContextContextKey).(map[string]any)
					result = reflect.ValueOf(itemValues["source"])
				}
				return []reflect.Value{result}
			}))
		}
		resources.Elem().FieldByName("Services").Set(services)
	})
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)

	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	assert.Equal(t, []string{"test", "test"}, lifecycle.outputs)
	assert.Equal(t, []int{0, 1}, lifecycle.ends)
}

func TestIncrementalComponentVectorEligibilityFailsClosed(t *testing.T) {
	tests := map[string]string{
		"binding address":  `{% var pointer = &item %}{% _ = pointer %}{{ source }}`,
		"binding mutation": `{% source = "changed" %}{{ source }}`,
		"reserved name":    `{% var __haptic_vector_user = source %}{{ __haptic_vector_user }}`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			engine := newIncrementalVectorTestEngine(t, source)
			_, eligible := engine.IncrementalComponentVectorEligibility("component")
			assert.False(t, eligible)
		})
	}
}

func newIncrementalVectorTestEngine(tb testing.TB, source string) *ScriggoEngine {
	tb.Helper()
	return newIncrementalVectorTestEngineFromTemplates(tb, map[string]string{"component": source})
}

func newIncrementalVectorTestEngineFromTemplates(
	tb testing.TB,
	templates map[string]string,
) *ScriggoEngine {
	tb.Helper()
	engine, err := New(templates, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations: map[string]any{
			"resources": incrementalBatchResourcesDeclaration(),
		},
	})
	require.NoError(tb, err)
	return engine
}

func newIncrementalVectorTestInput(
	tb testing.TB,
	engine *ScriggoEngine,
	count int,
	mutate func(index int, values map[string]any),
) IncrementalComponentVectorInput {
	tb.Helper()
	return newIncrementalVectorTestInputForTemplate(tb, engine, "component", count, mutate)
}

func newIncrementalVectorTestInputForTemplate(
	tb testing.TB,
	engine *ScriggoEngine,
	templateName string,
	count int,
	mutate func(index int, values map[string]any),
) IncrementalComponentVectorInput {
	tb.Helper()
	entryPoint := engine.incrementalVectorEntryPoints[templateName]
	require.NotNil(tb, entryPoint)
	contexts := make([]context.Context, count)
	valuesByItem := make([]map[string]any, count)
	for index := range count {
		values := incrementalVectorTestValues(entryPoint)
		if mutate != nil {
			mutate(index, values)
		}
		itemCtx := WithIncrementalImmutableInputs(
			context.Background(),
			values["item"],
			values["props"],
			values["renderSubject"],
			values["controller"],
		)
		itemCtx = WithIncrementalImmutableCapabilityInputs(
			itemCtx,
			values["resources"],
			values["shared"],
		)
		require.NoError(tb, BindIncrementalImmutableInputs(values, itemCtx))
		itemCtx = context.WithValue(itemCtx, RenderContextContextKey, values)
		contexts[index] = itemCtx
		valuesByItem[index] = values
	}
	columns := make(map[string]any, len(entryPoint.bindings))
	for _, binding := range entryPoint.bindings {
		elementType := binding.variableType
		if binding.name == "resources" {
			elementType = reflect.PointerTo(binding.variableType)
		}
		column := reflect.MakeSlice(reflect.SliceOf(elementType), count, count)
		for index := range count {
			value := reflect.ValueOf(valuesByItem[index][binding.name])
			if value.IsValid() {
				column.Index(index).Set(value)
			}
		}
		columns[binding.name] = column.Interface()
	}
	return IncrementalComponentVectorInput{
		Count:         count,
		SharedContext: map[string]any{},
		Bindings:      columns,
		Contexts:      contexts,
		Lifecycle:     newIncrementalVectorTestLifecycle(count),
	}
}

func incrementalVectorTestValues(entryPoint *incrementalVectorEntryPoint) map[string]any {
	values := map[string]any{
		"controller":    map[string]ResourceStore{},
		"http":          nil,
		"item":          map[string]any{},
		"planRegistry":  nil,
		"props":         map[string]any{},
		"renderMode":    "reconcile",
		"renderSubject": map[string]any{"mode": "reconcile"},
		"shared":        NewSharedContributionContext(&noOpSharedContributionRecorder{}),
		"source":        "test",
	}
	for _, binding := range entryPoint.bindings {
		if binding.name == "resources" {
			values[binding.name] = reflect.New(binding.variableType).Interface()
		}
	}
	return values
}

func TestIncrementalVectorBindingNamesStayExact(t *testing.T) {
	names := slices.Clone(incrementalVectorBaseBindingNames[:])
	assert.True(t, slices.IsSorted(names))
	assert.Equal(t, []string{
		"controller", "http", "item", "planRegistry", "props", "renderMode",
		"renderSubject", "resources", "shared", "source",
	}, names)
}

func TestIncrementalVectorLifecycleEndFailureAbortsWithoutReplacement(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{{ source }}`)
	input := newIncrementalVectorTestInput(t, engine, 2, nil)
	wantErr := errors.New("end failed")
	base := input.Lifecycle.(*incrementalVectorTestLifecycle)
	input.Lifecycle = &incrementalVectorEndFailureLifecycle{base: base, err: wantErr}

	err := engine.RenderIncrementalComponentVector(t.Context(), "component", input)
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, []int{0}, base.begins)
	assert.Equal(t, []int{0}, base.ends)
	assert.True(t, base.abortCalled)
}

type incrementalVectorEndFailureLifecycle struct {
	base *incrementalVectorTestLifecycle
	err  error
}

type incrementalVectorMutatingLifecycle struct {
	*incrementalVectorTestLifecycle
	mutate func(index int)
}

func (lifecycle *incrementalVectorMutatingLifecycle) Begin(index int) error {
	if err := lifecycle.incrementalVectorTestLifecycle.Begin(index); err != nil {
		return err
	}
	if lifecycle.mutate != nil {
		lifecycle.mutate(index)
	}
	return nil
}

func (lifecycle *incrementalVectorEndFailureLifecycle) Begin(index int) error {
	return lifecycle.base.Begin(index)
}

func (lifecycle *incrementalVectorEndFailureLifecycle) End(index int, output string) error {
	if err := lifecycle.base.End(index, output); err != nil {
		return err
	}
	return lifecycle.err
}

func (lifecycle *incrementalVectorEndFailureLifecycle) Abort(index int, cause error) {
	lifecycle.base.Abort(index, cause)
}

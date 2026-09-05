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
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalComponentContextTestLease struct {
	calls atomic.Int64
}

func (l *incrementalComponentContextTestLease) BeginIncrementalExecution(
	context.Context,
	string,
) (func(), error) {
	l.calls.Add(1)
	return func() {}, nil
}

func (*incrementalComponentContextTestLease) BeforeIncrementalNativeCall(context.Context) error {
	return nil
}

type incrementalComponentContextTestDeriver struct{}

func (*incrementalComponentContextTestDeriver) DeriveResource(string, any, string, any) (any, error) {
	return nil, errors.New("unexpected DeriveResource call")
}

type incrementalComponentContextTestEventRecorder struct{}

func (*incrementalComponentContextTestEventRecorder) RecordEvent(
	string,
	string,
	string,
	string,
	string,
	string,
	string,
) error {
	return nil
}

type incrementalComponentContextTestStatusRecorder struct{}

func (*incrementalComponentContextTestStatusRecorder) RecordStatusPatch(
	string,
	string,
	string,
	string,
	string,
	string,
	map[string]map[string]any,
	string,
	int,
) error {
	return nil
}

type incrementalComponentContextParentKey struct{}
type incrementalComponentContextOpaqueKey struct{}

var incrementalComponentContextBenchmarkSink context.Context

func TestIncrementalComponentContextTableSealsOneAuthenticatedContext(t *testing.T) {
	parent, cancel := context.WithCancelCause(context.WithValue(
		t.Context(),
		incrementalComponentContextParentKey{},
		"parent",
	))
	lease := &incrementalComponentContextTestLease{}
	deriver := &incrementalComponentContextTestDeriver{}
	events := &incrementalComponentContextTestEventRecorder{}
	status := &incrementalComponentContextTestStatusRecorder{}
	item := map[string]any{"name": "route"}
	props := map[string]any{"enabled": true}
	subject := map[string]any{"mode": "reconcile"}
	resources := map[string]any{"routes": []any{item}}
	controller := map[string]ResourceStore{}

	table, err := NewIncrementalComponentContextTable(1)
	require.NoError(t, err)
	ctx, err := table.Prepare(
		0,
		parent,
		IncrementalComponentContextOptions{
			ExecutionLease:  lease,
			ResourceDeriver: deriver,
			EventRecorder:   events,
			StatusRecorder:  status,
			TransitionTime:  "2026-08-26T00:00:00Z",
		},
		CertifyIncrementalImmutableInputs(item),
		CertifyIncrementalImmutableInputs(props),
		CertifyIncrementalImmutableInputs(subject),
		CertifyIncrementalImmutableInputs(resources),
	)
	require.NoError(t, err)
	assert.Equal(t, "parent", ctx.Value(incrementalComponentContextParentKey{}))
	assert.Nil(t, ctx.Value(RenderContextContextKey))
	_, err = beginIncrementalExecution(ctx, "before seal")
	require.ErrorContains(t, err, "not sealed")

	values := map[string]any{
		"source":        "routes",
		"item":          item,
		"props":         props,
		"renderSubject": subject,
		"resources":     resources,
		"controller":    controller,
	}
	require.NoError(t, table.Seal(0, values, controller))
	assert.Equal(
		t,
		reflect.ValueOf(values).Pointer(),
		reflect.ValueOf(ctx.Value(RenderContextContextKey)).Pointer(),
	)
	assert.Same(t, lease, ctx.Value(incrementalExecutionLeaseContextKey{}))
	assert.Same(t, deriver, ctx.Value(incrementalResourceDeriverContextKey{}))
	assert.Same(t, events, ctx.Value(incrementalEventRecorderContextKey{}))
	assert.Same(t, status, ctx.Value(incrementalStatusPatchRecorderContextKey{}))
	assert.Equal(t, "2026-08-26T00:00:00Z", ctx.Value(incrementalTransitionTimeContextKey{}))
	bound, err := withBoundIncrementalImmutableInputs(ctx, values, nil)
	require.NoError(t, err)
	assert.Same(t, ctx, bound)
	release, err := beginIncrementalExecution(ctx, "after seal")
	require.NoError(t, err)
	release()
	assert.Equal(t, int64(1), lease.calls.Load())
	vectorSeal := &incrementalVectorContextSeal{index: 7}
	vectorSeal.seal = vectorSeal
	vectorContext, err := bindIncrementalVectorContext(ctx, vectorSeal)
	require.NoError(t, err)
	assert.Same(t, ctx, vectorContext)
	assert.Same(t, vectorSeal, ctx.Value(incrementalVectorContextKey{}))
	_, err = bindIncrementalVectorContext(ctx, vectorSeal)
	require.ErrorContains(t, err, "already bound")
	require.ErrorContains(t, table.Seal(0, values, controller), "is not prepared")

	values["item"] = map[string]any{"name": "poison"}
	_, err = withBoundIncrementalImmutableInputs(ctx, values, nil)
	require.ErrorContains(t, err, "does not match item")
	cancel(assert.AnError)
	assert.ErrorIs(t, context.Cause(ctx), assert.AnError)
}

func TestIncrementalVectorContextBindingFallsBackForForeignContext(t *testing.T) {
	seal := &incrementalVectorContextSeal{index: 3}
	seal.seal = seal
	parent := context.WithValue(t.Context(), incrementalComponentContextParentKey{}, "parent")
	bound, err := bindIncrementalVectorContext(parent, seal)
	require.NoError(t, err)
	assert.NotSame(t, parent, bound)
	assert.Same(t, seal, bound.Value(incrementalVectorContextKey{}))
	assert.Equal(t, "parent", bound.Value(incrementalComponentContextParentKey{}))

	copySeal := *seal
	_, err = bindIncrementalVectorContext(parent, &copySeal)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalComponentContextTableRejectsCopiesCrossIndexAndInvalidOptions(t *testing.T) {
	table, err := NewIncrementalComponentContextTable(2)
	require.NoError(t, err)
	copyTable := *table
	_, err = copyTable.Prepare(
		0,
		t.Context(),
		IncrementalComponentContextOptions{ExecutionLease: &incrementalComponentContextTestLease{}},
		CertifyIncrementalImmutableInputs(map[string]any{}),
	)
	require.ErrorContains(t, err, "invalid")

	_, err = table.Prepare(
		0,
		t.Context(),
		IncrementalComponentContextOptions{},
		CertifyIncrementalImmutableInputs(map[string]any{}),
	)
	require.ErrorContains(t, err, "execution lease is nil")
	_, err = table.Prepare(
		1,
		t.Context(),
		IncrementalComponentContextOptions{ExecutionLease: &incrementalComponentContextTestLease{}},
		nil,
	)
	require.ErrorContains(t, err, "certificate 0 is nil")
	_, err = table.Prepare(
		2,
		t.Context(),
		IncrementalComponentContextOptions{ExecutionLease: &incrementalComponentContextTestLease{}},
		CertifyIncrementalImmutableInputs(map[string]any{}),
	)
	require.ErrorContains(t, err, "is invalid")
}

func TestIncrementalComponentContextTableConcurrentReadAfterSeal(t *testing.T) {
	item := map[string]any{"name": "route"}
	props := map[string]any{}
	subject := map[string]any{"mode": "reconcile"}
	resources := map[string]any{}
	controller := map[string]ResourceStore{}
	lease := &incrementalComponentContextTestLease{}
	table, err := NewIncrementalComponentContextTable(1)
	require.NoError(t, err)
	ctx, err := table.Prepare(
		0,
		t.Context(),
		IncrementalComponentContextOptions{ExecutionLease: lease},
		CertifyIncrementalImmutableInputs(item),
		CertifyIncrementalImmutableInputs(props),
		CertifyIncrementalImmutableInputs(subject),
		CertifyIncrementalImmutableInputs(resources),
	)
	require.NoError(t, err)
	values := map[string]any{
		"source": "routes", "item": item, "props": props, "renderSubject": subject,
		"resources": resources, "controller": controller,
	}
	require.NoError(t, table.Seal(0, values, controller))

	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 1000 {
				if reflect.ValueOf(ctx.Value(RenderContextContextKey)).Pointer() != reflect.ValueOf(values).Pointer() {
					t.Error("context returned another render binding")
					return
				}
				release, err := beginIncrementalExecution(ctx, "race")
				if err != nil {
					t.Error(err)
					return
				}
				release()
			}
		}()
	}
	workers.Wait()
	assert.Equal(t, int64(32000), lease.calls.Load())
}

func TestIncrementalComponentContextTableSealsCompactValuesWithoutMap(t *testing.T) {
	item := map[string]any{"name": "route"}
	props := map[string]any{"enabled": true}
	subject := map[string]any{"mode": "reconcile"}
	resources := map[string]any{"routes": []any{item}}
	controller := map[string]ResourceStore{}
	shared := NewSharedContributionContext(&noOpSharedContributionRecorder{})
	token := &struct{}{}
	values := IncrementalComponentContextValues{
		Source: "routes", Item: item, Props: props, RenderSubject: subject,
		RenderMode: "reconcile", Resources: resources, Controller: controller, Shared: shared,
	}
	table, err := NewIncrementalComponentContextTable(1)
	require.NoError(t, err)
	ctx, err := table.Prepare(
		0,
		t.Context(),
		IncrementalComponentContextOptions{
			ExecutionLease:  &incrementalComponentContextTestLease{},
			ContextValueKey: incrementalComponentContextOpaqueKey{},
			ContextValue:    token,
		},
		CertifyIncrementalImmutableInputs(item),
		CertifyIncrementalImmutableInputs(props),
		CertifyIncrementalImmutableInputs(subject),
		CertifyIncrementalImmutableInputs(resources),
	)
	require.NoError(t, err)
	require.NoError(t, table.SealValues(0, values))
	compact := ctx.(*incrementalComponentExecutionContext)
	assert.Same(t, token, ctx.Value(incrementalComponentContextOpaqueKey{}))
	assert.Nil(t, compact.templateContext)
	assert.True(t, compact.storage.contains(reflect.ValueOf(shared)))
	assert.True(t, compact.storage.contains(reflect.ValueOf(controller)))
	assert.Equal(
		t,
		reflect.ValueOf(resources).Pointer(),
		reflect.ValueOf(mustRenderContextValue(t, ctx, "resources")).Pointer(),
	)
	assert.Nil(t, compact.templateContext)

	columns := map[string]reflect.Value{
		"source":        reflect.ValueOf([]string{values.Source}),
		"item":          reflect.ValueOf([]map[string]any{values.Item}),
		"props":         reflect.ValueOf([]map[string]any{values.Props}),
		"renderSubject": reflect.ValueOf([]map[string]any{values.RenderSubject}),
		"renderMode":    reflect.ValueOf([]string{values.RenderMode}),
		"resources":     reflect.ValueOf([]map[string]any{resources}),
		"controller":    reflect.ValueOf([]map[string]ResourceStore{values.Controller}),
		"shared":        reflect.ValueOf([]SharedContributionContext{values.Shared}),
		"http":          reflect.ValueOf([]HTTPFetcher{nil}),
		"planRegistry":  reflect.ValueOf([]IncrementalBackendPlanRegistrar{nil}),
	}
	require.NoError(t, validateIncrementalVectorItemContext(ctx, columns, 0))
	assert.Nil(t, compact.templateContext)

	compatibility := ctx.Value(RenderContextContextKey).(map[string]any)
	assert.Equal(t, reflect.ValueOf(item).Pointer(), reflect.ValueOf(compatibility["item"]).Pointer())
	assert.Same(t, &compact.binding, compatibility[incrementalImmutableBindingTemplateContextKey])
	compatibility["item"] = map[string]any{"name": "poison"}
	_, err = withBoundIncrementalImmutableInputs(ctx, compatibility, nil)
	require.ErrorContains(t, err, "does not match item")
}

func mustRenderContextValue(t *testing.T, ctx context.Context, name string) any {
	t.Helper()
	value, found := lookupRenderContextValue(ctx, name)
	require.True(t, found)
	return value
}

const incrementalComponentContextBenchmarkCount = 128

type incrementalComponentContextBenchmarkFixture struct {
	item         map[string]any
	props        map[string]any
	subject      map[string]any
	controller   map[string]ResourceStore
	certificates [3]*IncrementalImmutableCertificate
}

type incrementalComponentContextBenchmarkInputs struct {
	fixtures            []incrementalComponentContextBenchmarkFixture
	resources           map[string]any
	resourceCertificate *IncrementalImmutableCertificate
	lease               *incrementalComponentContextTestLease
	shared              SharedContributionContext
}

func newIncrementalComponentContextBenchmarkInputs() *incrementalComponentContextBenchmarkInputs {
	resources := map[string]any{"routes": []any{}}
	inputs := &incrementalComponentContextBenchmarkInputs{
		fixtures: make(
			[]incrementalComponentContextBenchmarkFixture,
			incrementalComponentContextBenchmarkCount,
		),
		resources:           resources,
		resourceCertificate: CertifyIncrementalImmutableInputs(resources),
		lease:               &incrementalComponentContextTestLease{},
		shared:              NewSharedContributionContext(&noOpSharedContributionRecorder{}),
	}
	for index := range inputs.fixtures {
		item := map[string]any{"index": int64(index)}
		props := map[string]any{"enabled": true}
		subject := map[string]any{"mode": "reconcile"}
		inputs.fixtures[index] = incrementalComponentContextBenchmarkFixture{
			item: item, props: props, subject: subject, controller: map[string]ResourceStore{},
			certificates: [3]*IncrementalImmutableCertificate{
				CertifyIncrementalImmutableInputs(item),
				CertifyIncrementalImmutableInputs(props),
				CertifyIncrementalImmutableInputs(subject),
			},
		}
	}
	return inputs
}

func (in *incrementalComponentContextBenchmarkInputs) values(
	value *incrementalComponentContextBenchmarkFixture,
) map[string]any {
	return map[string]any{
		"source": "routes", "item": value.item, "props": value.props,
		"renderSubject": value.subject, "resources": in.resources, "controller": value.controller,
	}
}

func BenchmarkIncrementalComponentContextPreparation(b *testing.B) {
	inputs := newIncrementalComponentContextBenchmarkInputs()
	b.Run("context-value-chain", inputs.benchmarkContextValueChain)
	b.Run("sealed-table", inputs.benchmarkSealedTable)
	b.Run("sealed-table-values", inputs.benchmarkSealedTableValues)
}

func (in *incrementalComponentContextBenchmarkInputs) benchmarkContextValueChain(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		for index := range in.fixtures {
			value := &in.fixtures[index]
			ctx := WithIncrementalImmutableCertificates(
				WithImmutableResourceInputs(context.WithValue(
					b.Context(), incrementalComponentContextParentKey{}, index,
				)),
				value.certificates[0],
				value.certificates[1],
				value.certificates[2],
				in.resourceCertificate,
			)
			ctx = WithIncrementalExecutionLease(ctx, in.lease)
			ctx = WithIncrementalImmutableCapabilityInputs(ctx, value.controller)
			templateContext := in.values(value)
			if err := BindIncrementalImmutableInputs(templateContext, ctx); err != nil {
				b.Fatal(err)
			}
			incrementalComponentContextBenchmarkSink = context.WithValue(
				ctx, RenderContextContextKey, templateContext,
			)
		}
	}
}

func (in *incrementalComponentContextBenchmarkInputs) benchmarkSealedTable(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		table, err := NewIncrementalComponentContextTable(incrementalComponentContextBenchmarkCount)
		if err != nil {
			b.Fatal(err)
		}
		for index := range in.fixtures {
			value := &in.fixtures[index]
			ctx, err := table.Prepare(
				index,
				context.WithValue(b.Context(), incrementalComponentContextParentKey{}, index),
				IncrementalComponentContextOptions{ExecutionLease: in.lease},
				value.certificates[0],
				value.certificates[1],
				value.certificates[2],
				in.resourceCertificate,
			)
			if err != nil {
				b.Fatal(err)
			}
			if err := table.Seal(index, in.values(value), value.controller); err != nil {
				b.Fatal(err)
			}
			incrementalComponentContextBenchmarkSink = ctx
		}
	}
}

func (in *incrementalComponentContextBenchmarkInputs) benchmarkSealedTableValues(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		table, err := NewIncrementalComponentContextTable(incrementalComponentContextBenchmarkCount)
		if err != nil {
			b.Fatal(err)
		}
		for index := range in.fixtures {
			value := &in.fixtures[index]
			ctx, err := table.Prepare(
				index,
				b.Context(),
				IncrementalComponentContextOptions{
					ExecutionLease:  in.lease,
					ContextValueKey: incrementalComponentContextOpaqueKey{},
					ContextValue:    value,
				},
				value.certificates[0],
				value.certificates[1],
				value.certificates[2],
				in.resourceCertificate,
			)
			if err != nil {
				b.Fatal(err)
			}
			if err := table.SealValues(index, IncrementalComponentContextValues{
				Source: "routes", Item: value.item, Props: value.props,
				RenderSubject: value.subject, RenderMode: "reconcile",
				Resources: in.resources, Controller: value.controller, Shared: in.shared,
			}); err != nil {
				b.Fatal(err)
			}
			incrementalComponentContextBenchmarkSink = ctx
		}
	}
}

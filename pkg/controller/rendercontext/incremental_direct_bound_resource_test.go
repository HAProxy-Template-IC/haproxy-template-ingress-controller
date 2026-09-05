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

package rendercontext

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

var benchmarkDirectBoundInvocationError error

type directBoundResourceFrameView struct {
	*boundResourceFrameViewCore
	expectedCtx   context.Context
	expectedLease templating.IncrementalResourceInvocationLease
	sequence      atomic.Uint64
	slots         [64]atomic.Uint64
	begins        atomic.Int64
	ends          atomic.Int64
	lists         atomic.Int64
	gets          atomic.Int64
	endErr        error
	mu            sync.Mutex
	lastKeys      []string
}

type directBoundMaterializationFrameView struct {
	*directBoundResourceFrameView
	cache            *ResourceItemCache
	projection       *resourceMaterializationFrameProjection
	items            []any
	certificate      *templating.IncrementalImmutableCertificate
	materializations atomic.Int64
}

func (v *directBoundMaterializationFrameView) ResourceItemCache() *ResourceItemCache {
	return v.cache
}

func (v *directBoundMaterializationFrameView) MaterializeDirectBoundResource(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	request *DirectBoundResourceMaterializationRequest,
	_ stores.Store,
	keys []string,
) (reflect.Value, error) {
	if err := v.validate(ctx, invocation); err != nil {
		return reflect.Value{}, err
	}
	v.materializations.Add(1)
	return request.Materialize(ctx, v.projection, keys)
}

func (v *directBoundResourceFrameView) BeginDirectBoundStoreInvocation(
	ctx context.Context,
	lease templating.IncrementalResourceInvocationLease,
) (DirectBoundStoreInvocation, error) {
	if ctx == nil || ctx != v.expectedCtx || lease != v.expectedLease {
		return DirectBoundStoreInvocation{}, errors.New("direct resource invocation has foreign provenance")
	}
	generation := v.sequence.Add(1)
	if generation == 0 {
		return DirectBoundStoreInvocation{}, errors.New("direct resource invocation generation wrapped")
	}
	for slot := range v.slots {
		if v.slots[slot].CompareAndSwap(0, generation) {
			v.begins.Add(1)
			return NewDirectBoundStoreInvocation(lease, slot, generation)
		}
	}
	return DirectBoundStoreInvocation{}, errors.New("direct resource invocation slots are exhausted")
}

func (v *directBoundResourceFrameView) EndDirectBoundStoreInvocation(
	invocation DirectBoundStoreInvocation,
) error {
	if invocation.Lease() != v.expectedLease || invocation.Generation() == 0 ||
		invocation.Slot() < 0 || invocation.Slot() >= len(v.slots) ||
		!v.slots[invocation.Slot()].CompareAndSwap(invocation.Generation(), 0) {
		return errors.New("direct resource invocation is stale")
	}
	v.ends.Add(1)
	return v.endErr
}

func (v *directBoundResourceFrameView) ListDirectBound(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	_ string,
	_ stores.Store,
) ([]any, error) {
	if err := v.validate(ctx, invocation); err != nil {
		return nil, err
	}
	v.lists.Add(1)
	return v.result()
}

func (v *directBoundResourceFrameView) GetDirectBound(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	_ string,
	_ stores.Store,
	keys ...string,
) ([]any, error) {
	if err := v.validate(ctx, invocation); err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.lastKeys = append(v.lastKeys[:0], keys...)
	v.mu.Unlock()
	v.gets.Add(1)
	return v.result()
}

func (v *directBoundResourceFrameView) validate(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
) error {
	if ctx == nil || ctx != v.expectedCtx || invocation.Lease() != v.expectedLease ||
		invocation.Generation() == 0 || invocation.Slot() < 0 ||
		invocation.Slot() >= len(v.slots) ||
		v.slots[invocation.Slot()].Load() != invocation.Generation() {
		return errors.New("direct resource invocation is unauthenticated")
	}
	return nil
}

func (v *directBoundResourceFrameView) result() ([]any, error) {
	v.reads.Add(1)
	if v.readErr != nil {
		return nil, v.readErr
	}
	return []any{v.item}, nil
}

func TestDirectBoundResourceFrameCoversListFetchAndGetSingle(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	lease := &boundResourceFrameLease{expectedCtx: ctx}
	core := &boundResourceFrameViewCore{item: map[string]any{"name": "route"}}
	view := &directBoundResourceFrameView{
		boundResourceFrameViewCore: core,
		expectedCtx:                ctx,
		expectedLease:              lease,
	}
	resources := bindBoundResourceFrameTestResources(t, ctx, view, lease)
	env := &boundResourceFrameEnv{ctx: ctx}

	listed := callDirectBoundResourceFrame(resources, env, "List")
	fetched := callDirectBoundResourceFrame(resources, env, "Fetch", "default", "route")
	single := callDirectBoundResourceFrame(resources, env, "GetSingle", "default", "route")

	require.NoError(t, env.stopError())
	assert.Len(t, listed.([]any), 1)
	assert.Len(t, fetched.([]any), 1)
	assert.NotNil(t, single)
	assert.Equal(t, int64(3), view.begins.Load())
	assert.Equal(t, int64(3), view.ends.Load())
	assert.Equal(t, int64(1), view.lists.Load())
	assert.Equal(t, int64(2), view.gets.Load())
	assert.Equal(t, int64(3), view.reads.Load())
	assert.Zero(t, core.legacyBegins.Load())
	assert.Zero(t, core.boundBegins.Load())
	assert.Zero(t, core.releases.Load())
	assert.Zero(t, lease.validations.Load())
	view.mu.Lock()
	assert.Equal(t, []string{"default", "route"}, view.lastKeys)
	view.mu.Unlock()
}

func TestDirectBoundResourceFrameUsesSharedMaterializationFrames(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	lease := &boundResourceFrameLease{expectedCtx: ctx}
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	core := &boundResourceFrameViewCore{item: items[0]}
	direct := &directBoundResourceFrameView{
		boundResourceFrameViewCore: core,
		expectedCtx:                ctx,
		expectedLease:              lease,
	}
	view := &directBoundMaterializationFrameView{
		directBoundResourceFrameView: direct,
		cache:                        NewResourceItemCache(),
		projection:                   newResourceMaterializationFrameProjection(items, certificate),
		items:                        items,
		certificate:                  certificate,
	}
	resources := bindBoundResourceFrameTestResources(t, ctx, view, lease)
	env := &boundResourceFrameEnv{ctx: ctx}

	for range 2 {
		listed := callDirectBoundResourceFrame(resources, env, "List")
		fetched := callDirectBoundResourceFrame(resources, env, "Fetch", "default", "route")
		single := callDirectBoundResourceFrame(resources, env, "GetSingle", "default", "route")
		assert.Len(t, listed.([]any), 1)
		assert.Len(t, fetched.([]any), 1)
		assert.NotNil(t, single)
	}

	require.NoError(t, env.stopError())
	assert.Equal(t, int64(6), view.begins.Load())
	assert.Equal(t, int64(6), view.ends.Load())
	assert.Equal(t, int64(6), view.materializations.Load())
	assert.Zero(t, view.lists.Load())
	assert.Zero(t, view.gets.Load())
	assert.Zero(t, view.reads.Load())
	assert.Equal(t, 3, directResourceMaterializationFrameCount(view.cache))
}

func TestDirectBoundResourceFrameFailureReleasesBeforeStopping(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	lease := &boundResourceFrameLease{expectedCtx: ctx}
	core := &boundResourceFrameViewCore{
		item: map[string]any{"name": "route"}, readErr: errors.New("direct read failed"),
	}
	view := &directBoundResourceFrameView{
		boundResourceFrameViewCore: core,
		expectedCtx:                ctx,
		expectedLease:              lease,
	}
	resources := bindBoundResourceFrameTestResources(t, ctx, view, lease)
	env := &boundResourceFrameEnv{ctx: ctx}

	result := callDirectBoundResourceFrame(resources, env, "GetSingle", "default", "route")

	assert.Nil(t, result)
	require.ErrorContains(t, env.stopError(), "direct read failed")
	assert.Equal(t, int64(1), view.begins.Load())
	assert.Equal(t, int64(1), view.ends.Load())
	for slot := range view.slots {
		assert.Zero(t, view.slots[slot].Load())
	}
}

func TestDirectBoundResourceInvocationPanicReleases(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	lease := &boundResourceFrameLease{expectedCtx: ctx}
	view := &directBoundResourceFrameView{
		boundResourceFrameViewCore: &boundResourceFrameViewCore{},
		expectedCtx:                ctx,
		expectedLease:              lease,
	}
	adapter := &perResourceStoreAdapter{wrapper: &StoreWrapper{SnapshotView: view}}

	assert.PanicsWithValue(t, "direct panic", func() {
		_, _ = adapter.invokeBoundResource(
			ctx,
			lease,
			resourceInvocationKeys{},
			func(context.Context, []any) (reflect.Value, error) {
				return reflect.Value{}, errors.New("bound fallback reached")
			},
			func(
				context.Context,
				DirectBoundStoreInvocation,
				resourceInvocationKeys,
			) (reflect.Value, error) {
				panic("direct panic")
			},
		)
	})
	assert.Equal(t, int64(1), view.begins.Load())
	assert.Equal(t, int64(1), view.ends.Load())
	for slot := range view.slots {
		assert.Zero(t, view.slots[slot].Load())
	}
}

func TestDirectBoundResourceInvocationEndFailureZerosResult(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	lease := &boundResourceFrameLease{expectedCtx: ctx}
	view := &directBoundResourceFrameView{
		boundResourceFrameViewCore: &boundResourceFrameViewCore{},
		expectedCtx:                ctx,
		expectedLease:              lease,
		endErr:                     errors.New("direct end failed"),
	}
	adapter := &perResourceStoreAdapter{wrapper: &StoreWrapper{SnapshotView: view}}

	result, err := adapter.invokeBoundResource(
		ctx,
		lease,
		resourceInvocationKeys{},
		func(context.Context, []any) (reflect.Value, error) {
			return reflect.Value{}, errors.New("bound fallback reached")
		},
		func(
			context.Context,
			DirectBoundStoreInvocation,
			resourceInvocationKeys,
		) (reflect.Value, error) {
			return reflect.ValueOf("unsafe result"), nil
		},
	)
	require.ErrorContains(t, err, "direct end failed")
	assert.False(t, result.IsValid())
	assert.Equal(t, int64(1), view.ends.Load())
	for slot := range view.slots {
		assert.Zero(t, view.slots[slot].Load())
	}
}

func TestDirectBoundResourceFrameMatchesBoundFallback(t *testing.T) {
	ctx := templating.WithImmutableResourceInputs(t.Context())
	item := map[string]any{"name": "route"}
	directLease := &boundResourceFrameLease{expectedCtx: ctx}
	directCore := &boundResourceFrameViewCore{item: item}
	directView := &directBoundResourceFrameView{
		boundResourceFrameViewCore: directCore,
		expectedCtx:                ctx,
		expectedLease:              directLease,
	}
	directResources := bindBoundResourceFrameTestResources(t, ctx, directView, directLease)
	directEnv := &boundResourceFrameEnv{ctx: ctx}

	boundLease := &boundResourceFrameLease{expectedCtx: ctx}
	boundCore := &boundResourceFrameViewCore{item: item}
	boundView := &boundResourceFrameView{boundResourceFrameViewCore: boundCore, expectedLease: boundLease}
	boundResources := bindBoundResourceFrameTestResources(t, ctx, boundView, boundLease)
	boundEnv := &boundResourceFrameEnv{ctx: ctx}

	directResult := callDirectBoundResourceFrame(
		directResources, directEnv, "GetSingle", "default", "route",
	)
	boundResult := callDirectBoundResourceFrame(
		boundResources, boundEnv, "GetSingle", "default", "route",
	)

	require.NoError(t, directEnv.stopError())
	require.NoError(t, boundEnv.stopError())
	assert.Equal(t, boundResult, directResult)
	assert.Equal(t, int64(1), directView.begins.Load())
	assert.Zero(t, directCore.boundBegins.Load())
	assert.Equal(t, int64(1), boundCore.boundBegins.Load())
	assert.Zero(t, boundCore.legacyBegins.Load())
}

func BenchmarkDirectBoundResourceInvocation(b *testing.B) {
	ctx := templating.WithImmutableResourceInputs(b.Context())
	result := reflect.ValueOf("result")
	legacyInvoke := func(context.Context, []any) (reflect.Value, error) {
		return result, benchmarkDirectBoundInvocationError
	}
	directInvoke := func(
		context.Context,
		DirectBoundStoreInvocation,
		resourceInvocationKeys,
	) (reflect.Value, error) {
		return result, benchmarkDirectBoundInvocationError
	}
	for _, test := range []struct {
		name string
		view func(templating.IncrementalResourceInvocationLease) StoreSnapshotView
	}{
		{
			name: "bound_context",
			view: func(lease templating.IncrementalResourceInvocationLease) StoreSnapshotView {
				return &boundResourceFrameView{
					boundResourceFrameViewCore: &boundResourceFrameViewCore{}, expectedLease: lease,
				}
			},
		},
		{
			name: "direct",
			view: func(lease templating.IncrementalResourceInvocationLease) StoreSnapshotView {
				return &directBoundResourceFrameView{
					boundResourceFrameViewCore: &boundResourceFrameViewCore{},
					expectedCtx:                ctx,
					expectedLease:              lease,
				}
			},
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			lease := &boundResourceFrameLease{expectedCtx: ctx}
			adapter := &perResourceStoreAdapter{wrapper: &StoreWrapper{
				readContext:  ctx,
				SnapshotView: test.view(lease),
			}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				value, err := adapter.invokeBoundResource(
					ctx, lease, resourceInvocationKeys{}, legacyInvoke, directInvoke,
				)
				if err != nil || value.String() != "result" {
					b.Fatalf("invokeBoundResource() = %v, %v", value, err)
				}
			}
		})
	}
}

func callDirectBoundResourceFrame(
	resources any,
	env native.Env,
	method string,
	keys ...any,
) any {
	resource := reflect.ValueOf(resources).Elem().FieldByName("Routes").Elem()
	callable := resource.FieldByName(method)
	args := []reflect.Value{reflect.ValueOf(env).Convert(reflect.TypeFor[native.Env]())}
	if callable.Type().IsVariadic() {
		args = append(args, reflect.ValueOf(keys))
		return callable.CallSlice(args)[0].Interface()
	}
	return callable.Call(args)[0].Interface()
}

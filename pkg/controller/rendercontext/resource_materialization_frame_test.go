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
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var benchmarkDirectResourceFrame reflect.Value

type resourceMaterializationFrameProjection struct {
	seal        *resourceMaterializationFrameProjection
	resource    string
	items       []any
	certificate *templating.IncrementalImmutableCertificate
	authentics  atomic.Int64
	projects    atomic.Int64
	invalid     atomic.Bool
	started     chan struct{}
	release     <-chan struct{}
	panicValue  any
}

const materializationFrameResource = "routes"

func newResourceMaterializationFrameProjection(
	items []any,
	certificate *templating.IncrementalImmutableCertificate,
) *resourceMaterializationFrameProjection {
	projection := &resourceMaterializationFrameProjection{
		resource: materializationFrameResource, items: items, certificate: certificate,
	}
	projection.seal = projection
	return projection
}

func (p *resourceMaterializationFrameProjection) AuthenticateDirectBoundResourceProjection(
	resource string,
) error {
	p.authentics.Add(1)
	if p == nil || p.seal != p || p.invalid.Load() || resource != p.resource ||
		p.certificate == nil || !p.certificate.Guards(p.items) {
		return errors.New("projection is stale")
	}
	return nil
}

func (p *resourceMaterializationFrameProjection) ProjectDirectBoundResourceProjection(
	ctx context.Context,
	resource string,
	elementType reflect.Type,
) ([]reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.AuthenticateDirectBoundResourceProjection(resource); err != nil {
		return nil, err
	}
	if p.projects.Add(1) == 1 && p.started != nil {
		close(p.started)
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	values := make([]reflect.Value, len(p.items))
	for index, item := range p.items {
		if elementType == nil {
			if item != nil {
				values[index] = reflect.ValueOf(item)
			}
			continue
		}
		value, err := wrapImmutableItemToPointer(item, elementType)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}

func TestDirectResourceMaterializationFrameReusesExactDeclaration(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	var firstBuilds atomic.Int64
	first := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			firstBuilds.Add(1)
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	var secondBuilds atomic.Int64
	second := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			secondBuilds.Add(1)
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)

	left, err := first.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	require.NoError(t, err)
	right, err := second.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	require.NoError(t, err)

	assert.Zero(t, firstBuilds.Load())
	assert.Zero(t, secondBuilds.Load())
	assert.Equal(t, int64(1), projection.projects.Load())
	assert.Equal(t, left.Pointer(), right.Pointer())
	assert.Equal(t, int64(4), projection.authentics.Load())
}

func TestDirectResourceMaterializationFrameSeparatesProjectionAndDeclaration(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	leftProjection := newResourceMaterializationFrameProjection(items, certificate)
	rightProjection := newResourceMaterializationFrameProjection(items, certificate)
	var builds atomic.Int64
	build := func(_ context.Context, values []any) reflect.Value {
		builds.Add(1)
		return reflect.ValueOf(append([]any(nil), values...))
	}
	fetch := resourceMaterializationFrameRequest(cache, DirectBoundResourceFetch, build)
	list := resourceMaterializationFrameRequest(cache, DirectBoundResourceList, build)

	_, err := fetch.Materialize(templating.WithImmutableResourceInputs(t.Context()), leftProjection, []string{"route"})
	require.NoError(t, err)
	_, err = list.Materialize(templating.WithImmutableResourceInputs(t.Context()), leftProjection, nil)
	require.NoError(t, err)
	_, err = fetch.Materialize(templating.WithImmutableResourceInputs(t.Context()), rightProjection, []string{"route"})
	require.NoError(t, err)

	assert.Zero(t, builds.Load())
	assert.Equal(t, int64(2), leftProjection.projects.Load())
	assert.Equal(t, int64(1), rightProjection.projects.Load())
}

func TestDirectResourceMaterializationFramePublishesOneConcurrentBuild(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	started := make(chan struct{})
	release := make(chan struct{})
	projection.started = started
	projection.release = release
	var builds atomic.Int64
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			builds.Add(1)
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	const workerCount = 64
	values := make(chan uintptr, workerCount)
	errs := make(chan error, workerCount)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		value, err := request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
		if err != nil {
			errs <- err
			return
		}
		values <- value.Pointer()
	}()
	<-started
	for range workerCount - 1 {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
			if err != nil {
				errs <- err
				return
			}
			values <- value.Pointer()
		}()
	}
	close(release)
	group.Wait()
	close(values)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var expected uintptr
	for value := range values {
		if expected == 0 {
			expected = value
		}
		assert.Equal(t, expected, value)
	}
	assert.Zero(t, builds.Load())
	assert.Equal(t, int64(1), projection.projects.Load())
}

func TestDirectResourceMaterializationFrameRejectsPoison(t *testing.T) {
	tests := map[string]func(*directBoundResourceFrameSlot){
		"slot seal": func(slot *directBoundResourceFrameSlot) {
			slot.seal = nil
		},
		"slot key": func(slot *directBoundResourceFrameSlot) {
			slot.key.declaration.resourceType = "other"
		},
		"frame seal": func(slot *directBoundResourceFrameSlot) {
			slot.frame.seal = nil
		},
		"frame proof": func(slot *directBoundResourceFrameSlot) {
			slot.frame.proof.seal = nil
		},
		"frame key": func(slot *directBoundResourceFrameSlot) {
			slot.frame.key.declaration.operation = DirectBoundResourceList
		},
		"frame value": func(slot *directBoundResourceFrameSlot) {
			slot.frame.value = reflect.ValueOf([]any{map[string]any{"name": "poison"}})
		},
		"frame certificate": func(slot *directBoundResourceFrameSlot) {
			slot.frame.certificate = templating.CertifyIncrementalImmutableInputs([]any{})
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			cache := NewResourceItemCache()
			items := []any{map[string]any{"name": "route"}}
			certificate := templating.CertifyIncrementalImmutableInputs(items)
			projection := newResourceMaterializationFrameProjection(items, certificate)
			var builds atomic.Int64
			request := resourceMaterializationFrameRequest(
				cache,
				DirectBoundResourceFetch,
				func(_ context.Context, values []any) reflect.Value {
					builds.Add(1)
					return reflect.ValueOf(append([]any(nil), values...))
				},
			)
			_, err := request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
			require.NoError(t, err)
			key := directBoundResourceFrameKey{projection: projection, declaration: request.declaration}
			raw, found := cache.frames.Load(key)
			require.True(t, found)
			slot, ok := raw.(*directBoundResourceFrameSlot)
			require.True(t, ok)
			poison(slot)

			_, err = request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
			require.ErrorContains(t, err, "invalid provenance")
			assert.Zero(t, builds.Load())
		})
	}
}

func TestDirectResourceMaterializationFrameRejectsStaleProjectionWithoutRebuild(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	var builds atomic.Int64
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			builds.Add(1)
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	_, err := request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	require.NoError(t, err)
	projection.invalid.Store(true)

	_, err = request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	require.ErrorContains(t, err, "projection is stale")
	assert.Zero(t, builds.Load())
}

func TestDirectResourceMaterializationFrameBuildPanicDoesNotPublish(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	projection.panicValue = "frame panic"
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)

	assert.PanicsWithValue(t, "frame panic", func() {
		_, _ = request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	})
	key := directBoundResourceFrameKey{projection: projection, declaration: request.declaration}
	_, found := cache.frames.Load(key)
	assert.False(t, found)
}

func TestDirectResourceMaterializationRequestRejectsPoison(t *testing.T) {
	request := resourceMaterializationFrameRequest(
		NewResourceItemCache(),
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	request.declaration.resourceType = "other"
	_, err := request.Describe()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestDirectResourceMaterializationDeclarationAuthenticatesExactRequest(t *testing.T) {
	request := resourceMaterializationFrameRequest(
		NewResourceItemCache(),
		DirectBoundResourceGetSingle,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	declaration, err := request.Describe()
	require.NoError(t, err)
	require.NoError(t, declaration.Authenticate())

	tests := map[string]func(*DirectBoundResourceMaterialization){
		"resource": func(candidate *DirectBoundResourceMaterialization) {
			candidate.ResourceType = "other"
		},
		"operation": func(candidate *DirectBoundResourceMaterialization) {
			candidate.Operation = DirectBoundResourceFetch
		},
		"element type": func(candidate *DirectBoundResourceMaterialization) {
			candidate.ElementType = reflect.TypeFor[string]()
		},
		"return type": func(candidate *DirectBoundResourceMaterialization) {
			candidate.ReturnType = reflect.TypeFor[string]()
		},
		"request copy": func(candidate *DirectBoundResourceMaterialization) {
			copied := *candidate.request
			candidate.request = &copied
		},
		"proof copy": func(candidate *DirectBoundResourceMaterialization) {
			copied := *candidate.proof
			candidate.proof = &copied
		},
		"foreign proof": func(candidate *DirectBoundResourceMaterialization) {
			foreign := resourceMaterializationFrameRequest(
				NewResourceItemCache(),
				DirectBoundResourceGetSingle,
				func(_ context.Context, values []any) reflect.Value {
					return reflect.ValueOf(append([]any(nil), values...))
				},
			)
			candidate.proof = foreign.proof
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := declaration
			poison(&candidate)
			require.ErrorContains(t, candidate.Authenticate(), "invalid provenance")
		})
	}
}

func TestDirectResourceMaterializationDeclarationRejectsStaleRequest(t *testing.T) {
	cache := NewResourceItemCache()
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceGetSingle,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	declaration, err := request.Describe()
	require.NoError(t, err)
	cache.Revoke()

	require.ErrorContains(t, declaration.Authenticate(), "invalid provenance")
}

func TestDirectResourceMaterializationFrameReleaseDropsState(t *testing.T) {
	cache := NewResourceItemCache()
	_, err := resourceItemCacheTestWrapper(cache, "routes").wrap(
		templating.WithImmutableResourceInputs(t.Context()), resourceItemCacheTestItem("route"),
	)
	require.NoError(t, err)
	require.Len(t, cache.entries, 1)
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	_, err = request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
	require.NoError(t, err)
	require.Equal(t, 1, directResourceMaterializationFrameCount(cache))

	cache.Revoke()
	cache.Revoke()

	assert.Zero(t, directResourceMaterializationFrameCount(cache))
	assert.Nil(t, cache.entries)
	_, err = request.Describe()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestDirectResourceMaterializationFrameReleasePoisonsConcurrentBuild(t *testing.T) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	started := make(chan struct{})
	release := make(chan struct{})
	projection.started = started
	projection.release = release
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	result := make(chan error, 1)
	go func() {
		_, err := request.Materialize(templating.WithImmutableResourceInputs(t.Context()), projection, []string{"route"})
		result <- err
	}()
	<-started
	cache.Revoke()
	close(release)

	err := <-result
	require.ErrorContains(t, err, "invalid provenance")
	assert.Zero(t, directResourceMaterializationFrameCount(cache))
}

func BenchmarkDirectResourceMaterializationFrameHit(b *testing.B) {
	cache := NewResourceItemCache()
	items := []any{map[string]any{"name": "route"}}
	certificate := templating.CertifyIncrementalImmutableInputs(items)
	projection := newResourceMaterializationFrameProjection(items, certificate)
	request := resourceMaterializationFrameRequest(
		cache,
		DirectBoundResourceFetch,
		func(_ context.Context, values []any) reflect.Value {
			return reflect.ValueOf(append([]any(nil), values...))
		},
	)
	value, err := request.Materialize(templating.WithImmutableResourceInputs(b.Context()), projection, []string{"route"})
	if err != nil {
		b.Fatal(err)
	}
	benchmarkDirectResourceFrame = value
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkDirectResourceFrame, err = request.Materialize(
			templating.WithImmutableResourceInputs(b.Context()), projection, []string{"route"},
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func resourceMaterializationFrameRequest(
	cache *ResourceItemCache,
	operation DirectBoundResourceOperation,
	build func(context.Context, []any) reflect.Value,
) *DirectBoundResourceMaterializationRequest {
	return newDirectBoundResourceMaterializationRequest(
		cache,
		materializationFrameResource,
		nil,
		reflect.TypeFor[[]any](),
		operation,
		build,
		nil,
		nil,
	)
}

func directResourceMaterializationFrameCount(cache *ResourceItemCache) int {
	count := 0
	cache.frames.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

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

package templating

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundIncrementalImmutableInputsRejectPoison(t *testing.T) {
	contextValues, componentCtx := boundIncrementalComponentTestContext(t, "original")
	require.NoError(t, BindIncrementalImmutableInputs(contextValues, componentCtx))
	binding := contextValues[incrementalImmutableBindingTemplateContextKey].(*incrementalImmutableBinding)

	tests := []struct {
		name   string
		ctx    func() context.Context
		values func() map[string]any
	}{
		{
			name: "forged binding",
			ctx:  func() context.Context { return componentCtx },
			values: func() map[string]any {
				values := cloneAnyMap(contextValues)
				values[incrementalImmutableBindingTemplateContextKey] = "forged"
				return values
			},
		},
		{
			name: "copied binding",
			ctx:  func() context.Context { return componentCtx },
			values: func() map[string]any {
				values := cloneAnyMap(contextValues)
				copied := *binding
				values[incrementalImmutableBindingTemplateContextKey] = &copied
				return values
			},
		},
		{
			name: "copied storage root",
			ctx: func() context.Context {
				storage := componentCtx.Value(immutableStorageContextKey{}).(*immutableStorage)
				return context.WithValue(componentCtx, immutableStorageContextKey{}, copyImmutableStorageRoot(storage))
			},
			values: func() map[string]any { return cloneAnyMap(contextValues) },
		},
		{
			name: "substituted storage root",
			ctx: func() context.Context {
				return WithIncrementalImmutableInputs(t.Context(), map[string]any{"other": true})
			},
			values: func() map[string]any { return cloneAnyMap(contextValues) },
		},
		{
			name: "replaced item",
			ctx:  func() context.Context { return componentCtx },
			values: func() map[string]any {
				values := cloneAnyMap(contextValues)
				values["item"] = map[string]any{"value": "poison"}
				return values
			},
		},
		{
			name: "replaced resources",
			ctx:  func() context.Context { return componentCtx },
			values: func() map[string]any {
				values := cloneAnyMap(contextValues)
				values["resources"] = &incrementalImmutableResources{Values: map[string]string{}}
				return values
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := withBoundIncrementalImmutableInputs(test.ctx(), test.values(), nil)
			require.Error(t, err)
		})
	}
}

func copyImmutableStorageRoot(storage *immutableStorage) *immutableStorage {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return &immutableStorage{
		parent:         storage.parent,
		certified:      storage.certified,
		certifiedSmall: storage.certifiedSmall,
		certifiedCount: storage.certifiedCount,
		identities:     storage.identities,
		identitySmall:  storage.identitySmall,
		identityCount:  storage.identityCount,
		ranges:         storage.ranges,
		keep:           storage.keep,
	}
}

func TestMissingIncrementalImmutableBindingFallsBackToCertification(t *testing.T) {
	item := map[string]any{"value": "original"}
	ctx, err := withBoundIncrementalImmutableInputs(t.Context(), map[string]any{}, []any{item})
	require.NoError(t, err)
	storage := ctx.Value(immutableStorageContextKey{}).(*immutableStorage)
	assert.True(t, storage.contains(reflect.ValueOf(item)))
}

func TestBoundIncrementalImmutableInputsRejectMutation(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{% item["value"] = "changed" %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)
	contextValues, componentCtx := boundIncrementalComponentTestContext(t, "original")
	require.NoError(t, BindIncrementalImmutableInputs(contextValues, componentCtx))

	_, err = engine.RenderIncrementalComponent(componentCtx, "component", contextValues)
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", contextValues["item"].(map[string]any)["value"])
}

func TestBoundIncrementalImmutableInputsIsolateBatchItems(t *testing.T) {
	firstValues, firstCtx := boundIncrementalComponentTestContext(t, "first")
	secondValues, secondCtx := boundIncrementalComponentTestContext(t, "second")
	require.NoError(t, BindIncrementalImmutableInputs(firstValues, firstCtx))
	require.NoError(t, BindIncrementalImmutableInputs(secondValues, secondCtx))

	_, err := withBoundIncrementalImmutableInputs(firstCtx, secondValues, nil)
	require.ErrorContains(t, err, "storage")
	_, err = withBoundIncrementalImmutableInputs(secondCtx, firstValues, nil)
	require.ErrorContains(t, err, "storage")

	firstLate := map[string]any{"value": "first-late"}
	secondLate := map[string]any{"value": "second-late"}
	require.NoError(t, RegisterIncrementalImmutableInputs(firstCtx, firstLate))
	require.NoError(t, RegisterIncrementalImmutableInputs(secondCtx, secondLate))
	firstStorage := firstCtx.Value(immutableStorageContextKey{}).(*immutableStorage)
	secondStorage := secondCtx.Value(immutableStorageContextKey{}).(*immutableStorage)
	assert.True(t, firstStorage.contains(reflect.ValueOf(firstLate)))
	assert.False(t, firstStorage.contains(reflect.ValueOf(secondLate)))
	assert.True(t, secondStorage.contains(reflect.ValueOf(secondLate)))
	assert.False(t, secondStorage.contains(reflect.ValueOf(firstLate)))
}

func TestBoundIncrementalImmutableInputsConcurrentIsolation(t *testing.T) {
	var group sync.WaitGroup
	for index := range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			values, ctx := boundIncrementalComponentTestContext(t, fmt.Sprintf("item-%d", index))
			assert.NoError(t, BindIncrementalImmutableInputs(values, ctx))
			_, err := withBoundIncrementalImmutableInputs(ctx, values, nil)
			assert.NoError(t, err)
		}()
	}
	group.Wait()
}

func boundIncrementalComponentTestContext(
	t *testing.T,
	value string,
) (map[string]any, context.Context) {
	t.Helper()
	item := map[string]any{"value": value}
	props := map[string]any{"value": "props"}
	renderSubject := map[string]any{"mode": "reconcile"}
	controller := map[string]ResourceStore{}
	resources := &incrementalImmutableResources{Values: map[string]string{"value": "resource"}}
	ctx := WithIncrementalImmutableCertificates(
		t.Context(),
		CertifyIncrementalImmutableInputs(item),
		CertifyIncrementalImmutableInputs(props),
		CertifyIncrementalImmutableInputs(renderSubject),
	)
	ctx = WithIncrementalImmutableCapabilityInputs(ctx, resources, controller)
	return incrementalComponentContext(map[string]any{
		"source": "source", "item": item, "props": props, "renderSubject": renderSubject,
		"controller": controller, "resources": resources,
	}), ctx
}

func cloneAnyMap(values map[string]any) map[string]any {
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

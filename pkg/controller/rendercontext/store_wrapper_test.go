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
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type testSnapshotView struct {
	listed []any
	got    []any
	keys   []string
	lists  int
	gets   int
}

type normalizingTestSnapshotView struct {
	testSnapshotView
	normalize func(string, []any) ([]string, error)
	calls     int
	rawKeys   []any
	resource  string
}

type trackedStringer struct {
	calls *int
}

func (s trackedStringer) String() string {
	(*s.calls)++
	return "legacy-stringer"
}

type trackedFormatter struct {
	calls *int
}

func (f trackedFormatter) Format(fmt.State, rune) {
	(*f.calls)++
}

type selectiveTestSnapshotView struct {
	testSnapshotView
	supported bool
}

func (v *selectiveTestSnapshotView) Supports(string) bool {
	return v.supported
}

func (v *testSnapshotView) List(_ string, _ stores.Store) ([]any, error) {
	v.lists++
	return v.listed, nil
}

func (v *testSnapshotView) Get(_ string, _ stores.Store, keys ...string) ([]any, error) {
	v.gets++
	v.keys = append([]string(nil), keys...)
	return v.got, nil
}

func (v *normalizingTestSnapshotView) NormalizeLookupKeys(
	resourceType string,
	keys []any,
) ([]string, error) {
	v.calls++
	v.resource = resourceType
	v.rawKeys = append([]any(nil), keys...)
	return v.normalize(resourceType, keys)
}

// createResourceMap creates a pre-converted resource map (as stores now contain).
func createResourceMap(name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
		},
	}
}

func TestStoreWrapper_List_Empty(t *testing.T) {
	store := &storetest.MockStore{Items: []any{}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.List()
	assert.Empty(t, result)
}

func TestStoreWrapper_List_WithItems(t *testing.T) {
	// Create pre-converted resource maps (as stores now contain)
	item1 := createResourceMap("item1")
	item2 := createResourceMap("item2")

	store := &storetest.MockStore{Items: []any{item1, item2}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.List()
	require.Len(t, result, 2)

	// Items are already maps, returned as-is
	m1, ok := result[0].(map[string]any)
	require.True(t, ok, "item should be a map")
	assert.Equal(t, "item1", m1["metadata"].(map[string]any)["name"])
}

func TestStoreWrapper_MutationsStayInsideOneRender(t *testing.T) {
	stored := createResourceMap("item")
	stored["metadata"].(map[string]any)["annotations"] = map[string]any{"existing": "value"}
	store := &storetest.MockStore{Items: []any{stored}}
	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          store,
		ResourceType:   "test",
		Logger:         testutil.NewTestLogger(),
		IndexBy:        []string{"metadata.namespace", "metadata.name"},
		resourceErrors: NewResourceErrorCollector(),
	}

	listed := wrapper.List()[0].(map[string]any)
	listed["metadata"].(map[string]any)["annotations"].(map[string]any)["injected"] = "yes"

	withinRender := wrapper.GetSingle("default", "item").(map[string]any)
	assert.Equal(t, "yes", withinRender["metadata"].(map[string]any)["annotations"].(map[string]any)["injected"])
	storedAnnotations := stored["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.NotContains(t, storedAnnotations, "injected")

	nextRender := (&StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          store,
		ResourceType:   "test",
		Logger:         testutil.NewTestLogger(),
		IndexBy:        []string{"metadata.namespace", "metadata.name"},
		resourceErrors: NewResourceErrorCollector(),
	}).List()[0].(map[string]any)
	assert.NotContains(t, nextRender["metadata"].(map[string]any)["annotations"].(map[string]any), "injected")
}

func TestStoreWrapper_UsesSnapshotView(t *testing.T) {
	listed := createResourceMap("listed")
	fetched := createResourceMap("fetched")
	view := &testSnapshotView{listed: []any{listed}, got: []any{fetched}}
	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          &storetest.MockStore{ListErr: errors.New("underlying store must not be read")},
		ResourceType:   "test",
		Logger:         testutil.NewTestLogger(),
		IndexBy:        []string{"metadata.namespace", "metadata.name"},
		SnapshotView:   view,
		resourceErrors: NewResourceErrorCollector(),
	}

	assert.Equal(t, []any{listed}, wrapper.List())
	assert.Equal(t, fetched, wrapper.GetSingle("default", "fetched"))
	assert.Equal(t, []string{"default", "fetched"}, view.keys)
	assert.NoError(t, wrapper.resourceErrors.Err())

	exposed := wrapper.List()[0].(map[string]any)
	exposed["metadata"].(map[string]any)["name"] = "mutated"
	assert.Equal(t, "listed", listed["metadata"].(map[string]any)["name"])
	assert.Equal(t, "listed", wrapper.List()[0].(map[string]any)["metadata"].(map[string]any)["name"])
}

func TestStoreWrapper_MemoizesCompleteSnapshotView(t *testing.T) {
	item := createResourceMap("item")
	view := &testSnapshotView{listed: []any{item}}
	wrapper := &StoreWrapper{
		readContext:         templating.WithImmutableResourceInputs(t.Context()),
		Store:               &storetest.MockStore{},
		ResourceType:        "test",
		Logger:              testutil.NewTestLogger(),
		IndexBy:             []string{"metadata.namespace", "metadata.name"},
		SnapshotView:        view,
		MemoizeSnapshotView: true,
		resourceErrors:      NewResourceErrorCollector(),
	}

	first := wrapper.List()
	second := wrapper.List()
	single := wrapper.GetSingle("default", "item")

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, reflect.ValueOf(first[0]).Pointer(), reflect.ValueOf(second[0]).Pointer())
	assert.Equal(t, reflect.ValueOf(first[0]).Pointer(), reflect.ValueOf(single).Pointer())
	assert.Equal(t, 1, view.lists)
	assert.Zero(t, view.gets)
	assert.NoError(t, wrapper.resourceErrors.Err())
}

func TestStoreWrapperMemoizedLazySnapshotViewDefersCompleteList(t *testing.T) {
	warm := createResourceMap("warm")
	target := createResourceMap("target")
	view := &testSnapshotView{
		got:    []any{target},
		listed: []any{warm, target},
	}
	wrapper := &StoreWrapper{
		readContext:         templating.WithImmutableResourceInputs(t.Context()),
		Store:               &storetest.MockStore{},
		ResourceType:        "test",
		Logger:              testutil.NewTestLogger(),
		IndexBy:             []string{"metadata.namespace", "metadata.name"},
		LazySnapshot:        true,
		SnapshotView:        view,
		MemoizeSnapshotView: true,
		resourceErrors:      NewResourceErrorCollector(),
	}

	first := wrapper.GetSingle("default", "target")
	second := wrapper.GetSingle("default", "target")
	listed := wrapper.List()

	assert.Equal(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(second).Pointer())
	require.Len(t, listed, 2)
	assert.Equal(t, 1, view.lists)
	assert.Equal(t, 1, view.gets)
	assert.NoError(t, wrapper.resourceErrors.Err())
}

func TestStoreWrapper_StrictSnapshotViewRejectsKeysBeforeLegacyMethods(t *testing.T) {
	keyCases := []struct {
		name string
		key  func(*int) any
	}{
		{name: "Stringer", key: func(calls *int) any { return trackedStringer{calls: calls} }},
		{name: "Formatter", key: func(calls *int) any { return trackedFormatter{calls: calls} }},
	}
	operations := []struct {
		name string
		read func(*StoreWrapper, any) any
	}{
		{name: "Fetch", read: func(wrapper *StoreWrapper, key any) any { return wrapper.Fetch(key) }},
		{name: "GetSingle", read: func(wrapper *StoreWrapper, key any) any { return wrapper.GetSingle(key) }},
	}
	for _, keyCase := range keyCases {
		for _, operation := range operations {
			t.Run(keyCase.name+"/"+operation.name, func(t *testing.T) {
				methodCalls := 0
				view := &normalizingTestSnapshotView{
					normalize: func(string, []any) ([]string, error) {
						return nil, errors.New("unsupported lookup key")
					},
				}
				collector := NewResourceErrorCollector()
				wrapper := &StoreWrapper{
					readContext:    templating.WithImmutableResourceInputs(t.Context()),
					Store:          &storetest.MockStore{},
					ResourceType:   "strict",
					Logger:         testutil.NewTestLogger(),
					SnapshotView:   view,
					resourceErrors: collector,
				}

				var result any
				require.NotPanics(t, func() {
					result = operation.read(wrapper, keyCase.key(&methodCalls))
				})

				if operation.name == "Fetch" {
					assert.Empty(t, result)
				} else {
					assert.Nil(t, result)
				}
				assert.Zero(t, methodCalls)
				assert.Zero(t, view.gets)
				require.ErrorContains(t, collector.Err(), "lookup keys were rejected: unsupported lookup key")
			})
		}
	}
}

func TestStoreWrapper_StrictSnapshotViewForwardsNormalizedScalarKeys(t *testing.T) {
	item := createResourceMap("matched")
	operations := []struct {
		name string
		read func(*StoreWrapper) any
	}{
		{name: "Fetch", read: func(wrapper *StoreWrapper) any {
			return wrapper.Fetch("default", int64(42), true)
		}},
		{name: "GetSingle", read: func(wrapper *StoreWrapper) any {
			return wrapper.GetSingle("default", int64(42), true)
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			view := &normalizingTestSnapshotView{
				testSnapshotView: testSnapshotView{got: []any{item}},
				normalize: func(string, []any) ([]string, error) {
					return []string{"default", "42", "true"}, nil
				},
			}
			wrapper := &StoreWrapper{
				readContext:    templating.WithImmutableResourceInputs(t.Context()),
				Store:          &storetest.MockStore{},
				ResourceType:   "strict",
				Logger:         testutil.NewTestLogger(),
				SnapshotView:   view,
				resourceErrors: NewResourceErrorCollector(),
			}

			result := operation.read(wrapper)

			assert.NotNil(t, result)
			assert.Equal(t, "strict", view.resource)
			assert.Equal(t, []any{"default", int64(42), true}, view.rawKeys)
			assert.Equal(t, []string{"default", "42", "true"}, view.keys)
			assert.Equal(t, 1, view.calls)
			assert.Equal(t, 1, view.gets)
			assert.NoError(t, wrapper.resourceErrors.Err())
		})
	}
}

func TestStoreWrapper_LegacySnapshotViewKeepsStringerCompatibility(t *testing.T) {
	methodCalls := 0
	view := &testSnapshotView{}
	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          &storetest.MockStore{},
		ResourceType:   "legacy",
		Logger:         testutil.NewTestLogger(),
		SnapshotView:   view,
		resourceErrors: NewResourceErrorCollector(),
	}

	wrapper.Fetch(trackedStringer{calls: &methodCalls})

	assert.Equal(t, 1, methodCalls)
	assert.Equal(t, []string{"legacy-stringer"}, view.keys)
}

func TestStoreWrapper_StrictSnapshotViewRejectsChangedKeyCardinality(t *testing.T) {
	view := &normalizingTestSnapshotView{
		normalize: func(string, []any) ([]string, error) {
			return nil, nil
		},
	}
	collector := NewResourceErrorCollector()
	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          &storetest.MockStore{},
		ResourceType:   "strict",
		Logger:         testutil.NewTestLogger(),
		SnapshotView:   view,
		resourceErrors: collector,
	}

	assert.Empty(t, wrapper.Fetch("key"))
	assert.Zero(t, view.gets)
	require.ErrorContains(t, collector.Err(), "normalizer returned 0 keys for 1 inputs")
}

func TestStoreWrapper_UnsupportedSnapshotViewUsesRenderLocalSnapshot(t *testing.T) {
	stored := createResourceMap("stored")
	view := &selectiveTestSnapshotView{
		testSnapshotView: testSnapshotView{listed: []any{createResourceMap("pinned")}},
		supported:        false,
	}
	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          &storetest.MockStore{Items: []any{stored}},
		ResourceType:   "test",
		Logger:         testutil.NewTestLogger(),
		IndexBy:        []string{"metadata.namespace", "metadata.name"},
		SnapshotView:   view,
		resourceErrors: NewResourceErrorCollector(),
	}

	listed := wrapper.List()
	require.Len(t, listed, 1)
	assert.Equal(t, "stored", listed[0].(map[string]any)["metadata"].(map[string]any)["name"])
}

func TestStoreWrapper_List_Error(t *testing.T) {
	store := &storetest.MockStore{ListErr: errors.New("list failed")}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.List()
	assert.Empty(t, result, "should return empty slice on error")
}

func TestStoreWrapper_Fetch(t *testing.T) {
	item := createResourceMap("test-item")

	store := &storetest.MockStore{Items: []any{item}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.Fetch("default", "test-item")
	require.Len(t, result, 1)
}

func TestStoreWrapper_Fetch_Error(t *testing.T) {
	store := &storetest.MockStore{GetErr: errors.New("get failed")}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.Fetch("default", "test-item")
	assert.Empty(t, result, "should return empty slice on error")
}

func TestStoreWrapper_GetSingle(t *testing.T) {
	item := createResourceMap("single-item")

	store := &storetest.MockStore{Items: []any{item}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.GetSingle("default", "single-item")
	require.NotNil(t, result)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "single-item", m["metadata"].(map[string]any)["name"])
}

func TestStoreWrapper_GetSingle_NotFound(t *testing.T) {
	store := &storetest.MockStore{Items: []any{}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.GetSingle("default", "missing")
	assert.Nil(t, result)
}

func TestStoreWrapper_GetSingle_Ambiguous(t *testing.T) {
	item1 := createResourceMap("item1")
	item2 := createResourceMap("item2")

	store := &storetest.MockStore{Items: []any{item1, item2}}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "test",
		Logger:       testutil.NewTestLogger(),
	}

	result := wrapper.GetSingle("default", "ambiguous")
	assert.Nil(t, result, "should return nil for ambiguous lookup")
}

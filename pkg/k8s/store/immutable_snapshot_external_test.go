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

package store_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type snapshotProjectionEmbedded struct {
	Embedded string `json:"embedded"`
}

type snapshotProjectionCustom string

func (v *snapshotProjectionCustom) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = snapshotProjectionCustom("decoded:" + value)
	return nil
}

type snapshotProjectionAdversarial struct {
	snapshotProjectionEmbedded
	Custom snapshotProjectionCustom `json:"custom"`
	Labels map[string]string        `json:"labels"`
	hidden string
}

func TestImmutableSnapshotProjectionNeverExposesOwnedGraph(t *testing.T) {
	resourceStore := k8sstore.NewMemoryStore(2)
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "target"},
		"spec":     map[string]any{"labels": map[string]any{"team": "edge"}},
	}
	require.NoError(t, resourceStore.Add(source, []string{"default", "target"}))
	snapshot, err := resourceStore.Pin()
	require.NoError(t, err)
	projection, supported, err := k8sstore.ProjectImmutableSnapshotList(t.Context(), snapshot)
	require.NoError(t, err)
	require.True(t, supported)
	before, err := projection.Encode()
	require.NoError(t, err)

	source["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "caller poison"
	public, err := snapshot.List()
	require.NoError(t, err)
	public[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "read poison"
	projected, err := projection.ProjectItems(nil)
	require.NoError(t, err)
	projected[0].Interface().(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "projection poison"

	after, err := projection.Encode()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	again, err := projection.ProjectItems(nil)
	require.NoError(t, err)
	assert.Equal(t, "edge", again[0].Interface().(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"])

	typeOf := reflect.TypeFor[k8sstore.ImmutableSnapshotProjection]()
	for index := range typeOf.NumField() {
		assert.False(t, typeOf.Field(index).IsExported())
	}
	for index := range reflect.PointerTo(typeOf).NumMethod() {
		method := reflect.PointerTo(typeOf).Method(index)
		for argument := 1; argument < method.Type.NumIn(); argument++ {
			assert.NotEqual(t, reflect.Func, method.Type.In(argument).Kind(), method.Name)
		}
		for result := range method.Type.NumOut() {
			assert.NotEqual(t, reflect.TypeFor[[]any](), method.Type.Out(result), method.Name)
		}
	}
}

func TestImmutableSnapshotProjectionPreservesJSONSemanticsForForeignTypes(t *testing.T) {
	resourceStore := k8sstore.NewMemoryStore(1)
	source := map[string]any{
		"metadata": map[string]any{"name": "target"},
		"embedded": "promoted",
		"custom":   "value",
		"labels":   map[string]any{"team": "edge"},
		"hidden":   "must be ignored",
	}
	require.NoError(t, resourceStore.Add(source, []string{"target"}))
	snapshot, err := resourceStore.Pin()
	require.NoError(t, err)
	projection, supported, err := k8sstore.ProjectImmutableSnapshotList(t.Context(), snapshot)
	require.NoError(t, err)
	require.True(t, supported)

	projected, err := projection.ProjectItems(reflect.TypeFor[snapshotProjectionAdversarial]())
	require.NoError(t, err)
	require.Len(t, projected, 1)
	value := projected[0].Interface().(*snapshotProjectionAdversarial)
	assert.Equal(t, "promoted", value.Embedded)
	assert.Equal(t, snapshotProjectionCustom("decoded:value"), value.Custom)
	assert.Empty(t, value.hidden)
	assert.Equal(t, "edge", value.Labels["team"])

	value.Labels["team"] = "projection poison"
	again, err := projection.ProjectItems(reflect.TypeFor[snapshotProjectionAdversarial]())
	require.NoError(t, err)
	assert.Equal(t, "edge", again[0].Interface().(*snapshotProjectionAdversarial).Labels["team"])
}

func TestImmutableSnapshotProjectionEncodingMatchesPinnedValues(t *testing.T) {
	resourceStore := k8sstore.NewMemoryStore(2)
	resources := []map[string]any{
		{
			"metadata": map[string]any{"namespace": "other", "name": "second"},
			"spec": map[string]any{
				"escaped": "<route>&\u2028", "enabled": true, "weight": int64(2),
			},
		},
		{
			"metadata": map[string]any{"namespace": "default", "name": "first"},
			"spec": map[string]any{
				"hostnames": []any{"one.example", "two.example"},
			},
		},
	}
	for _, resource := range resources {
		metadata := resource["metadata"].(map[string]any)
		require.NoError(t, resourceStore.Add(resource, []string{
			metadata["namespace"].(string), metadata["name"].(string),
		}))
	}

	snapshot, err := resourceStore.Pin()
	require.NoError(t, err)
	assertProjectionEncodingMatchesRead(t, snapshot, nil)
	assertProjectionEncodingMatchesRead(t, snapshot, []string{"default"})
	assertProjectionEncodingMatchesRead(t, snapshot, []string{"other", "second"})
	assertProjectionEncodingMatchesRead(t, snapshot, []string{"missing", "missing"})

	updated := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "first"},
		"spec":     map[string]any{"hostnames": []any{"updated.example"}},
	}
	require.NoError(t, resourceStore.Update(updated, []string{"default", "first"}))
	assertProjectionEncodingMatchesRead(t, snapshot, nil)
	updatedSnapshot, err := resourceStore.Pin()
	require.NoError(t, err)
	assertProjectionEncodingMatchesRead(t, updatedSnapshot, nil)
}

func TestImmutableSnapshotProjectionEncodingPreservesEmptySliceShape(t *testing.T) {
	resourceStore := k8sstore.NewMemoryStore(1)
	snapshot, err := resourceStore.Pin()
	require.NoError(t, err)

	listProjection, supported, err := k8sstore.ProjectImmutableSnapshotList(t.Context(), snapshot)
	require.NoError(t, err)
	require.True(t, supported)
	encoded, err := listProjection.Encode()
	require.NoError(t, err)
	assert.JSONEq(t, "null", string(encoded))

	getProjection, supported, err := k8sstore.ProjectImmutableSnapshotGet(t.Context(), snapshot, "missing")
	require.NoError(t, err)
	require.True(t, supported)
	encoded, err = getProjection.Encode()
	require.NoError(t, err)
	assert.JSONEq(t, "[]", string(encoded))
}

func assertProjectionEncodingMatchesRead(
	t *testing.T,
	snapshot stores.ReadSnapshot,
	keys []string,
) {
	t.Helper()
	var (
		items      []any
		projection *k8sstore.ImmutableSnapshotProjection
		supported  bool
		err        error
	)
	if keys == nil {
		items, err = snapshot.List()
		require.NoError(t, err)
		projection, supported, err = k8sstore.ProjectImmutableSnapshotList(t.Context(), snapshot)
	} else {
		items, err = snapshot.Get(keys...)
		require.NoError(t, err)
		projection, supported, err = k8sstore.ProjectImmutableSnapshotGet(t.Context(), snapshot, keys...)
	}
	require.NoError(t, err)
	require.True(t, supported)
	want, err := json.Marshal(items)
	require.NoError(t, err)
	got, err := projection.Encode()
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestMemoryStoreOwnsUnidentifiedValues(t *testing.T) {
	resourceStore := k8sstore.NewMemoryStore(1)
	source := map[string]any{
		"spec": map[string]any{"labels": map[string]any{"team": "edge"}},
	}
	require.NoError(t, resourceStore.Add(source, []string{"target"}))
	source["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "caller poison"
	publicGet, err := resourceStore.Get("target")
	require.NoError(t, err)
	require.Len(t, publicGet, 1)
	assert.Equal(
		t,
		"edge",
		publicGet[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"],
	)
	publicGet[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "get poison"
	public, err := resourceStore.List()
	require.NoError(t, err)
	assert.Equal(
		t,
		"edge",
		public[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"],
	)
	public[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "read poison"
	again, err := resourceStore.List()
	require.NoError(t, err)
	assert.Equal(
		t,
		"edge",
		again[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"],
	)
	updated := map[string]any{
		"spec": map[string]any{"labels": map[string]any{"team": "updated"}},
	}
	require.NoError(t, resourceStore.Update(updated, []string{"target"}))
	updated["spec"].(map[string]any)["labels"].(map[string]any)["team"] = "update poison"
	readUpdated, err := resourceStore.Get("target")
	require.NoError(t, err)
	require.Len(t, readUpdated, 1)
	assert.Equal(
		t,
		"updated",
		readUpdated[0].(map[string]any)["spec"].(map[string]any)["labels"].(map[string]any)["team"],
	)
	_, err = resourceStore.Pin()
	require.ErrorIs(t, err, stores.ErrSnapshotUnsupported)
}

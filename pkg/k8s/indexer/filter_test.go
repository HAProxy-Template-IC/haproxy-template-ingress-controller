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

package indexer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseJSONPathPattern(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "single segment", in: "metadata", want: []string{"metadata"}},
		{name: "dot path", in: "metadata.name", want: []string{"metadata", "name"}},
		{name: "leading dot trimmed", in: ".metadata.name", want: []string{"metadata", "name"}},
		{name: "single quoted bracket key", in: "metadata.labels['app']", want: []string{"metadata", "labels", "app"}},
		{name: "double quoted bracket key", in: `metadata.labels["app"]`, want: []string{"metadata", "labels", "app"}},
		{name: "unquoted bracket index", in: "spec.rules[0].host", want: []string{"spec", "rules", "0", "host"}},
		{name: "consecutive brackets", in: "a[0][1]", want: []string{"a", "0", "1"}},
		{name: "empty brackets ignored", in: "a[].b", want: []string{"a", "b"}},
		{name: "empty input", in: "", want: nil},
		{name: "only dot", in: ".", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseJSONPathPattern(tt.in))
		})
	}
}

func TestDerefForFilter(t *testing.T) {
	type myStruct struct{ X int }
	concrete := myStruct{X: 1}
	pointer := &concrete

	tests := []struct {
		name     string
		in       reflect.Value
		wantOK   bool
		wantKind reflect.Kind
	}{
		{name: "string concrete", in: reflect.ValueOf("hello"), wantOK: true, wantKind: reflect.String},
		{name: "int concrete", in: reflect.ValueOf(42), wantOK: true, wantKind: reflect.Int},
		{name: "struct value", in: reflect.ValueOf(concrete), wantOK: true, wantKind: reflect.Struct},
		{name: "non-nil pointer", in: reflect.ValueOf(pointer), wantOK: true, wantKind: reflect.Struct},
		{name: "nil pointer", in: reflect.ValueOf((*myStruct)(nil)), wantOK: false},
		{name: "nil interface inside ValueOf is invalid", in: reflect.ValueOf(any(nil)), wantOK: true, wantKind: reflect.Invalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := derefForFilter(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantKind, got.Kind())
			}
		})
	}
}

func TestFieldFilter_Filter(t *testing.T) {
	t.Run("removes top-level field", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata"})
		obj := map[string]any{"metadata": map[string]any{"name": "x"}, "spec": "kept"}
		require.NoError(t, filter.Filter(obj))
		_, exists := obj["metadata"]
		assert.False(t, exists)
		assert.Contains(t, obj, "spec")
	})

	t.Run("removes nested field", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata.managedFields"})
		obj := map[string]any{
			"metadata": map[string]any{
				"name":          "x",
				"managedFields": []any{"f1", "f2"},
			},
		}
		require.NoError(t, filter.Filter(obj))

		md, ok := obj["metadata"].(map[string]any)
		require.True(t, ok)
		_, exists := md["managedFields"]
		assert.False(t, exists)
		assert.Contains(t, md, "name")
	})

	t.Run("missing path is not an error", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata.notthere"})
		obj := map[string]any{"metadata": map[string]any{"name": "x"}}
		assert.NoError(t, filter.Filter(obj))
	})

	t.Run("empty patterns is no-op", func(t *testing.T) {
		filter := NewFieldFilter(nil)
		obj := map[string]any{"a": 1}
		assert.NoError(t, filter.Filter(obj))
		assert.Contains(t, obj, "a")
	})

	t.Run("works with unstructured.Unstructured", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata.name"})
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "x", "namespace": "default"},
		}}
		require.NoError(t, filter.Filter(u))

		md, ok := u.Object["metadata"].(map[string]any)
		require.True(t, ok)
		_, exists := md["name"]
		assert.False(t, exists)
		assert.Contains(t, md, "namespace")
	})

	t.Run("removes bracketed map key", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata.labels['app']"})
		obj := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{
					"app":  "demo",
					"team": "platform",
				},
			},
		}
		require.NoError(t, filter.Filter(obj))

		md := obj["metadata"].(map[string]any)
		labels := md["labels"].(map[string]any)
		_, exists := labels["app"]
		assert.False(t, exists)
		assert.Contains(t, labels, "team")
	})

	t.Run("nil resource is no-op (deref fails)", func(t *testing.T) {
		filter := NewFieldFilter([]string{"metadata"})
		var p *map[string]any
		assert.NoError(t, filter.Filter(p))
	})
}

// TestFilterError is in indexer_test.go; FilterError unwrap behaviour is
// covered there.

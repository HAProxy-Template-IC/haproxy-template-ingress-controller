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

package templating

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

// fakeEnv is a minimal native.Env whose Context() carries a render-context map,
// so scriggoResource can be unit-tested without the full engine.
type fakeEnv struct{ ctx context.Context }

func (e *fakeEnv) CallPath() string                    { return "" }
func (e *fakeEnv) CallLine() int                       { return 0 }
func (e *fakeEnv) Context() context.Context            { return e.ctx }
func (e *fakeEnv) Fatal(any)                           {}
func (e *fakeEnv) MarkdownConverter() native.Converter { return nil }
func (e *fakeEnv) Print(...any)                        {}
func (e *fakeEnv) Println(...any)                      {}
func (e *fakeEnv) Stop(error)                          {}
func (e *fakeEnv) TypeOf(v reflect.Value) reflect.Type { return v.Type() }

func envWithResources(res any) *fakeEnv {
	ctx := context.WithValue(context.Background(), RenderContextContextKey, map[string]any{"resources": res})
	return &fakeEnv{ctx: ctx}
}

func TestScriggoResource(t *testing.T) {
	type resItem struct{ Name string }
	type resStore struct{ List func() []*resItem }
	type resources struct {
		Ingresses *resStore `json:"ingresses"`
		Nil       *resStore `json:"nilresource"`
	}
	items := []*resItem{{Name: "a"}, {Name: "b"}}
	res := &resources{Ingresses: &resStore{List: func() []*resItem { return items }}}

	t.Run("known resource returns its items as []any", func(t *testing.T) {
		got := scriggoResource(envWithResources(res), "ingresses")
		require.Len(t, got, 2)
		assert.Same(t, items[0], got[0].(*resItem))
		assert.Same(t, items[1], got[1].(*resItem))
	})
	t.Run("unknown resource name returns nil", func(t *testing.T) {
		assert.Nil(t, scriggoResource(envWithResources(res), "widgets"))
	})
	t.Run("nil pointer field returns nil", func(t *testing.T) {
		assert.Nil(t, scriggoResource(envWithResources(res), "nilresource"))
	})
	t.Run("nil env returns nil", func(t *testing.T) {
		assert.Nil(t, scriggoResource(&fakeEnv{ctx: nil}, "ingresses"))
	})
	t.Run("missing resources key returns nil", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), RenderContextContextKey, map[string]any{})
		assert.Nil(t, scriggoResource(&fakeEnv{ctx: ctx}, "ingresses"))
	})
}

// govTestMeta / govTestRes mimic a typed watched-resource *T (json tags match
// the typegen convention: lowercase JSON keys).
type govTestMeta struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
type govTestRes struct {
	Metadata govTestMeta    `json:"metadata"`
	Spec     map[string]any `json:"spec,omitempty"`
}

func TestParseConcreteJSONPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    []pathSeg
		wantErr bool
	}{
		{"dotted", "metadata.name", []pathSeg{{key: "metadata"}, {key: "name"}}, false},
		{"leading $.", "$.spec.foo", []pathSeg{{key: "spec"}, {key: "foo"}}, false},
		{"bracket key with dots and slash", "metadata.annotations['haproxy-haptic.org/rate-limit-requests']",
			[]pathSeg{{key: "metadata"}, {key: "annotations"}, {key: "haproxy-haptic.org/rate-limit-requests"}}, false},
		{"double-quoted bracket key", `metadata.annotations["k8s.io/x"]`,
			[]pathSeg{{key: "metadata"}, {key: "annotations"}, {key: "k8s.io/x"}}, false},
		{"array index", "spec.rules[0].host",
			[]pathSeg{{key: "spec"}, {key: "rules"}, {index: 0, isIndex: true}, {key: "host"}}, false},
		{"filtered rejected", "spec.rules[?(@.host=='x')].host", nil, true},
		{"wildcard rejected", "spec.rules[*].host", nil, true},
		{"empty rejected", "", nil, true},
		{"double dot rejected", "metadata..name", nil, true},
		{"trailing dot rejected", "metadata.name.", nil, true},
		{"empty bracket key rejected", "metadata.annotations['']", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseConcreteJSONPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetSetAtPath(t *testing.T) {
	m := map[string]any{
		"metadata": map[string]any{"name": "x", "annotations": map[string]any{"a": "1"}},
		"spec":     map[string]any{"rules": []any{map[string]any{"host": "h0"}}},
	}
	segs, _ := parseConcreteJSONPath("spec.rules[0].host")
	v, ok := getAtPath(m, segs)
	require.True(t, ok)
	assert.Equal(t, "h0", v)

	// set an existing array-element field
	require.NoError(t, setAtPath(m, segs, "h1"))
	v, _ = getAtPath(m, segs)
	assert.Equal(t, "h1", v)

	// set a deep path, creating intermediate nodes
	deep, _ := parseConcreteJSONPath("spec.new.child")
	require.NoError(t, setAtPath(m, deep, "v"))
	v, ok = getAtPath(m, deep)
	require.True(t, ok)
	assert.Equal(t, "v", v)
}

func TestJSONPathGet(t *testing.T) {
	res := &govTestRes{
		Metadata: govTestMeta{Namespace: "ns", Name: "app", Annotations: map[string]string{"haproxy-haptic.org/rate-limit-requests": "500"}},
		Spec:     map[string]any{"tls": []any{map[string]any{"secretName": "c"}}},
	}
	// annotation fast path
	assert.Equal(t, "500", scriggoJSONPathGet(res, "metadata.annotations['haproxy-haptic.org/rate-limit-requests']"))
	// missing annotation
	assert.Nil(t, scriggoJSONPathGet(res, "metadata.annotations['nope']"))
	// spec field via json round-trip
	assert.Equal(t, "app", scriggoJSONPathGet(res, "metadata.name"))
	assert.Equal(t, "c", scriggoJSONPathGet(res, "spec.tls[0].secretName"))
	// untyped map item
	m := map[string]any{"metadata": map[string]any{"name": "u"}}
	assert.Equal(t, "u", scriggoJSONPathGet(m, "metadata.name"))
}

func TestJSONPathSet(t *testing.T) {
	t.Run("annotation fast path sets in place", func(t *testing.T) {
		res := &govTestRes{Metadata: govTestMeta{Annotations: map[string]string{"a": "1"}}}
		require.True(t, scriggoJSONPathSet(res, "metadata.annotations['b']", "2"))
		assert.Equal(t, "2", res.Metadata.Annotations["b"])
		assert.Equal(t, "1", res.Metadata.Annotations["a"], "existing annotations preserved")
	})
	t.Run("annotation fast path allocates nil map", func(t *testing.T) {
		res := &govTestRes{Metadata: govTestMeta{Name: "x"}} // Annotations nil
		require.True(t, scriggoJSONPathSet(res, "metadata.annotations['k']", "v"))
		assert.Equal(t, "v", res.Metadata.Annotations["k"])
	})
	t.Run("spec field via json round-trip persists in place", func(t *testing.T) {
		res := &govTestRes{Spec: map[string]any{"existing": "keep"}}
		require.True(t, scriggoJSONPathSet(res, "spec.injected", "yes"))
		assert.Equal(t, "yes", res.Spec["injected"])
		assert.Equal(t, "keep", res.Spec["existing"])
	})
	t.Run("filtered path rejected", func(t *testing.T) {
		res := &govTestRes{}
		assert.False(t, scriggoJSONPathSet(res, "spec.rules[?(@.host=='x')]", "v"))
	})
	t.Run("numeric value coerced to string for annotation", func(t *testing.T) {
		res := &govTestRes{}
		require.True(t, scriggoJSONPathSet(res, "metadata.annotations['n']", 1000))
		assert.Equal(t, "1000", res.Metadata.Annotations["n"])
	})
}

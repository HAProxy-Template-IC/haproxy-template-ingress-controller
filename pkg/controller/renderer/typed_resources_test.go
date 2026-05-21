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

package renderer

import (
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// stubStore implements stores.Store for the typed-resources tests.
// We only exercise List(); the other methods aren't reached by
// addTypedRenderContextEntries.
type stubStore struct {
	items []any
	err   error
}

func (s *stubStore) List() ([]any, error)           { return s.items, s.err }
func (s *stubStore) Get(_ ...string) ([]any, error) { return nil, nil }
func (s *stubStore) Add(_ any, _ []string) error    { return nil }
func (s *stubStore) Update(_ any, _ []string) error { return nil }
func (s *stubStore) Delete(_ ...string) error       { return nil }
func (s *stubStore) Clear() error                   { return nil }

// stubProvider implements stores.StoreProvider for the tests. The
// real provider in production wraps a stores.Manager; here we hand
// it a map of name→store directly so the tests don't need a
// Manager dance.
type stubProvider struct {
	storesByName map[string]stores.Store
}

func (p *stubProvider) GetStore(name string) stores.Store {
	return p.storesByName[name]
}
func (p *stubProvider) StoreNames() []string {
	names := make([]string, 0, len(p.storesByName))
	for n := range p.storesByName {
		names = append(names, n)
	}
	return names
}

// gatewayType builds a Gateway-shaped struct via reflect.StructOf,
// matching what pkg/k8s/typegen would produce for a real Gateway
// CRD. The exact shape doesn't matter here — these tests pin the
// renderer's *wrapping* behaviour, not typegen's generation.
func gatewayType() reflect.Type {
	metaType := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeOf(""), Tag: `json:"name"`},
		{Name: "Namespace", Type: reflect.TypeOf(""), Tag: `json:"namespace"`},
	})
	return reflect.StructOf([]reflect.StructField{
		{Name: "Metadata", Type: metaType, Tag: `json:"metadata"`},
	})
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestAddTypedRenderContextEntries_HappyPath is the keystone: a
// typed entry with a matching store produces a *[]*<Gateway> value
// in the render context, populated from the store's items via
// typegen.WrapSlice. Templates compile against the matching typed
// global declaration and Render finds the value.
func TestAddTypedRenderContextEntries_HappyPath(t *testing.T) {
	gw := gatewayType()
	provider := &stubProvider{storesByName: map[string]stores.Store{
		"gateways": &stubStore{items: []any{
			map[string]any{"metadata": map[string]any{"name": "a", "namespace": "ns1"}},
			map[string]any{"metadata": map[string]any{"name": "b", "namespace": "ns2"}},
		}},
	}}

	ctx := map[string]any{}
	addTypedRenderContextEntries(ctx, provider, map[string]reflect.Type{"gateways": gw}, silentLogger())

	entry, ok := ctx["gateways"]
	require.True(t, ok, "typed entry must be added")

	// Shape: *[]*<gateway>. Walk through and verify values came
	// through from the unstructured snapshot.
	v := reflect.ValueOf(entry)
	require.Equal(t, reflect.Ptr, v.Kind(), "outer must be a pointer")
	slice := v.Elem()
	require.Equal(t, reflect.Slice, slice.Kind())
	require.Equal(t, 2, slice.Len())

	first := slice.Index(0)
	require.Equal(t, reflect.Ptr, first.Kind(), "elements must be pointers")
	assert.Equal(t, "a", first.Elem().FieldByName("Metadata").FieldByName("Name").String())
	assert.Equal(t, "ns1", first.Elem().FieldByName("Metadata").FieldByName("Namespace").String())
}

// TestAddTypedRenderContextEntries_SkipsMissingStore pins the
// fail-open behaviour when typebootstrap produced a type for a
// resource the local provider doesn't have a store for. Common in
// tests; would also happen in production if a watcher build raced
// the bootstrap (impossible given iteration ordering, but cheap to
// guard).
func TestAddTypedRenderContextEntries_SkipsMissingStore(t *testing.T) {
	provider := &stubProvider{storesByName: map[string]stores.Store{}}
	ctx := map[string]any{}
	addTypedRenderContextEntries(ctx, provider, map[string]reflect.Type{"gateways": gatewayType()}, silentLogger())
	_, ok := ctx["gateways"]
	assert.False(t, ok, "no typed entry must be emitted when the store is missing")
}

// TestAddTypedRenderContextEntries_LogsAndSkipsOnStoreError is the
// branch where the underlying store.List() fails. We must NOT
// crash and must NOT inject a half-built typed entry — the
// template's declared global stays at its zero value (iterates as
// empty), the operator sees the warn log.
func TestAddTypedRenderContextEntries_LogsAndSkipsOnStoreError(t *testing.T) {
	provider := &stubProvider{storesByName: map[string]stores.Store{
		"gateways": &stubStore{err: errors.New("store down")},
	}}
	ctx := map[string]any{}
	addTypedRenderContextEntries(ctx, provider, map[string]reflect.Type{"gateways": gatewayType()}, silentLogger())
	_, ok := ctx["gateways"]
	assert.False(t, ok)
}

// TestAddTypedRenderContextEntries_NoTypes confirms the no-op
// path: an empty types map skips the whole loop, the context is
// untouched. This is the typical render-time state today because
// no chart template uses the typed shape yet.
func TestAddTypedRenderContextEntries_NoTypes(t *testing.T) {
	ctx := map[string]any{}
	addTypedRenderContextEntries(ctx, &stubProvider{}, nil, silentLogger())
	assert.Empty(t, ctx)
	addTypedRenderContextEntries(ctx, &stubProvider{}, map[string]reflect.Type{}, silentLogger())
	assert.Empty(t, ctx)
}

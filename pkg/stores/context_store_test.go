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

package stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storeContextKey struct{}

type contextProbeStore struct {
	*mockStore
	getValue  any
	listValue any
	cached    []any
}

func (s *contextProbeStore) GetContext(ctx context.Context, keys ...string) ([]any, error) {
	s.getValue = ctx.Value(storeContextKey{})
	return s.Get(keys...)
}

func (s *contextProbeStore) ListContext(ctx context.Context) ([]any, error) {
	s.listValue = ctx.Value(storeContextKey{})
	return s.List()
}

func (s *contextProbeStore) ListCached() ([]any, error) {
	return s.cached, nil
}

func TestContextStorePropagationThroughAdapters(t *testing.T) {
	inner := &contextProbeStore{mockStore: newMockStore(), cached: []any{"warm"}}
	require.NoError(t, inner.Add("value", []string{"key"}))
	adapter := &TypesStoreAdapter{Inner: inner}
	composite := NewCompositeStore(adapter, NewStoreOverlay())
	ctx := context.WithValue(t.Context(), storeContextKey{}, "render")

	items, err := composite.GetContext(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, []any{"value"}, items)
	assert.Equal(t, "render", inner.getValue)

	items, err = composite.ListContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, []any{"value"}, items)
	assert.Equal(t, "render", inner.listValue)

	items, err = composite.ListCached()
	require.NoError(t, err)
	assert.Equal(t, []any{"warm"}, items)
}

func TestTypesStoreAdapterContextFallbackPreservesLegacyStore(t *testing.T) {
	inner := newMockStore()
	require.NoError(t, inner.Add("value", []string{"key"}))
	adapter := &TypesStoreAdapter{Inner: inner}

	items, err := adapter.GetContext(t.Context(), "key")
	require.NoError(t, err)
	assert.Equal(t, []any{"value"}, items)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = adapter.ListContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

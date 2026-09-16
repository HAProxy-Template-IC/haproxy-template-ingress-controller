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

func TestCompositeStorePropagatesContext(t *testing.T) {
	inner := &contextProbeStore{mockStore: newMockStore(), cached: []any{"warm"}}
	require.NoError(t, inner.Add("value", []string{"key"}))
	composite := NewCompositeStore(inner, NewStoreOverlay())
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

func TestCompositeStoreContextFallbackPreservesLegacyStore(t *testing.T) {
	inner := newMockStore()
	require.NoError(t, inner.Add("value", []string{"key"}))
	composite := NewCompositeStore(inner, NewStoreOverlay())

	items, err := composite.GetContext(t.Context(), "key")
	require.NoError(t, err)
	assert.Equal(t, []any{"value"}, items)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = composite.ListContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

type cancelOnReadStore struct {
	*mockStore
	cancel context.CancelFunc
	calls  int
}

func (s *cancelOnReadStore) Get(keys ...string) ([]any, error) {
	s.calls++
	s.cancel()
	return s.mockStore.Get(keys...)
}

func (s *cancelOnReadStore) List() ([]any, error) {
	s.calls++
	s.cancel()
	return s.mockStore.List()
}

func TestContextReadsRejectResultsAfterCancellation(t *testing.T) {
	reads := map[string]func(context.Context, Store) ([]any, error){
		"get": func(ctx context.Context, store Store) ([]any, error) {
			return GetContext(ctx, store, "key")
		},
		"list": ListContext,
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			for phase, cancelBeforeRead := range map[string]bool{"before read": true, "during read": false} {
				t.Run(phase, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					store := &cancelOnReadStore{mockStore: newMockStore(), cancel: cancel}
					require.NoError(t, store.Add("value", []string{"key"}))
					wantCalls := 1
					if cancelBeforeRead {
						cancel()
						wantCalls = 0
					}
					items, err := read(ctx, store)
					require.ErrorIs(t, err, context.Canceled)
					require.Nil(t, items)
					require.Equal(t, wantCalls, store.calls)
				})
			}
		})
	}
}

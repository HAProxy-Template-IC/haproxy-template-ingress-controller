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

package store

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type cancelAwareDynamicClient struct {
	resource *cancelAwareResourceClient
}

func (c *cancelAwareDynamicClient) Resource(schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return c.resource
}

type cancelAwareResourceClient struct {
	dynamic.ResourceInterface
	started chan struct{}
	calls   atomic.Int32
}

func (c *cancelAwareResourceClient) Namespace(string) dynamic.ResourceInterface {
	return c
}

func (c *cancelAwareResourceClient) Get(ctx context.Context, _ string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	c.calls.Add(1)
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCachedStoreContextReadsCancelSequentialFetches(t *testing.T) {
	tests := []struct {
		name string
		read func(context.Context, *CachedStore) ([]any, error)
	}{
		{name: "get", read: func(ctx context.Context, store *CachedStore) ([]any, error) {
			return store.GetContext(ctx, "shared")
		}},
		{name: "list", read: func(ctx context.Context, store *CachedStore) ([]any, error) {
			return store.ListContext(ctx)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resourceClient := &cancelAwareResourceClient{started: make(chan struct{}, 3)}
			store, err := NewCachedStore(&CachedStoreConfig{
				NumKeys:   1,
				Client:    &cancelAwareDynamicClient{resource: resourceClient},
				GVR:       configMapGVR,
				Indexer:   createTestIndexer(),
				Projected: true,
			})
			require.NoError(t, err)

			for i, name := range []string{"one", "two", "three"} {
				require.NoError(t, store.Add(createTestResource("default", name), []string{"shared"}), "ref %d", i)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, readErr := test.read(ctx, store)
				done <- readErr
			}()

			select {
			case <-resourceClient.started:
			case <-time.After(2 * time.Second):
				t.Fatal("API fetch did not start")
			}
			cancel()

			select {
			case readErr := <-done:
				require.ErrorIs(t, readErr, context.Canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("context-aware store read did not stop after cancellation")
			}
			require.Equal(t, int32(1), resourceClient.calls.Load(), "cancellation must stop before the next matching reference")
		})
	}
}

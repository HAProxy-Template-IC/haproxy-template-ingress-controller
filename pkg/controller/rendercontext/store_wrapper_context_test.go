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
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type wrapperBlockingStore struct {
	started         chan struct{}
	release         chan struct{}
	startedOnce     sync.Once
	contextualCalls atomic.Int32
	legacyCalls     atomic.Int32
}

func (s *wrapperBlockingStore) wait(ctx context.Context) ([]any, error) {
	s.contextualCalls.Add(1)
	s.startedOnce.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return nil, nil
	}
}

func (s *wrapperBlockingStore) Get(...string) ([]any, error) {
	s.legacyCalls.Add(1)
	<-s.release
	return nil, nil
}

func (s *wrapperBlockingStore) GetContext(ctx context.Context, _ ...string) ([]any, error) {
	return s.wait(ctx)
}

func (s *wrapperBlockingStore) List() ([]any, error) {
	s.legacyCalls.Add(1)
	<-s.release
	return nil, nil
}

func (s *wrapperBlockingStore) ListContext(ctx context.Context) ([]any, error) {
	return s.wait(ctx)
}

func (s *wrapperBlockingStore) Add(any, []string) error               { return nil }
func (s *wrapperBlockingStore) Update(any, []string) error            { return nil }
func (s *wrapperBlockingStore) Delete(string, string, []string) error { return nil }
func (s *wrapperBlockingStore) Clear() error                          { return nil }

func TestStoreWrapperPropagatesReadContext(t *testing.T) {
	tests := []struct {
		name string
		run  func(*StoreWrapper)
	}{
		{name: "eager list", run: func(wrapper *StoreWrapper) { wrapper.List() }},
		{name: "lazy exact fetch", run: func(wrapper *StoreWrapper) { wrapper.Fetch("shared") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inner := &wrapperBlockingStore{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			var releaseOnce sync.Once
			releaseStore := func() { releaseOnce.Do(func() { close(inner.release) }) }
			defer releaseStore()

			adapter := &stores.TypesStoreAdapter{Inner: inner}
			composite := stores.NewCompositeStore(adapter, stores.NewStoreOverlay())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapper := &StoreWrapper{
				Store:        composite,
				ResourceType: "items",
				Logger:       testutil.NewTestLogger(),
				IndexBy:      []string{"metadata.labels.group"},
				LazySnapshot: test.name == "lazy exact fetch",
				readContext:  ctx,
			}

			done := make(chan struct{})
			go func() {
				test.run(wrapper)
				close(done)
			}()

			select {
			case <-inner.started:
			case <-time.After(2 * time.Second):
				releaseStore()
				t.Fatal("contextual store read did not start")
			}
			cancel()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				releaseStore()
				t.Fatal("StoreWrapper read did not stop after cancellation")
			}
			require.Equal(t, int32(1), inner.contextualCalls.Load())
			require.Equal(t, int32(0), inner.legacyCalls.Load())
		})
	}
}

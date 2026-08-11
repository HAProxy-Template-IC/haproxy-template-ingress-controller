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
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type renderBlockingStore struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
}

func (s *renderBlockingStore) signalStarted() {
	s.startedOnce.Do(func() { close(s.started) })
}

func (s *renderBlockingStore) Get(...string) ([]any, error) {
	s.signalStarted()
	<-s.release
	return nil, nil
}

func (s *renderBlockingStore) GetContext(ctx context.Context, _ ...string) ([]any, error) {
	s.signalStarted()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return nil, nil
	}
}

func (s *renderBlockingStore) List() ([]any, error) {
	return nil, nil
}

func (s *renderBlockingStore) ListContext(ctx context.Context) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *renderBlockingStore) Add(any, []string) error               { return nil }
func (s *renderBlockingStore) Update(any, []string) error            { return nil }
func (s *renderBlockingStore) Delete(string, string, []string) error { return nil }
func (s *renderBlockingStore) Clear() error                          { return nil }

func TestRenderCancelsOnDemandStoreFetch(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: `global
{% for _, item := range resources.items.Fetch("shared") %}
# {{ item }}
{% end %}
`},
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"items": {
				APIVersion: "v1",
				Resources:  "configmaps",
				IndexBy:    []string{"metadata.labels.group"},
				Store:      "on-demand",
			},
		},
	}
	decls := typebootstrap.BuildEngineDeclarations(&typebootstrap.Result{}, "items")
	engine, err := templating.New(
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		&templating.Options{EntryPoints: []string{"haproxy.cfg"}, Declarations: decls},
	)
	require.NoError(t, err)

	blocking := &renderBlockingStore{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseStore := func() { releaseOnce.Do(func() { close(blocking.release) }) }
	defer releaseStore()

	adapter := &stores.TypesStoreAdapter{Inner: blocking}
	composite := stores.NewCompositeStore(adapter, stores.NewStoreOverlay())
	svc := NewRenderService(&RenderServiceConfig{
		Engine: engine,
		Config: cfg,
		Logger: slog.Default(),
	})
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{"items": composite}}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, renderErr := svc.Render(ctx, provider, rendercontext.RenderModeReconcile)
		done <- renderErr
	}()

	select {
	case <-blocking.started:
	case renderErr := <-done:
		t.Fatalf("render returned before the store fetch started: %v", renderErr)
	case <-time.After(2 * time.Second):
		releaseStore()
		t.Fatal("render did not reach the on-demand store fetch")
	}

	cancel()
	select {
	case renderErr := <-done:
		require.Error(t, renderErr)
	case <-time.After(2 * time.Second):
		releaseStore()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("render did not stop after its context was canceled")
	}
}

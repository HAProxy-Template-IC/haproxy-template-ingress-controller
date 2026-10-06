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

package renderer

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const coldCompletionRenderTimeout = 300 * time.Millisecond

// coldCompletionFixture holds a render inside its store read until the test
// releases it, so a cold render costs whatever the test decides; renders after
// the release read without waiting.
type coldCompletionFixture struct {
	service  *RenderService
	provider stores.StoreProvider
	query    incremental.QueryKey
	entered  chan struct{}
	release  chan struct{}
}

type gatedPinStore struct {
	*k8sstore.MemoryStore
	fixture *coldCompletionFixture
}

func (s *gatedPinStore) Pin() (stores.ReadSnapshot, error) {
	select {
	case <-s.fixture.release:
	default:
		s.fixture.entered <- struct{}{}
		<-s.fixture.release
	}
	return s.MemoryStore.Pin()
}

func newColdCompletionFixture(t *testing.T, runToCompletion bool) *coldCompletionFixture {
	t.Helper()
	fixture := &coldCompletionFixture{
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
	cfg := &config.Config{
		Dataplane:          testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{RenderTimeout: coldCompletionRenderTimeout.String()},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template:    `{{ item | dig_string("", "metadata", "name") }}=ok` + "\n",
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	fixture.service = NewRenderService(&RenderServiceConfig{
		Engine:                     engine,
		Config:                     cfg,
		Logger:                     slog.Default(),
		ColdRendersRunToCompletion: runToCompletion,
	})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "route", map[string]any{}),
		[]string{"default", "route"},
	))
	fixture.provider = stores.NewRealStoreProvider(map[string]stores.Store{
		"routes": &gatedPinStore{MemoryStore: routes, fixture: fixture},
	})
	component := fixture.service.incremental.components["routes"]
	fixture.query = componentQueryKey(&component, "routes", "default", "route")
	t.Cleanup(fixture.releaseWork)
	return fixture
}

func (f *coldCompletionFixture) executions() uint64 {
	return f.service.incremental.graph.Counters(f.query).Executions
}

func (f *coldCompletionFixture) releaseWork() {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
}

func (f *coldCompletionFixture) awaitWork(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the cold render never reached the component")
	}
}

type renderOutcome struct {
	result *RenderResult
	err    error
}

func (f *coldCompletionFixture) renderAsync(ctx context.Context, mode rendercontext.RenderMode) <-chan renderOutcome {
	done := make(chan renderOutcome, 1)
	go func() {
		result, err := f.service.Render(ctx, f.provider, mode)
		done <- renderOutcome{result: result, err: err}
	}()
	return done
}

func awaitRender(t *testing.T, done <-chan renderOutcome) renderOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatal("render did not return")
		return renderOutcome{}
	}
}

func commitRender(t *testing.T, outcome renderOutcome) {
	t.Helper()
	require.NoError(t, outcome.err)
	require.NotNil(t, outcome.result.InputTransaction)
	require.NoError(t, outcome.result.InputTransaction.Commit(t.Context()))
}

// A cold render costs the full resource set, so it can exceed the render
// timeout that bounds a warm one. Retrying it under the same deadline repeats
// the same work and never commits (#285); running it to completion commits the
// graph, and the next render is warm and inside the timeout.
func TestColdReconcileRenderPastTheRenderTimeoutConverges(t *testing.T) {
	fixture := newColdCompletionFixture(t, true)

	done := fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile)
	fixture.awaitWork(t)
	time.Sleep(2 * coldCompletionRenderTimeout)
	fixture.releaseWork()
	commitRender(t, awaitRender(t, done))
	require.True(t, fixture.service.IncrementalGraphWarm(), "the committed cold render must leave a graph behind")
	assert.True(t, fixture.service.FirstGraphPublished())

	warm := awaitRender(t, fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile))
	commitRender(t, warm)
	assert.Equal(t, "route=ok\n", warm.result.HAProxyConfig)
	assert.NotEqual(t, "cold", warm.result.CacheState)
	assert.Equal(t, uint64(1), fixture.executions(), "the warm render must reuse the component")
}

// Without the opt-in a cold render keeps the render timeout, which callers
// without a shutdown-scoped context, such as the playground, rely on to stop
// a template that never returns.
func TestColdRenderKeepsTheRenderTimeoutWithoutOptIn(t *testing.T) {
	fixture := newColdCompletionFixture(t, false)

	done := fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile)
	fixture.awaitWork(t)
	time.Sleep(2 * coldCompletionRenderTimeout)
	fixture.releaseWork()
	outcome := awaitRender(t, done)
	require.Error(t, outcome.err)
	assert.ErrorIs(t, outcome.err, context.DeadlineExceeded)
}

// Two cold renders on one replica double its memory while neither has a graph
// to share. The second reconcile render waits for the first to commit and then
// builds on its graph instead of repeating the cold work.
func TestSecondReconcileRenderWaitsForTheColdOneAndRendersWarm(t *testing.T) {
	fixture := newColdCompletionFixture(t, true)

	first := fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile)
	fixture.awaitWork(t)
	second := fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile)
	select {
	case <-fixture.entered:
		t.Fatal("a second cold render ran alongside the first")
	case outcome := <-second:
		t.Fatalf("the second render returned before the cold one committed: %v", outcome.err)
	case <-time.After(2 * coldCompletionRenderTimeout):
	}
	fixture.releaseWork()
	commitRender(t, awaitRender(t, first))

	outcome := awaitRender(t, second)
	commitRender(t, outcome)
	assert.NotEqual(t, "cold", outcome.result.CacheState)
	assert.Equal(t, uint64(1), fixture.executions())
}

// Admission waits for the cold reconcile render in flight instead of starting
// a cold render of its own. A request whose budget ends first is denied with a
// message that says why and what to do.
func TestAdmissionWaitsForTheColdReconcileRender(t *testing.T) {
	fixture := newColdCompletionFixture(t, true)

	reconcile := fixture.renderAsync(t.Context(), rendercontext.RenderModeReconcile)
	fixture.awaitWork(t)

	expiring, cancel := context.WithTimeout(t.Context(), coldCompletionRenderTimeout/3)
	defer cancel()
	denied := awaitRender(t, fixture.renderAsync(expiring, rendercontext.RenderModeAdmission))
	require.Error(t, denied.err)
	assert.Contains(t, denied.err.Error(), "still building its first render")
	assert.NotErrorIs(t, denied.err, context.DeadlineExceeded,
		"the explanation must survive the pipeline's cancellation wrapping")

	admission := fixture.renderAsync(t.Context(), rendercontext.RenderModeAdmission)
	fixture.releaseWork()
	commitRender(t, awaitRender(t, reconcile))
	admitted := awaitRender(t, admission)
	require.NoError(t, admitted.err)
	assert.Equal(t, "route=ok\n", admitted.result.HAProxyConfig)
	assert.Equal(t, uint64(1), fixture.executions(), "admission must not run the cold work itself")
}

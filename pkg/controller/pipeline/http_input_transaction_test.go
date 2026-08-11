// Copyright 2026 Philipp Hossner
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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestInitialHTTPInputCommitsOnlyAfterFullPipelineValidation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "candidate-%d", requests.Add(1))
	}))
	defer server.Close()

	checks := atomic.Int32{}
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(func(string, []string) ([]byte, error) {
		if checks.Add(1) == 1 {
			return []byte("[ALERT] config : rejected candidate\n"), errors.New("exit status 1")
		}
		return nil, nil
	})))

	pipeline, component := newHTTPInputPipeline(t, httpTemplate(server.URL), nil)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, err := pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.Error(t, err)
	assert.Nil(t, result)
	_, accepted := component.GetStore().Get(server.URL)
	assert.False(t, accepted)
	assert.Nil(t, currentHTTPRefresher(component, server.URL))

	result, err = pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result)
	acceptedContent, accepted := component.GetStore().Get(server.URL)
	require.True(t, accepted)
	assert.Equal(t, "candidate-2", acceptedContent)
	assert.Equal(t, int32(2), requests.Load())

	_, err = pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, int32(2), requests.Load())
}

func TestInitialHTTPInputAbortsAfterOutputValidationFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "candidate-%d", requests.Add(1))
	}))
	defer server.Close()

	outputValidator := &recordingOutputValidator{err: errors.New("payload rejected")}
	pipeline, component := newHTTPInputPipeline(t, httpTemplate(server.URL), outputValidator)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	_, err := pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.Error(t, err)
	_, accepted := component.GetStore().Get(server.URL)
	assert.False(t, accepted)

	pipeline.outputValidator = nil
	_, err = pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	acceptedContent, accepted := component.GetStore().Get(server.URL)
	require.True(t, accepted)
	assert.Equal(t, "candidate-2", acceptedContent)
	assert.Equal(t, int32(2), requests.Load())
}

func TestInitialHTTPInputAbortsWhenValidationAuthorityExpires(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancelCause(t.Context())
	authorityErr := errors.New("authority expired")
	outputValidator := &cancelingOutputValidator{cancel: cancel, cause: authorityErr}
	pipeline, component := newHTTPInputPipeline(t, httpTemplate(server.URL), outputValidator)

	_, err := pipeline.Execute(
		ctx,
		&mockStoreProvider{storeMap: map[string]stores.Store{}},
		rendercontext.RenderModeReconcile,
	)
	require.ErrorIs(t, err, authorityErr)
	_, accepted := component.GetStore().Get(server.URL)
	assert.False(t, accepted)
}

func TestRenderFailureAbortsInitialHTTPInput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	template := httpTemplate(server.URL) + `{{ fail("after fetch") }}`
	pipeline, component := newHTTPInputPipeline(t, template, nil)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	_, err := pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "after fetch")
	_, accepted := component.GetStore().Get(server.URL)
	assert.False(t, accepted)

	_, err = pipeline.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "after fetch")
	assert.Equal(t, int32(2), requests.Load())
}

func TestReadOnlyHTTPInputPipelineHasNoTransaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	t.Run("success", func(t *testing.T) {
		pipeline, component := newHTTPInputPipeline(t, httpTemplate(server.URL), nil)
		result, err := pipeline.Execute(t.Context(), provider, rendercontext.RenderModeAdmission)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Nil(t, component.GetStore().GetEntry(server.URL))
	})

	t.Run("render error", func(t *testing.T) {
		pipeline, component := newHTTPInputPipeline(
			t,
			httpTemplate(server.URL)+`{{ fail("after fetch") }}`,
			nil,
		)
		result, err := pipeline.Execute(t.Context(), provider, rendercontext.RenderModeAdmission)
		require.ErrorContains(t, err, "after fetch")
		assert.Nil(t, result)
		assert.Nil(t, component.GetStore().GetEntry(server.URL))
	})
}

func newHTTPInputPipeline(
	t *testing.T,
	template string,
	outputValidator RenderedOutputValidator,
) (*Pipeline, *controllerhttpstore.Component) {
	t.Helper()

	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, 0)
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)
	cfg := &config.Config{HAProxyConfig: config.HAProxyConfig{Template: template}}
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:             engine,
		Config:             cfg,
		Logger:             slog.Default(),
		Capabilities:       defaultCapabilities(),
		HTTPStoreComponent: component,
	})
	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})
	return New(&PipelineConfig{
		Renderer:        renderService,
		Validator:       validationService,
		OutputValidator: outputValidator,
		Logger:          slog.Default(),
	}), component
}

func httpTemplate(url string) string {
	return testutil.MinimalHAProxyConfig + fmt.Sprintf("\n# {{ http.Fetch(%q, map[string]any{\"critical\": true}) }}\n", url)
}

func currentHTTPRefresher(component *controllerhttpstore.Component, url string) any {
	state, exists := component.GetStore().GetSourceState(url)
	if !exists || state.Delay == 0 || !state.HasAccepted {
		return nil
	}
	return state
}

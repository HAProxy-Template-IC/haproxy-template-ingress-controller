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

package pipeline

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// mockStoreProvider implements stores.StoreProvider for testing.
type mockStoreProvider struct {
	storeMap map[string]stores.Store
}

func (m *mockStoreProvider) GetStore(name string) stores.Store {
	return m.storeMap[name]
}

func (m *mockStoreProvider) StoreNames() []string {
	names := make([]string, 0, len(m.storeMap))
	for name := range m.storeMap {
		names = append(names, name)
	}
	return names
}

func defaultCapabilities() dataplane.Capabilities {
	return dataplane.Capabilities{
		SupportsCrtList:        true,
		SupportsMapStorage:     true,
		SupportsGeneralStorage: true,
	}
}

func createTestPipeline(t *testing.T, template string) *Pipeline {
	t.Helper()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: template,
		},
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": template},
		nil, nil, nil)
	require.NoError(t, err)

	renderSvc := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	validationSvc := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	return New(&PipelineConfig{
		Renderer:  renderSvc,
		Validator: validationSvc,
		Logger:    slog.Default(),
	})
}

func TestNew(t *testing.T) {
	template := "global\n    daemon\n"

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: template,
		},
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": template},
		nil, nil, nil)
	require.NoError(t, err)

	renderSvc := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	validationSvc := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger: slog.Default(),
	})

	pipeline := New(&PipelineConfig{
		Renderer:  renderSvc,
		Validator: validationSvc,
		Logger:    slog.Default(),
	})

	require.NotNil(t, pipeline)
	assert.NotNil(t, pipeline.renderer)
	assert.NotNil(t, pipeline.validator)
	assert.NotNil(t, pipeline.logger)
}

func TestPipeline_Execute_ValidConfig(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	pipeline := createTestPipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, err := pipeline.Execute(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.HAProxyConfig, "global")
	assert.Contains(t, result.HAProxyConfig, "frontend http_front")
	assert.Contains(t, result.HAProxyConfig, "backend http_back")
	assert.GreaterOrEqual(t, result.RenderDurationMs, int64(0))
	assert.GreaterOrEqual(t, result.ValidateDurationMs, int64(0))
	assert.GreaterOrEqual(t, result.TotalDurationMs, int64(0))
}

func TestPipeline_Execute_InvalidConfig(t *testing.T) {
	// This template produces invalid HAProxy configuration
	template := `global
    daemon

defaults
    invalid_directive
`

	pipeline := createTestPipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, err := pipeline.Execute(context.Background(), provider)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestPipeline_ExecuteWithResult_ValidConfig(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	pipeline := createTestPipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, valResult, err := pipeline.ExecuteWithResult(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, valResult)
	assert.True(t, valResult.Valid)
	assert.Contains(t, result.HAProxyConfig, "global")
}

func TestPipeline_ExecuteWithResult_InvalidConfig(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    acl test hdr(host) -f maps/nonexistent.map
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	pipeline := createTestPipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, valResult, err := pipeline.ExecuteWithResult(context.Background(), provider)

	require.NoError(t, err) // No render error
	require.NotNil(t, result)
	require.NotNil(t, valResult)
	assert.False(t, valResult.Valid)
	assert.NotNil(t, valResult.Error)
	assert.Equal(t, "semantic", valResult.Phase)
}

func TestPipeline_RenderOnly(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
`

	pipeline := createTestPipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}

	result, err := pipeline.RenderOnly(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.HAProxyConfig, "global")
	assert.Contains(t, result.HAProxyConfig, "defaults")
}

func TestPipeline_ValidateConfig(t *testing.T) {
	template := "global\n    daemon\n"
	pipeline := createTestPipeline(t, template)

	validConfig := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	result := pipeline.ValidateConfig(context.Background(), validConfig, nil)

	require.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
}

func TestPipeline_ValidateConfig_Invalid(t *testing.T) {
	template := "global\n    daemon\n"
	pipeline := createTestPipeline(t, template)

	invalidConfig := `global
    daemon

defaults
    unknown_option
`

	result := pipeline.ValidateConfig(context.Background(), invalidConfig, nil)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.NotNil(t, result.Error)
}

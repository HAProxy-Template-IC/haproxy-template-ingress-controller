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

package proposalvalidator

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func defaultCapabilities() dataplane.Capabilities {
	return dataplane.Capabilities{
		SupportsCrtList:        true,
		SupportsMapStorage:     true,
		SupportsGeneralStorage: true,
	}
}

func createTestPipeline(t *testing.T, template string) *pipeline.Pipeline {
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

	return pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderSvc,
		Validator: validationSvc,
		Logger:    slog.Default(),
	})
}

func TestNew(t *testing.T) {
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

	bus := busevents.NewEventBus(100)
	testPipeline := createTestPipeline(t, template)
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})

	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          testPipeline,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	require.NotNil(t, component)
	assert.NotNil(t, component.eventBus)
	assert.NotNil(t, component.pipeline)
	assert.NotNil(t, component.baseStore)
	assert.NotNil(t, component.logger)
}

func TestComponent_ValidateSync_ValidConfig(t *testing.T) {
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

	bus := busevents.NewEventBus(100)
	pipelineInstance := createTestPipeline(t, template)
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})

	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	// Empty overlays should result in valid config
	overlays := map[string]*stores.StoreOverlay{}

	result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Phase)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
}

func TestComponent_ValidateSync_InvalidOverlay(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
`

	bus := busevents.NewEventBus(100)
	pipelineInstance := createTestPipeline(t, template)
	// Empty base store - no stores registered
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})

	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	// Overlay references non-existent store
	overlays := map[string]*stores.StoreOverlay{
		"nonexistent": stores.NewStoreOverlay(),
	}

	result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.Equal(t, "setup", result.Phase)
	assert.NotNil(t, result.Error)
}

func TestValidationResult_ErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		result   *validation.ValidationResult
		expected string
	}{
		{
			name: "valid result returns empty",
			result: &validation.ValidationResult{
				Valid: true,
			},
			expected: "",
		},
		{
			name: "invalid with error returns message",
			result: &validation.ValidationResult{
				Valid: false,
				Error: assert.AnError,
			},
			expected: assert.AnError.Error(),
		},
		{
			name: "invalid with nil error returns empty",
			result: &validation.ValidationResult{
				Valid: false,
				Error: nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.ErrorMessage())
		})
	}
}

func TestComponent_Start_ProcessesEvents(t *testing.T) {
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

	bus := busevents.NewEventBus(100)
	pipelineInstance := createTestPipeline(t, template)
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})

	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	// Subscribe to completion events
	resultChan := bus.Subscribe("test", 10)

	// Start event bus and component
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = component.Start(ctx)
	}()

	// Allow component to start
	time.Sleep(50 * time.Millisecond)

	// Publish validation request (nil HTTP overlay for K8s-only validation)
	req := events.NewProposalValidationRequestedEvent(
		map[string]*stores.StoreOverlay{},
		nil, // no HTTP overlay
		"test",
		"test context",
	)
	bus.Publish(req)

	// Wait for completion event
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-resultChan:
			if completed, ok := event.(*events.ProposalValidationCompletedEvent); ok {
				assert.Equal(t, req.ID, completed.RequestID)
				assert.True(t, completed.Valid)
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for validation completion event")
		}
	}
}

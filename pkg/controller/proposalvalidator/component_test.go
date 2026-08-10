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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
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

	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
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

func createStoreTestPipeline(t *testing.T, template string) *pipeline.Pipeline {
	t.Helper()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: template},
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses"},
		},
	}
	declarations := typebootstrap.BuildEngineDeclarations(&typebootstrap.Result{}, "ingresses")
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, &templating.Options{
		EntryPoints:  []string{"haproxy.cfg"},
		Declarations: declarations,
	})
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
	template := testutil.MinimalHAProxyConfig

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
	assert.NotNil(t, component.Base, "async mode must embed the component.Base event loop")
	assert.NotNil(t, component.pipeline)
	assert.NotNil(t, component.baseStore)
	assert.NotNil(t, component.logger)
}

func TestComponent_ValidateSync_ValidConfig(t *testing.T) {
	template := testutil.MinimalHAProxyConfig

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

	pipelineResult, result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Phase)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
	// Successful validation must populate the pipeline result so callers
	// can feed the rendered files to downstream consumers (e.g. pluggable
	// validators in dryrunvalidator).
	require.NotNil(t, pipelineResult)
	assert.NotEmpty(t, pipelineResult.HAProxyConfig)
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

	pipelineResult, result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.Equal(t, "setup", result.Phase)
	assert.NotNil(t, result.Error)
	assert.Nil(t, pipelineResult, "failed validation must not leak a partial pipeline result")
}

func TestComponent_ValidateSync_UnchangedInvalidContent_Admits(t *testing.T) {
	t.Cleanup(dataplanetest.InstallFakeHAProxy(
		dataplanetest.WithRejectAll("parsing [haproxy.cfg:3] : unknown keyword 'nosuch_directive_haproxy_will_reject'")))
	template := `global
    daemon
    nosuch_directive_haproxy_will_reject this_is_an_alert_trigger
`

	bus := busevents.NewEventBus(100)
	pipelineInstance := createTestPipeline(t, template)
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": &storetest.MockStore{}})

	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	overlays := map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "unrelated")),
	}
	pipelineResult, result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Phase)
	assert.Nil(t, result.Error)
	require.NotNil(t, pipelineResult)
	assert.NotEmpty(t, pipelineResult.ContentChecksum)
}

func TestComponent_ValidateSync_AdmissionSubjectOnlyDifference_Admits(t *testing.T) {
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithRejectAll("invalid configuration")))
	template := `global
    daemon
# subject: {{ admissionSubject | dig("name") | fallback("") }}
`

	component := New(&ComponentConfig{
		Pipeline:          createTestPipeline(t, template),
		BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": &storetest.MockStore{}}),
		Logger:            slog.Default(),
		SyncOnly:          true,
	})
	overlays := map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "new-failure")),
	}

	pipelineResult, result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Phase)
	assert.NoError(t, result.Error)
	require.NotNil(t, pipelineResult)
}

func TestComponent_ValidateSync_ChangedInvalidContent_Denies(t *testing.T) {
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithRejectAll("invalid configuration")))
	template := `global
    daemon
# ingress-count: {{ len(resources.ingresses.List()) }}
`

	component := New(&ComponentConfig{
		Pipeline:          createStoreTestPipeline(t, template),
		BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": &storetest.MockStore{}}),
		Logger:            slog.Default(),
		SyncOnly:          true,
	})
	overlays := map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "new-failure")),
	}

	pipelineResult, result := component.ValidateSync(context.Background(), overlays)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.Equal(t, "semantic", result.Phase)
	assert.Error(t, result.Error)
	assert.Nil(t, pipelineResult)
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

// HTTP proposals always deny invalid candidate content instead of comparing
// it with the live Kubernetes-only baseline.
func TestComponent_Start_AsyncPath_DeniesOnFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	failingEngine, err := templating.New(map[string]string{"haproxy.cfg": `{{ fail("pretend HTTP content is bad") }}`}, nil)
	require.NoError(t, err)

	renderSvc := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:       failingEngine,
		Config:       &config.Config{},
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})
	validationSvc := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})
	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderSvc,
		Validator: validationSvc,
		Logger:    slog.Default(),
	})

	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})
	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	resultChan := bus.Subscribe("test", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = component.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	req := events.NewProposalValidationRequestedEvent(
		map[string]*stores.StoreOverlay{},
		nil,
		"http-store",
		"pending-content-promotion",
	)
	bus.Publish(req)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-resultChan:
			completed, ok := event.(*events.ProposalValidationCompletedEvent)
			if !ok {
				continue
			}
			if completed.RequestID != req.ID {
				continue
			}
			assert.False(t, completed.Valid,
				"async path MUST publish Valid=false on validation failure — "+
					"HTTPStore.handleProposalValidationCompleted promotes pending "+
					"content on Valid=true, so admitting here would compound broken "+
					"state by promoting BAD content into the live config; phase=%q err=%q",
				completed.Phase, completed.Error)
			return
		case <-deadline:
			t.Fatal("timeout waiting for validation completion event — async handler may not be running, " +
				"or it published nothing (which would also be a regression)")
		}
	}
}

func TestComponent_Start_ProcessesEvents(t *testing.T) {
	template := testutil.MinimalHAProxyConfig

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

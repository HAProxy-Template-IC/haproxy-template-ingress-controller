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

package controller

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// testEndpoint implements fmt.Stringer for endpoint URL conversion.
type testEndpoint struct {
	url string
}

func (e *testEndpoint) String() string {
	return e.url
}

func TestNewStateCache(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()

	cache := NewStateCache(bus, nil, logger)

	require.NotNil(t, cache)
	assert.NotNil(t, cache.eventChan)
	assert.Equal(t, bus, cache.eventBus)
	assert.Nil(t, cache.resourceWatcher)
}

func TestStateCache_Start_ContextCancellation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- cache.Start(ctx)
	}()

	// Allow time for goroutine to start
	time.Sleep(10 * time.Millisecond)

	// Cancel context
	cancel()

	// Should exit gracefully
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Start did not exit after context cancellation")
	}
}

func TestStateCache_HandleConfigValidated(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Create test config with a simple field
	testConfig := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {APIVersion: "v1", Resources: "services"},
		},
	}

	// Publish event
	bus.Publish(events.NewConfigValidatedEvent(testConfig, nil, "v123", ""))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify state was updated
	cfg, version, err := cache.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, testConfig, cfg)
	assert.Equal(t, "v123", version)
}

func TestStateCache_HandleConfigValidated_WrongType(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish event with wrong config type
	bus.Publish(events.NewConfigValidatedEvent("not a config", nil, "v123", ""))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Config should remain nil
	_, _, err := cache.GetConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config not loaded yet")
}

func TestStateCache_HandleCredentialsUpdated(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Create test credentials
	testCreds := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret123",
	}

	// Publish event
	bus.Publish(events.NewCredentialsUpdatedEvent(testCreds, "secret-v456"))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify state was updated
	creds, version, err := cache.GetCredentials()
	require.NoError(t, err)
	assert.Equal(t, testCreds, creds)
	assert.Equal(t, "secret-v456", version)
}

func TestStateCache_HandleCredentialsUpdated_WrongType(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish event with wrong credentials type
	bus.Publish(events.NewCredentialsUpdatedEvent("not credentials", "v456"))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Credentials should remain nil
	_, _, err := cache.GetCredentials()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credentials not loaded yet")
}

func TestStateCache_HandleTemplateRendered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	testConfig := "global\n  maxconn 2000\n"
	testAuxFiles := &dataplane.AuxiliaryFiles{
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/etc/haproxy/certs/cert.pem", Content: "cert-content"},
		},
	}

	// Publish event
	bus.Publish(events.NewTemplateRenderedEvent(
		testConfig,
		testConfig,
		nil,
		testAuxFiles,
		nil,
		1,
		100,
		"",
		true, // coalescible
	))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify rendered config was updated
	rendered, timestamp, err := cache.GetRenderedConfig()
	require.NoError(t, err)
	assert.Equal(t, testConfig, rendered)
	assert.False(t, timestamp.IsZero())

	// Verify aux files were updated
	auxFiles, auxTime, err := cache.GetAuxiliaryFiles()
	require.NoError(t, err)
	assert.Equal(t, testAuxFiles, auxFiles)
	assert.False(t, auxTime.IsZero())

	// Verify render status was updated
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Rendering)
	assert.Equal(t, statusSucceeded, status.Rendering.Status)
}

func TestStateCache_HandleTemplateRenderFailed(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish render failed event
	bus.Publish(events.NewTemplateRenderFailedEvent("haproxy.cfg", "template error", "stack trace"))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify render status shows failure
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Rendering)
	assert.Equal(t, statusFailed, status.Rendering.Status)
	assert.Equal(t, "template error", status.Rendering.Error)
}

func TestStateCache_HandleReconciliationTriggered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish reconciliation triggered event
	bus.Publish(events.NewReconciliationTriggeredEvent("config_change", true))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify trigger was recorded
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.LastTrigger)
	assert.Equal(t, "config_change", status.LastTrigger.Reason)
	assert.False(t, status.LastTrigger.Timestamp.IsZero())
}

func TestStateCache_HandleValidationStarted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish validation started event
	bus.Publish(events.NewValidationStartedEvent())

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify validation status is pending
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, statusPending, status.Validation.Status)
}

func TestStateCache_HandleValidationCompleted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// First set rendered config (validation stores this as validated config)
	testConfig := "global\n  daemon\n"
	bus.Publish(events.NewTemplateRenderedEvent(testConfig, testConfig, nil, nil, nil, 0, 100, "", true))
	time.Sleep(50 * time.Millisecond)

	// Publish validation completed event
	bus.Publish(events.NewValidationCompletedEvent([]string{"warning1"}, 150, "", true))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify validation status is succeeded
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, statusSucceeded, status.Validation.Status)
	assert.Equal(t, int64(150), status.Validation.DurationMs)
	assert.Equal(t, []string{"warning1"}, status.Validation.Warnings)

	// Verify validated config was stored
	validatedInfo, err := cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, testConfig, validatedInfo.Config)
	assert.Equal(t, int64(150), validatedInfo.ValidationDurationMs)
}

func TestStateCache_HandleValidationFailed(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish validation failed event
	bus.Publish(events.NewValidationFailedEvent([]string{"[ALERT] parsing error"}, 50, ""))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify validation status is failed
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, statusFailed, status.Validation.Status)
	assert.Equal(t, []string{"[ALERT] parsing error"}, status.Validation.Errors)

	// Verify deployment was marked as skipped
	require.NotNil(t, status.Deployment)
	assert.Equal(t, statusSkipped, status.Deployment.Status)
	assert.Equal(t, "validation_failed", status.Deployment.Reason)
}

func TestStateCache_HandleDeploymentStarted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	endpoints := []interface{}{
		&testEndpoint{url: "http://haproxy1:5555"},
		&testEndpoint{url: "http://haproxy2:5555"},
	}

	// Publish deployment started event
	bus.Publish(events.NewDeploymentStartedEvent(endpoints))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify deployment status is pending
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Deployment)
	assert.Equal(t, statusPending, status.Deployment.Status)
	assert.Equal(t, 2, status.Deployment.EndpointsTotal)
}

func TestStateCache_HandleDeploymentCompleted_AllSucceeded(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - all succeeded
	bus.Publish(events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      2,
		Succeeded:  2,
		Failed:     0,
		DurationMs: 500,
	}))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify deployment status is succeeded
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Deployment)
	assert.Equal(t, statusSucceeded, status.Deployment.Status)
	assert.Equal(t, 2, status.Deployment.EndpointsTotal)
	assert.Equal(t, 2, status.Deployment.EndpointsSucceeded)
	assert.Equal(t, 0, status.Deployment.EndpointsFailed)
}

func TestStateCache_HandleDeploymentCompleted_Partial(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - partial success
	bus.Publish(events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      3,
		Succeeded:  2,
		Failed:     1,
		DurationMs: 500,
	}))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify deployment status is partial
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Deployment)
	assert.Equal(t, statusPartial, status.Deployment.Status)
}

func TestStateCache_HandleDeploymentCompleted_AllFailed(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - all failed
	bus.Publish(events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      2,
		Succeeded:  0,
		Failed:     2,
		DurationMs: 500,
	}))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify deployment status is failed
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Deployment)
	assert.Equal(t, statusFailed, status.Deployment.Status)
}

func TestStateCache_HandleInstanceDeploymentFailed(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	endpoints := []interface{}{&testEndpoint{url: "http://haproxy1:5555"}}
	endpoint := &testEndpoint{url: "http://haproxy1:5555"}

	// Start deployment first (sets deploymentStatus to "pending")
	bus.Publish(events.NewDeploymentStartedEvent(endpoints))
	time.Sleep(50 * time.Millisecond)

	// Publish instance deployment failed event
	bus.Publish(events.NewInstanceDeploymentFailedEvent(endpoint, "connection refused", true))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify failed endpoint was recorded
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Deployment)
	require.Len(t, status.Deployment.FailedEndpoints, 1)
	assert.Equal(t, "http://haproxy1:5555", status.Deployment.FailedEndpoints[0].URL)
	assert.Equal(t, "connection refused", status.Deployment.FailedEndpoints[0].Error)
}

func TestStateCache_GetConfig_NotLoaded(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, _, err := cache.GetConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config not loaded yet")
}

func TestStateCache_GetCredentials_NotLoaded(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, _, err := cache.GetCredentials()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials not loaded yet")
}

func TestStateCache_GetRenderedConfig_NotRendered(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, _, err := cache.GetRenderedConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no config rendered yet")
}

func TestStateCache_GetAuxiliaryFiles_NotAvailable(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	// Returns empty structure when not available
	auxFiles, _, err := cache.GetAuxiliaryFiles()
	require.NoError(t, err)
	assert.NotNil(t, auxFiles)
}

func TestStateCache_GetResourceCounts_NoWatcher(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, err := cache.GetResourceCounts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource watcher not initialized")
}

func TestStateCache_GetResourcesByType_NoWatcher(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, err := cache.GetResourcesByType("ingresses")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource watcher not initialized")
}

func TestStateCache_GetPipelineStatus_Empty(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	assert.Nil(t, status.LastTrigger)
	assert.Nil(t, status.Rendering)
	assert.Nil(t, status.Validation)
	assert.Nil(t, status.Deployment)
}

func TestStateCache_GetValidatedConfig_NotValidated(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	_, err := cache.GetValidatedConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no config validated yet")
}

func TestStateCache_GetErrors_NoErrors(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	summary, err := cache.GetErrors()
	require.NoError(t, err)
	assert.Nil(t, summary.TemplateRenderError)
	assert.Nil(t, summary.HAProxyValidationError)
	assert.Empty(t, summary.DeploymentErrors)
}

func TestStateCache_GetErrors_RenderError(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Trigger render failure
	bus.Publish(events.NewTemplateRenderFailedEvent("haproxy.cfg", "syntax error", ""))

	time.Sleep(50 * time.Millisecond)

	summary, err := cache.GetErrors()
	require.NoError(t, err)
	require.NotNil(t, summary.TemplateRenderError)
	assert.Equal(t, []string{"syntax error"}, summary.TemplateRenderError.Errors)
}

func TestStateCache_GetErrors_ValidationError(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Trigger validation failure
	bus.Publish(events.NewValidationFailedEvent([]string{"[ALERT] parsing error"}, 50, ""))

	time.Sleep(50 * time.Millisecond)

	summary, err := cache.GetErrors()
	require.NoError(t, err)
	require.NotNil(t, summary.HAProxyValidationError)
	assert.Equal(t, []string{"[ALERT] parsing error"}, summary.HAProxyValidationError.Errors)
}

func TestStateCache_GetErrors_DeploymentErrors(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Trigger deployment failure
	endpoint := &testEndpoint{url: "http://haproxy1:5555"}
	bus.Publish(events.NewInstanceDeploymentFailedEvent(endpoint, "connection refused", true))

	time.Sleep(50 * time.Millisecond)

	summary, err := cache.GetErrors()
	require.NoError(t, err)
	require.Len(t, summary.DeploymentErrors, 1)
	assert.Equal(t, []string{"connection refused"}, summary.DeploymentErrors[0].Errors)
}

func TestStateCache_ImplementsStateProvider(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	// Compile-time check is in the file, but let's verify at runtime too
	var _ debug.StateProvider = cache
}

func TestStateCache_ReconciliationResetsPipelineState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cache.Start(ctx)
	bus.Start()

	// Set up some pipeline state
	bus.Publish(events.NewTemplateRenderedEvent("config", "config", nil, nil, nil, 0, 100, "", true))
	bus.Publish(events.NewValidationCompletedEvent(nil, 50, "", true))
	bus.Publish(events.NewDeploymentCompletedEvent(events.DeploymentResult{
		Total:      2,
		Succeeded:  2,
		Failed:     0,
		DurationMs: 500,
	}))
	bus.Publish(events.NewInstanceDeploymentFailedEvent(&testEndpoint{url: "http://fail:5555"}, "error", false))

	time.Sleep(50 * time.Millisecond)

	// Verify state is set
	status, _ := cache.GetPipelineStatus()
	assert.NotNil(t, status.Rendering)
	assert.NotNil(t, status.Validation)
	assert.NotNil(t, status.Deployment)
	assert.NotEmpty(t, status.Deployment.FailedEndpoints)

	// Trigger new reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("config_change", true))

	time.Sleep(50 * time.Millisecond)

	// Verify pipeline state was reset
	status, _ = cache.GetPipelineStatus()
	assert.NotNil(t, status.LastTrigger) // Trigger should be set
	assert.Nil(t, status.Rendering)      // Render status reset
	assert.Nil(t, status.Validation)     // Validation status reset
	assert.Nil(t, status.Deployment)     // Deployment status reset (nil because status is "")
}

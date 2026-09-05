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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	controllertestutil "gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// testEndpoint implements fmt.Stringer for endpoint URL conversion.
type testEndpoint struct {
	url string
}

func (e *testEndpoint) String() string {
	return e.url
}

func newStateCacheRenderedEvent(
	tb testing.TB,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
) *events.TemplateRenderedEvent {
	tb.Helper()
	const durationMs = 100
	fixture := controllertestutil.NewRenderCycleFixture(tb)
	artifacts := fixture.Artifacts(tb, auxFiles, nil)
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	if auxFiles != nil {
		for index := range auxFiles.SSLCertificates {
			file := auxFiles.SSLCertificates[index]
			plan.Files = append(plan.Files, renderplan.File{
				Path: file.Path, Kind: renderplan.FileKindCert,
				Content: file.Content, ContentKnown: true,
				Digest: renderplan.DigestString(file.Content), Size: int64(len(file.Content)),
			})
		}
	}
	plan.ComputeID()
	cycle := fixture.SnapshotWithEffects(tb, config, plan, artifacts, nil, nil, nil, nil)
	event, err := events.NewTemplateRenderedEventWithCycle(cycle, durationMs, "test", true)
	require.NoError(tb, err)
	return event
}

func mustRenderOccurrence(
	tb testing.TB,
	event *events.TemplateRenderedEvent,
) *rendercycle.Occurrence {
	tb.Helper()
	occurrence, err := event.RenderOccurrence()
	require.NoError(tb, err)
	return occurrence
}

func newStateCacheGateEvent(
	tb testing.TB,
	rendered *events.TemplateRenderedEvent,
	newest bool,
	durationMs int64,
) *events.RenderGateCompletedEvent {
	tb.Helper()
	const refused = false
	event, err := events.NewRenderGateCompletedEventWithCycle(
		mustRenderOccurrence(tb, rendered), true, refused, newest, "", false, durationMs,
	)
	require.NoError(tb, err)
	return event
}

func TestNewStateCache(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()

	cache := NewStateCache(bus, nil, logger)

	require.NotNil(t, cache)
	assert.NotNil(t, cache.Base)
	assert.Equal(t, bus, cache.EventBus())
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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Create test config with a simple field
	testConfig := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {APIVersion: "v1", Resources: "services"},
		},
	}

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

	ctx := t.Context()

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Create test credentials
	testCreds := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret123",
	}

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

	ctx := t.Context()

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	testConfig := "global\n  maxconn 2000\n"
	testAuxFiles := &dataplane.AuxiliaryFiles{
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/etc/haproxy/certs/cert.pem", Content: "cert-content"},
		},
	}

	bus.Publish(newStateCacheRenderedEvent(t, testConfig, testAuxFiles))

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

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

func TestStateCache_HandleRenderGateCompleted(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// First set rendered config (validation stores this as validated config)
	testConfig := "global\n  daemon\n"
	rendered := newStateCacheRenderedEvent(t, testConfig, nil)
	bus.Publish(rendered)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(newStateCacheGateEvent(t, rendered, true, 150))

	// Allow time for event processing
	time.Sleep(50 * time.Millisecond)

	// Verify validation status is succeeded
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, statusSucceeded, status.Validation.Status)
	assert.Equal(t, int64(150), status.Validation.DurationMs)

	// Verify validated config was stored
	validatedInfo, err := cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, testConfig, validatedInfo.Config)
	assert.Equal(t, int64(150), validatedInfo.ValidationDurationMs)
}

// A verdict for a plan the cache has moved past reports which plan it judged
// and leaves the validated config on the render it actually describes.
func TestStateCache_RenderGateVerdictForASupersededPlan(t *testing.T) {
	cache := NewStateCache(busevents.NewEventBus(100), nil, slog.Default())

	first := newStateCacheRenderedEvent(t, "global\n  daemon\n", nil)
	cache.handleTemplateRendered(first)
	cache.handleRenderGateCompleted(newStateCacheGateEvent(t, first, true, 10))

	second := newStateCacheRenderedEvent(t, "global\n  daemon\n  nbthread 2\n", nil)
	cache.handleTemplateRendered(second)
	cache.handleRenderGateCompleted(newStateCacheGateEvent(t, first, false, 10))

	validatedInfo, err := cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n", validatedInfo.Config,
		"a straggler's verdict must not promote the render it did not judge")

	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, first.PlanID, status.Validation.PlanID)
}

func TestStateCacheCycleIgnoresPoisonedOutputShadows(t *testing.T) {
	cache := NewStateCache(busevents.NewEventBus(100), nil, slog.Default())
	fixture := controllertestutil.NewRenderCycleFixture(t)
	cycleA := fixture.Snapshot(t, "config-a", nil, nil)
	cycleB := fixture.Snapshot(t, "config-b", nil, cycleA)
	outputA, err := cycleA.OutputSnapshot()
	require.NoError(t, err)
	outputB, err := cycleB.OutputSnapshot()
	require.NoError(t, err)
	planA, err := outputA.PlanID()
	require.NoError(t, err)
	planB, err := outputB.PlanID()
	require.NoError(t, err)

	rendered, err := events.NewTemplateRenderedEventWithCycle(cycleA, 7, "test", false)
	require.NoError(t, err)
	rendered.OutputSnapshot = outputB
	rendered.HAProxyConfig = "poisoned"
	rendered.PlanID = planB
	rendered.ContentChecksum = "poisoned"
	cache.handleTemplateRendered(rendered)

	config, _, err := cache.GetRenderedConfig()
	require.NoError(t, err)
	assert.Equal(t, "config-a", config)
	assert.Same(t, cycleA, cache.lastCycleSnapshot)
	assert.Same(t, outputA, cache.lastOutputSnapshot)

	gate, err := events.NewRenderGateCompletedEventWithCycle(
		mustRenderOccurrence(t, rendered), true, false, true, "", false, 11,
	)
	require.NoError(t, err)
	gate.OutputSnapshot = outputB
	gate.PlanID = planB
	cache.handleRenderGateCompleted(gate)

	validated, err := cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, "config-a", validated.Config)
	status, err := cache.GetPipelineStatus()
	require.NoError(t, err)
	require.NotNil(t, status.Validation)
	assert.Equal(t, planA, status.Validation.PlanID)
}

func TestStateCacheCycleDistinguishesABA(t *testing.T) {
	cache := NewStateCache(busevents.NewEventBus(100), nil, slog.Default())
	fixture := controllertestutil.NewRenderCycleFixture(t)
	cycleA1 := fixture.Snapshot(t, "config-a", nil, nil)
	cycleB := fixture.Snapshot(t, "config-b", nil, cycleA1)
	cycleA2 := fixture.Snapshot(t, "config-a", nil, cycleB)
	same, err := cycleA1.SameRoot(cycleA2)
	require.NoError(t, err)
	assert.False(t, same)

	renderAndValidate := func(cycleConfig string, cycle *rendercycle.Snapshot) *events.TemplateRenderedEvent {
		t.Helper()
		rendered, renderErr := events.NewTemplateRenderedEventWithCycle(cycle, 7, "test", false)
		require.NoError(t, renderErr)
		cache.handleTemplateRendered(rendered)
		gate, gateErr := events.NewRenderGateCompletedEventWithCycle(
			mustRenderOccurrence(t, rendered), true, false, true, "", false, 11,
		)
		require.NoError(t, gateErr)
		cache.handleRenderGateCompleted(gate)
		validated, validatedErr := cache.GetValidatedConfig()
		require.NoError(t, validatedErr)
		assert.Equal(t, cycleConfig, validated.Config)
		return rendered
	}

	renderedA1 := renderAndValidate("config-a", cycleA1)
	renderAndValidate("config-b", cycleB)
	renderedA2, err := events.NewTemplateRenderedEventWithCycle(cycleA2, 7, "test", false)
	require.NoError(t, err)
	cache.handleTemplateRendered(renderedA2)

	staleGate, err := events.NewRenderGateCompletedEventWithCycle(
		mustRenderOccurrence(t, renderedA1), true, false, true, "", false, 11,
	)
	require.NoError(t, err)
	cache.handleRenderGateCompleted(staleGate)
	validated, err := cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, "config-b", validated.Config)

	currentGate, err := events.NewRenderGateCompletedEventWithCycle(
		mustRenderOccurrence(t, renderedA2), true, false, true, "", false, 11,
	)
	require.NoError(t, err)
	cache.handleRenderGateCompleted(currentGate)
	validated, err = cache.GetValidatedConfig()
	require.NoError(t, err)
	assert.Equal(t, "config-a", validated.Config)
}

func TestStateCache_HandleValidationFailed(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	cache := NewStateCache(bus, nil, logger)

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	endpoints := []dataplane.Endpoint{
		{URL: "http://haproxy1:5555"},
		{URL: "http://haproxy2:5555"},
	}

	bus.Publish(events.NewDeploymentStartedEvent(len(endpoints)))

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - all succeeded
	bus.Publish(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - partial success
	bus.Publish(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Publish deployment completed event - all failed
	bus.Publish(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	endpoints := []dataplane.Endpoint{{URL: "http://haproxy1:5555"}}
	endpoint := &testEndpoint{url: "http://haproxy1:5555"}

	// Start deployment first (sets deploymentStatus to "pending")
	bus.Publish(events.NewDeploymentStartedEvent(len(endpoints)))
	time.Sleep(50 * time.Millisecond)

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

// listCountingStore implements types.Store and records List() calls. It does
// NOT implement Size() — used to exercise the resourceCounts List() fallback.
type listCountingStore struct {
	items     []any
	listCalls int
}

func (s *listCountingStore) Get(_ ...string) ([]any, error)       { return s.items, nil }
func (s *listCountingStore) List() ([]any, error)                 { s.listCalls++; return s.items, nil }
func (s *listCountingStore) Add(_ any, _ []string) error          { return nil }
func (s *listCountingStore) Update(_ any, _ []string) error       { return nil }
func (s *listCountingStore) Delete(_, _ string, _ []string) error { return nil }
func (s *listCountingStore) Clear() error                         { return nil }

// sizedStore implements types.Store and Size(). Its List() fails the test if
// called — resourceCounts must use the cheap Size() path and never trigger the
// per-item API fetch storm.
type sizedStore struct {
	t    *testing.T
	size int
}

func (s *sizedStore) Get(_ ...string) ([]any, error) { return nil, nil }
func (s *sizedStore) List() ([]any, error) {
	s.t.Fatal("List() must not be called when the store implements Size()")
	return nil, nil
}
func (s *sizedStore) Add(_ any, _ []string) error          { return nil }
func (s *sizedStore) Update(_ any, _ []string) error       { return nil }
func (s *sizedStore) Delete(_, _ string, _ []string) error { return nil }
func (s *sizedStore) Clear() error                         { return nil }
func (s *sizedStore) Size() int                            { return s.size }

func TestResourceCounts_UsesSizeAndAvoidsListStorm(t *testing.T) {
	listOnly := &listCountingStore{items: []any{"a", "b", "c"}}
	sized := &sizedStore{t: t, size: 347}

	counts, err := resourceCounts(map[string]types.Store{
		"ingresses": listOnly, // no Size() → List() fallback
		"secrets":   sized,    // Size() → cheap path, List() would t.Fatal
	})
	require.NoError(t, err)

	assert.Equal(t, 3, counts["ingresses"])
	assert.Equal(t, 347, counts["secrets"])
	assert.Equal(t, 1, listOnly.listCalls, "fallback store should be listed exactly once")
}

func TestResourceCounts_RealMemoryStore(t *testing.T) {
	ms := store.NewMemoryStore(2)
	require.NoError(t, ms.Add(map[string]any{"id": "1"}, []string{"default", "a"}))
	require.NoError(t, ms.Add(map[string]any{"id": "2"}, []string{"default", "b"}))

	counts, err := resourceCounts(map[string]types.Store{"things": ms})
	require.NoError(t, err)
	assert.Equal(t, 2, counts["things"])
}

// cachedListerStore implements types.Store and ListCached(). Its List() fails
// the test if called — listResources must prefer ListCached() for on-demand
// stores so introspection never fans out per-reference API fetches.
type cachedListerStore struct {
	t      *testing.T
	cached []any
}

func (s *cachedListerStore) Get(_ ...string) ([]any, error) { return nil, nil }
func (s *cachedListerStore) List() ([]any, error) {
	s.t.Fatal("List() must not be called when the store implements ListCached()")
	return nil, nil
}
func (s *cachedListerStore) Add(_ any, _ []string) error          { return nil }
func (s *cachedListerStore) Update(_ any, _ []string) error       { return nil }
func (s *cachedListerStore) Delete(_, _ string, _ []string) error { return nil }
func (s *cachedListerStore) Clear() error                         { return nil }
func (s *cachedListerStore) ListCached() ([]any, error)           { return s.cached, nil }

func TestListResources_PrefersListCachedForOnDemandStore(t *testing.T) {
	cached := &cachedListerStore{t: t, cached: []any{"warm-1", "warm-2"}}

	got, err := listResources(cached)
	require.NoError(t, err)
	assert.Equal(t, []any{"warm-1", "warm-2"}, got)
}

func TestListResources_FallsBackToListForPlainStore(t *testing.T) {
	listOnly := &listCountingStore{items: []any{"x"}}

	got, err := listResources(listOnly)
	require.NoError(t, err)
	assert.Equal(t, []any{"x"}, got)
	assert.Equal(t, 1, listOnly.listCalls)
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

	ctx := t.Context()

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

	ctx := t.Context()

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

	ctx := t.Context()

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

	ctx := t.Context()

	go cache.Start(ctx)
	bus.Start()

	// Set up some pipeline state
	rendered := newStateCacheRenderedEvent(t, "config", nil)
	bus.Publish(rendered)
	bus.Publish(newStateCacheGateEvent(t, rendered, true, 50))
	bus.Publish(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
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

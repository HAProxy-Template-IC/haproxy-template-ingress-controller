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

package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestNewCoordinator(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	require.NotNil(t, coordinator)
	assert.Equal(t, CoordinatorComponentName, coordinator.Name())
}

func TestCoordinator_Start_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := coordinator.Start(ctx)
	assert.Nil(t, err)
}

func TestCoordinator_HandleReconciliationTriggered_Success(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create mock pipeline that returns success
	mp := &mockPipeline{
		result: &pipeline.PipelineResult{
			HAProxyConfig:      "test config",
			AuxiliaryFiles:     &dataplane.AuxiliaryFiles{},
			ValidationWarnings: []string{"external validator warning"},
			AuxFileCount:       0,
			RenderDurationMs:   10,
			ValidateDurationMs: 5,
			TotalDurationMs:    15,
		},
	}

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	// Subscribe to events we expect
	eventChan := bus.Subscribe("test", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start coordinator in goroutine
	go func() {
		_ = coordinator.Start(ctx)
	}()

	// Give coordinator time to start
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test_trigger", true))

	// Verify ReconciliationStartedEvent
	startedEvent := testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test_trigger", startedEvent.Trigger)

	// Verify TemplateRenderedEvent
	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test config", renderedEvent.HAProxyConfig)
	assert.Equal(t, "test_trigger", renderedEvent.TriggerReason)

	// Verify ValidationCompletedEvent
	validationEvent := testutil.WaitForEvent[*events.ValidationCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "test_trigger", validationEvent.TriggerReason)
	assert.Equal(t, []string{"external validator warning"}, validationEvent.Warnings)

	// Verify ReconciliationCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.True(t, completedEvent.DurationMs >= 0)
}

func TestCoordinator_HandleReconciliationTriggered_RenderFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create mock pipeline that returns render failure with structured PipelineError
	mp := &mockPipeline{
		err: &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: errors.New("template error"),
		},
	}

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = coordinator.Start(ctx)
	}()

	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test_trigger", true))

	// Verify ReconciliationStartedEvent
	_ = testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)

	// Verify TemplateRenderFailedEvent is published before ReconciliationFailedEvent
	renderFailedEvent := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Contains(t, renderFailedEvent.Error, "template error")

	// Verify ReconciliationFailedEvent
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Contains(t, failedEvent.Error, "template error")
	assert.Equal(t, "render", failedEvent.Phase)
}

func TestCoordinator_HandleReconciliationTriggered_ValidationFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create mock pipeline that returns validation failure with structured PipelineError
	mp := &mockPipeline{
		err: &pipeline.PipelineError{
			Phase:           pipeline.PhaseValidation,
			ValidationPhase: "syntax",
			Cause:           errors.New("syntax error"),
		},
	}

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = coordinator.Start(ctx)
	}()

	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test_trigger", true))

	// Verify ReconciliationStartedEvent
	_ = testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)

	// Verify ValidationFailedEvent is published before ReconciliationFailedEvent
	validationFailedEvent := testutil.WaitForEvent[*events.ValidationFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Contains(t, validationFailedEvent.Errors, "validation failed in syntax phase: syntax error")

	// Verify ReconciliationFailedEvent
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Contains(t, failedEvent.Error, "syntax error")
	assert.Equal(t, "validation", failedEvent.Phase)
}

func TestCoordinator_Name(t *testing.T) {
	coordinator := &Coordinator{}
	assert.Equal(t, CoordinatorComponentName, coordinator.Name())
}

// TestCoordinator_PipelineFailureForwardsLastSuccessfulPatches pins the
// contract that the Coordinator caches `lastSuccessfulPatches` on every
// successful pipeline run and forwards it into `ReconciliationFailedEvent`
// when a subsequent pipeline fails. The StatusApplier reads patches from
// the failure event directly — there is no fallback cache anywhere — so a
// regression here would silently stop the chart from emitting
// renderFailed / deployFailed status variants.
func TestCoordinator_PipelineFailureForwardsLastSuccessfulPatches(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	patches := []templating.StatusPatch{
		{Name: "gw", Kind: "Gateway"},
		{Name: "route", Kind: "HTTPRoute"},
	}

	// Pipeline that returns success on first call, failure on second.
	mp := &flipFlopPipeline{
		success: &pipeline.PipelineResult{
			HAProxyConfig:  "global\n  daemon\n",
			AuxiliaryFiles: &dataplane.AuxiliaryFiles{},
			StatusPatches:  patches,
		},
		failure: &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: errors.New("second-pass template error"),
		},
	}

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = coordinator.Start(ctx)
	}()
	time.Sleep(testutil.StartupDelay)

	// First reconcile: success — coordinator caches lastSuccessfulPatches.
	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	_ = testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)

	// Second reconcile: failure — the failure event must carry the cached patches.
	bus.Publish(events.NewReconciliationTriggeredEvent("second", true))
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)

	require.Equal(t, 2, len(failedEvent.StatusPatches),
		"ReconciliationFailedEvent must carry lastSuccessfulPatches so the "+
			"StatusApplier can apply the renderFailed / deployFailed variant")
	require.Equal(t, "gw", failedEvent.StatusPatches[0].Name)
}

// TestCoordinator_FailureBeforeAnySuccessHasNilPatches pins the
// early-bootstrap case: a pipeline failure before any successful render
// means lastSuccessfulPatches is nil, and the failure event carries nil
// (not empty) so the StatusApplier's `len(patches) == 0` guard cleanly
// short-circuits.
func TestCoordinator_FailureBeforeAnySuccessHasNilPatches(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	mp := &mockPipeline{
		err: &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: errors.New("first-pass template error"),
		},
	}

	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = coordinator.Start(ctx)
	}()
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	failedEvent := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)

	require.Nil(t, failedEvent.StatusPatches,
		"failure before any successful render should carry nil patches "+
			"(StatusApplier guards on len(patches) == 0)")
}

func TestCoordinator_DiscardsPipelineResultsAfterCancellation(t *testing.T) {
	tests := []struct {
		name   string
		result *pipeline.PipelineResult
		err    error
	}{
		{
			name: "success",
			result: &pipeline.PipelineResult{
				HAProxyConfig:  "global\n    daemon\n",
				AuxiliaryFiles: &dataplane.AuxiliaryFiles{},
			},
		},
		{
			name: "failure",
			err: &pipeline.PipelineError{
				Phase: pipeline.PhaseValidation,
				Cause: errors.New("validation failed"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			eventChan := bus.Subscribe("test-canceled-result", 100)
			bus.Start()
			authorityErr := errors.New("leader term ended")
			ctx, cancel := context.WithCancelCause(context.Background())
			coordinator := NewCoordinator(&CoordinatorConfig{
				EventBus: bus,
				Pipeline: &cancelingPipeline{
					cancel: cancel,
					cause:  authorityErr,
					result: tt.result,
					err:    tt.err,
				},
				StoreProvider: stores.NewRealStoreProvider(nil),
				Logger:        logger,
			})

			coordinator.handleReconciliationTriggered(
				ctx,
				events.NewReconciliationTriggeredEvent("test", true),
			)

			started := false
			for {
				select {
				case event := <-eventChan:
					switch event.(type) {
					case *events.ReconciliationStartedEvent:
						started = true
					case *events.TemplateRenderedEvent,
						*events.ValidationCompletedEvent,
						*events.ReconciliationCompletedEvent,
						*events.TemplateRenderFailedEvent,
						*events.ValidationFailedEvent,
						*events.ReconciliationFailedEvent:
						t.Fatalf("published %T after lifecycle cancellation", event)
					}
				default:
					require.True(t, started, "coordinator must publish the start event before the pipeline runs")
					return
				}
			}
		})
	}
}

// mockPipeline implements PipelineExecutor interface for testing.
type mockPipeline struct {
	result *pipeline.PipelineResult
	err    error
}

type cancelingPipeline struct {
	cancel context.CancelCauseFunc
	cause  error
	result *pipeline.PipelineResult
	err    error
}

func (p *cancelingPipeline) Execute(_ context.Context, _ stores.StoreProvider, _ rendercontext.RenderMode, _ ...rendercontext.Option) (*pipeline.PipelineResult, error) {
	p.cancel(p.cause)
	return p.result, p.err
}

func (m *mockPipeline) Execute(_ context.Context, _ stores.StoreProvider, _ rendercontext.RenderMode, _ ...rendercontext.Option) (*pipeline.PipelineResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

// flipFlopPipeline returns success once, then failure thereafter. Used to
// pin the Coordinator's "cache patches on success, attach to failure event"
// contract.
type flipFlopPipeline struct {
	success *pipeline.PipelineResult
	failure error
	calls   int
}

func (m *flipFlopPipeline) Execute(_ context.Context, _ stores.StoreProvider, _ rendercontext.RenderMode, _ ...rendercontext.Option) (*pipeline.PipelineResult, error) {
	m.calls++
	if m.calls == 1 {
		return m.success, nil
	}
	return nil, m.failure
}

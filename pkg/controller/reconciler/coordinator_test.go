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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
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

	// Publish trigger event
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

	// Publish trigger event
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

	// Publish trigger event
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

// mockPipeline implements PipelineExecutor interface for testing.
type mockPipeline struct {
	result *pipeline.PipelineResult
	err    error
}

func (m *mockPipeline) Execute(_ context.Context, _ stores.StoreProvider) (*pipeline.PipelineResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

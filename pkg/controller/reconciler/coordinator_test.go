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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
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
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "test config", nil, nil)

	// Create mock pipeline that returns success
	mp := &mockPipeline{
		result: &pipeline.PipelineResult{
			CycleSnapshot:      cycle,
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

	// No validation event follows the render: HAProxy's verdict comes from the
	// render gate, off this path (ADR-0022).

	// Verify ReconciliationCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.True(t, completedEvent.DurationMs >= 0)
}

func TestCoordinatorCurrentFilesAdvancesBeforeEventDelivery(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingOutputCurrentFilesAuthority{
		recordingCurrentFilesAuthority: recordingCurrentFilesAuthority{
			files: map[string]string{"routes.map": "published"},
		},
	}
	cycle, _ := coordinatorCycleWithMap(
		t, "global\n", auxiliaryfiles.MapFile{Path: "maps/routes.map", Content: "accepted"},
	)
	pipelineExecutor := &mockPipeline{result: &pipeline.PipelineResult{
		CycleSnapshot: cycle,
	}}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      pipelineExecutor,
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  authority,
		Logger:        logger,
	})
	generation := authority.BeginTerm()

	coordinator.handleReconciliationTriggered(context.Background(), events.NewReconciliationTriggeredEvent("first", true), generation)
	coordinator.handleReconciliationTriggered(context.Background(), events.NewReconciliationTriggeredEvent("second", true), generation)

	require.Equal(t, []map[string]string{
		{"routes.map": "published"},
		{"routes.map": "accepted"},
	}, authority.snapshots)
	assert.Equal(t, []int{1, 1}, pipelineExecutor.optionCounts)
}

func TestCoordinatorAcceptsAuthenticatedAuxiliarySnapshot(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	eventChan := bus.Subscribe("snapshot-success", 100)
	bus.Start()
	files := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/routes.map", Content: "backend"}},
	}
	cycle, snapshot := coordinatorCycleWithMap(t, "global\n", files.MapFiles[0])
	output, err := cycle.OutputSnapshot()
	require.NoError(t, err)
	outputAuthority := &recordingOutputCurrentFilesAuthority{}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus,
		Pipeline: &mockPipeline{result: &pipeline.PipelineResult{
			CycleSnapshot: cycle,
		}},
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  outputAuthority,
		Logger:        logger,
	})
	generation := outputAuthority.BeginTerm()

	coordinator.handleReconciliationTriggered(
		context.Background(), events.NewReconciliationTriggeredEvent("snapshot", true), generation,
	)

	require.Len(t, outputAuthority.acceptedOutputs, 1)
	require.Same(t, output, outputAuthority.acceptedOutputs[0])
	acceptedArtifacts, err := outputAuthority.acceptedOutputs[0].ArtifactSnapshot()
	require.NoError(t, err)
	require.Same(t, snapshot, acceptedArtifacts)
	_ = testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)
}

func TestCoordinatorRejectsLegacyPipelineResultBeforeCurrentFilesMutation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	eventChan := bus.Subscribe("legacy-output", 100)
	bus.Start()
	authority := &recordingOutputCurrentFilesAuthority{}
	snapshot := coordinatorArtifactSnapshot(t, &dataplane.AuxiliaryFiles{})
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus,
		Pipeline: &mockPipeline{result: &pipeline.PipelineResult{
			HAProxyConfig:         "global\n",
			AuxiliaryFiles:        &dataplane.AuxiliaryFiles{},
			AuxiliaryFileSnapshot: snapshot,
			PlanID:                "plan-1",
		}},
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  authority,
		Logger:        logger,
	})

	coordinator.handleReconciliationTriggered(
		context.Background(), events.NewReconciliationTriggeredEvent("mixed", true), authority.BeginTerm(),
	)

	failed := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](t, eventChan, testutil.EventTimeout)
	require.Contains(t, failed.Error, "no authenticated render cycle")
	assert.Empty(t, authority.acceptedOutputs)
	assert.Empty(t, authority.acceptedOutputs)
}

func TestCoordinatorRejectsUnauthenticatedCycle(t *testing.T) {
	tests := []struct {
		name      string
		cycle     *rendercycle.Snapshot
		authority CurrentFilesAuthority
		wantError string
	}{
		{
			name:      "unauthenticated",
			cycle:     &rendercycle.Snapshot{},
			authority: &recordingOutputCurrentFilesAuthority{},
			wantError: "snapshot is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			eventChan := bus.Subscribe("invalid-snapshot", 100)
			bus.Start()
			coordinator := NewCoordinator(&CoordinatorConfig{
				EventBus: bus,
				Pipeline: &mockPipeline{result: &pipeline.PipelineResult{
					CycleSnapshot: tt.cycle,
				}},
				StoreProvider: stores.NewRealStoreProvider(nil),
				CurrentFiles:  tt.authority,
				Logger:        logger,
			})

			coordinator.handleReconciliationTriggered(
				context.Background(), events.NewReconciliationTriggeredEvent("invalid", true), tt.authority.BeginTerm(),
			)

			failed := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](t, eventChan, testutil.EventTimeout)
			require.Contains(t, failed.Error, tt.wantError)
		})
	}
}

func TestCoordinatorTreatsNilPipelineResultAsFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	eventChan := bus.Subscribe("nil-result", 100)
	bus.Start()
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	require.NotPanics(t, func() {
		coordinator.handleReconciliationTriggered(
			context.Background(), events.NewReconciliationTriggeredEvent("nil", true), 0,
		)
	})
	failed := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](t, eventChan, testutil.EventTimeout)
	require.Contains(t, failed.Error, "pipeline returned no result")
}

// The term's auxiliary baseline is what the next render reads back as "what is
// deployed", so it moves with HAProxy's verdict on the render that produced it.
func TestCoordinatorSettlesCurrentFilesOnTheGateVerdict(t *testing.T) {
	tests := []struct {
		name           string
		ok             bool
		refused        bool
		newest         bool
		wantConfirmed  int
		wantRolledBack int
	}{
		{
			name:          "a pass confirms the render's files",
			ok:            true,
			newest:        true,
			wantConfirmed: 1,
		},
		{
			name:           "HAProxy's refusal puts the baseline back",
			refused:        true,
			newest:         true,
			wantRolledBack: 1,
		},
		{
			name:   "a gate that could not run leaves the baseline where it is",
			newest: true,
		},
		{
			name: "a verdict for a superseded plan settles nothing",
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			authority := &recordingOutputCurrentFilesAuthority{}
			cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "global\n", nil, nil)
			rendered, err := events.NewTemplateRenderedEventWithCycle(cycle, 0, "test", true)
			require.NoError(t, err)
			occurrence, err := rendered.RenderOccurrence()
			require.NoError(t, err)
			verdict, err := events.NewRenderGateCompletedEventWithCycle(
				occurrence, tt.ok, tt.refused, tt.newest, "", false, 5,
			)
			require.NoError(t, err)
			coordinator := NewCoordinator(&CoordinatorConfig{
				EventBus:      bus,
				Pipeline:      &mockPipeline{},
				StoreProvider: stores.NewRealStoreProvider(nil),
				CurrentFiles:  authority,
				Logger:        logger,
			})

			coordinator.settleCurrentFiles(authority.BeginTerm(), verdict)

			assert.Equal(t, tt.wantConfirmed, authority.confirmedOutputs)
			assert.Equal(t, tt.wantRolledBack, authority.rolledBackOutputs)
		})
	}
}

func TestCoordinatorCoalesceQueuedTriggersPreservesEventBoundary(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})
	queued := make(chan busevents.Event, 4)
	coordinator.eventChan = queued
	first := events.NewReconciliationTriggeredEvent("first", true)
	latest := events.NewReconciliationTriggeredEvent("latest", true)
	gate := events.NewRenderGateCompletedEvent("plan-1", false, true, true, "refused", false, 5)
	trailing := events.NewReconciliationTriggeredEvent("trailing", true)
	queued <- latest
	queued <- gate
	queued <- trailing

	got, boundary := coordinator.coalesceQueuedTriggers(first)

	assert.Same(t, latest, got)
	assert.Same(t, gate, boundary)
	assert.Same(t, trailing, <-queued)
}

func TestCoordinatorCoalesceQueuedTriggersKeepsNonCoalescibleTriggerBeforeBoundary(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})
	queued := make(chan busevents.Event, 3)
	coordinator.eventChan = queued
	first := events.NewReconciliationTriggeredEvent("first", true)
	forced := events.NewReconciliationTriggeredEvent("forced", false)
	latest := events.NewReconciliationTriggeredEvent("latest", true)
	gate := events.NewRenderGateCompletedEvent("plan-1", true, false, true, "", false, 5)
	queued <- forced
	queued <- latest
	queued <- gate

	got, boundary := coordinator.coalesceQueuedTriggers(first)

	assert.Same(t, forced, got)
	assert.Same(t, gate, boundary)
}

func TestCoordinatorCurrentFilesDoesNotAdvanceOnPipelineFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingOutputCurrentFilesAuthority{
		recordingCurrentFilesAuthority: recordingCurrentFilesAuthority{
			files: map[string]string{"ticket.keys": "accepted"},
		},
	}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus,
		Pipeline: &mockPipeline{err: &pipeline.PipelineError{
			Phase: pipeline.PhaseValidation,
			Cause: errors.New("invalid output"),
		}},
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  authority,
		Logger:        logger,
	})
	generation := authority.BeginTerm()

	coordinator.handleReconciliationTriggered(context.Background(), events.NewReconciliationTriggeredEvent("failed", true), generation)

	assert.Empty(t, authority.acceptedOutputs)
	assert.Equal(t, "accepted", authority.files["ticket.keys"])
}

func TestCoordinatorDoesNotRenderWhenCurrentFilesUnavailable(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingOutputCurrentFilesAuthority{
		recordingCurrentFilesAuthority: recordingCurrentFilesAuthority{
			snapshotErr: errors.New("published currentFiles unavailable"),
		},
	}
	pipelineExecutor := &mockPipeline{}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      pipelineExecutor,
		StoreProvider: stores.NewRealStoreProvider(nil),
		CurrentFiles:  authority,
		Logger:        logger,
	})
	generation := authority.BeginTerm()

	coordinator.handleReconciliationTriggered(context.Background(), events.NewReconciliationTriggeredEvent("blocked", true), generation)

	assert.Empty(t, pipelineExecutor.optionCounts)
	assert.Empty(t, authority.acceptedOutputs)
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

	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "gw", "example.test/v1", "Gateway",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "HTTPRoute",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	patchSnapshot, err := collector.Snapshot()
	require.NoError(t, err)
	cycle := testutil.NewRenderCycleFixture(t).Snapshot(
		t, "global\n  daemon\n", patchSnapshot, nil,
	)

	// Pipeline that returns success on first call, failure on second.
	mp := &flipFlopPipeline{
		success: &pipeline.PipelineResult{
			CycleSnapshot: cycle,
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

	require.Same(t, patchSnapshot, failedEvent.StatusPatchSnapshot)
	require.Nil(t, failedEvent.StatusPatches)
}

func TestCoordinatorCarriesExactResultSnapshotsAcrossSuccessAndFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	eventCollector := templating.NewEventCollector()
	require.NoError(t, eventCollector.Register(
		"default", "route", "example.test/v1", "Route", templating.EventTypeWarning, "Conflict", "stable",
	))
	eventSnapshot, err := eventCollector.Snapshot()
	require.NoError(t, err)
	resourceCollector := templating.NewRenderedResourceCollector()
	require.NoError(t, resourceCollector.Register(
		"v1", "ConfigMap", "default", "settings", map[string]any{"data": map[string]any{"value": "stable"}},
	))
	resourceSnapshot, err := resourceCollector.Snapshot()
	require.NoError(t, err)
	cycle := testutil.NewRenderCycleFixture(t).SnapshotWithEffects(
		t, "global\n  daemon\n", nil, nil, snapshot, eventSnapshot, resourceSnapshot, nil,
	)
	mp := &flipFlopPipeline{
		success: &pipeline.PipelineResult{
			CycleSnapshot: cycle,
		},
		failure: &pipeline.PipelineError{Phase: pipeline.PhaseRender, Cause: errors.New("failure")},
	}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus, Pipeline: mp, StoreProvider: stores.NewRealStoreProvider(nil), Logger: logger,
	})
	eventChan := bus.Subscribe("test", 100)
	bus.Start()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = coordinator.Start(ctx) }()
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("first", true))
	rendered := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)
	completed := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	require.Same(t, snapshot, rendered.StatusPatchSnapshot)
	require.Same(t, snapshot, completed.StatusPatchSnapshot)
	require.Same(t, eventSnapshot, completed.EventSnapshot)
	require.Same(t, resourceSnapshot, completed.RenderedResourceSnapshot)
	require.Nil(t, rendered.StatusPatches)
	require.Nil(t, completed.StatusPatches)
	require.Nil(t, completed.Events)
	require.Nil(t, completed.RenderedResources)

	bus.Publish(events.NewReconciliationTriggeredEvent("second", true))
	failed := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)
	require.Same(t, snapshot, failed.StatusPatchSnapshot)
	require.Nil(t, failed.StatusPatches)
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
			authority := &recordingOutputCurrentFilesAuthority{
				recordingCurrentFilesAuthority: recordingCurrentFilesAuthority{
					files: map[string]string{"ticket.keys": "published"},
				},
			}
			generation := authority.BeginTerm()
			coordinator := NewCoordinator(&CoordinatorConfig{
				EventBus: bus,
				Pipeline: &cancelingPipeline{
					cancel: cancel,
					cause:  authorityErr,
					result: tt.result,
					err:    tt.err,
				},
				StoreProvider: stores.NewRealStoreProvider(nil),
				CurrentFiles:  authority,
				Logger:        logger,
			})

			coordinator.handleReconciliationTriggered(
				ctx,
				events.NewReconciliationTriggeredEvent("test", true),
				generation,
			)
			assert.Empty(t, authority.acceptedOutputs)

			started := false
			for {
				select {
				case event := <-eventChan:
					switch event.(type) {
					case *events.ReconciliationStartedEvent:
						started = true
					case *events.TemplateRenderedEvent,
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
	result       *pipeline.PipelineResult
	err          error
	optionCounts []int
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

func (m *mockPipeline) Execute(_ context.Context, _ stores.StoreProvider, _ rendercontext.RenderMode, opts ...rendercontext.Option) (*pipeline.PipelineResult, error) {
	m.optionCounts = append(m.optionCounts, len(opts))
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

type recordingCurrentFilesAuthority struct {
	generation  uint64
	files       map[string]string
	snapshotErr error
	snapshots   []map[string]string
}

type recordingOutputCurrentFilesAuthority struct {
	recordingCurrentFilesAuthority
	acceptedOutputs   []*renderoutput.Snapshot
	confirmedOutputs  int
	rolledBackOutputs int
}

func coordinatorArtifactSnapshot(
	t *testing.T,
	files *dataplane.AuxiliaryFiles,
) *renderartifact.Snapshot {
	t.Helper()
	snapshot, err := dataplane.BuildAuxiliaryFileSnapshot(renderartifact.NewAuthority(), nil, files)
	require.NoError(t, err)
	return snapshot
}

func coordinatorCycleWithMap(
	t *testing.T,
	config string,
	mapFile auxiliaryfiles.MapFile,
) (cycleSnapshot *rendercycle.Snapshot, artifactSnapshot *renderartifact.Snapshot) {
	t.Helper()
	fixture := testutil.NewRenderCycleFixture(t)
	files := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{mapFile}}
	artifacts := fixture.Artifacts(t, files, nil)
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0",
			TextDigest: renderplan.DigestString(config), Length: len(config),
			Text: config, TextKnown: true,
		}},
		Maps: map[string]renderplan.Map{mapFile.Path: {
			Path: mapFile.Path, Ordered: true,
			Entries: renderplan.ParseMapEntries(mapFile.Content),
		}},
		Files: []renderplan.File{
			{
				Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
				Digest: renderplan.DigestString(config), Size: int64(len(config)),
				ReloadOnChange: true, Content: config, ContentKnown: true,
			},
			{
				Path: mapFile.Path, Kind: renderplan.FileKindMap,
				Digest: renderplan.DigestString(mapFile.Content), Size: int64(len(mapFile.Content)),
				Content: mapFile.Content, ContentKnown: true,
			},
		},
	}
	plan.ComputeID()
	return fixture.SnapshotWithEffects(
		t, config, plan, artifacts, nil, nil, nil, nil,
	), artifacts
}

func (a *recordingCurrentFilesAuthority) BeginTerm() uint64 {
	a.generation++
	return a.generation
}

func (a *recordingCurrentFilesAuthority) EndTerm(uint64) {}

func (a *recordingCurrentFilesAuthority) Snapshot(uint64) (map[string]string, error) {
	snapshot := make(map[string]string, len(a.files))
	for name, content := range a.files {
		snapshot[name] = content
	}
	a.snapshots = append(a.snapshots, snapshot)
	return snapshot, a.snapshotErr
}

func (a *recordingOutputCurrentFilesAuthority) AcceptOutput(
	_ uint64,
	output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return err
	}
	artifacts, err := output.ArtifactSnapshot()
	if err != nil {
		return err
	}
	files, err := dataplane.SnapshotCurrentFiles(artifacts)
	if err != nil {
		return err
	}
	a.acceptedOutputs = append(a.acceptedOutputs, output)
	a.files = files
	return nil
}

func (a *recordingOutputCurrentFilesAuthority) ConfirmOutput(
	_ uint64,
	output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return err
	}
	a.confirmedOutputs++
	return nil
}

func (a *recordingOutputCurrentFilesAuthority) RollbackOutput(
	_ uint64,
	output *renderoutput.Snapshot,
) error {
	if err := output.ValidateAuthentication(); err != nil {
		return err
	}
	a.rolledBackOutputs++
	return nil
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

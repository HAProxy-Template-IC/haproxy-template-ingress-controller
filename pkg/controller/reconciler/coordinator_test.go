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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
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

	// No validation event follows the render: HAProxy's verdict comes from the
	// render gate, off this path (ADR-0022).

	// Verify ReconciliationCompletedEvent
	completedEvent := testutil.WaitForEvent[*events.ReconciliationCompletedEvent](t, eventChan, testutil.EventTimeout)
	assert.True(t, completedEvent.DurationMs >= 0)
}

func TestCoordinatorCurrentFilesAdvancesBeforeEventDelivery(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingCurrentFilesAuthority{files: map[string]string{"ticket.keys": "published"}}
	pipelineExecutor := &mockPipeline{result: &pipeline.PipelineResult{
		HAProxyConfig: "global\n",
		AuxiliaryFiles: &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/ticket.keys", Content: "accepted"}},
		},
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
		{"ticket.keys": "published"},
		{"ticket.keys": "accepted"},
	}, authority.snapshots)
	assert.Equal(t, []int{1, 1}, pipelineExecutor.optionCounts)
}

// The term's auxiliary baseline is what the next render reads back as "what is
// deployed", so it moves with HAProxy's verdict on the render that produced it.
func TestCoordinatorSettlesCurrentFilesOnTheGateVerdict(t *testing.T) {
	tests := []struct {
		name           string
		verdict        *events.RenderGateCompletedEvent
		wantConfirmed  int
		wantRolledBack int
	}{
		{
			name:          "a pass confirms the render's files",
			verdict:       events.NewRenderGateCompletedEvent("plan-1", true, false, true, "", false, 5),
			wantConfirmed: 1,
		},
		{
			name:           "HAProxy's refusal puts the baseline back",
			verdict:        events.NewRenderGateCompletedEvent("plan-1", false, true, true, "boom", false, 5),
			wantRolledBack: 1,
		},
		{
			name:    "a gate that could not run leaves the baseline where it is",
			verdict: events.NewRenderGateCompletedEvent("plan-1", false, false, true, "read-only", false, 5),
		},
		{
			name:    "a verdict for a superseded plan settles nothing",
			verdict: events.NewRenderGateCompletedEvent("plan-1", true, false, false, "", false, 5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			authority := &recordingCurrentFilesAuthority{pendingPlanID: "plan-1"}
			coordinator := NewCoordinator(&CoordinatorConfig{
				EventBus:      bus,
				Pipeline:      &mockPipeline{},
				StoreProvider: stores.NewRealStoreProvider(nil),
				CurrentFiles:  authority,
				Logger:        logger,
			})

			coordinator.settleCurrentFiles(authority.BeginTerm(), tt.verdict)

			assert.Equal(t, tt.wantConfirmed, authority.confirmed)
			assert.Equal(t, tt.wantRolledBack, authority.rolledBack)
		})
	}
}

func TestCoordinatorCurrentFilesDoesNotAdvanceOnPipelineFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingCurrentFilesAuthority{files: map[string]string{"ticket.keys": "accepted"}}
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

	assert.Zero(t, authority.accepted)
	assert.Equal(t, "accepted", authority.files["ticket.keys"])
}

func TestCoordinatorDoesNotRenderWhenCurrentFilesUnavailable(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	authority := &recordingCurrentFilesAuthority{snapshotErr: errors.New("published currentFiles unavailable")}
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
	assert.Zero(t, authority.accepted)
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
			authority := &recordingCurrentFilesAuthority{files: map[string]string{"ticket.keys": "published"}}
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
			assert.Zero(t, authority.accepted)

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
	generation    uint64
	files         map[string]string
	snapshotErr   error
	snapshots     []map[string]string
	accepted      int
	pendingPlanID string
	confirmed     int
	rolledBack    int
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

func (a *recordingCurrentFilesAuthority) Accept(
	_ uint64, planID string, auxiliaryFiles *dataplane.AuxiliaryFiles,
) {
	a.accepted++
	a.pendingPlanID = planID
	a.files = auxiliaryFiles.CurrentFiles()
}

func (a *recordingCurrentFilesAuthority) Confirm(_ uint64, planID string) {
	if planID != "" && planID == a.pendingPlanID {
		a.confirmed++
	}
}

func (a *recordingCurrentFilesAuthority) Rollback(_ uint64, planID string) {
	if planID != "" && planID == a.pendingPlanID {
		a.rolledBack++
	}
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

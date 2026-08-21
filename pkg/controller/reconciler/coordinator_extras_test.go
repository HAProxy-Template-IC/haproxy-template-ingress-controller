// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// Three Coordinator contracts are not exercised by the existing tests
// in coordinator_test.go (which cover the happy path + the two
// PipelineError-typed failure phases):
//
//  1. Default-to-render fallback when the pipeline returns a plain
//     (non-PipelineError) error. coordinator.go falls through `errors.
//     AsType[*pipeline.PipelineError]` and defaults `phase = "render"`.
//     A regression that defaulted to "validation" (or to empty) would
//     silently mis-attribute an arbitrary pipeline crash and break
//     phase-based metrics / commentator routing for unexpected errors.
//
//  2. NewCoordinator(cfg) with nil Logger must substitute slog.Default
//     (lines 104-107). A regression that required a non-nil logger
//     would force every call site to construct one even when they
//     don't care about diagnostic output.
//
//  3. The Coalescible() flag from the trigger event must propagate to
//     TemplateRenderedEvent, which is the deploy trigger. The doc on
//     coalescible says it enables coalescing throughout the
//     reconciliation pipeline; a regression that dropped it would
//     silently defeat coalescing for downstream consumers.

func TestNewCoordinator_NilLoggerDefaultsToSlog(t *testing.T) {
	bus, _ := testutil.NewTestBusAndLogger()

	// Pass nil Logger — constructor must NOT panic and must populate
	// the field with a non-nil default so subsequent log calls don't
	// nil-deref.
	c := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      &mockPipeline{},
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        nil, // <-- the contract under test
	})
	require.NotNil(t, c)
	assert.NotNil(t, c.logger,
		"after substitution, the logger field MUST be non-nil so the "+
			"first log call doesn't crash the reconciliation hot path")
}

func TestCoordinator_HandleReconciliationTriggered_NonPipelineErrorDefaultsToRenderPhase(t *testing.T) {
	// Pipeline returns a plain error (NOT *pipeline.PipelineError).
	// coordinator.go falls back to phase = "render" and emits a
	// TemplateRenderFailedEvent. Pin both the phase string and the
	// failure-event type so a regression in the fallback branch
	// surfaces.
	bus, logger := testutil.NewTestBusAndLogger()

	mp := &mockPipeline{err: errors.New("unexpected non-pipeline crash")}

	c := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test-default-phase", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Start(ctx) }()
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test", true))

	_ = testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)

	// Default phase MUST be "render" → TemplateRenderFailedEvent (not
	// ValidationFailedEvent). A regression that defaulted to the
	// validation branch would silently mis-attribute every
	// non-PipelineError to validation failures and break commentator/
	// metrics phase routing.
	renderFailed := testutil.WaitForEvent[*events.TemplateRenderFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Contains(t, renderFailed.Error, "unexpected non-pipeline crash",
		"the underlying error message must surface so operators see the "+
			"actual cause of the unexpected pipeline crash")

	// And the ReconciliationFailedEvent.Phase must also be "render".
	failed := testutil.WaitForEvent[*events.ReconciliationFailedEvent](t, eventChan, testutil.EventTimeout)
	assert.Equal(t, "render", failed.Phase,
		"non-PipelineError must default to 'render' phase — a regression "+
			"that flipped this to 'validation' or empty would mis-attribute "+
			"the failure in every downstream consumer that filters by phase")
	assert.Contains(t, failed.Error, "unexpected non-pipeline crash")
}

func TestCoordinator_HandlePipelineSuccess_PublishesTheRenderPlan(t *testing.T) {
	// The deployer diffs the plan against what each pod applied, so a render
	// whose plan is dropped here degrades every deploy to a full push.
	plan := &renderplan.Plan{ID: "plan-abc"}
	bus, logger := testutil.NewTestBusAndLogger()

	mp := &mockPipeline{
		result: &pipeline.PipelineResult{
			HAProxyConfig:  "cfg",
			AuxiliaryFiles: &dataplane.AuxiliaryFiles{},
			Plan:           plan,
			PlanID:         plan.ID,
		},
	}

	c := NewCoordinator(&CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      mp,
		StoreProvider: stores.NewRealStoreProvider(nil),
		Logger:        logger,
	})

	eventChan := bus.Subscribe("test-plan", 100)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Start(ctx) }()
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test", true))

	rendered := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)
	assert.Same(t, plan, rendered.Plan)
	assert.Equal(t, "plan-abc", rendered.PlanID)
}

func TestCoordinator_HandlePipelineSuccess_PropagatesCoalescibleFlagToRender(t *testing.T) {
	// Coalescible propagation is a contract the coordinator MUST honor:
	// trigger.Coalescible() flows through to TemplateRenderedEvent, which
	// is what arms deployment. A regression that dropped it would silently
	// defeat coalescing for downstream consumers (renderer-vs-deployment
	// fan-in, reconciler debouncer, etc.).
	tests := []struct {
		name        string
		coalescible bool
	}{
		{name: "coalescible=true propagates", coalescible: true},
		{name: "coalescible=false propagates", coalescible: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()

			mp := &mockPipeline{
				result: &pipeline.PipelineResult{
					HAProxyConfig:      "cfg",
					AuxiliaryFiles:     &dataplane.AuxiliaryFiles{},
					RenderDurationMs:   1,
					ValidateDurationMs: 1,
				},
			}

			c := NewCoordinator(&CoordinatorConfig{
				EventBus:      bus,
				Pipeline:      mp,
				StoreProvider: stores.NewRealStoreProvider(nil),
				Logger:        logger,
			})

			eventChan := bus.Subscribe("test-coalescible", 100)
			bus.Start()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			go func() { _ = c.Start(ctx) }()
			time.Sleep(testutil.StartupDelay)

			bus.Publish(events.NewReconciliationTriggeredEvent("test", tt.coalescible))

			_ = testutil.WaitForEvent[*events.ReconciliationStartedEvent](t, eventChan, testutil.EventTimeout)

			rendered := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)
			assert.Equal(t, tt.coalescible, rendered.Coalescible(),
				"coalescible flag MUST propagate from trigger → "+
					"TemplateRenderedEvent — without this, the renderer-side "+
					"of the coalescing pipeline can't honor the trigger's "+
					"intent and would over- or under-coalesce")
		})
	}
}

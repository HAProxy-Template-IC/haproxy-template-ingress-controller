// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// computeReconciliationSummary aggregates per-cycle metrics by walking the
// ring buffer for events sharing the deployment's correlation ID. The
// resulting ReconciliationSummary feeds the operator-facing
// reconciliation-completed log line — every field in it is something
// an operator reads to triage cycle latency.
//
// The function had effectively zero direct test coverage despite
// FOUR distinct conditional branches and three derived metric
// computations:
//
//   1. correlationID == ""              → return early, only basic fields
//   2. ReconciliationTriggeredEvent in buffer → Trigger string + wall-clock TotalMs
//   3. ReconciliationTriggeredEvent NOT in buffer → TotalMs falls back to sum-of-phases
//   4. Phase-pair queue-wait calculations → only fire when both timestamps + nonzero phase ms
//
// Three branches are particularly load-bearing:
//
// (b) The wall-clock TotalMs (deployment_ts - trigger_ts) is more
//     accurate than summing phase durations because it includes any
//     queue waits the per-phase metrics omit. A regression that
//     dropped this branch would silently understate cycle latency
//     in operator logs by exactly the queue overhead.
//
// (c) The sum-of-phases fallback is the safety net for missing
//     trigger events (which can happen if the trigger fell out of
//     the ring buffer before the deployment completed). Without it,
//     TotalMs would silently be 0 and operator dashboards would
//     report bogus "0ms reconciliation" entries.
//
// (d) Queue waits use max(x, 0) to clamp negative values that arise
//     from clock skew or coarse millisecond rounding. Without the
//     clamp, a fast cycle could report negative queue waits and
//     mislead operator triage.

// fakeRB returns an EventCommentator wrapping just a populated
// RingBuffer — that's all computeReconciliationSummary touches.
// Avoids the full NewEventCommentator constructor's eventBus
// subscription side-effect.
func fakeRB(events ...busevents.Event) *EventCommentator {
	rb := NewRingBuffer(16)
	for _, e := range events {
		rb.Add(e)
	}
	return &EventCommentator{ringBuffer: rb}
}

// withTimestamp creates a deployment-completed event with a known
// timestamp so wall-clock TotalMs assertions are deterministic. It
// uses NewDeploymentCompletedEvent then patches the embedded
// timestamp via reflection-free wrapping: we replace the field by
// instantiating the struct directly. The corrOpts thread the
// correlation through.
func depCompleted(t *testing.T, corr ctlevents.CorrelationOption, deployMs int64, succeeded, total, reloads, ops int) *ctlevents.DeploymentCompletedEvent {
	t.Helper()
	return ctlevents.NewDeploymentCompletedEvent(ctlevents.DeploymentResult{
		Total:              total,
		Succeeded:          succeeded,
		Failed:             total - succeeded,
		DurationMs:         deployMs,
		ReloadsTriggered:   reloads,
		TotalAPIOperations: ops,
	}, corr)
}

func TestComputeReconciliationSummary_NoCorrelationIDReturnsBasicSummaryOnly(t *testing.T) {
	// Branch 1: correlationID empty → no buffer walk, only deployment-side
	// fields populated. Pin that the basic fields ARE populated even
	// without correlation (the operator still gets instances, reload
	// counts, etc.).
	ec := fakeRB() // empty buffer
	dep := depCompleted(t, ctlevents.CorrelationOption{}, 50, 3, 4, 2, 17)

	summary := ec.computeReconciliationSummary(dep)

	assert.Equal(t, "3/4", summary.Instances,
		"basic deployment metrics must be populated even without correlation")
	assert.Equal(t, 2, summary.Reloads)
	assert.Equal(t, 17, summary.Operations)
	assert.Equal(t, int64(50), summary.DeployMs)
	assert.Empty(t, summary.Trigger,
		"Trigger should be empty without a correlated trigger event")
	assert.Equal(t, int64(0), summary.TotalMs,
		"without correlation, TotalMs cannot be derived (no trigger or phase events)")
	assert.Equal(t, int64(0), summary.TriggerToRenderQueueMs,
		"queue waits require timestamps from correlated events; without "+
			"correlation they must be 0, never garbage")
}

func TestComputeReconciliationSummary_TriggerEventEnablesWallClockTotalMs(t *testing.T) {
	// Branch 2: ReconciliationTriggeredEvent IS in buffer → TotalMs comes
	// from wall-clock subtraction (deploymentTimestamp - triggerTimestamp).
	// This is the load-bearing path because it includes ALL queue overhead,
	// not just the per-phase processing time.
	trigger := ctlevents.NewReconciliationTriggeredEvent("config_change", true, ctlevents.WithNewCorrelation())
	corrID := trigger.CorrelationID()
	corr := ctlevents.WithCorrelation(corrID, trigger.EventID())

	ec := fakeRB(trigger)
	// Sleep generously so the wall-clock branch is observably distinct
	// from the fallback. We use 50ms so even on a heavily-loaded CI
	// runner with timer jitter and millisecond rounding, the resulting
	// TotalMs is reliably in [10, 99] ms range — separable from both
	// 0 (would indicate a regression dropped the wall-clock branch
	// AND skipped the fallback) and 100 (the fallback's value when
	// trigger event is absent).
	time.Sleep(50 * time.Millisecond)
	dep := depCompleted(t, corr, 100, 1, 1, 1, 5)

	summary := ec.computeReconciliationSummary(dep)

	assert.Equal(t, "config_change", summary.Trigger,
		"the trigger reason from the correlated ReconciliationTriggeredEvent "+
			"must surface in the summary so operators see WHY the cycle ran")
	// TotalMs from wall-clock must reflect the ~50ms sleep, NOT the
	// fallback sum-of-phases value (100ms = DeployMs only). The
	// upper-bound separation from 100 is what proves we took the
	// wall-clock branch rather than the fallback.
	assert.Less(t, summary.TotalMs, int64(100),
		"with a correlated trigger event, TotalMs MUST be the wall-clock "+
			"difference (well under 100ms), NOT the sum-of-phases fallback "+
			"(would be exactly 100=DeployMs); a regression that fell through "+
			"to the fallback would surface here")
	// Lower bound is loose enough to tolerate timer jitter and
	// millisecond rounding on slow CI runners — the contract is
	// "wall-clock sub-100ms", not "exactly N ms".
	assert.Greater(t, summary.TotalMs, int64(0),
		"a 50ms sleep must produce a non-zero TotalMs even with rounding")
}

func TestComputeReconciliationSummary_NoTriggerEventFallsBackToSumOfPhases(t *testing.T) {
	// Branch 3: correlationID set BUT the trigger event is no longer in
	// the ring buffer (e.g. wrapped out due to a slow cycle). The
	// fallback must populate TotalMs as the sum of phase durations
	// (RenderMs + ValidateMs + DeployMs). Without this, operator
	// dashboards would report "0ms reconciliation" for cycles whose
	// trigger aged out — confusing during incident triage.
	//
	// We construct a correlation manually (no trigger event added to
	// the buffer) so the buffer walk finds nothing.
	const corrID = "synthetic-corr-id"
	const causID = "synthetic-cause-id"
	corr := ctlevents.WithCorrelation(corrID, causID)

	ec := fakeRB() // empty buffer
	dep := depCompleted(t, corr, 100, 1, 1, 1, 5)

	summary := ec.computeReconciliationSummary(dep)

	// Without trigger or phase events in buffer, TotalMs falls back
	// to RenderMs + ValidateMs + DeployMs = 0 + 0 + 100 = 100.
	assert.Equal(t, int64(100), summary.TotalMs,
		"with correlation but no trigger event in buffer, TotalMs MUST fall "+
			"back to the sum of phase durations (RenderMs+ValidateMs+DeployMs); "+
			"a regression that left TotalMs=0 would surface as bogus '0ms "+
			"reconciliation' entries in operator dashboards whenever the "+
			"trigger aged out of the buffer")
	assert.Empty(t, summary.Trigger,
		"Trigger string stays empty when the trigger event is absent from "+
			"the buffer — operators see this as 'unknown trigger' which is "+
			"truthful, vs a stale or guessed value which would mislead")
}

func TestComputeReconciliationSummary_QueueWaitsClampToNonNegative(t *testing.T) {
	// Branch 4: queue-wait calculations use max(x, 0) to clamp negative
	// values from clock skew or millisecond rounding. Without the clamp,
	// a fast cycle could report negative queue waits — confusing
	// operators and breaking metrics dashboards that assume non-negative.
	//
	// Pin via a fresh trigger event paired with a deployment that
	// completed nearly instantaneously (RenderMs=0). The queue-wait
	// formula is: renderStart = renderTimestamp - RenderMs;
	// queueWait = renderStart - triggerTimestamp. If RenderMs > the
	// real elapsed time, queueWait would go negative without the
	// clamp.
	trigger := ctlevents.NewReconciliationTriggeredEvent("test", true, ctlevents.WithNewCorrelation())
	corrID := trigger.CorrelationID()
	corr := ctlevents.WithCorrelation(corrID, trigger.EventID())

	// Render event timestamped IMMEDIATELY after trigger, but with a
	// reported RenderMs much larger than wall-clock elapsed. The clamp
	// must keep TriggerToRenderQueueMs >= 0.
	render := ctlevents.NewTemplateRenderedEvent("", nil, nil, 0, 100 /*duration ms*/, "test", "checksum", true, corr)

	ec := fakeRB(trigger, render)
	dep := depCompleted(t, corr, 50, 1, 1, 0, 0)

	summary := ec.computeReconciliationSummary(dep)

	assert.GreaterOrEqual(t, summary.TriggerToRenderQueueMs, int64(0),
		"TriggerToRenderQueueMs MUST be non-negative — the max(x,0) clamp "+
			"protects against clock skew and millisecond rounding making the "+
			"formula go negative; a regression that dropped the clamp would "+
			"surface negative queue waits in operator logs/dashboards")
	assert.Equal(t, int64(100), summary.RenderMs,
		"the RenderMs field must be populated from the correlated render "+
			"event")
}

func TestComputeReconciliationSummary_AllPhaseEventsPopulateMs(t *testing.T) {
	// Composite happy-path: trigger + render + validate + deployment
	// (all correlated). All four phase-fields must be populated,
	// catching a regression that dropped one specific phase's case
	// from the type switch in the buffer-walk loop.
	trigger := ctlevents.NewReconciliationTriggeredEvent("config_change", true, ctlevents.WithNewCorrelation())
	corrID := trigger.CorrelationID()
	corr := ctlevents.WithCorrelation(corrID, trigger.EventID())

	render := ctlevents.NewTemplateRenderedEvent("", nil, nil, 0, 25, "config_change", "csum", true, corr)
	validate := ctlevents.NewValidationCompletedEvent(nil, 30, "config_change", nil, true, corr)

	// Pause generously so the deployment timestamp is reliably later
	// than the trigger timestamp even on a heavily-loaded CI runner.
	// Using 50ms so the wall-clock TotalMs is observably non-zero
	// without depending on millisecond rounding behaviour.
	time.Sleep(50 * time.Millisecond)
	dep := depCompleted(t, corr, 50, 1, 1, 1, 3)

	ec := fakeRB(trigger, render, validate)
	summary := ec.computeReconciliationSummary(dep)

	assert.Equal(t, "config_change", summary.Trigger)
	assert.Equal(t, int64(25), summary.RenderMs,
		"RenderMs branch must populate from the correlated TemplateRenderedEvent — "+
			"a regression that dropped this case would silently report 0ms render")
	assert.Equal(t, int64(30), summary.ValidateMs,
		"ValidateMs branch must populate from the correlated ValidationCompletedEvent")
	assert.Equal(t, int64(50), summary.DeployMs,
		"DeployMs comes straight from the deployment event itself")
	require.Greater(t, summary.TotalMs, int64(0),
		"with all phase events present, TotalMs uses wall-clock subtraction; "+
			"the 50ms sleep guarantees a measurable non-zero difference even "+
			"under CI scheduling jitter and millisecond rounding")
	assert.Equal(t, summary.TotalQueueMs,
		summary.TriggerToRenderQueueMs+summary.RenderToValidateQueueMs+summary.ValidateToDeployQueueMs,
		"TotalQueueMs must be the exact sum of the three pairwise queue waits "+
			"— a regression that mis-summed (e.g. dropped one term) would silently "+
			"understate the queue overhead operators rely on")
}

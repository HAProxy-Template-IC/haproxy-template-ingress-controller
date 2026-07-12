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

package events

import (
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ReconciliationTriggeredEvent is published when a reconciliation cycle should start.
//
// This event is typically published by the Reconciler after the debounce timer.
// expires, or immediately for config changes.
//
// This event starts a new correlation chain. Downstream events (TemplateRenderedEvent,
// ValidationCompletedEvent, DeploymentScheduledEvent, etc.) should propagate
// the correlation ID to enable end-to-end tracing.
//
// This event implements CoalescibleEvent. The coalescible flag is set by the emitter
// (Reconciler) based on the trigger context:
//   - coalescible=true for state updates (debounce_timer, resource_change)
//   - coalescible=false for commands (index_synchronized, drift_prevention)
type ReconciliationTriggeredEvent struct {
	// Reason describes why reconciliation was triggered.
	// Examples: "debounce_timer", "config_change", "manual_trigger"
	Reason string
	timestamped

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Set by the emitter (Reconciler).
	coalescible bool

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationTriggeredEvent creates a new ReconciliationTriggeredEvent.
//
// The coalescible parameter is set by the emitter based on trigger context:
//   - true for state updates where only the latest matters (debounce_timer, resource_change)
//   - false for commands that must be processed (index_synchronized, drift_prevention)
//
// Use WithNewCorrelation() to start a new correlation chain:
//
//	event := events.NewReconciliationTriggeredEvent("config_change", true,
//	    events.WithNewCorrelation())
func NewReconciliationTriggeredEvent(reason string, coalescible bool, opts ...CorrelationOption) *ReconciliationTriggeredEvent {
	return &ReconciliationTriggeredEvent{
		Reason:      reason,
		coalescible: coalescible,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *ReconciliationTriggeredEvent) EventType() string { return EventTypeReconciliationTriggered }

// Coalescible returns true if this event can be safely skipped when a newer
// event of the same type is available. This implements the CoalescibleEvent interface.
func (e *ReconciliationTriggeredEvent) Coalescible() bool { return e.coalescible }

// ReconciliationStartedEvent is published when the Executor begins a reconciliation cycle.
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
type ReconciliationStartedEvent struct {
	// Trigger describes what triggered this reconciliation.
	Trigger string
	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationStartedEvent creates a new ReconciliationStartedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewReconciliationStartedEvent(trigger,
//	    events.PropagateCorrelation(triggeredEvent))
func NewReconciliationStartedEvent(trigger string, opts ...CorrelationOption) *ReconciliationStartedEvent {
	return &ReconciliationStartedEvent{
		Trigger:     trigger,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *ReconciliationStartedEvent) EventType() string { return EventTypeReconciliationStarted }

// ReconciliationCompletedEvent is published when a reconciliation cycle completes successfully.
//
// This event propagates the correlation ID from the reconciliation chain.
type ReconciliationCompletedEvent struct {
	DurationMs int64

	// RenderedResources are the Kubernetes resources the templates declared
	// under spec.k8sResources in this cycle. The
	// ResourceApplier reads them directly from the event so it stays
	// stateless on the success path — patches/resources travel with the
	// event that triggers their apply, never via a side-channel cache. May
	// be nil when the render didn't emit any K8s resources.
	RenderedResources []templating.RenderedResource

	// StatusPatches are the chart-rendered status patches of this cycle.
	// The ResourceApplier forwards them on ResourcesAppliedEvent after its
	// apply pass so the StatusApplier writes the "rendered" variant only
	// AFTER the same render's infrastructure resources exist (conditions
	// must describe materialized state — conformance's GatewayInfrastructure
	// lists labeled resources the moment Accepted turns True).
	StatusPatches []templating.StatusPatch

	// Events are the Kubernetes Events templates asked to emit this cycle via
	// recordEvent() (e.g. a RouteConflict Warning on an Ingress). The
	// EventEmitter (leader-only) reads them directly off this event and emits
	// them via an EventRecorder. May be nil when no template recorded an event.
	// The publisher sets this from a cloned slice before Publish; treat as
	// read-only like every other event field.
	Events []templating.RenderedEvent

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationCompletedEvent creates a new ReconciliationCompletedEvent.
//
// renderedResources is the slice of resources the templates declared under
// spec.k8sResources in this cycle. The outer slice is defensively cloned so
// publishers reusing a cached slice (e.g. coordinator forwarding
// PipelineResult.RenderedResources) can't mutate published events.
//
// Use WithCorrelation() to propagate correlation from the pipeline:
//
//	event := events.NewReconciliationCompletedEvent(durationMs, resources,
//	    events.WithCorrelation(correlationID, causationID))
func NewReconciliationCompletedEvent(
	durationMs int64,
	renderedResources []templating.RenderedResource,
	statusPatches []templating.StatusPatch,
	opts ...CorrelationOption,
) *ReconciliationCompletedEvent {
	return &ReconciliationCompletedEvent{
		DurationMs:        durationMs,
		RenderedResources: slices.Clone(renderedResources),
		StatusPatches:     slices.Clone(statusPatches),
		timestamped:       newTimestamped(),
		Correlation:       newCorrelation(opts...),
	}
}

func (e *ReconciliationCompletedEvent) EventType() string { return EventTypeReconciliationCompleted }

// Coalescible implements busevents.CoalescibleEvent. A completed cycle is a
// full-state notification — RenderedResources and StatusPatches are the
// COMPLETE desired set of the render — so for consumers that declare it in
// their CoalescesOn list only the newest of an uninterrupted run matters.
func (e *ReconciliationCompletedEvent) Coalescible() bool { return true }

// ResourcesAppliedEvent is published by the ResourceApplier after it finishes
// applying a cycle's rendered resources (and pruning orphans). It forwards
// the cycle's StatusPatches so the StatusApplier writes the "rendered"
// status variant strictly AFTER the same render's infrastructure resources
// exist. Without this ordering the two appliers race: Accepted=True could
// land while e.g. the per-Gateway Service is still being created, and
// consumers (including the Gateway API conformance GatewayInfrastructure
// test) that list infrastructure the moment Accepted turns True find
// nothing.
type ResourcesAppliedEvent struct {
	// StatusPatches forwarded from the ReconciliationCompletedEvent that
	// triggered the apply pass.
	StatusPatches []templating.StatusPatch

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewResourcesAppliedEvent creates a new ResourcesAppliedEvent. The patches
// slice is NOT cloned: the publisher forwards the (already defensively
// cloned) slice from the ReconciliationCompletedEvent it consumed.
func NewResourcesAppliedEvent(statusPatches []templating.StatusPatch, opts ...CorrelationOption) *ResourcesAppliedEvent {
	return &ResourcesAppliedEvent{
		StatusPatches: statusPatches,
		timestamped:   newTimestamped(),
		Correlation:   newCorrelation(opts...),
	}
}

func (e *ResourcesAppliedEvent) EventType() string { return EventTypeResourcesApplied }

// Coalescible implements busevents.CoalescibleEvent — full-state semantics,
// same rationale as ReconciliationCompletedEvent.Coalescible.
func (e *ResourcesAppliedEvent) Coalescible() bool { return true }

// ReconciliationFailedEvent is published when a reconciliation cycle fails.
//
// This event propagates the correlation ID from the reconciliation chain.
type ReconciliationFailedEvent struct {
	Error string
	Phase string // Which phase failed: "render", "validate", "deploy"

	// StatusPatches are the chart-rendered status patches from the most
	// recent SUCCESSFUL render (the failure itself rarely produces patches —
	// render failures have none; validation failures have the just-rendered
	// set). The StatusApplier reads them to write the renderFailed /
	// deployFailed variant on the affected resources. May be nil if no
	// successful render has happened yet (early bootstrap failures); the
	// applier handles nil gracefully by skipping the apply.
	StatusPatches []templating.StatusPatch

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationFailedEvent creates a new ReconciliationFailedEvent.
//
// statusPatches should be the patches from the most recent successful render,
// or nil if none exists yet. The outer slice is defensively cloned.
//
// Use WithCorrelation() to propagate correlation from the pipeline:
//
//	event := events.NewReconciliationFailedEvent(err, phase, statusPatches,
//	    events.WithCorrelation(correlationID, causationID))
func NewReconciliationFailedEvent(err, phase string, statusPatches []templating.StatusPatch, opts ...CorrelationOption) *ReconciliationFailedEvent {
	return &ReconciliationFailedEvent{
		Error:         err,
		Phase:         phase,
		StatusPatches: slices.Clone(statusPatches),
		timestamped:   newTimestamped(),
		Correlation:   newCorrelation(opts...),
	}
}

func (e *ReconciliationFailedEvent) EventType() string { return EventTypeReconciliationFailed }

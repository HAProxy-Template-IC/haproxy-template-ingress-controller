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
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ReconciliationTriggeredEvent is published when a reconciliation cycle should start.
//
// This event is typically published by the Reconciler after the debounce timer.
// expires, or immediately for config changes.
//
// This event starts a new correlation chain. Downstream events (TemplateRenderedEvent,
// RenderGateCompletedEvent, DeploymentScheduledEvent, etc.) should propagate
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
	renderOccurrenceCarrier

	DurationMs int64

	// CycleSnapshot binds the complete output and effects of this reconciliation.
	CycleSnapshot *rendercycle.Snapshot

	// RenderProof identifies this occurrence of the authenticated cycle.
	RenderProof string

	// RenderedResources are the Kubernetes resources the templates declared
	// under spec.k8sResources in this cycle. The
	// ResourceApplier reads them directly from the event so it stays
	// stateless on the success path — patches/resources travel with the
	// event that triggers their apply, never via a side-channel cache. May
	// be nil when the render didn't emit any K8s resources.
	RenderedResources []templating.RenderedResource

	// RenderedResourceSnapshot is the authenticated immutable production representation.
	RenderedResourceSnapshot *templating.RenderedResourceSnapshot

	// StatusPatches are the chart-rendered status patches of this cycle.
	// The ResourceApplier forwards them on ResourcesAppliedEvent after its
	// apply pass so the StatusApplier writes the "rendered" variant only
	// AFTER the same render's infrastructure resources exist (conditions
	// must describe materialized state — conformance's GatewayInfrastructure
	// lists labeled resources the moment Accepted turns True).
	StatusPatches []templating.StatusPatch

	// StatusPatchSnapshot is the authenticated immutable production representation.
	StatusPatchSnapshot *templating.StatusPatchSnapshot

	// Events are the Kubernetes Events templates asked to emit this cycle via
	// recordEvent() (e.g. a RouteConflict Warning on an Ingress). The
	// EventEmitter (leader-only) reads them directly off this event and emits
	// them via an EventRecorder. May be nil when no template recorded an event.
	// The publisher sets this from a cloned slice before Publish; treat as
	// read-only like every other event field.
	Events []templating.RenderedEvent

	// EventSnapshot is the authenticated immutable production representation.
	EventSnapshot *templating.RenderedEventSnapshot

	// PlanID is the render this cycle produced, so a consumer can pair the
	// cycle with the render gate's later verdict on it.
	PlanID string

	// ProfileCount is the number of distinct backend profiles this render
	// emitted, harvested from the plan. The metrics component sets the
	// haptic_render_profiles gauge from it. Zero when the render produced no plan.
	ProfileCount int

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewReconciliationCompletedEventWithCycle creates a production completion.
func NewReconciliationCompletedEventWithCycle(
	durationMs int64,
	occurrence *rendercycle.Occurrence,
	opts ...CorrelationOption,
) (*ReconciliationCompletedEvent, error) {
	carrier, identity, err := inspectRenderOccurrence(occurrence)
	if err != nil {
		return nil, fmt.Errorf("reconciliation completed event: %w", err)
	}
	event := newReconciliationCompletedEvent(
		durationMs, "", nil, nil, nil, nil, nil, opts...,
	)
	event.renderOccurrenceCarrier = carrier
	owned := withReconciliationCompletedIdentity(event, identity)
	return &owned, nil
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
	planID string,
	renderedResources []templating.RenderedResource,
	statusPatches []templating.StatusPatch,
	opts ...CorrelationOption,
) *ReconciliationCompletedEvent {
	return newReconciliationCompletedEvent(
		durationMs, planID, renderedResources, nil, statusPatches, nil, nil, opts...,
	)
}

// NewReconciliationCompletedEventWithStatusSnapshot avoids detaching status payloads.
func NewReconciliationCompletedEventWithStatusSnapshot(
	durationMs int64,
	planID string,
	renderedResources []templating.RenderedResource,
	statusPatchSnapshot *templating.StatusPatchSnapshot,
	opts ...CorrelationOption,
) *ReconciliationCompletedEvent {
	return newReconciliationCompletedEvent(
		durationMs, planID, renderedResources, nil, nil, statusPatchSnapshot, nil, opts...,
	)
}

func newReconciliationCompletedEvent(
	durationMs int64,
	planID string,
	renderedResources []templating.RenderedResource,
	renderedResourceSnapshot *templating.RenderedResourceSnapshot,
	statusPatches []templating.StatusPatch,
	statusPatchSnapshot *templating.StatusPatchSnapshot,
	eventSnapshot *templating.RenderedEventSnapshot,
	opts ...CorrelationOption,
) *ReconciliationCompletedEvent {
	return &ReconciliationCompletedEvent{
		DurationMs:               durationMs,
		PlanID:                   planID,
		RenderedResources:        cloneRenderedResources(renderedResources),
		RenderedResourceSnapshot: renderedResourceSnapshot,
		StatusPatches:            cloneStatusPatches(statusPatches),
		StatusPatchSnapshot:      statusPatchSnapshot,
		EventSnapshot:            eventSnapshot,
		timestamped:              newTimestamped(),
		Correlation:              newCorrelation(opts...),
	}
}

func (e *ReconciliationCompletedEvent) EventType() string { return EventTypeReconciliationCompleted }

// CloneForSubscriber restores authenticated shadows and isolates legacy payloads.
func (e *ReconciliationCompletedEvent) CloneForSubscriber() busevents.Event {
	if e == nil {
		panic("cannot clone nil reconciliation completed event")
	}
	clone := *e
	clone.RenderedResources = cloneRenderedResources(e.RenderedResources)
	clone.StatusPatches = cloneStatusPatches(e.StatusPatches)
	clone.Events = slices.Clone(e.Events)
	if e.occurrence != nil {
		clone = withReconciliationCompletedIdentity(&clone, mustInspectRenderOccurrence(e.renderOccurrenceCarrier))
	}
	return &clone
}

func withReconciliationCompletedIdentity(source *ReconciliationCompletedEvent, identity *renderOccurrenceIdentity) ReconciliationCompletedEvent {
	event := *source
	event.CycleSnapshot = identity.cycle
	event.RenderProof = identity.proof
	event.RenderedResources = nil
	event.RenderedResourceSnapshot = identity.renderedResources
	event.StatusPatches = nil
	event.StatusPatchSnapshot = identity.statusPatches
	event.Events = nil
	event.EventSnapshot = identity.renderedEvents
	event.PlanID = identity.planID
	event.ProfileCount = identity.counts.Profiles
	return event
}

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
	renderOccurrenceCarrier

	// CycleSnapshot is the exact cycle whose resources now exist.
	CycleSnapshot *rendercycle.Snapshot

	// RenderProof identifies this occurrence of the authenticated cycle.
	RenderProof string

	// StatusPatches forwarded from the ReconciliationCompletedEvent that
	// triggered the apply pass.
	StatusPatches []templating.StatusPatch

	// StatusPatchSnapshot forwards the immutable patch set from the same cycle.
	StatusPatchSnapshot *templating.StatusPatchSnapshot

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewResourcesAppliedEventWithCycle forwards one exact cycle after its resources exist.
func NewResourcesAppliedEventWithCycle(
	occurrence *rendercycle.Occurrence,
	opts ...CorrelationOption,
) (*ResourcesAppliedEvent, error) {
	carrier, identity, err := inspectRenderOccurrence(occurrence)
	if err != nil {
		return nil, fmt.Errorf("resources applied event: %w", err)
	}
	event := NewResourcesAppliedEventWithStatusSnapshot(nil, opts...)
	event.renderOccurrenceCarrier = carrier
	owned := withResourcesAppliedIdentity(event, identity)
	return &owned, nil
}

// NewResourcesAppliedEvent creates a legacy ResourcesAppliedEvent.
func NewResourcesAppliedEvent(statusPatches []templating.StatusPatch, opts ...CorrelationOption) *ResourcesAppliedEvent {
	return &ResourcesAppliedEvent{
		StatusPatches: cloneStatusPatches(statusPatches),
		timestamped:   newTimestamped(),
		Correlation:   newCorrelation(opts...),
	}
}

// NewResourcesAppliedEventWithStatusSnapshot forwards an immutable patch set.
func NewResourcesAppliedEventWithStatusSnapshot(statusPatchSnapshot *templating.StatusPatchSnapshot, opts ...CorrelationOption) *ResourcesAppliedEvent {
	return &ResourcesAppliedEvent{
		StatusPatchSnapshot: statusPatchSnapshot,
		timestamped:         newTimestamped(),
		Correlation:         newCorrelation(opts...),
	}
}

func (e *ResourcesAppliedEvent) EventType() string { return EventTypeResourcesApplied }

// CloneForSubscriber restores authenticated shadows and isolates legacy payloads.
func (e *ResourcesAppliedEvent) CloneForSubscriber() busevents.Event {
	if e == nil {
		panic("cannot clone nil resources applied event")
	}
	clone := *e
	clone.StatusPatches = cloneStatusPatches(e.StatusPatches)
	if e.occurrence != nil {
		clone = withResourcesAppliedIdentity(&clone, mustInspectRenderOccurrence(e.renderOccurrenceCarrier))
	}
	return &clone
}

func withResourcesAppliedIdentity(source *ResourcesAppliedEvent, identity *renderOccurrenceIdentity) ResourcesAppliedEvent {
	event := *source
	event.CycleSnapshot = identity.cycle
	event.RenderProof = identity.proof
	event.StatusPatches = nil
	event.StatusPatchSnapshot = identity.statusPatches
	return event
}

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

	// StatusPatchSnapshot is the most recent successful immutable patch set.
	StatusPatchSnapshot *templating.StatusPatchSnapshot

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

// NewReconciliationFailedEventWithStatusSnapshot carries the last successful patch set.
func NewReconciliationFailedEventWithStatusSnapshot(err, phase string, statusPatchSnapshot *templating.StatusPatchSnapshot, opts ...CorrelationOption) *ReconciliationFailedEvent {
	return &ReconciliationFailedEvent{
		Error:               err,
		Phase:               phase,
		StatusPatchSnapshot: statusPatchSnapshot,
		timestamped:         newTimestamped(),
		Correlation:         newCorrelation(opts...),
	}
}

func (e *ReconciliationFailedEvent) EventType() string { return EventTypeReconciliationFailed }

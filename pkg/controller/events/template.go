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
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var renderProofSequence atomic.Uint64

func nextRenderProof() string {
	sequence := renderProofSequence.Add(1)
	if sequence == 0 {
		panic("render proof sequence exhausted")
	}
	return "r:" + strconv.FormatUint(sequence, 10)
}

// TemplateRenderedEvent is published when template rendering completes successfully.
//
// This event carries a single rendered HAProxy configuration using relative paths
// (maps/, ssl/, files/) that work with HAProxy's `default-path origin` directive.
// The same config works in any directory where the config file is placed.
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
//
// This event implements CoalescibleEvent. The coalescible flag is propagated from
// ReconciliationTriggeredEvent to enable coalescing throughout the reconciliation pipeline.
type TemplateRenderedEvent struct {
	renderOccurrenceCarrier

	// CycleSnapshot binds this output to every effect from the same render.
	CycleSnapshot *rendercycle.Snapshot

	// OutputSnapshot binds the exact config, plan, and auxiliary artifacts.
	OutputSnapshot *renderoutput.Snapshot

	// HAProxyConfig is the rendered main HAProxy configuration.
	// Uses relative paths (maps/, ssl/, files/) that work with HAProxy's `default-path origin`.
	HAProxyConfig string

	// AuxiliaryFiles contains all rendered auxiliary files (maps, certificates, general files).
	AuxiliaryFiles *dataplane.AuxiliaryFiles

	// AuxiliaryFileSnapshot is the authenticated immutable production representation.
	AuxiliaryFileSnapshot *renderartifact.Snapshot

	// StatusPatches contains status patches registered by templates during rendering.
	// Each patch targets a Kubernetes resource and contains outcome-keyed variants
	// for different pipeline lifecycle phases (rendered, deployed, renderFailed, deployFailed).
	StatusPatches []templating.StatusPatch

	// StatusPatchSnapshot is the authenticated immutable production representation.
	StatusPatchSnapshot *templating.StatusPatchSnapshot

	// RenderedResources contains full Kubernetes resources the templates declared
	// the controller should own and reconcile (e.g. an auxiliary Service or other
	// object a template emits alongside the HAProxy config). The applier compares
	// each against the last-applied checksum and skips unchanged entries to avoid
	// hammering the API server.
	RenderedResources []templating.RenderedResource

	// ContentChecksum is the pre-computed content checksum covering config + aux files.
	// Computed once in the pipeline and propagated to downstream consumers to avoid
	// redundant hashing in config publisher and deployment scheduler.
	ContentChecksum string

	// Plan is the structure this render declared, carried to the deployer so it
	// can diff against what each pod applied. Nil for renders that produced none.
	Plan *renderplan.Plan

	// PlanID is the digest identifying Plan.
	PlanID string

	// RenderProof identifies this render instance inside one controller process.
	RenderProof string

	// Metrics for observability
	ConfigBytes        int   // Size of HAProxyConfig
	AuxiliaryFileCount int   // Number of auxiliary files
	DurationMs         int64 // Total rendering duration

	// TriggerReason is the reason that triggered this reconciliation.
	// Propagated from ReconciliationTriggeredEvent.Reason.
	// Examples: "config_change", "debounce_timer", "drift_prevention"
	// Used by downstream components (e.g., DeploymentScheduler) to determine fallback behavior.
	TriggerReason string

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Propagated from ReconciliationTriggeredEvent.
	coalescible bool

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewTemplateRenderedEventWithCycle creates a production event from one render cycle.
func NewTemplateRenderedEventWithCycle(
	cycleSnapshot *rendercycle.Snapshot,
	durationMs int64,
	triggerReason string,
	coalescible bool,
	opts ...CorrelationOption,
) (*TemplateRenderedEvent, error) {
	carrier, err := createRenderOccurrenceCarrier(cycleSnapshot)
	if err != nil {
		return nil, fmt.Errorf("template rendered event: %w", err)
	}
	occurrence, err := carrier.RenderOccurrence()
	if err != nil {
		return nil, fmt.Errorf("template rendered event: %w", err)
	}
	return newTemplateRenderedEventWithOccurrence(
		occurrence, durationMs, triggerReason, coalescible, opts...,
	)
}

// NewTemplateRenderedEventWithOccurrence propagates an existing occurrence.
func NewTemplateRenderedEventWithOccurrence(
	occurrence *rendercycle.Occurrence,
	durationMs int64,
	triggerReason string,
	coalescible bool,
	opts ...CorrelationOption,
) (*TemplateRenderedEvent, error) {
	return newTemplateRenderedEventWithOccurrence(
		occurrence, durationMs, triggerReason, coalescible, opts...,
	)
}

func newTemplateRenderedEventWithOccurrence(
	occurrence *rendercycle.Occurrence,
	durationMs int64,
	triggerReason string,
	coalescible bool,
	opts ...CorrelationOption,
) (*TemplateRenderedEvent, error) {
	carrier, identity, err := inspectRenderOccurrence(occurrence)
	if err != nil {
		return nil, fmt.Errorf("template rendered event: %w", err)
	}
	event := newTemplateRenderedEvent(
		"", nil, nil, nil, nil, identity.counts.Artifacts, durationMs,
		triggerReason, "", nil, "", coalescible, opts...,
	)
	event.renderOccurrenceCarrier = carrier
	owned := withTemplateRenderedIdentity(event, identity)
	return &owned, nil
}

// NewTemplateRenderedEvent creates a new TemplateRenderedEvent.
//
// The coalescible parameter should be propagated from ReconciliationTriggeredEvent.Coalescible()
// to enable coalescing throughout the reconciliation pipeline.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewTemplateRenderedEvent(..., triggerReason, trigger.Coalescible(),
//	    events.PropagateCorrelation(triggeredEvent))
func NewTemplateRenderedEvent(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	statusPatches []templating.StatusPatch,
	renderedResources []templating.RenderedResource,
	auxFileCount int,
	durationMs int64,
	triggerReason string,
	contentChecksum string,
	plan *renderplan.Plan,
	planID string,
	coalescible bool,
	opts ...CorrelationOption,
) *TemplateRenderedEvent {
	return newTemplateRenderedEvent(
		haproxyConfig, auxiliaryFiles, statusPatches, nil, renderedResources, auxFileCount, durationMs,
		triggerReason, contentChecksum, plan, planID, coalescible, opts...,
	)
}

// NewTemplateRenderedEventWithStatusSnapshot creates an event without detaching status payloads.
func NewTemplateRenderedEventWithStatusSnapshot(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	statusPatchSnapshot *templating.StatusPatchSnapshot,
	renderedResources []templating.RenderedResource,
	auxFileCount int,
	durationMs int64,
	triggerReason string,
	contentChecksum string,
	plan *renderplan.Plan,
	planID string,
	coalescible bool,
	opts ...CorrelationOption,
) *TemplateRenderedEvent {
	return newTemplateRenderedEvent(
		haproxyConfig, auxiliaryFiles, nil, statusPatchSnapshot, renderedResources, auxFileCount, durationMs,
		triggerReason, contentChecksum, plan, planID, coalescible, opts...,
	)
}

// NewTemplateRenderedEventWithSnapshots creates an event from authenticated immutable output.
func NewTemplateRenderedEventWithSnapshots(
	haproxyConfig string,
	auxiliaryFileSnapshot *renderartifact.Snapshot,
	statusPatchSnapshot *templating.StatusPatchSnapshot,
	renderedResources []templating.RenderedResource,
	auxFileCount int,
	durationMs int64,
	triggerReason string,
	contentChecksum string,
	plan *renderplan.Plan,
	planID string,
	coalescible bool,
	opts ...CorrelationOption,
) (*TemplateRenderedEvent, error) {
	if auxiliaryFileSnapshot == nil {
		return nil, errors.New("template rendered event auxiliary-file snapshot is nil")
	}
	if err := auxiliaryFileSnapshot.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("template rendered event auxiliary-file snapshot: %w", err)
	}
	if statusPatchSnapshot != nil {
		if err := statusPatchSnapshot.ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf("template rendered event status-patch snapshot: %w", err)
		}
	}
	event := newTemplateRenderedEvent(
		haproxyConfig, nil, nil, statusPatchSnapshot, renderedResources, auxFileCount, durationMs,
		triggerReason, contentChecksum, plan, planID, coalescible, opts...,
	)
	event.AuxiliaryFileSnapshot = auxiliaryFileSnapshot
	return event, nil
}

func newTemplateRenderedEvent(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	statusPatches []templating.StatusPatch,
	statusPatchSnapshot *templating.StatusPatchSnapshot,
	renderedResources []templating.RenderedResource,
	auxFileCount int,
	durationMs int64,
	triggerReason string,
	contentChecksum string,
	plan *renderplan.Plan,
	planID string,
	coalescible bool,
	opts ...CorrelationOption,
) *TemplateRenderedEvent {
	return &TemplateRenderedEvent{
		HAProxyConfig:       haproxyConfig,
		AuxiliaryFiles:      dataplane.CloneAuxiliaryFiles(auxiliaryFiles),
		StatusPatches:       cloneStatusPatches(statusPatches),
		StatusPatchSnapshot: statusPatchSnapshot,
		RenderedResources:   cloneRenderedResources(renderedResources),
		ContentChecksum:     contentChecksum,
		Plan:                plan.Clone(),
		PlanID:              planID,
		RenderProof:         nextRenderProof(),
		ConfigBytes:         len(haproxyConfig),
		AuxiliaryFileCount:  auxFileCount,
		DurationMs:          durationMs,
		TriggerReason:       triggerReason,
		coalescible:         coalescible,
		timestamped:         newTimestamped(),
		Correlation:         newCorrelation(opts...),
	}
}

func (e *TemplateRenderedEvent) EventType() string { return EventTypeTemplateRendered }

// CloneForSubscriber restores authenticated shadows and isolates legacy payloads.
func (e *TemplateRenderedEvent) CloneForSubscriber() busevents.Event {
	if e == nil {
		panic("cannot clone nil template rendered event")
	}
	clone := *e
	clone.AuxiliaryFiles = dataplane.CloneAuxiliaryFiles(e.AuxiliaryFiles)
	clone.StatusPatches = cloneStatusPatches(e.StatusPatches)
	clone.RenderedResources = cloneRenderedResources(e.RenderedResources)
	clone.Plan = e.Plan.Clone()
	if e.occurrence != nil {
		clone = withTemplateRenderedIdentity(&clone, mustInspectRenderOccurrence(e.renderOccurrenceCarrier))
	}
	return &clone
}

func withTemplateRenderedIdentity(source *TemplateRenderedEvent, identity *renderOccurrenceIdentity) TemplateRenderedEvent {
	event := *source
	event.CycleSnapshot = identity.cycle
	event.OutputSnapshot = identity.output
	event.HAProxyConfig = identity.config
	event.AuxiliaryFiles = nil
	event.AuxiliaryFileSnapshot = identity.artifacts
	event.StatusPatches = nil
	event.StatusPatchSnapshot = identity.statusPatches
	event.RenderedResources = nil
	event.ContentChecksum = identity.contentChecksum
	event.Plan = nil
	event.PlanID = identity.planID
	event.RenderProof = identity.proof
	event.ConfigBytes = len(identity.config)
	event.AuxiliaryFileCount = identity.counts.Artifacts
	return event
}

// Coalescible returns true if this event can be safely skipped when a newer
// event of the same type is available. This implements the CoalescibleEvent interface.
func (e *TemplateRenderedEvent) Coalescible() bool { return e.coalescible }

// TemplateRenderFailedEvent is published when template rendering fails.
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
type TemplateRenderFailedEvent struct {
	// TemplateName is the name of the template that failed to render.
	TemplateName string

	// Error is the error message.
	Error string

	// StackTrace provides additional debugging context.
	StackTrace string

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewTemplateRenderFailedEvent creates a new TemplateRenderFailedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewTemplateRenderFailedEvent(name, err, stackTrace,
//	    events.PropagateCorrelation(triggeredEvent))
func NewTemplateRenderFailedEvent(templateName, err, stackTrace string, opts ...CorrelationOption) *TemplateRenderFailedEvent {
	return &TemplateRenderFailedEvent{
		TemplateName: templateName,
		Error:        err,
		StackTrace:   stackTrace,
		timestamped:  newTimestamped(),
		Correlation:  newCorrelation(opts...),
	}
}

func (e *TemplateRenderFailedEvent) EventType() string { return EventTypeTemplateRenderFailed }

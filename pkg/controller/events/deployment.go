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
	"maps"
	"slices"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// DeploymentStartedEvent is published when deployment to HAProxy instances begins.
//
// This event propagates the correlation ID from DeploymentScheduledEvent.
type DeploymentStartedEvent struct {
	Endpoints []dataplane.Endpoint
	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewDeploymentStartedEvent creates a new DeploymentStartedEvent.
// Performs defensive copy of the endpoints slice.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewDeploymentStartedEvent(endpoints,
//	    events.PropagateCorrelation(scheduledEvent))
func NewDeploymentStartedEvent(endpoints []dataplane.Endpoint, opts ...CorrelationOption) *DeploymentStartedEvent {
	return &DeploymentStartedEvent{
		Endpoints:   copySlice(endpoints),
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *DeploymentStartedEvent) EventType() string { return EventTypeDeploymentStarted }

// InstanceDeployedEvent is published when deployment to a single HAProxy instance succeeds.
//
// This event propagates the correlation ID from DeploymentStartedEvent.
type InstanceDeployedEvent struct {
	Endpoint       any // The HAProxy endpoint that was deployed to
	DurationMs     int64
	ReloadRequired bool // Whether this deployment required a HAProxy reload
	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewInstanceDeployedEvent creates a new InstanceDeployedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewInstanceDeployedEvent(endpoint, durationMs, reloadRequired,
//	    events.PropagateCorrelation(startedEvent))
func NewInstanceDeployedEvent(endpoint any, durationMs int64, reloadRequired bool, opts ...CorrelationOption) *InstanceDeployedEvent {
	return &InstanceDeployedEvent{
		Endpoint:       endpoint,
		DurationMs:     durationMs,
		ReloadRequired: reloadRequired,
		timestamped:    newTimestamped(),
		Correlation:    newCorrelation(opts...),
	}
}

func (e *InstanceDeployedEvent) EventType() string { return EventTypeInstanceDeployed }

// InstanceDeploymentFailedEvent is published when deployment to a single HAProxy instance fails.
//
// This event propagates the correlation ID from DeploymentStartedEvent.
type InstanceDeploymentFailedEvent struct {
	Endpoint  any
	Error     string
	Retryable bool // Whether this failure is retryable
	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewInstanceDeploymentFailedEvent creates a new InstanceDeploymentFailedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewInstanceDeploymentFailedEvent(endpoint, err, retryable,
//	    events.PropagateCorrelation(startedEvent))
func NewInstanceDeploymentFailedEvent(endpoint any, err string, retryable bool, opts ...CorrelationOption) *InstanceDeploymentFailedEvent {
	return &InstanceDeploymentFailedEvent{
		Endpoint:    endpoint,
		Error:       err,
		Retryable:   retryable,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *InstanceDeploymentFailedEvent) EventType() string { return EventTypeInstanceDeploymentFailed }

// DeploymentCompletedEvent is published when deployment to all HAProxy instances completes.
//
// This event propagates the correlation ID from DeploymentStartedEvent.
type DeploymentCompletedEvent struct {
	Total              int   // Total number of instances
	Succeeded          int   // Number of successful deployments
	Failed             int   // Number of failed deployments
	DurationMs         int64 // Total deployment duration in milliseconds
	ReloadsTriggered   int   // Count of instances that triggered HAProxy reload
	TotalAPIOperations int   // Sum of API operations across all instances
	timestamped

	// OperationBreakdown provides a generic breakdown of operations performed.
	// Keys are formatted as "section_type" (e.g., "backend_create", "server_update", "global_update").
	// Values are the count of operations of that type.
	// Aggregated across all successfully deployed instances.
	OperationBreakdown map[string]int

	// BackendDiffFields summarizes which BackendBase fields caused backend updates.
	// Empty when no backend attribute diffs were detected.
	// Example: "[GUID] (48 backends)" or "[Mode, Balance] (3 backends)"
	BackendDiffFields string

	// StatusPatches are the chart-rendered status patches that correspond to
	// the configuration this deployment carried. The StatusApplier reads them
	// from this event and applies the "deployed" variant — guaranteeing that
	// the status conditions it writes describe the config the data plane is
	// actually serving (no side-channel cache, no LATEST-vs-deployed race).
	//
	// Threaded through unchanged from the DeploymentScheduledEvent that
	// triggered this deployment.
	StatusPatches []templating.StatusPatch

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// DeploymentResult contains the outcome of a deployment operation.
// Used with NewDeploymentCompletedEvent for cleaner parameter passing.
type DeploymentResult struct {
	Total              int   // Total number of instances
	Succeeded          int   // Number of successful deployments
	Failed             int   // Number of failed deployments
	DurationMs         int64 // Total deployment duration in milliseconds
	ReloadsTriggered   int   // Count of instances that triggered HAProxy reload
	TotalAPIOperations int   // Sum of API operations across all instances

	// OperationBreakdown provides a generic breakdown of operations performed.
	// Keys are formatted as "section_type" (e.g., "backend_create", "server_update", "global_update").
	// Values are the count of operations of that type.
	OperationBreakdown map[string]int

	// BackendDiffFields summarizes which BackendBase fields caused backend updates.
	// Empty when no backend attribute diffs were detected.
	BackendDiffFields string

	// StatusPatches are the chart-rendered status patches for the
	// configuration this deployment carried. Forwarded from the
	// DeploymentScheduledEvent and surfaced on DeploymentCompletedEvent for
	// the StatusApplier to consume.
	StatusPatches []templating.StatusPatch
}

// NewDeploymentCompletedEvent creates a new DeploymentCompletedEvent.
//
// `result` is taken by pointer because DeploymentResult is large enough
// (≥96 bytes) that gocritic flags pass-by-value as `hugeParam`.
//
// `result.StatusPatches` should be forwarded unchanged from the
// DeploymentScheduledEvent that triggered the deployment so the
// StatusApplier reads the patches that correspond exactly to the
// configuration that just shipped (the chart's "deployed" variant).
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewDeploymentCompletedEvent(&events.DeploymentResult{
//	    Total:              len(endpoints),
//	    Succeeded:          successCount,
//	    Failed:             failureCount,
//	    DurationMs:         totalDurationMs,
//	    ReloadsTriggered:   reloads,
//	    TotalAPIOperations: ops,
//	    OperationBreakdown: breakdown,
//	    StatusPatches:      scheduledEvent.StatusPatches, // forward unchanged
//	}, events.PropagateCorrelation(startedEvent))
func NewDeploymentCompletedEvent(result *DeploymentResult, opts ...CorrelationOption) *DeploymentCompletedEvent {
	// Defensive copy of the map
	var breakdownCopy map[string]int
	if result.OperationBreakdown != nil {
		breakdownCopy = make(map[string]int, len(result.OperationBreakdown))
		maps.Copy(breakdownCopy, result.OperationBreakdown)
	}

	return &DeploymentCompletedEvent{
		Total:              result.Total,
		Succeeded:          result.Succeeded,
		Failed:             result.Failed,
		DurationMs:         result.DurationMs,
		ReloadsTriggered:   result.ReloadsTriggered,
		TotalAPIOperations: result.TotalAPIOperations,
		OperationBreakdown: breakdownCopy,
		BackendDiffFields:  result.BackendDiffFields,
		StatusPatches:      slices.Clone(result.StatusPatches),
		timestamped:        newTimestamped(),
		Correlation:        newCorrelation(opts...),
	}
}

func (e *DeploymentCompletedEvent) EventType() string { return EventTypeDeploymentCompleted }

// DeploymentSkippedEvent is published when the deployment scheduler determines
// that the data plane is already at the just-rendered configuration and no
// deployment work needs to be performed (typically: rendered config hash and
// pod-set hash both match the last successful deployment).
//
// Semantically this is NOT a deployment — nothing was pushed, no reload was
// triggered, no API operations were issued. It exists as its own event type
// so that downstream consumers can distinguish "the controller is converged"
// from "the controller just completed work."
//
// Currently consumed by:
//   - statusapplier, which treats this equivalently to DeploymentCompletedEvent
//     for the purpose of applying the "deployed" status-patch variant — the
//     data plane is serving the latest config, so Kubernetes status conditions
//     gated on data-plane readiness (e.g. Gateway.Programmed) should reflect
//     the current generation.
//
// Other consumers (metrics, commentator, drift_monitor, scheduler,
// statecache) do not subscribe by design — skipped deployments are a
// steady-state signal and bursting through those consumers would either
// produce log spam (commentator) or misleading counters (metrics). They can
// opt in later if there's a concrete need.
//
// This event propagates the correlation ID from the triggering event
// (typically ValidationCompletedEvent) so the converged path remains
// observable in correlation-based tracing.
type DeploymentSkippedEvent struct {
	// Total is the number of HAProxy endpoints already serving the rendered
	// configuration. Mirrors DeploymentCompletedEvent.Total so subscribers
	// can apply the same "is there actually a data plane to talk to?" guard.
	Total int

	// Reason is a short tag describing why the deployment was skipped.
	// Currently always "config_unchanged"; left as a string to leave room
	// for future skip causes (e.g. "drift_check_only") without an event
	// schema change.
	Reason string

	// ConfigHash is the content checksum of the rendered HAProxy
	// configuration that matched the last successful deployment. Useful
	// for debugging / correlation across the deployer's logs.
	ConfigHash string

	// PodSetHash is the hash of the endpoint set that matched the last
	// successful deployment. Useful for debugging / correlation.
	PodSetHash string

	// StatusPatches are the chart-rendered status patches for the
	// already-deployed configuration. The StatusApplier reads them from
	// this event to write the "deployed" variant — the data plane is
	// serving this exact config, so conditions gated on data-plane
	// readiness (e.g. Gateway.Programmed) should reflect the current
	// generation.
	StatusPatches []templating.StatusPatch

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewDeploymentSkippedEvent creates a new DeploymentSkippedEvent.
//
// statusPatches is the chart-rendered patch set for the already-deployed
// configuration; the StatusApplier reads it from the event to write the
// "deployed" variant. The outer slice is defensively cloned per the
// immutability contract documented in events/CLAUDE.md.
//
// Use PropagateCorrelation() to propagate correlation from the triggering
// event so the skip remains correlated with the originating reconciliation:
//
//	event := events.NewDeploymentSkippedEvent(
//	    len(endpoints),
//	    "config_unchanged",
//	    configHash,
//	    podSetHash,
//	    statusPatches,
//	    events.PropagateCorrelation(scheduledEvent),
//	)
func NewDeploymentSkippedEvent(total int, reason, configHash, podSetHash string, statusPatches []templating.StatusPatch, opts ...CorrelationOption) *DeploymentSkippedEvent {
	return &DeploymentSkippedEvent{
		Total:         total,
		Reason:        reason,
		ConfigHash:    configHash,
		PodSetHash:    podSetHash,
		StatusPatches: slices.Clone(statusPatches),
		timestamped:   newTimestamped(),
		Correlation:   newCorrelation(opts...),
	}
}

func (e *DeploymentSkippedEvent) EventType() string { return EventTypeDeploymentSkipped }

// DeploymentScheduledEvent is published when the deployment scheduler has decided.
// to execute a deployment. This event contains all necessary data for the deployer
// to execute the deployment without maintaining state.
//
// Published by: DeploymentScheduler.
// Consumed by: Deployer component.
//
// This event propagates the correlation ID from ValidationCompletedEvent.
//
// This event implements CoalescibleEvent. The coalescible flag is propagated from
// ValidationCompletedEvent to enable coalescing throughout the reconciliation pipeline.
type DeploymentScheduledEvent struct {
	// Config is the rendered HAProxy configuration to deploy.
	Config string

	// AuxiliaryFiles contains all rendered auxiliary files.
	AuxiliaryFiles *dataplane.AuxiliaryFiles

	// ParsedConfig is the pre-parsed desired configuration from validation.
	// May be nil if validation cache was used.
	// When non-nil, passed to sync operations to skip redundant parsing.
	ParsedConfig *parser.StructuredConfig

	// Endpoints is the list of HAProxy endpoints to deploy to.
	Endpoints []dataplane.Endpoint

	// RuntimeConfigName is the name of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigName string

	// RuntimeConfigNamespace is the namespace of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigNamespace string

	// ContentChecksum is the pre-computed content checksum covering config + aux files.
	// Propagated from TemplateRenderedEvent to enable aux file comparison caching
	// in the deployer — when the checksum matches the last-deployed checksum for
	// an endpoint, the expensive aux file comparison (Dataplane API downloads) is skipped.
	ContentChecksum string

	// Reason describes why this deployment was scheduled.
	// Examples: "config_validation", "pod_discovery", "drift_prevention"
	Reason string

	// StatusPatches are the chart-rendered status patches for this
	// configuration. The Deployer forwards them unchanged into
	// DeploymentCompletedEvent so the StatusApplier can apply the
	// "deployed" variant with the patches that correspond exactly to
	// the config this deployment shipped.
	StatusPatches []templating.StatusPatch

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Propagated from ValidationCompletedEvent.
	coalescible bool

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewDeploymentScheduledEvent creates a new DeploymentScheduledEvent.
// Performs defensive copy of endpoints slice.
//
// The coalescible parameter should be propagated from ValidationCompletedEvent.Coalescible()
// to enable coalescing throughout the reconciliation pipeline.
//
// The parsedConfig parameter contains the pre-parsed desired configuration from validation.
// Pass nil if validation cache was used or if the parsed config is not available.
//
// The contentChecksum is the pre-computed checksum of config + aux files, propagated from
// TemplateRenderedEvent. It enables the deployer to skip expensive aux file comparison
// when the content hasn't changed since the last successful sync to an endpoint.
//
// statusPatches is the chart-rendered patch set for this configuration. The Deployer
// forwards it unchanged into DeploymentCompletedEvent so the StatusApplier can apply
// the "deployed" variant with the patches that correspond exactly to the config this
// deployment shipped. The outer slice is defensively cloned.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewDeploymentScheduledEvent(config, auxFiles, parsedConfig, endpoints, name, ns, reason, contentChecksum, statusPatches, coalescible,
//	    events.PropagateCorrelation(validationEvent))
func NewDeploymentScheduledEvent(config string, auxFiles *dataplane.AuxiliaryFiles, parsedConfig *parser.StructuredConfig, endpoints []dataplane.Endpoint, runtimeConfigName, runtimeConfigNamespace, reason, contentChecksum string, statusPatches []templating.StatusPatch, coalescible bool, opts ...CorrelationOption) *DeploymentScheduledEvent {
	return &DeploymentScheduledEvent{
		Config:                 config,
		AuxiliaryFiles:         auxFiles,
		ParsedConfig:           parsedConfig,
		Endpoints:              copySlice(endpoints),
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		ContentChecksum:        contentChecksum,
		Reason:                 reason,
		StatusPatches:          slices.Clone(statusPatches),
		coalescible:            coalescible,
		timestamped:            newTimestamped(),
		Correlation:            newCorrelation(opts...),
	}
}

func (e *DeploymentScheduledEvent) EventType() string { return EventTypeDeploymentScheduled }

// Coalescible returns true if this event can be safely skipped when a newer
// event of the same type is available. This implements the CoalescibleEvent interface.
func (e *DeploymentScheduledEvent) Coalescible() bool { return e.coalescible }

// DeploymentCancelRequestEvent is published when the scheduler requests cancellation
// of an in-progress deployment (e.g., due to timeout).
//
// Published by: DeploymentScheduler (on timeout)
// Consumed by: Deployer (to cancel running deployment)
//
// The CorrelationID must match the deployment being cancelled.
type DeploymentCancelRequestEvent struct {
	// Reason describes why the deployment is being cancelled.
	Reason string

	timestamped
	Correlation
}

// NewDeploymentCancelRequestEvent creates a new DeploymentCancelRequestEvent.
func NewDeploymentCancelRequestEvent(reason string, opts ...CorrelationOption) *DeploymentCancelRequestEvent {
	return &DeploymentCancelRequestEvent{
		Reason:      reason,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *DeploymentCancelRequestEvent) EventType() string { return EventTypeDeploymentCancelRequest }

// DriftPreventionTriggeredEvent is published when the drift prevention monitor.
// detects that no deployment has occurred within the configured interval and
// triggers a deployment to prevent configuration drift.
//
// Published by: DriftPreventionMonitor.
// Consumed by: DeploymentScheduler (which then schedules a deployment).
type DriftPreventionTriggeredEvent struct {
	// TimeSinceLastDeployment is the duration since the last deployment completed.
	TimeSinceLastDeployment time.Duration

	timestamped
}

// NewDriftPreventionTriggeredEvent creates a new DriftPreventionTriggeredEvent.
func NewDriftPreventionTriggeredEvent(timeSinceLast time.Duration) *DriftPreventionTriggeredEvent {
	return &DriftPreventionTriggeredEvent{
		TimeSinceLastDeployment: timeSinceLast,
		timestamped:             newTimestamped(),
	}
}

func (e *DriftPreventionTriggeredEvent) EventType() string { return EventTypeDriftPreventionTriggered }

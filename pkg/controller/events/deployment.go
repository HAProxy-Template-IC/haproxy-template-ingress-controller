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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// -----------------------------------------------------------------------------
// Deployment Events.
// -----------------------------------------------------------------------------

// DeploymentStartedEvent is published when deployment to HAProxy instances begins.
//
// This event propagates the correlation ID from DeploymentScheduledEvent.
type DeploymentStartedEvent struct {
	Endpoints []interface{}
	timestamp time.Time

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
func NewDeploymentStartedEvent(endpoints []interface{}, opts ...CorrelationOption) *DeploymentStartedEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentStartedEvent{
		Endpoints:   endpointsCopy,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *DeploymentStartedEvent) EventType() string    { return EventTypeDeploymentStarted }
func (e *DeploymentStartedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeployedEvent is published when deployment to a single HAProxy instance succeeds.
//
// This event propagates the correlation ID from DeploymentStartedEvent.
type InstanceDeployedEvent struct {
	Endpoint       interface{} // The HAProxy endpoint that was deployed to
	DurationMs     int64
	ReloadRequired bool // Whether this deployment required an HAProxy reload
	timestamp      time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewInstanceDeployedEvent creates a new InstanceDeployedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewInstanceDeployedEvent(endpoint, durationMs, reloadRequired,
//	    events.PropagateCorrelation(startedEvent))
func NewInstanceDeployedEvent(endpoint interface{}, durationMs int64, reloadRequired bool, opts ...CorrelationOption) *InstanceDeployedEvent {
	return &InstanceDeployedEvent{
		Endpoint:       endpoint,
		DurationMs:     durationMs,
		ReloadRequired: reloadRequired,
		timestamp:      time.Now(),
		Correlation:    NewCorrelation(opts...),
	}
}

func (e *InstanceDeployedEvent) EventType() string    { return EventTypeInstanceDeployed }
func (e *InstanceDeployedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeploymentFailedEvent is published when deployment to a single HAProxy instance fails.
//
// This event propagates the correlation ID from DeploymentStartedEvent.
type InstanceDeploymentFailedEvent struct {
	Endpoint  interface{}
	Error     string
	Retryable bool // Whether this failure is retryable
	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewInstanceDeploymentFailedEvent creates a new InstanceDeploymentFailedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewInstanceDeploymentFailedEvent(endpoint, err, retryable,
//	    events.PropagateCorrelation(startedEvent))
func NewInstanceDeploymentFailedEvent(endpoint interface{}, err string, retryable bool, opts ...CorrelationOption) *InstanceDeploymentFailedEvent {
	return &InstanceDeploymentFailedEvent{
		Endpoint:    endpoint,
		Error:       err,
		Retryable:   retryable,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *InstanceDeploymentFailedEvent) EventType() string    { return EventTypeInstanceDeploymentFailed }
func (e *InstanceDeploymentFailedEvent) Timestamp() time.Time { return e.timestamp }

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
	timestamp          time.Time

	// OperationBreakdown provides a generic breakdown of operations performed.
	// Keys are formatted as "section_type" (e.g., "backend_create", "server_update", "global_update").
	// Values are the count of operations of that type.
	// Aggregated across all successfully deployed instances.
	OperationBreakdown map[string]int

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
}

// NewDeploymentCompletedEvent creates a new DeploymentCompletedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewDeploymentCompletedEvent(events.DeploymentResult{
//	    Total:              len(endpoints),
//	    Succeeded:          successCount,
//	    Failed:             failureCount,
//	    DurationMs:         totalDurationMs,
//	    ReloadsTriggered:   reloads,
//	    TotalAPIOperations: ops,
//	    OperationBreakdown: breakdown,
//	}, events.PropagateCorrelation(startedEvent))
func NewDeploymentCompletedEvent(result DeploymentResult, opts ...CorrelationOption) *DeploymentCompletedEvent {
	// Defensive copy of the map
	var breakdownCopy map[string]int
	if result.OperationBreakdown != nil {
		breakdownCopy = make(map[string]int, len(result.OperationBreakdown))
		for k, v := range result.OperationBreakdown {
			breakdownCopy[k] = v
		}
	}

	return &DeploymentCompletedEvent{
		Total:              result.Total,
		Succeeded:          result.Succeeded,
		Failed:             result.Failed,
		DurationMs:         result.DurationMs,
		ReloadsTriggered:   result.ReloadsTriggered,
		TotalAPIOperations: result.TotalAPIOperations,
		OperationBreakdown: breakdownCopy,
		timestamp:          time.Now(),
		Correlation:        NewCorrelation(opts...),
	}
}

func (e *DeploymentCompletedEvent) EventType() string    { return EventTypeDeploymentCompleted }
func (e *DeploymentCompletedEvent) Timestamp() time.Time { return e.timestamp }

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
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	AuxiliaryFiles interface{}

	// ParsedConfig is the pre-parsed desired configuration from validation.
	// May be nil if validation cache was used.
	// When non-nil, passed to sync operations to skip redundant parsing.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *parser.StructuredConfig.
	ParsedConfig interface{}

	// Endpoints is the list of HAProxy endpoints to deploy to.
	Endpoints []interface{}

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

	// coalescible indicates if this event can be safely skipped when a newer
	// event of the same type is available. Propagated from ValidationCompletedEvent.
	coalescible bool

	timestamp time.Time

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
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewDeploymentScheduledEvent(config, auxFiles, parsedConfig, endpoints, name, ns, reason, contentChecksum, coalescible,
//	    events.PropagateCorrelation(validationEvent))
func NewDeploymentScheduledEvent(config string, auxFiles interface{}, parsedConfig *parser.StructuredConfig, endpoints []interface{}, runtimeConfigName, runtimeConfigNamespace, reason, contentChecksum string, coalescible bool, opts ...CorrelationOption) *DeploymentScheduledEvent {
	// Defensive copy of endpoints slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentScheduledEvent{
		Config:                 config,
		AuxiliaryFiles:         auxFiles,
		ParsedConfig:           parsedConfig,
		Endpoints:              endpointsCopy,
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		ContentChecksum:        contentChecksum,
		Reason:                 reason,
		coalescible:            coalescible,
		timestamp:              time.Now(),
		Correlation:            NewCorrelation(opts...),
	}
}

func (e *DeploymentScheduledEvent) EventType() string    { return EventTypeDeploymentScheduled }
func (e *DeploymentScheduledEvent) Timestamp() time.Time { return e.timestamp }

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

	timestamp time.Time
	Correlation
}

// NewDeploymentCancelRequestEvent creates a new DeploymentCancelRequestEvent.
func NewDeploymentCancelRequestEvent(reason string, opts ...CorrelationOption) *DeploymentCancelRequestEvent {
	return &DeploymentCancelRequestEvent{
		Reason:      reason,
		timestamp:   time.Now(),
		Correlation: NewCorrelation(opts...),
	}
}

func (e *DeploymentCancelRequestEvent) EventType() string    { return EventTypeDeploymentCancelRequest }
func (e *DeploymentCancelRequestEvent) Timestamp() time.Time { return e.timestamp }

// DriftPreventionTriggeredEvent is published when the drift prevention monitor.
// detects that no deployment has occurred within the configured interval and
// triggers a deployment to prevent configuration drift.
//
// Published by: DriftPreventionMonitor.
// Consumed by: DeploymentScheduler (which then schedules a deployment).
type DriftPreventionTriggeredEvent struct {
	// TimeSinceLastDeployment is the duration since the last deployment completed.
	TimeSinceLastDeployment time.Duration

	timestamp time.Time
}

// NewDriftPreventionTriggeredEvent creates a new DriftPreventionTriggeredEvent.
func NewDriftPreventionTriggeredEvent(timeSinceLast time.Duration) *DriftPreventionTriggeredEvent {
	return &DriftPreventionTriggeredEvent{
		TimeSinceLastDeployment: timeSinceLast,
		timestamp:               time.Now(),
	}
}

func (e *DriftPreventionTriggeredEvent) EventType() string    { return EventTypeDriftPreventionTriggered }
func (e *DriftPreventionTriggeredEvent) Timestamp() time.Time { return e.timestamp }

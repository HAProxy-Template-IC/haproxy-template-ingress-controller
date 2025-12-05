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

import "time"

// -----------------------------------------------------------------------------
// Deployment Events.
// -----------------------------------------------------------------------------

// DeploymentStartedEvent is published when deployment to HAProxy instances begins.
type DeploymentStartedEvent struct {
	Endpoints []interface{}
	timestamp time.Time
}

// NewDeploymentStartedEvent creates a new DeploymentStartedEvent.
// Performs defensive copy of the endpoints slice.
func NewDeploymentStartedEvent(endpoints []interface{}) *DeploymentStartedEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentStartedEvent{
		Endpoints: endpointsCopy,
		timestamp: time.Now(),
	}
}

func (e *DeploymentStartedEvent) EventType() string    { return EventTypeDeploymentStarted }
func (e *DeploymentStartedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeployedEvent is published when deployment to a single HAProxy instance succeeds.
type InstanceDeployedEvent struct {
	Endpoint       interface{} // The HAProxy endpoint that was deployed to
	DurationMs     int64
	ReloadRequired bool // Whether this deployment required an HAProxy reload
	timestamp      time.Time
}

// NewInstanceDeployedEvent creates a new InstanceDeployedEvent.
func NewInstanceDeployedEvent(endpoint interface{}, durationMs int64, reloadRequired bool) *InstanceDeployedEvent {
	return &InstanceDeployedEvent{
		Endpoint:       endpoint,
		DurationMs:     durationMs,
		ReloadRequired: reloadRequired,
		timestamp:      time.Now(),
	}
}

func (e *InstanceDeployedEvent) EventType() string    { return EventTypeInstanceDeployed }
func (e *InstanceDeployedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeploymentFailedEvent is published when deployment to a single HAProxy instance fails.
type InstanceDeploymentFailedEvent struct {
	Endpoint  interface{}
	Error     string
	Retryable bool // Whether this failure is retryable
	timestamp time.Time
}

// NewInstanceDeploymentFailedEvent creates a new InstanceDeploymentFailedEvent.
func NewInstanceDeploymentFailedEvent(endpoint interface{}, err string, retryable bool) *InstanceDeploymentFailedEvent {
	return &InstanceDeploymentFailedEvent{
		Endpoint:  endpoint,
		Error:     err,
		Retryable: retryable,
		timestamp: time.Now(),
	}
}

func (e *InstanceDeploymentFailedEvent) EventType() string    { return EventTypeInstanceDeploymentFailed }
func (e *InstanceDeploymentFailedEvent) Timestamp() time.Time { return e.timestamp }

// DeploymentCompletedEvent is published when deployment to all HAProxy instances completes.
type DeploymentCompletedEvent struct {
	Total      int // Total number of instances
	Succeeded  int // Number of successful deployments
	Failed     int // Number of failed deployments
	DurationMs int64
	timestamp  time.Time
}

// NewDeploymentCompletedEvent creates a new DeploymentCompletedEvent.
func NewDeploymentCompletedEvent(total, succeeded, failed int, durationMs int64) *DeploymentCompletedEvent {
	return &DeploymentCompletedEvent{
		Total:      total,
		Succeeded:  succeeded,
		Failed:     failed,
		DurationMs: durationMs,
		timestamp:  time.Now(),
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
type DeploymentScheduledEvent struct {
	// Config is the rendered HAProxy configuration to deploy.
	Config string

	// AuxiliaryFiles contains all rendered auxiliary files.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	AuxiliaryFiles interface{}

	// Endpoints is the list of HAProxy endpoints to deploy to.
	Endpoints []interface{}

	// RuntimeConfigName is the name of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigName string

	// RuntimeConfigNamespace is the namespace of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigNamespace string

	// Reason describes why this deployment was scheduled.
	// Examples: "config_validation", "pod_discovery", "drift_prevention"
	Reason string

	timestamp time.Time
}

// NewDeploymentScheduledEvent creates a new DeploymentScheduledEvent.
// Performs defensive copy of endpoints slice.
func NewDeploymentScheduledEvent(config string, auxFiles interface{}, endpoints []interface{}, runtimeConfigName, runtimeConfigNamespace, reason string) *DeploymentScheduledEvent {
	// Defensive copy of endpoints slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentScheduledEvent{
		Config:                 config,
		AuxiliaryFiles:         auxFiles,
		Endpoints:              endpointsCopy,
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		Reason:                 reason,
		timestamp:              time.Now(),
	}
}

func (e *DeploymentScheduledEvent) EventType() string    { return EventTypeDeploymentScheduled }
func (e *DeploymentScheduledEvent) Timestamp() time.Time { return e.timestamp }

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

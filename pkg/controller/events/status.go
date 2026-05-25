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

// StatusPatchPhase identifies which pipeline phase triggered a status patch application.
type StatusPatchPhase string

const (
	// StatusPatchPhaseRendered is applied after successful template rendering.
	StatusPatchPhaseRendered StatusPatchPhase = "rendered"

	// StatusPatchPhaseDeployed is applied after successful HAProxy deployment.
	StatusPatchPhaseDeployed StatusPatchPhase = "deployed"

	// StatusPatchPhaseRenderFailed is applied when template rendering fails
	// before any output is produced.
	StatusPatchPhaseRenderFailed StatusPatchPhase = "renderFailed"

	// StatusPatchPhaseValidateFailed is applied when the rendered config
	// passed templating but was rejected by validation (syntax, schema, or
	// semantic checks) before any deploy attempt. Distinct from
	// renderFailed (no output produced) and deployFailed (deploy attempted
	// and rolled back). Chart templates may emit the same payload as
	// renderFailed until validation failures need a distinct surface.
	StatusPatchPhaseValidateFailed StatusPatchPhase = "validateFailed"

	// StatusPatchPhaseDeployFailed is applied when HAProxy deployment fails.
	StatusPatchPhaseDeployFailed StatusPatchPhase = "deployFailed"
)

// StatusUpdateCompletedEvent is published when status patch application completes.
//
// This event propagates the correlation ID from the triggering reconciliation event.
type StatusUpdateCompletedEvent struct {
	// Phase identifies which pipeline phase triggered this status update.
	Phase StatusPatchPhase

	// AppliedCount is the number of status patches successfully applied.
	AppliedCount int

	// SkippedCount is the number of status patches skipped (checksum match).
	SkippedCount int

	// DurationMs is the total duration of status patch application.
	DurationMs int64

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewStatusUpdateCompletedEvent creates a new StatusUpdateCompletedEvent.
func NewStatusUpdateCompletedEvent(
	phase StatusPatchPhase,
	appliedCount int,
	skippedCount int,
	durationMs int64,
	opts ...CorrelationOption,
) *StatusUpdateCompletedEvent {
	return &StatusUpdateCompletedEvent{
		Phase:        phase,
		AppliedCount: appliedCount,
		SkippedCount: skippedCount,
		DurationMs:   durationMs,
		timestamped:  newTimestamped(),
		Correlation:  newCorrelation(opts...),
	}
}

func (e *StatusUpdateCompletedEvent) EventType() string { return EventTypeStatusUpdateCompleted }

// StatusUpdateFailedEvent is published when a status patch application fails for a resource.
//
// This event propagates the correlation ID from the triggering reconciliation event.
type StatusUpdateFailedEvent struct {
	// Namespace is the namespace of the target Kubernetes resource.
	Namespace string

	// Name is the name of the target Kubernetes resource.
	Name string

	// GVR is the GroupVersionResource string of the target resource (e.g., "networking.k8s.io/v1/ingresses").
	GVR string

	// Error is the error message from the failed SSA patch.
	Error string

	// Retriable indicates whether the failure is transient and can be retried.
	Retriable bool

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewStatusUpdateFailedEvent creates a new StatusUpdateFailedEvent.
func NewStatusUpdateFailedEvent(
	namespace string,
	name string,
	gvr string,
	err string,
	retriable bool,
	opts ...CorrelationOption,
) *StatusUpdateFailedEvent {
	return &StatusUpdateFailedEvent{
		Namespace:   namespace,
		Name:        name,
		GVR:         gvr,
		Error:       err,
		Retriable:   retriable,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *StatusUpdateFailedEvent) EventType() string { return EventTypeStatusUpdateFailed }

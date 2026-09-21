// Copyright 2026 Philipp Hossner
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

// Package diagnostics collects content-free evidence about a HAPTIC fleet.
package diagnostics

import "time"

// Report deliberately has no arbitrary payload fields; see CLAUDE.md.
type Report struct {
	SchemaVersion  int             `json:"schema_version"`
	CollectedAt    time.Time       `json:"collected_at"`
	Namespace      string          `json:"namespace"`
	Release        string          `json:"release"`
	Complete       bool            `json:"complete"`
	Healthy        bool            `json:"healthy"`
	Configurations []Configuration `json:"configurations"`
	Controllers    []Controller    `json:"controllers"`
	Agents         []Agent         `json:"agents"`
	Resources      []Resource      `json:"resources"`
	Findings       []Finding       `json:"findings"`
}

type Identity struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

type Condition struct {
	Path               string    `json:"path"`
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	ObservedGeneration int64     `json:"observed_generation,omitempty"`
	LastTransitionTime time.Time `json:"last_transition_time,omitzero"`
}

type Resource struct {
	Identity
	Watch      string      `json:"watch,omitempty"`
	Conditions []Condition `json:"conditions"`
}

type Configuration struct {
	Resource
	ValidationStatus   string          `json:"validation_status,omitempty"`
	ValidationErrors   int             `json:"validation_error_count"`
	ObservedGeneration int64           `json:"observed_generation,omitempty"`
	DesiredChecksum    string          `json:"desired_checksum,omitempty"`
	Deployments        []PodDeployment `json:"deployments,omitempty"`
}

type PodDeployment struct {
	Pod               string `json:"pod"`
	UID               string `json:"uid,omitempty"`
	Checksum          string `json:"checksum,omitempty"`
	AppliedPlanID     string `json:"applied_plan_id,omitempty"`
	RunningPlanID     string `json:"running_plan_id,omitempty"`
	ConsecutiveErrors int    `json:"consecutive_errors"`
	HasError          bool   `json:"has_error"`
}

type Pod struct {
	Identity
	Ready      bool        `json:"ready"`
	Containers []Container `json:"containers"`
}

type Container struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	ImageID  string `json:"image_id,omitempty"`
	Ready    bool   `json:"ready"`
	Restarts int32  `json:"restarts"`
	Phase    string `json:"phase"`
	Reason   string `json:"reason,omitempty"`
}

type Phase struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp,omitzero"`
	Errors    int       `json:"error_count"`
}

type Failure struct {
	Timestamp     time.Time `json:"timestamp"`
	Type          string    `json:"type"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

type Controller struct {
	Pod
	Observed      bool     `json:"observed"`
	Rendering     Phase    `json:"rendering"`
	Validation    Phase    `json:"validation"`
	Deployment    Phase    `json:"deployment"`
	ValidatedPlan string   `json:"validated_plan,omitempty"`
	LastFailure   *Failure `json:"last_failure,omitempty"`
}

type Agent struct {
	Pod
	Observed                  bool   `json:"observed"`
	ProtocolVersion           int    `json:"protocol_version"`
	PlanSchemaVersion         int    `json:"plan_schema_version"`
	AgentVersion              string `json:"agent_version,omitempty"`
	HAProxyVersion            string `json:"haproxy_version,omitempty"`
	WorkerRunning             bool   `json:"worker_running"`
	WorkerPID                 int    `json:"worker_pid"`
	WorkerStartTimeUnixMicros int64  `json:"worker_start_time_unix_micros"`
	WorkerGeneration          uint64 `json:"worker_generation"`
	AppliedPlanID             string `json:"applied_plan_id,omitempty"`
	RunningPlanID             string `json:"running_plan_id,omitempty"`
	WorkerOpsPlanID           string `json:"worker_ops_plan_id,omitempty"`
	ConfigFileDigest          string `json:"config_file_digest,omitempty"`
	FileCount                 int    `json:"file_count"`
	ReloadFallback            bool   `json:"reload_fallback"`
	LastApplyFailed           bool   `json:"last_apply_failed"`
	InvariantFailed           bool   `json:"invariant_failed"`
}

type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Resource string `json:"resource,omitempty"`
	Action   string `json:"action"`
}

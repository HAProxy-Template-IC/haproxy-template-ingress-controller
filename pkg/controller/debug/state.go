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

// Package debug provides controller-specific debug variable implementations.
//
// This package integrates the generic pkg/introspection infrastructure with
// controller-specific state. It defines:
//   - StateProvider interface for accessing controller internal state
//   - Var implementations (ConfigVar, RenderedVar, etc.)
//   - Event buffer for tracking recent events
//   - Registration logic for publishing variables
//
// The controller implements StateProvider and provides thread-safe access
// to its internal state (config, rendered output, resources, etc.).
package debug

import (
	"time"

	"haptic/pkg/core/config"
	"haptic/pkg/dataplane"
)

// StateProvider provides access to controller internal state.
//
// This interface is implemented by the controller to expose its state
// to debug variables in a thread-safe manner. The controller caches
// state by subscribing to events (ConfigValidatedEvent, RenderCompletedEvent, etc.)
// and updates the cached values accordingly.
//
// All methods must be thread-safe as they may be called concurrently
// from HTTP request handlers.
type StateProvider interface {
	// GetConfig returns the current validated configuration and its version.
	//
	// Returns error if config is not yet loaded.
	//
	// Example return:
	//   config: &config.Config{...}
	//   version: "v123"
	//   error: nil
	GetConfig() (*config.Config, string, error)

	// GetCredentials returns the current credentials and their version.
	//
	// Returns error if credentials are not yet loaded.
	//
	// Example return:
	//   creds: &config.Credentials{...}
	//   version: "v456"
	//   error: nil
	GetCredentials() (*config.Credentials, string, error)

	// GetRenderedConfig returns the most recently rendered HAProxy configuration
	// and the timestamp when it was rendered.
	//
	// Returns error if no config has been rendered yet.
	//
	// Example return:
	//   rendered: "global\n  maxconn 2000\n..."
	//   timestamp: 2025-01-15 10:30:45
	//   error: nil
	GetRenderedConfig() (string, time.Time, error)

	// GetAuxiliaryFiles returns the most recently used auxiliary files
	// (SSL certificates, map files, general files) and the timestamp.
	//
	// Returns error if no auxiliary files have been cached yet.
	//
	// Example return:
	//   auxFiles: &dataplane.AuxiliaryFiles{SSLCertificates: [...], ...}
	//   timestamp: 2025-01-15 10:30:45
	//   error: nil
	GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error)

	// GetResourceCounts returns a map of resource type → count.
	//
	// The keys are resource names as defined in the controller configuration
	// (e.g., "ingresses", "services", "haproxy-pods").
	//
	// Example return:
	//   {
	//     "ingresses": 5,
	//     "services": 12,
	//     "haproxy-pods": 2
	//   }
	GetResourceCounts() (map[string]int, error)

	// GetResourcesByType returns all resources of a specific type.
	//
	// The resourceType parameter should match a key from GetResourceCounts().
	//
	// Returns error if the resource type is not found.
	//
	// Example:
	//   resources, err := provider.GetResourcesByType("ingresses")
	GetResourcesByType(resourceType string) ([]interface{}, error)

	// GetPipelineStatus returns the complete status of the last reconciliation pipeline.
	//
	// Returns error if no pipeline has run yet.
	//
	// Example return:
	//   status: &PipelineStatus{
	//     LastTrigger: &TriggerStatus{Timestamp: ..., Reason: "config_change"},
	//     Rendering:   &RenderingStatus{Status: "succeeded", ...},
	//     Validation:  &ValidationStatus{Status: "failed", Errors: [...]},
	//     Deployment:  &DeploymentStatus{Status: "skipped", Reason: "validation_failed"},
	//   }
	GetPipelineStatus() (*PipelineStatus, error)

	// GetValidatedConfig returns the last successfully validated HAProxy configuration.
	//
	// Unlike GetRenderedConfig(), this only returns configs that passed validation.
	// Returns error if no config has been validated successfully yet.
	//
	// Example return:
	//   info: &ValidatedConfigInfo{
	//     Config:               "global\n  maxconn 2000\n...",
	//     Timestamp:            2025-01-15 10:30:45,
	//     ConfigBytes:          4567,
	//     ValidationDurationMs: 200,
	//   }
	GetValidatedConfig() (*ValidatedConfigInfo, error)

	// GetErrors returns an aggregated summary of recent errors across all phases.
	//
	// This is useful for quick diagnosis of configuration issues.
	//
	// Example return:
	//   summary: &ErrorSummary{
	//     HAProxyValidationError: &ErrorInfo{
	//       Timestamp: 2025-01-15 10:30:47,
	//       Errors:    ["[ALERT] parsing [haproxy.cfg:3] : unknown keyword..."],
	//     },
	//     LastErrorTimestamp: 2025-01-15 10:30:47,
	//   }
	GetErrors() (*ErrorSummary, error)
}

// ComponentStatus represents the status of a controller component.
//
// Used by GetComponentStatus() to provide insight into component health.
type ComponentStatus struct {
	// Running indicates if the component is currently active
	Running bool `json:"running"`

	// LastSeen is the timestamp of the last activity from this component
	LastSeen time.Time `json:"last_seen"`

	// ErrorRate is the percentage of errors (0.0 to 1.0)
	// Optional - may be 0 if not tracked
	ErrorRate float64 `json:"error_rate,omitempty"`

	// Details provides additional component-specific information
	// Optional - may be nil
	Details map[string]interface{} `json:"details,omitempty"`
}

// PipelineStatus represents the complete status of the last reconciliation pipeline.
//
// This provides visibility into each stage of the pipeline: trigger, rendering,
// validation, and deployment.
type PipelineStatus struct {
	LastTrigger *TriggerStatus    `json:"last_trigger"`
	Rendering   *RenderingStatus  `json:"rendering"`
	Validation  *ValidationStatus `json:"validation"`
	Deployment  *DeploymentStatus `json:"deployment"`
}

// TriggerStatus represents what triggered the last reconciliation.
type TriggerStatus struct {
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
}

// RenderingStatus represents the template rendering phase status.
type RenderingStatus struct {
	Status      string    `json:"status"` // "succeeded" | "failed"
	Timestamp   time.Time `json:"timestamp"`
	DurationMs  int64     `json:"duration_ms"`
	ConfigBytes int       `json:"config_bytes"`
	Error       string    `json:"error,omitempty"`
}

// ValidationStatus represents the HAProxy validation phase status.
type ValidationStatus struct {
	Status     string    `json:"status"` // "succeeded" | "failed" | "pending"
	Timestamp  time.Time `json:"timestamp"`
	DurationMs int64     `json:"duration_ms"`
	Errors     []string  `json:"errors,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
}

// DeploymentStatus represents the deployment phase status.
type DeploymentStatus struct {
	Status             string           `json:"status"` // "succeeded" | "failed" | "skipped" | "pending"
	Reason             string           `json:"reason,omitempty"`
	Timestamp          time.Time        `json:"timestamp"`
	DurationMs         int64            `json:"duration_ms,omitempty"`
	EndpointsTotal     int              `json:"endpoints_total"`
	EndpointsSucceeded int              `json:"endpoints_succeeded"`
	EndpointsFailed    int              `json:"endpoints_failed"`
	FailedEndpoints    []FailedEndpoint `json:"failed_endpoints,omitempty"`
}

// FailedEndpoint contains details about a failed deployment endpoint.
type FailedEndpoint struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// ValidatedConfigInfo contains information about the last successfully validated config.
type ValidatedConfigInfo struct {
	Config               string    `json:"config"`
	Timestamp            time.Time `json:"timestamp"`
	ConfigBytes          int       `json:"config_bytes"`
	ValidationDurationMs int64     `json:"validation_duration_ms"`
}

// ErrorSummary provides an aggregated view of recent errors across all phases.
type ErrorSummary struct {
	ConfigParseError       *ErrorInfo  `json:"config_parse_error,omitempty"`
	TemplateRenderError    *ErrorInfo  `json:"template_render_error,omitempty"`
	HAProxyValidationError *ErrorInfo  `json:"haproxy_validation_error,omitempty"`
	DeploymentErrors       []ErrorInfo `json:"deployment_errors,omitempty"`
	LastErrorTimestamp     time.Time   `json:"last_error_timestamp,omitempty"`
}

// ErrorInfo contains details about a specific error.
type ErrorInfo struct {
	Timestamp time.Time `json:"timestamp"`
	Errors    []string  `json:"errors"`
}

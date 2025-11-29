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

package debug

import (
	"time"
)

// ConfigVar exposes the current controller configuration.
//
// Returns a JSON object containing:
//   - config: the full Config struct
//   - version: the ConfigMap resource version
//   - updated: timestamp when the config was last updated
//
// Example response:
//
//	{
//	  "config": {
//	    "templates": {"main": "..."},
//	    "watched_resources": [...]
//	  },
//	  "version": "v123",
//	  "updated": "2025-01-15T10:30:45Z"
//	}
type ConfigVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *ConfigVar) Get() (interface{}, error) {
	cfg, version, err := v.provider.GetConfig()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"config":  cfg,
		"version": version,
		"updated": time.Now(),
	}, nil
}

// CredentialsVar exposes the current controller credentials.
//
// Returns a JSON object containing:
//   - version: the Secret resource version
//   - updated: timestamp when credentials were last updated
//
// Note: Does NOT expose actual credential values for security.
//
// Example response:
//
//	{
//	  "version": "v456",
//	  "updated": "2025-01-15T10:30:45Z",
//	  "has_dataplane_creds": true
//	}
type CredentialsVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *CredentialsVar) Get() (interface{}, error) {
	creds, version, err := v.provider.GetCredentials()
	if err != nil {
		return nil, err
	}

	// Don't expose actual credentials - only metadata
	return map[string]interface{}{
		"version":             version,
		"updated":             time.Now(),
		"has_dataplane_creds": creds != nil && creds.DataplaneUsername != "" && creds.DataplanePassword != "",
	}, nil
}

// RenderedVar exposes the most recently rendered HAProxy configuration.
//
// Returns a JSON object containing:
//   - config: the rendered HAProxy config string
//   - timestamp: when it was rendered
//   - size: length of the config in bytes
//
// Example response:
//
//	{
//	  "config": "global\n  maxconn 2000\n...",
//	  "timestamp": "2025-01-15T10:30:45Z",
//	  "size": 4567
//	}
type RenderedVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *RenderedVar) Get() (interface{}, error) {
	rendered, timestamp, err := v.provider.GetRenderedConfig()
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"config":    rendered,
		"timestamp": timestamp,
		"size":      len(rendered),
	}, nil
}

// AuxFilesVar exposes the auxiliary files used in the last deployment.
//
// Returns a JSON object containing:
//   - files: the AuxiliaryFiles struct with SSL certs, maps, general files
//   - timestamp: when these files were last used
//   - summary: counts of each file type
//
// Example response:
//
//	{
//	  "files": {
//	    "ssl_certificates": [...],
//	    "map_files": [...],
//	    "general_files": [...]
//	  },
//	  "timestamp": "2025-01-15T10:30:45Z",
//	  "summary": {
//	    "ssl_count": 2,
//	    "map_count": 1,
//	    "general_count": 3
//	  }
//	}
type AuxFilesVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *AuxFilesVar) Get() (interface{}, error) {
	auxFiles, timestamp, err := v.provider.GetAuxiliaryFiles()
	if err != nil {
		return nil, err
	}

	summary := map[string]int{
		"ssl_count":     len(auxFiles.SSLCertificates),
		"map_count":     len(auxFiles.MapFiles),
		"general_count": len(auxFiles.GeneralFiles),
	}

	return map[string]interface{}{
		"files":     auxFiles,
		"timestamp": timestamp,
		"summary":   summary,
	}, nil
}

// ResourcesVar exposes resource counts by type.
//
// Returns a map of resource type → count.
//
// Example response:
//
//	{
//	  "ingresses": 5,
//	  "services": 12,
//	  "haproxy-pods": 2
//	}
type ResourcesVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *ResourcesVar) Get() (interface{}, error) {
	return v.provider.GetResourceCounts()
}

// FullStateVar exposes all controller state in a single dump.
//
// Warning: This can return very large responses. Use with caution.
// Prefer querying specific variables or using field selection.
//
// Returns a JSON object containing:
//   - config: current config and version
//   - rendered: last rendered config
//   - auxfiles: auxiliary files
//   - resources: resource counts
//   - recent_events: last 100 events
//   - snapshot_time: when this snapshot was taken
type FullStateVar struct {
	provider    StateProvider
	eventBuffer *EventBuffer
}

// Get implements introspection.Var.
func (v *FullStateVar) Get() (interface{}, error) {
	// Gather all state (best effort - don't fail if some parts are unavailable)
	cfg, cfgVer, _ := v.provider.GetConfig()
	rendered, renderedTime, _ := v.provider.GetRenderedConfig()
	auxFiles, auxTime, _ := v.provider.GetAuxiliaryFiles()
	resources, _ := v.provider.GetResourceCounts()

	recentEvents := []Event{}
	if v.eventBuffer != nil {
		recentEvents = v.eventBuffer.GetLast(100)
	}

	return map[string]interface{}{
		"config": map[string]interface{}{
			"config":  cfg,
			"version": cfgVer,
		},
		"rendered": map[string]interface{}{
			"config":    rendered,
			"timestamp": renderedTime,
		},
		"auxfiles": map[string]interface{}{
			"files":     auxFiles,
			"timestamp": auxTime,
		},
		"resources":     resources,
		"recent_events": recentEvents,
		"snapshot_time": time.Now(),
	}, nil
}

// PipelineVar exposes the reconciliation pipeline status.
//
// Returns a JSON object containing the status of each pipeline phase:
//   - last_trigger: what triggered the reconciliation
//   - rendering: template rendering status
//   - validation: HAProxy validation status
//   - deployment: deployment to HAProxy instances status
//
// Example response:
//
//	{
//	  "last_trigger": {"timestamp": "2025-01-15T10:30:45Z", "reason": "config_change"},
//	  "rendering": {"status": "succeeded", ...},
//	  "validation": {"status": "failed", "errors": [...]},
//	  "deployment": {"status": "skipped", "reason": "validation_failed"}
//	}
type PipelineVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *PipelineVar) Get() (interface{}, error) {
	return v.provider.GetPipelineStatus()
}

// ValidatedVar exposes the last successfully validated HAProxy configuration.
//
// Unlike RenderedVar, this only shows configs that passed validation.
// This is useful to see what config is actually safe to deploy.
//
// Example response:
//
//	{
//	  "config": "global\n  maxconn 2000\n...",
//	  "timestamp": "2025-01-15T10:30:45Z",
//	  "config_bytes": 4567,
//	  "validation_duration_ms": 200
//	}
type ValidatedVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *ValidatedVar) Get() (interface{}, error) {
	return v.provider.GetValidatedConfig()
}

// ErrorsVar exposes an aggregated view of recent errors across all phases.
//
// Returns a JSON object containing errors from each phase:
//   - config_parse_error: config parsing errors
//   - template_render_error: template rendering errors
//   - haproxy_validation_error: HAProxy validation errors
//   - deployment_errors: per-endpoint deployment errors
//   - last_error_timestamp: when the most recent error occurred
//
// Example response:
//
//	{
//	  "haproxy_validation_error": {
//	    "timestamp": "2025-01-15T10:30:47Z",
//	    "errors": ["[ALERT] parsing [haproxy.cfg:3] : unknown keyword..."]
//	  },
//	  "last_error_timestamp": "2025-01-15T10:30:47Z"
//	}
type ErrorsVar struct {
	provider StateProvider
}

// Get implements introspection.Var.
func (v *ErrorsVar) Get() (interface{}, error) {
	return v.provider.GetErrors()
}

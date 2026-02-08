// Copyright 2025 Philipp Hossner.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at.
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software.
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and.
// limitations under the License.

package controller

import (
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// GetConfig implements debug.StateProvider.
func (sc *StateCache) GetConfig() (*coreconfig.Config, string, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.currentConfig == nil {
		return nil, "", fmt.Errorf("config not loaded yet")
	}

	return sc.currentConfig, sc.currentConfigVersion, nil
}

// GetCredentials implements debug.StateProvider.
func (sc *StateCache) GetCredentials() (*coreconfig.Credentials, string, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.currentCreds == nil {
		return nil, "", fmt.Errorf("credentials not loaded yet")
	}

	return sc.currentCreds, sc.currentCredsVersion, nil
}

// GetRenderedConfig implements debug.StateProvider.
func (sc *StateCache) GetRenderedConfig() (string, time.Time, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.lastRendered == "" {
		return "", time.Time{}, fmt.Errorf("no config rendered yet")
	}

	return sc.lastRendered, sc.lastRenderedTime, nil
}

// GetAuxiliaryFiles implements debug.StateProvider.
func (sc *StateCache) GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.lastAuxFiles == nil {
		// Return empty but valid structure
		return &dataplane.AuxiliaryFiles{}, time.Time{}, nil
	}

	return sc.lastAuxFiles, sc.lastAuxFilesTime, nil
}

// GetResourceCounts implements debug.StateProvider.
func (sc *StateCache) GetResourceCounts() (map[string]int, error) {
	if sc.resourceWatcher == nil {
		return nil, fmt.Errorf("resource watcher not initialized")
	}

	stores := sc.resourceWatcher.GetAllStores()
	counts := make(map[string]int, len(stores))

	for name, store := range stores {
		items, err := store.List()
		if err != nil {
			return nil, fmt.Errorf("failed to list resources for %q: %w", name, err)
		}
		counts[name] = len(items)
	}

	return counts, nil
}

// GetResourcesByType implements debug.StateProvider.
func (sc *StateCache) GetResourcesByType(resourceType string) ([]interface{}, error) {
	if sc.resourceWatcher == nil {
		return nil, fmt.Errorf("resource watcher not initialized")
	}

	stores := sc.resourceWatcher.GetAllStores()
	store, ok := stores[resourceType]
	if !ok {
		return nil, fmt.Errorf("resource type %q not found", resourceType)
	}

	return store.List()
}

// GetPipelineStatus implements debug.StateProvider.
func (sc *StateCache) GetPipelineStatus() (*debug.PipelineStatus, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Return nil status fields if they haven't been populated yet
	var triggerStatus *debug.TriggerStatus
	if !sc.lastTriggerTime.IsZero() {
		triggerStatus = &debug.TriggerStatus{
			Timestamp: sc.lastTriggerTime,
			Reason:    sc.lastTriggerReason,
		}
	}

	var renderingStatus *debug.RenderingStatus
	if sc.renderStatus != "" {
		renderingStatus = &debug.RenderingStatus{
			Status:      sc.renderStatus,
			Timestamp:   sc.renderTime,
			DurationMs:  sc.renderDurationMs,
			ConfigBytes: len(sc.lastRendered),
			Error:       sc.renderError,
		}
	}

	var validationStatus *debug.ValidationStatus
	if sc.validationStatus != "" {
		validationStatus = &debug.ValidationStatus{
			Status:     sc.validationStatus,
			Timestamp:  sc.validationTime,
			DurationMs: sc.validationDurationMs,
			Errors:     sc.validationErrors,
			Warnings:   sc.validationWarnings,
		}
	}

	var deploymentStatus *debug.DeploymentStatus
	if sc.deploymentStatus != "" {
		deploymentStatus = &debug.DeploymentStatus{
			Status:             sc.deploymentStatus,
			Reason:             sc.deploymentReason,
			Timestamp:          sc.deploymentTime,
			DurationMs:         sc.deploymentDurationMs,
			EndpointsTotal:     sc.endpointsTotal,
			EndpointsSucceeded: sc.endpointsSucceeded,
			EndpointsFailed:    sc.endpointsFailed,
			FailedEndpoints:    sc.failedEndpoints,
		}
	}

	return &debug.PipelineStatus{
		LastTrigger: triggerStatus,
		Rendering:   renderingStatus,
		Validation:  validationStatus,
		Deployment:  deploymentStatus,
	}, nil
}

// GetValidatedConfig implements debug.StateProvider.
func (sc *StateCache) GetValidatedConfig() (*debug.ValidatedConfigInfo, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.lastValidatedConfig == "" {
		return nil, fmt.Errorf("no config validated yet")
	}

	return &debug.ValidatedConfigInfo{
		Config:               sc.lastValidatedConfig,
		Timestamp:            sc.lastValidatedTime,
		ConfigBytes:          len(sc.lastValidatedConfig),
		ValidationDurationMs: sc.validationDurationMs,
	}, nil
}

// GetErrors implements debug.StateProvider.
func (sc *StateCache) GetErrors() (*debug.ErrorSummary, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	summary := &debug.ErrorSummary{}

	// Check for render error
	if sc.renderStatus == statusFailed && sc.renderError != "" {
		summary.TemplateRenderError = &debug.ErrorInfo{
			Timestamp: sc.renderTime,
			Errors:    []string{sc.renderError},
		}
		if summary.LastErrorTimestamp.IsZero() || sc.renderTime.After(summary.LastErrorTimestamp) {
			summary.LastErrorTimestamp = sc.renderTime
		}
	}

	// Check for validation error
	if sc.validationStatus == statusFailed && len(sc.validationErrors) > 0 {
		summary.HAProxyValidationError = &debug.ErrorInfo{
			Timestamp: sc.validationTime,
			Errors:    sc.validationErrors,
		}
		if summary.LastErrorTimestamp.IsZero() || sc.validationTime.After(summary.LastErrorTimestamp) {
			summary.LastErrorTimestamp = sc.validationTime
		}
	}

	// Check for deployment errors
	if len(sc.failedEndpoints) > 0 {
		for _, failed := range sc.failedEndpoints {
			summary.DeploymentErrors = append(summary.DeploymentErrors, debug.ErrorInfo{
				Timestamp: sc.deploymentTime,
				Errors:    []string{failed.Error},
			})
		}
		if summary.LastErrorTimestamp.IsZero() || sc.deploymentTime.After(summary.LastErrorTimestamp) {
			summary.LastErrorTimestamp = sc.deploymentTime
		}
	}

	return summary, nil
}

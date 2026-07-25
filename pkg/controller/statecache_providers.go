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
	"errors"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// sizer is implemented by stores that can report their resource count cheaply,
// without the per-item API fetches that CachedStore.List() would trigger.
type sizer interface {
	Size() int
}

// cachedLister is implemented by on-demand (CachedStore) stores that can return
// only the resources currently warm in their LRU cache — no API fetches.
type cachedLister interface {
	ListCached() ([]any, error)
}

// GetConfig implements debug.StateProvider.
func (sc *StateCache) GetConfig() (*coreconfig.Config, string, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.currentConfig == nil {
		return nil, "", errors.New("config not loaded yet")
	}

	return sc.currentConfig, sc.currentConfigVersion, nil
}

// GetCredentials implements debug.StateProvider.
func (sc *StateCache) GetCredentials() (*coreconfig.Credentials, string, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.currentCreds == nil {
		return nil, "", errors.New("credentials not loaded yet")
	}

	return sc.currentCreds, sc.currentCredsVersion, nil
}

// GetRenderedConfig implements debug.StateProvider.
func (sc *StateCache) GetRenderedConfig() (string, time.Time, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if sc.lastRendered == "" {
		return "", time.Time{}, errors.New("no config rendered yet")
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

// currentAuxFilesProvider returns a callback that resolves the currently-deployed
// auxiliary files (base filename → content) the renderer exposes to templates as
// `currentFiles`, letting a template read its own prior output — the mechanism
// behind self-rotating TLS session-ticket keys. The same variable is available in
// every render (main config, map files, general files), so a self-referential
// snippet can be included from any template type. Keyed by base filename, the
// same name a template uses to register a map/file.
//
// Scope: the three CRD-backed aux kinds — map files, general files, and crt-list
// files. SSL certificates and CA files are excluded on purpose: their deployed
// content includes private keys and is published as Secrets, so surfacing it in
// every template's context would expand the private-key exposure surface without
// a real self-reference use case.
//
// It draws from two sources with a deliberate precedence:
//
//   - Once a render has recorded aux files this iteration, that in-process output
//     wins. It is always the latest render's result, so a self-rotating template
//     never re-rotates against a lagging published snapshot (no key churn).
//   - Before the first render (controller restart, config reload, or a follower
//     just promoted to leader) the StateCache is empty; the provider falls back
//     to the watched snapshot of published aux-file CRDs. This is the aux-file
//     analogue of currentConfig's read-back from HAProxyCfg — it lets a
//     self-referential template rotate from its prior keys instead of
//     bootstrapping fresh (which would reset all TLS ticket keys at once and
//     break session resumption). GetAuxiliaryFiles reports a zero timestamp until
//     a render records aux files, so that is the "cold" signal.
func currentAuxFilesProvider(sc *StateCache, published *publishedAuxFiles) func() map[string]string {
	return func() map[string]string {
		af, ts, _ := sc.GetAuxiliaryFiles()
		if ts.IsZero() {
			if published != nil {
				return published.get()
			}
			return nil
		}
		return af.CurrentFiles()
	}
}

// GetResourceCounts implements debug.StateProvider.
func (sc *StateCache) GetResourceCounts() (map[string]int, error) {
	if sc.resourceWatcher == nil {
		return nil, errors.New("resource watcher not initialized")
	}

	return resourceCounts(sc.resourceWatcher.GetAllStores())
}

// resourceCounts counts each store without forcing a full List(). On-demand
// (CachedStore) stores expose Size() (tracked-ref count, no API calls), so
// counting a debug endpoint no longer fans out one kube-apiserver GET per
// resource. The List() fallback only runs for stores that don't implement sizer.
func resourceCounts(stores map[string]types.Store) (map[string]int, error) {
	counts := make(map[string]int, len(stores))

	for name, store := range stores {
		if s, ok := store.(sizer); ok {
			counts[name] = s.Size()
			continue
		}

		items, err := store.List()
		if err != nil {
			return nil, fmt.Errorf("listing resources for %q: %w", name, err)
		}
		counts[name] = len(items)
	}

	return counts, nil
}

// GetResourcesByType implements debug.StateProvider.
//
// For `store: on-demand` types the result is the warm-cache subset only, not
// the full tracked set (see the partial-result contract on the interface
// declaration and listResources below). GetResourceCounts() remains the
// authoritative total.
func (sc *StateCache) GetResourcesByType(resourceType string) ([]any, error) {
	if sc.resourceWatcher == nil {
		return nil, errors.New("resource watcher not initialized")
	}

	stores := sc.resourceWatcher.GetAllStores()
	store, ok := stores[resourceType]
	if !ok {
		return nil, fmt.Errorf("resource type %q not found", resourceType)
	}

	return listResources(store)
}

// listResources returns the resources in a store for introspection. On-demand
// (cached) stores return only the warm LRU subset via ListCached(); calling
// List() on them would fan out one API call per reference — the storm we want
// to avoid on a debug path.
func listResources(store types.Store) ([]any, error) {
	if cl, ok := store.(cachedLister); ok {
		return cl.ListCached()
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
		return nil, errors.New("no config validated yet")
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

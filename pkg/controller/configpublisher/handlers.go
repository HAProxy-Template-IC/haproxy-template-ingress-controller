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

package configpublisher

import (
	"context"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

// lookupCachedConfig fetches the template config and rendered entry tied to the
// event's correlation ID. Returns ok=false (with a warning logged) when either the
// correlation ID is missing or the cached state isn't yet complete; callers should
// bail out in that case. eventName labels the event in the missing-correlation-id
// warning, action labels the intended operation in the missing-state warning.
func (c *Component) lookupCachedConfig(eventID, correlationID, eventName, action string) (*v1alpha1.HAProxyTemplateConfig, *renderedConfigEntry, bool) {
	if correlationID == "" {
		c.logger.Warn(eventName+" missing correlation ID, cannot match rendered config",
			"event_id", eventID)
		return nil, nil, false
	}

	c.mu.RLock()
	hasTemplateConfig := c.hasTemplateConfig
	templateConfig := c.templateConfig
	entry, hasRenderedConfig := c.renderedConfigs[correlationID]
	c.mu.RUnlock()

	if !hasTemplateConfig || !hasRenderedConfig {
		c.logger.Warn("cannot "+action+", missing cached state",
			"has_template_config", hasTemplateConfig,
			"has_rendered_config", hasRenderedConfig,
			"correlation_id", correlationID,
		)
		return nil, nil, false
	}

	return templateConfig, entry, true
}

// handleConfigValidated caches the template config for later publishing.
func (c *Component) handleConfigValidated(event *events.ConfigValidatedEvent) {
	// Extract HAProxyTemplateConfig from event.TemplateConfig (not event.Config)
	// event.Config contains *config.Config (parsed config for validation)
	// event.TemplateConfig contains *v1alpha1.HAProxyTemplateConfig (original CRD for metadata)

	if event.TemplateConfig == nil {
		c.logger.Warn("config validated event contains nil template config - this indicates a bug in event publishing",
			"version", event.Version)
		return
	}

	templateConfig, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		c.logger.Warn("config validated event contains unexpected template config type - expected *v1alpha1.HAProxyTemplateConfig",
			"actual_type", fmt.Sprintf("%T", event.TemplateConfig),
			"version", event.Version)
		return
	}

	c.logger.Debug("caching template config for publishing",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
		"version", event.Version,
	)

	// Cache the template config
	c.mu.Lock()
	c.templateConfig = templateConfig
	c.hasTemplateConfig = true
	c.mu.Unlock()
}

// handleTemplateRendered caches the rendered config for later publishing.
// The config is indexed by correlation ID to ensure we match it with the
// corresponding ValidationCompletedEvent.
func (c *Component) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	correlationID := event.CorrelationID()
	if correlationID == "" {
		c.logger.Warn("TemplateRenderedEvent missing correlation ID, using event ID as fallback",
			"event_id", event.EventID(),
			"config_bytes", event.ConfigBytes)
		correlationID = event.EventID()
	}

	c.logger.Debug("caching rendered config for publishing",
		"config_bytes", event.ConfigBytes,
		"auxiliary_file_count", event.AuxiliaryFileCount,
		"correlation_id", correlationID,
	)

	// Cache the rendered config indexed by correlation ID
	c.mu.Lock()
	c.renderedConfigs[correlationID] = &renderedConfigEntry{
		config:          event.HAProxyConfig,
		auxFiles:        event.AuxiliaryFiles,
		contentChecksum: event.ContentChecksum,
		renderedAt:      event.Timestamp(),
	}
	c.mu.Unlock()
}

// handleValidationCompleted queues the configuration for async publishing.
// Uses correlation ID to match with the corresponding TemplateRenderedEvent.
//
// This method is non-blocking - it queues work for the publishWorker instead of
// making K8S API calls directly. This prevents the event loop from blocking on
// slow API calls, allowing the component to keep up with event volume.
func (c *Component) handleValidationCompleted(event *events.ValidationCompletedEvent) {
	correlationID := event.CorrelationID()
	templateConfig, entry, ok := c.lookupCachedConfig(event.EventID(), correlationID, "ValidationCompletedEvent", "publish configuration")
	if !ok {
		return
	}

	c.logger.Debug("queuing configuration for async publishing",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
		"config_bytes", len(entry.config),
		"correlation_id", correlationID,
	)

	// Queue work for async processing. Use non-blocking send with coalescing:
	// - If channel is empty, work is queued immediately
	// - If channel has pending work, replace it with newer work (coalescing)
	// This ensures we always publish the latest config, not stale intermediate ones.
	workItem := &publishWorkItem{
		correlationID:  correlationID,
		event:          event,
		templateConfig: templateConfig,
		entry:          entry,
	}

	queueWithCoalesce(c, c.publishWork, workItem, "publish", correlationID,
		func(w *publishWorkItem) string { return w.correlationID })
}

// handleValidationFailed queues the invalid configuration for async publishing.
// Uses correlation ID to match with the corresponding TemplateRenderedEvent.
//
// This method is non-blocking - it queues work for the validationFailedWorker instead
// of making K8S API calls directly.
func (c *Component) handleValidationFailed(event *events.ValidationFailedEvent) {
	correlationID := event.CorrelationID()
	templateConfig, entry, ok := c.lookupCachedConfig(event.EventID(), correlationID, "ValidationFailedEvent", "publish invalid configuration")
	if !ok {
		return
	}

	c.logger.Debug("queuing invalid configuration for async publishing",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
		"error_count", len(event.Errors),
		"correlation_id", correlationID,
	)

	// Queue work for async processing
	workItem := &validationFailedWorkItem{
		correlationID:  correlationID,
		event:          event,
		templateConfig: templateConfig,
		entry:          entry,
	}

	queueWithCoalesce(c, c.validationFailedWork, workItem, "validation failed", correlationID,
		func(w *validationFailedWorkItem) string { return w.correlationID })
}

// queueWithCoalesce sends workItem on ch with non-blocking semantics and
// "latest wins" coalescing: if the channel is full, drain the pending item,
// drop its renderedConfigs entry (so we don't leak the cached render that
// will never be processed), then push the new item. If the worker grabs the
// drained slot before we can push, log and move on. logName is the work-item
// kind shown in debug logs ("publish" / "validation failed"), and
// correlationOf extracts the correlation ID from a drained item so the
// renderedConfigs cleanup can target the right entry.
func queueWithCoalesce[T any](
	c *Component,
	ch chan T,
	workItem T,
	logName, correlationID string,
	correlationOf func(T) string,
) {
	select {
	case ch <- workItem:
		return
	default:
	}

	// Channel full - drain old work and queue new work (coalescing).
	select {
	case oldWork := <-ch:
		oldID := correlationOf(oldWork)
		c.logger.Debug("coalescing "+logName+" work",
			"old_correlation_id", oldID,
			"new_correlation_id", correlationID,
		)
		// Cleanup the old entry since we're skipping it.
		c.mu.Lock()
		delete(c.renderedConfigs, oldID)
		c.mu.Unlock()
	default:
		// Channel was drained by worker between our checks - just try again.
	}

	select {
	case ch <- workItem:
	default:
		c.logger.Debug(logName+" work channel busy",
			"correlation_id", correlationID)
	}
}

// handleConfigAppliedToPod queues a pod status update for async processing with coalescing.
//
// This method is non-blocking - it stores work in a map for the statusWorker instead of
// queueing it directly. When multiple updates arrive for the same pod before the worker
// processes them, only the latest update is applied. This prevents channel overflow
// during high-frequency reconciliation cycles.
func (c *Component) handleConfigAppliedToPod(event *events.ConfigAppliedToPodEvent) {
	c.logger.Debug("queuing deployment status update for pod",
		"runtime_config_name", event.RuntimeConfigName,
		"runtime_config_namespace", event.RuntimeConfigNamespace,
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
		"checksum", event.Checksum,
		"is_drift_check", event.IsDriftCheck,
	)

	// Build a unique key for this pod's status update.
	// Format: namespace/runtimeConfigName/podName
	key := fmt.Sprintf("%s/%s/%s", event.RuntimeConfigNamespace, event.RuntimeConfigName, event.PodName)

	workItem := &statusWorkItem{
		event: event,
	}

	// Store (or replace) the pending update for this pod.
	// This provides natural coalescing - newer updates replace older ones.
	c.statusWorkPendingMu.Lock()
	c.statusWorkPending[key] = workItem
	c.statusWorkPendingMu.Unlock()

	// Signal the worker to wake up and process pending updates.
	// Non-blocking send - if signal is already pending, no need to add another.
	select {
	case c.statusWorkTrigger <- struct{}{}:
	default:
		// Worker already has a pending signal, no need to add another
	}
}

// handlePodTerminated cleans up pod references when a pod is terminated.
func (c *Component) handlePodTerminated(event *events.HAProxyPodTerminatedEvent) {
	// Get the namespace from cached templateConfig (namespace-scoped operations).
	c.mu.RLock()
	hasConfig := c.hasTemplateConfig
	namespace := ""
	if c.templateConfig != nil {
		namespace = c.templateConfig.Namespace
	}
	c.mu.RUnlock()

	if !hasConfig || namespace == "" {
		c.logger.Debug("skipping pod cleanup - no template config available yet",
			"pod_name", event.PodName,
		)
		return
	}

	c.logger.Info("cleaning up pod references after termination",
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
		"crd_namespace", namespace,
	)

	// Convert event to cleanup request with namespace
	cleanupReq := configpublisher.PodCleanupRequest{
		PodName:   event.PodName,
		Namespace: namespace,
	}

	// Call pure publisher (non-blocking - log errors but don't fail)
	ctx, cancel := context.WithTimeout(context.Background(), timeouts.KubernetesAPITimeout)
	defer cancel()

	if err := c.publisher.CleanupPodReferences(ctx, &cleanupReq); err != nil {
		c.logger.Warn("failed to cleanup pod references",
			"error", err,
			"pod_name", event.PodName,
			"pod_namespace", event.PodNamespace,
		)
		// Non-blocking - just log the error
		return
	}

	c.logger.Debug("pod references cleaned up successfully",
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
	)
}

// handlePodsDiscovered reconciles deployedToPods status against currently running pods.
//
// This cleans up stale entries from pods that terminated while the controller was
// restarting (or before the controller started). It is called whenever HAProxy pods
// are discovered, including on startup and when pods change.
func (c *Component) handlePodsDiscovered(event *events.HAProxyPodsDiscoveredEvent) {
	// Get the namespace from cached templateConfig (namespace-scoped operations).
	c.mu.RLock()
	hasConfig := c.hasTemplateConfig
	namespace := ""
	if c.templateConfig != nil {
		namespace = c.templateConfig.Namespace
	}
	c.mu.RUnlock()

	if !hasConfig || namespace == "" {
		c.logger.Debug("skipping pod reconciliation - no template config available yet",
			"pod_count", len(event.Endpoints),
		)
		return
	}

	// Extract pod names from discovered endpoints
	podNames := make([]string, 0, len(event.Endpoints))
	for _, ep := range event.Endpoints {
		podNames = append(podNames, ep.PodName)
	}

	// Create timeout context (same pattern as handlePodTerminated)
	ctx, cancel := context.WithTimeout(context.Background(), timeouts.KubernetesAPILongTimeout)
	defer cancel()

	// Reconcile status against running pods (namespace-scoped)
	if err := c.publisher.ReconcileDeployedToPods(ctx, namespace, podNames); err != nil {
		c.logger.Warn("failed to reconcile deployed pods status", "error", err)
	} else {
		c.logger.Debug("reconciled deployed pods status",
			"namespace", namespace,
			"running_pods", len(podNames))
	}
}

// convertAuxiliaryFiles converts dataplane auxiliary files to publisher auxiliary files.
func (c *Component) convertAuxiliaryFiles(dataplaneFiles *dataplane.AuxiliaryFiles) *configpublisher.AuxiliaryFiles {
	if dataplaneFiles == nil {
		return nil
	}

	return &configpublisher.AuxiliaryFiles{
		MapFiles:        dataplaneFiles.MapFiles,
		SSLCertificates: dataplaneFiles.SSLCertificates,
		SSLCaFiles:      dataplaneFiles.SSLCaFiles,
		GeneralFiles:    dataplaneFiles.GeneralFiles,
		CRTListFiles:    dataplaneFiles.CRTListFiles,
	}
}

// getCompressionThreshold returns the effective compression threshold,
// applying the default value when not set in the CRD.
func (c *Component) getCompressionThreshold(templateConfig *v1alpha1.HAProxyTemplateConfig) int64 {
	threshold := templateConfig.Spec.Controller.ConfigPublishing.CompressionThreshold
	if threshold == 0 {
		return config.DefaultCompressionThreshold
	}
	return threshold
}

// handleLostLeadership handles LostLeadershipEvent by clearing cached configuration state.
//
// When a replica loses leadership, leader-only components (including this publisher)
// are stopped via context cancellation. However, we defensively clear cached state
// to ensure clean state if leadership is reacquired.
//
// This prevents scenarios where:
//   - Stale templateConfig from previous leadership period is used
//   - Old renderedConfig is incorrectly published
//   - Cached auxiliary files reference non-existent resources
func (c *Component) handleLostLeadership(_ *events.LostLeadershipEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasTemplateConfig || len(c.renderedConfigs) > 0 {
		c.logger.Info("lost leadership, clearing cached configuration state",
			"had_template_config", c.hasTemplateConfig,
			"rendered_configs_count", len(c.renderedConfigs),
		)
	}

	// Clear all cached state
	c.templateConfig = nil
	c.hasTemplateConfig = false
	c.renderedConfigs = make(map[string]*renderedConfigEntry)
	c.lastPublishedChecksum = ""
}

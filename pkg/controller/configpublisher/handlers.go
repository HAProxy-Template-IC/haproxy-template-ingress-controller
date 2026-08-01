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
		c.logger.Warn("Cannot "+action+", missing cached state",
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
		c.logger.Warn("Config validated event contains nil template config - this indicates a bug in event publishing",
			"version", event.Version)
		return
	}

	templateConfig, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		c.logger.Warn("Config validated event contains unexpected template config type - expected *v1alpha1.HAProxyTemplateConfig",
			"actual_type", fmt.Sprintf("%T", event.TemplateConfig),
			"version", event.Version)
		return
	}

	c.logger.Debug("Caching template config for publishing",
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

	c.logger.Debug("Caching rendered config for publishing",
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

	c.logger.Debug("Queuing configuration for async publishing",
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

// handleDeployedConfigPublishRequest publishes, as the HAProxyCfg spec, the
// exact config the deployer just applied. This guarantees the deployed checksum
// — the same value stamped into status.deployedToPods[] — is observable as a
// published spec.Checksum even when the validation-driven publish for that
// render was throttled/coalesced away under churn. The bytes are carried on the
// event (inline entry), so no renderedConfigs cache lookup is needed, and it
// routes through a dedicated channel + pending slot so a validation publish
// cannot coalesce it away.
func (c *Component) handleDeployedConfigPublishRequest(event *events.DeployedConfigPublishRequest) {
	if event.ContentChecksum == "" {
		return
	}

	c.mu.RLock()
	templateConfig := c.templateConfig
	hasTemplateConfig := c.hasTemplateConfig
	c.mu.RUnlock()

	if !hasTemplateConfig || templateConfig == nil {
		c.logger.Debug("Skipping deployed-config publish, no template config cached yet",
			"checksum", event.ContentChecksum)
		return
	}

	workItem := &publishWorkItem{
		correlationID:  "deployed:" + event.ContentChecksum,
		templateConfig: templateConfig,
		entry: &renderedConfigEntry{
			config:          event.Config,
			auxFiles:        event.AuxiliaryFiles,
			contentChecksum: event.ContentChecksum,
		},
		deployDriven: true,
	}

	c.enqueueDeployed(workItem)
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

	c.logger.Debug("Queuing invalid configuration for async publishing",
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
		c.logger.Debug("Coalescing "+logName+" work",
			"old_correlation_id", oldID,
			"new_correlation_id", correlationID,
		)
		// Cleanup the old entry since we're skipping it.
		c.discardCachedConfig(oldID)
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

// statusWorkKey returns the coalescing key for a pod status update.
// Format: namespace/runtimeConfigName/podName.
func statusWorkKey(event *events.ConfigAppliedToPodEvent) string {
	return fmt.Sprintf("%s/%s/%s", event.RuntimeConfigNamespace, event.RuntimeConfigName, event.PodName)
}

// handleConfigAppliedToPod queues a pod status update for async processing with coalescing.
//
// This method is non-blocking - it stores work in a map for the statusWorker instead of
// queueing it directly. When multiple updates arrive for the same pod before the worker
// processes them, only the latest update is applied. This prevents channel overflow
// during high-frequency reconciliation cycles.
func (c *Component) handleConfigAppliedToPod(event *events.ConfigAppliedToPodEvent) {
	c.logger.Debug("Queuing deployment status update for pod",
		"runtime_config_name", event.RuntimeConfigName,
		"runtime_config_namespace", event.RuntimeConfigNamespace,
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
		"checksum", event.Checksum,
		"is_drift_check", event.IsDriftCheck,
	)

	key := statusWorkKey(event)

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
func (c *Component) handlePodTerminated(ctx context.Context, event *events.HAProxyPodTerminatedEvent) {
	// Get the namespace from cached templateConfig (namespace-scoped operations).
	c.mu.RLock()
	hasConfig := c.hasTemplateConfig
	namespace := ""
	if c.templateConfig != nil {
		namespace = c.templateConfig.Namespace
	}
	c.mu.RUnlock()

	if !hasConfig || namespace == "" {
		c.logger.Debug("Skipping pod cleanup - no template config available yet",
			"pod_name", event.PodName,
		)
		return
	}

	c.logger.Debug("Cleaning up pod references after termination",
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
	ctx, cancel := context.WithTimeout(ctx, timeouts.KubernetesAPITimeout)
	defer cancel()

	if err := c.publisher.CleanupPodReferences(ctx, &cleanupReq); err != nil {
		c.logger.Warn("Failed to cleanup pod references",
			"error", err,
			"pod_name", event.PodName,
			"pod_namespace", event.PodNamespace,
		)
		// Non-blocking - just log the error
		return
	}

	c.logger.Debug("Pod references cleaned up successfully",
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
	)
}

// handlePodsDiscovered reconciles deployedToPods status against currently running pods.
//
// This cleans up stale entries from pods that terminated while the controller was
// restarting (or before the controller started). It is called whenever HAProxy pods
// are discovered, including on startup and when pods change.
func (c *Component) handlePodsDiscovered(ctx context.Context, event *events.HAProxyPodsDiscoveredEvent) {
	// Get the namespace from cached templateConfig (namespace-scoped operations).
	c.mu.RLock()
	hasConfig := c.hasTemplateConfig
	namespace := ""
	if c.templateConfig != nil {
		namespace = c.templateConfig.Namespace
	}
	c.mu.RUnlock()

	if !hasConfig || namespace == "" {
		c.logger.Debug("Skipping pod reconciliation - no template config available yet",
			"pod_count", len(event.Endpoints),
		)
		return
	}

	// Extract pod names from discovered endpoints
	podNames := make([]string, 0, len(event.Endpoints))
	for _, ep := range event.Endpoints {
		podNames = append(podNames, ep.PodName)
	}

	// Create timeout context derived from the lifecycle ctx (same pattern as handlePodTerminated)
	ctx, cancel := context.WithTimeout(ctx, timeouts.KubernetesAPILongTimeout)
	defer cancel()

	// Reconcile status against running pods (namespace-scoped)
	if err := c.publisher.ReconcileDeployedToPods(ctx, namespace, podNames); err != nil {
		c.logger.Warn("Failed to reconcile deployed pods status", "error", err)
	} else {
		c.logger.Debug("Reconciled deployed pods status",
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
		c.logger.Info("Lost leadership, clearing cached configuration state",
			"had_template_config", c.hasTemplateConfig,
			"rendered_configs_count", len(c.renderedConfigs),
		)
	}

	// Clear all cached state
	c.templateConfig = nil
	c.hasTemplateConfig = false
	c.renderedConfigs = make(map[string]*renderedConfigEntry)
	c.lastPublishedChecksum = ""

	// Drop every queued deploy-driven publish so a lost leader doesn't later
	// flush a stale spec write. The Component outlives a leadership transition,
	// so anything queued under the previous term would otherwise be published
	// after the term ends — writing a spec the new leader never deployed.
	// (mu is held here; the queue and pending mutexes are always acquired after
	// mu, never before, so this nesting can't deadlock.)
	c.deployedPendingMu.Lock()
	c.deployedPending = nil
	c.deployedPendingMu.Unlock()

	c.pendingMu.Lock()
	c.pendingPublish = nil
	c.pendingMu.Unlock()
}

// enqueueDeployed appends a deployed render to the pending queue and wakes the
// publish worker.
//
// Deduplicated by content checksum rather than coalesced: a checksum already
// queued is replaced in place (keeping its position), but a DIFFERENT checksum
// never displaces one. Dropping a deployed checksum leaves
// status.deployedToPods advertising a config spec.content never carried.
func (c *Component) enqueueDeployed(work *publishWorkItem) {
	c.deployedPendingMu.Lock()
	replaced := false
	for i, pending := range c.deployedPending {
		if pending.entry.contentChecksum == work.entry.contentChecksum {
			c.deployedPending[i] = work
			replaced = true
			break
		}
	}
	if !replaced {
		c.deployedPending = append(c.deployedPending, work)
	}
	depth := len(c.deployedPending)
	c.deployedPendingMu.Unlock()

	c.logger.Debug("Queued deployed config for publishing",
		"checksum", work.entry.contentChecksum,
		"correlation_id", work.correlationID,
		"replaced_same_checksum", replaced,
		"queue_depth", depth)

	select {
	case c.deployedTrigger <- struct{}{}:
	default: // already signalled; the worker drains the whole queue
	}
}

// requeueDeployedFront puts a deployed render back at the head of the queue,
// for the throttle path: the gate closed before it could be published, and it
// must keep its place ahead of anything queued behind it.
func (c *Component) requeueDeployedFront(work *publishWorkItem) {
	c.deployedPendingMu.Lock()
	defer c.deployedPendingMu.Unlock()
	for i, pending := range c.deployedPending {
		if pending.entry.contentChecksum == work.entry.contentChecksum {
			c.deployedPending[i] = work
			return
		}
	}
	c.deployedPending = append([]*publishWorkItem{work}, c.deployedPending...)
}

// takeDeployed pops the oldest pending deployed render, or nil when empty.
func (c *Component) takeDeployed() *publishWorkItem {
	c.deployedPendingMu.Lock()
	defer c.deployedPendingMu.Unlock()
	if len(c.deployedPending) == 0 {
		return nil
	}
	work := c.deployedPending[0]
	c.deployedPending = c.deployedPending[1:]
	return work
}

// deployedQueueDepth reports how many deployed renders are still queued.
func (c *Component) deployedQueueDepth() int {
	c.deployedPendingMu.Lock()
	defer c.deployedPendingMu.Unlock()
	return len(c.deployedPending)
}

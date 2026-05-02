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
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

const haproxyConfigPath = "/etc/haproxy/haproxy.cfg"

// buildPublishRequest assembles the PublishRequest fields shared by both the
// happy-path publish and the validation-failed publish. Callers layer the
// extra fields (NameSuffix, ValidationError) on top.
func (c *Component) buildPublishRequest(templateConfig *v1alpha1.HAProxyTemplateConfig, entry *renderedConfigEntry) *configpublisher.PublishRequest {
	return &configpublisher.PublishRequest{
		TemplateConfigName:      templateConfig.Name,
		TemplateConfigNamespace: templateConfig.Namespace,
		TemplateConfigUID:       templateConfig.UID,
		Config:                  entry.config,
		ConfigPath:              haproxyConfigPath,
		AuxiliaryFiles:          c.convertAuxiliaryFiles(entry.auxFiles),
		Checksum:                entry.contentChecksum,
		CompressionThreshold:    c.getCompressionThreshold(templateConfig),
	}
}

// discardCachedConfig drops the rendered config entry for the given correlation
// ID. Used by both worker paths once the entry is no longer needed (after
// successful publish or to clean up after a publish failure).
func (c *Component) discardCachedConfig(correlationID string) {
	c.mu.Lock()
	delete(c.renderedConfigs, correlationID)
	c.mu.Unlock()
}

// publishWorker processes publish work items asynchronously.
// This worker runs in a separate goroutine to prevent blocking the event loop
// on slow K8S API calls.
//
// Throttling is delegated to publishThrottle (a *throttle.LeadingEdge): the
// first publish after idle fires immediately; submissions inside the
// refractory period are stashed in pendingPublish, and the worker flushes
// the latest buffered item when publishThrottle.FiredCh() signals.
func (c *Component) publishWorker(ctx context.Context) {
	for {
		select {
		case work := <-c.publishWork:
			c.processPublishWork(work)
		case <-c.publishThrottle.FiredCh():
			c.flushPendingPublish()
		case <-ctx.Done():
			// Flush any buffered publish before shutdown
			c.flushPendingPublish()
			return
		}
	}
}

// processPublishWork decides whether to publish immediately or buffer for throttle.
func (c *Component) processPublishWork(work *publishWorkItem) {
	c.logger.Debug("processing publish work",
		"config_name", work.templateConfig.Name,
		"config_namespace", work.templateConfig.Namespace,
		"config_bytes", len(work.entry.config),
		"correlation_id", work.correlationID,
	)

	// Skip publish if checksum unchanged (content deduplication).
	// This prevents redundant CRD updates when config content hasn't changed,
	// which commonly happens during high-frequency EndpointSlice reconciliations.
	if c.skipIfAlreadyPublished(work, "skipping publish, config unchanged") {
		return
	}

	// Throttle: leading-edge refractory via publishThrottle. If the gate is
	// closed, buffer the latest work and ask the throttle to wake us when
	// the refractory expires.
	if !c.publishThrottle.Available() {
		c.pendingMu.Lock()
		if c.pendingPublish != nil {
			c.discardCachedConfig(c.pendingPublish.correlationID)
		}
		c.pendingPublish = work
		c.pendingMu.Unlock()

		c.logger.Debug("throttling CRD publish, buffering for later",
			"checksum", work.entry.contentChecksum,
			"correlation_id", work.correlationID,
		)
		c.publishThrottle.ScheduleFlush()
		return
	}

	// Gate open — publish immediately
	c.executePublish(work)
}

// flushPendingPublish publishes the buffered work item (if any) when the throttle timer expires.
func (c *Component) flushPendingPublish() {
	c.pendingMu.Lock()
	work := c.pendingPublish
	c.pendingPublish = nil
	c.pendingMu.Unlock()

	if work == nil {
		return
	}

	// Re-check content deduplication (content may have been published by another path)
	if c.skipIfAlreadyPublished(work, "skipping throttled publish, config already published") {
		return
	}

	c.logger.Debug("flushing throttled CRD publish",
		"correlation_id", work.correlationID,
	)
	c.executePublish(work)
}

// skipIfAlreadyPublished returns true when work's content checksum matches the
// last successfully published checksum. In that case it logs msg at debug and
// drops the cached rendered-config entry so the caller can simply
// early-return. Empty checksums never match (we cannot deduplicate without
// one). The same content-deduplication check is needed both before throttle
// buffering and after the throttle timer fires; this helper is the single
// source of truth for that decision.
func (c *Component) skipIfAlreadyPublished(work *publishWorkItem, msg string) bool {
	checksum := work.entry.contentChecksum
	if checksum == "" {
		return false
	}
	c.mu.RLock()
	lastChecksum := c.lastPublishedChecksum
	c.mu.RUnlock()
	if checksum != lastChecksum {
		return false
	}
	c.logger.Debug(msg,
		"checksum", checksum,
		"correlation_id", work.correlationID,
	)
	c.discardCachedConfig(work.correlationID)
	return true
}

// executePublish performs the actual K8S API call to publish the config CRD.
func (c *Component) executePublish(work *publishWorkItem) {
	request := c.buildPublishRequest(work.templateConfig, work.entry)

	// Call pure publisher with timeout context
	publishCtx, cancel := context.WithTimeout(context.Background(), timeouts.KubernetesAPILongTimeout)
	defer cancel()

	result, err := c.publisher.PublishConfig(publishCtx, request)
	if err != nil {
		c.logger.Error("failed to publish runtime configuration",
			"error", err,
			"config_name", work.templateConfig.Name,
			"correlation_id", work.correlationID,
		)
		c.discardCachedConfig(work.correlationID)
		return
	}

	checksumHex := request.Checksum
	c.logger.Debug("runtime configuration published successfully",
		"runtime_config_name", result.RuntimeConfigName,
		"runtime_config_namespace", result.RuntimeConfigNamespace,
		"checksum", checksumHex,
		"correlation_id", work.correlationID,
	)

	// Update last published checksum, mark throttle fired, clean up.
	c.mu.Lock()
	c.lastPublishedChecksum = checksumHex
	delete(c.renderedConfigs, work.correlationID)
	c.mu.Unlock()

	c.publishThrottle.MarkFired()

	// Publish success event with runtime config info
	c.eventBus.Publish(events.NewConfigPublishedEvent(
		result.RuntimeConfigName,
		result.RuntimeConfigNamespace,
		len(result.MapFileNames),
		len(result.SecretNames),
	))
}

// validationFailedWorker processes validation failed work items asynchronously.
func (c *Component) validationFailedWorker(ctx context.Context) {
	for {
		select {
		case work := <-c.validationFailedWork:
			c.processValidationFailedWork(work)
		case <-ctx.Done():
			return
		}
	}
}

// processValidationFailedWork performs the actual invalid config publishing.
func (c *Component) processValidationFailedWork(work *validationFailedWorkItem) {
	c.logger.Debug("processing validation failed work",
		"config_name", work.templateConfig.Name,
		"config_namespace", work.templateConfig.Namespace,
		"error_count", len(work.event.Errors),
		"correlation_id", work.correlationID,
	)

	// Build validation error summary
	var validationError string
	if len(work.event.Errors) > 0 {
		validationError = work.event.Errors[0]
		if len(work.event.Errors) > 1 {
			validationError = fmt.Sprintf("%s (+%d more errors)", validationError, len(work.event.Errors)-1)
		}
	}

	// Layer the invalid-state extras on top of the shared request.
	request := c.buildPublishRequest(work.templateConfig, work.entry)
	request.NameSuffix = "-invalid"
	request.ValidationError = validationError

	// Call pure publisher with timeout context
	publishCtx, cancel := context.WithTimeout(context.Background(), timeouts.KubernetesAPILongTimeout)
	defer cancel()

	result, err := c.publisher.PublishConfig(publishCtx, request)
	if err != nil {
		c.logger.Error("failed to publish invalid runtime configuration",
			"error", err,
			"config_name", work.templateConfig.Name,
			"correlation_id", work.correlationID,
		)
		c.discardCachedConfig(work.correlationID)
		return
	}

	c.logger.Warn("invalid runtime configuration published",
		"runtime_config_name", result.RuntimeConfigName,
		"runtime_config_namespace", result.RuntimeConfigNamespace,
		"validation_error", validationError,
		"correlation_id", work.correlationID,
	)

	c.discardCachedConfig(work.correlationID)
}

// statusWorker processes pod status update work items asynchronously with coalescing.
//
// Instead of processing each update as it arrives, this worker waits for a trigger
// signal and then processes all pending updates at once. This ensures that when
// multiple updates arrive for the same pod, only the latest one is applied.
//
// Throttling is delegated to statusThrottle (a *throttle.LeadingEdge): the
// first flush after idle fires immediately; while inside the refractory
// period, pending updates keep accumulating in statusWorkPending and the
// worker flushes them when statusThrottle.FiredCh() signals.
func (c *Component) statusWorker(ctx context.Context) {
	for {
		select {
		case <-c.statusWorkTrigger:
			c.handleStatusTrigger()
		case <-c.statusThrottle.FiredCh():
			c.processAllPendingStatusWork()
		case <-ctx.Done():
			// Flush any pending status updates before shutdown
			c.processAllPendingStatusWork()
			return
		}
	}
}

// handleStatusTrigger decides whether to process status updates immediately or defer.
func (c *Component) handleStatusTrigger() {
	if c.statusThrottle.Available() {
		c.processAllPendingStatusWork()
		return
	}
	// Inside refractory — wake up at the end. Pending updates already
	// accumulate (and coalesce per pod) in statusWorkPending.
	c.statusThrottle.ScheduleFlush()
}

// processAllPendingStatusWork drains the pending status work map and processes all items.
// This is called when the worker receives a trigger signal.
func (c *Component) processAllPendingStatusWork() {
	// Take a snapshot of pending work and clear the map atomically.
	// This allows new updates to accumulate while we process these.
	c.statusWorkPendingMu.Lock()
	if len(c.statusWorkPending) == 0 {
		c.statusWorkPendingMu.Unlock()
		return
	}

	// Take ownership of the pending map and create a new empty one
	pendingWork := c.statusWorkPending
	c.statusWorkPending = make(map[string]*statusWorkItem)
	c.statusWorkPendingMu.Unlock()

	// Process all pending work items
	c.logger.Debug("processing coalesced status updates",
		"pending_count", len(pendingWork),
	)

	for _, work := range pendingWork {
		c.processStatusWork(work)
	}

	// Record write time for throttle refractory.
	c.statusThrottle.MarkFired()
}

// processStatusWork performs the actual pod status update.
func (c *Component) processStatusWork(work *statusWorkItem) {
	event := work.event

	c.logger.Debug("processing status update for pod",
		"runtime_config_name", event.RuntimeConfigName,
		"runtime_config_namespace", event.RuntimeConfigNamespace,
		"pod_name", event.PodName,
		"checksum", event.Checksum,
	)

	// Convert event to status update
	update := configpublisher.DeploymentStatusUpdate{
		RuntimeConfigName:      event.RuntimeConfigName,
		RuntimeConfigNamespace: event.RuntimeConfigNamespace,
		PodName:                event.PodName,
		Checksum:               event.Checksum,
	}

	// Extract error information from sync metadata if available
	if event.SyncMetadata != nil {
		update.Error = event.SyncMetadata.Error
	}

	// Call pure publisher with timeout context
	updateCtx, cancel := context.WithTimeout(context.Background(), timeouts.KubernetesAPITimeout)
	defer cancel()

	if err := c.publisher.UpdateDeploymentStatus(updateCtx, &update); err != nil {
		c.logger.Warn("failed to update deployment status",
			"error", err,
			"runtime_config_name", event.RuntimeConfigName,
			"pod_name", event.PodName,
		)
		return
	}

	c.logger.Debug("deployment status updated successfully",
		"runtime_config_name", event.RuntimeConfigName,
		"pod_name", event.PodName,
	)
}

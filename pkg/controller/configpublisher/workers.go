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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

// buildPublishRequest assembles the PublishRequest fields shared by both the
// happy-path publish and the validation-failed publish. Callers layer the
// extra fields (NameSuffix, ValidationError) on top.
func (c *Component) buildPublishRequest(templateConfig *v1alpha1.HAProxyTemplateConfig, entry *renderedConfigEntry) *configpublisher.PublishRequest {
	return &configpublisher.PublishRequest{
		TemplateConfigName:      templateConfig.Name,
		TemplateConfigNamespace: templateConfig.Namespace,
		TemplateConfigUID:       templateConfig.UID,
		Config:                  entry.config,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
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
// When a publish interval is configured, the worker uses leading-edge throttling:
// the first publish after idle fires immediately, subsequent publishes within
// the refractory period are buffered and flushed when the timer expires.
func (c *Component) publishWorker(ctx context.Context) {
	for {
		select {
		case work := <-c.publishWork:
			c.processPublishWork(work)
		case <-c.throttleTimerCh:
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

	// Use pre-computed checksum from pipeline (propagated via TemplateRenderedEvent)
	checksumHex := work.entry.contentChecksum

	// Skip publish if checksum unchanged (content deduplication).
	// This prevents redundant CRD updates when config content hasn't changed,
	// which commonly happens during high-frequency EndpointSlice reconciliations.
	c.mu.RLock()
	lastChecksum := c.lastPublishedChecksum
	c.mu.RUnlock()

	if checksumHex != "" && checksumHex == lastChecksum {
		c.logger.Debug("skipping publish, config unchanged",
			"checksum", checksumHex,
			"correlation_id", work.correlationID,
		)
		// Clean up cached entry
		c.mu.Lock()
		delete(c.renderedConfigs, work.correlationID)
		c.mu.Unlock()
		return
	}

	// Throttle: if a publish interval is configured, use leading-edge refractory.
	if c.publishInterval > 0 {
		c.throttleMu.Lock()
		timeSinceLast := time.Since(c.lastPublishTime)
		if timeSinceLast < c.publishInterval {
			// Inside refractory — buffer latest work, clean up any previously buffered entry
			if c.pendingPublish != nil {
				c.mu.Lock()
				delete(c.renderedConfigs, c.pendingPublish.correlationID)
				c.mu.Unlock()
			}
			c.pendingPublish = work
			remaining := c.publishInterval - timeSinceLast
			c.throttleMu.Unlock()

			c.logger.Debug("throttling CRD publish, buffering for later",
				"remaining", remaining,
				"checksum", checksumHex,
				"correlation_id", work.correlationID,
			)
			c.ensureThrottleTimer(remaining)
			return
		}
		c.throttleMu.Unlock()
	}

	// Outside refractory (or no throttle configured) — publish immediately
	c.executePublish(work)
}

// flushPendingPublish publishes the buffered work item (if any) when the throttle timer expires.
func (c *Component) flushPendingPublish() {
	c.throttleMu.Lock()
	work := c.pendingPublish
	c.pendingPublish = nil
	c.throttleMu.Unlock()

	if work == nil {
		return
	}

	// Re-check content deduplication (content may have been published by another path)
	c.mu.RLock()
	lastChecksum := c.lastPublishedChecksum
	c.mu.RUnlock()

	if work.entry.contentChecksum != "" && work.entry.contentChecksum == lastChecksum {
		c.logger.Debug("skipping throttled publish, config already published",
			"checksum", work.entry.contentChecksum,
			"correlation_id", work.correlationID,
		)
		c.mu.Lock()
		delete(c.renderedConfigs, work.correlationID)
		c.mu.Unlock()
		return
	}

	c.logger.Debug("flushing throttled CRD publish",
		"correlation_id", work.correlationID,
	)
	c.executePublish(work)
}

// ensureThrottleTimer starts a one-shot timer that signals the publishWorker via throttleTimerCh.
// The timer fires after the remaining refractory period. Only one timer runs at a time;
// if a timer is already pending, this is a no-op (the existing timer will flush the
// latest buffered work).
func (c *Component) ensureThrottleTimer(remaining time.Duration) {
	// Non-blocking send — if a signal is already pending the worker will pick it up
	time.AfterFunc(remaining, func() {
		select {
		case c.throttleTimerCh <- struct{}{}:
		default:
		}
	})
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

	// Update last published checksum, last publish time, and clean up
	c.mu.Lock()
	c.lastPublishedChecksum = checksumHex
	delete(c.renderedConfigs, work.correlationID)
	c.mu.Unlock()

	c.throttleMu.Lock()
	c.lastPublishTime = time.Now()
	c.throttleMu.Unlock()

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
// When a publish interval is configured, the worker uses leading-edge throttling
// (same pattern as the publish worker) to limit how often status is written to
// the CRD. Each UpdateStatus writes the full ~509 KB object to etcd, so throttling
// significantly reduces etcd write pressure.
func (c *Component) statusWorker(ctx context.Context) {
	for {
		select {
		case <-c.statusWorkTrigger:
			c.handleStatusTrigger()
		case <-c.statusThrottleTimerCh:
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
	if c.publishInterval <= 0 {
		c.processAllPendingStatusWork()
		return
	}

	c.throttleMu.Lock()
	timeSinceLast := time.Since(c.lastStatusWriteTime)
	if timeSinceLast >= c.publishInterval {
		c.throttleMu.Unlock()
		// Outside refractory — process immediately (leading-edge)
		c.processAllPendingStatusWork()
		return
	}
	remaining := c.publishInterval - timeSinceLast
	c.throttleMu.Unlock()

	// Inside refractory — ensure a timer is running to flush later.
	// Pending updates accumulate in statusWorkPending (already coalesced per pod).
	c.ensureStatusThrottleTimer(remaining)
}

// ensureStatusThrottleTimer starts a one-shot timer that signals the statusWorker.
func (c *Component) ensureStatusThrottleTimer(remaining time.Duration) {
	time.AfterFunc(remaining, func() {
		select {
		case c.statusThrottleTimerCh <- struct{}{}:
		default:
		}
	})
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

	// Record write time for throttle refractory
	if c.publishInterval > 0 {
		c.throttleMu.Lock()
		c.lastStatusWriteTime = time.Now()
		c.throttleMu.Unlock()
	}
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

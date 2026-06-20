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
	"sync"

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
			c.processPublishWork(ctx, work)
		case work := <-c.deployedPublishWork:
			c.processPublishWork(ctx, work)
		case <-c.publishThrottle.FiredCh():
			c.flushPendingPublish(ctx)
		case <-ctx.Done():
			// Flush any buffered publish before shutdown. The lifecycle ctx is
			// already cancelled, so detach from its cancellation (WithoutCancel)
			// so the final write isn't instantly aborted.
			c.flushPendingPublish(context.WithoutCancel(ctx))
			return
		}
	}
}

// processPublishWork decides whether to publish immediately or buffer for throttle.
func (c *Component) processPublishWork(ctx context.Context, work *publishWorkItem) {
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
		var superseded *publishWorkItem
		c.pendingMu.Lock()
		if work.deployDriven {
			// Deploy-driven items use their own slot (newest-deployed wins) so a
			// validation publish can't coalesce away a deployed checksum. The
			// entry is inline (carried on the event), so there's no cached render
			// to discard when overwriting.
			c.pendingDeployedPublish = work
		} else {
			superseded = c.pendingPublish
			c.pendingPublish = work
		}
		c.pendingMu.Unlock()

		// Drop the superseded item's cached render AFTER releasing pendingMu:
		// discardCachedConfig takes mu, and handleLostLeadership takes mu THEN
		// pendingMu — nesting pendingMu->mu here would invert that order and
		// deadlock. Keeping the two mutexes non-nested at this site makes the
		// mu->pendingMu in handleLostLeadership the only nesting, so no AB-BA
		// lock inversion is possible.
		if superseded != nil {
			c.discardCachedConfig(superseded.correlationID)
		}

		c.logger.Debug("throttling CRD publish, buffering for later",
			"checksum", work.entry.contentChecksum,
			"correlation_id", work.correlationID,
			"deploy_driven", work.deployDriven,
		)
		c.publishThrottle.ScheduleFlush()
		return
	}

	// Gate open — publish immediately
	c.executePublish(ctx, work)
}

// flushPendingPublish publishes the buffered work item (if any) when the throttle timer expires.
func (c *Component) flushPendingPublish(ctx context.Context) {
	c.pendingMu.Lock()
	deployWork := c.pendingDeployedPublish
	c.pendingDeployedPublish = nil
	work := c.pendingPublish
	c.pendingPublish = nil
	c.pendingMu.Unlock()

	// Deploy-driven first: spec should reflect what's actually deployed, and the
	// deployed checksum must become observable. When both slots carry the same
	// content (the common case — deploy of the render that validation just
	// published), skipIfAlreadyPublished collapses the second to a no-op, so this
	// stays at one CRD write per window in steady state.
	for _, w := range []*publishWorkItem{deployWork, work} {
		if w == nil {
			continue
		}
		// Re-check content deduplication (content may have been published by another path).
		if c.skipIfAlreadyPublished(w, "skipping throttled publish, config already published") {
			continue
		}
		c.logger.Debug("flushing throttled CRD publish",
			"correlation_id", w.correlationID,
			"deploy_driven", w.deployDriven,
		)
		c.executePublish(ctx, w)
	}
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
func (c *Component) executePublish(ctx context.Context, work *publishWorkItem) {
	request := c.buildPublishRequest(work.templateConfig, work.entry)

	// Call pure publisher with timeout context
	publishCtx, cancel := context.WithTimeout(ctx, timeouts.KubernetesAPILongTimeout)
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
			c.processValidationFailedWork(ctx, work)
		case <-ctx.Done():
			return
		}
	}
}

// processValidationFailedWork performs the actual invalid config publishing.
func (c *Component) processValidationFailedWork(ctx context.Context, work *validationFailedWorkItem) {
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
	publishCtx, cancel := context.WithTimeout(ctx, timeouts.KubernetesAPILongTimeout)
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
			c.handleStatusTrigger(ctx)
		case <-c.statusThrottle.FiredCh():
			c.processAllPendingStatusWork(ctx)
		case <-ctx.Done():
			// Flush any pending status updates before shutdown. The lifecycle
			// ctx is already cancelled, so detach from its cancellation
			// (WithoutCancel) so the final writes aren't instantly aborted.
			c.processAllPendingStatusWork(context.WithoutCancel(ctx))
			return
		}
	}
}

// handleStatusTrigger decides whether to process status updates immediately or defer.
func (c *Component) handleStatusTrigger(ctx context.Context) {
	if c.statusThrottle.Available() {
		c.processAllPendingStatusWork(ctx)
		return
	}
	// Inside refractory — wake up at the end. Pending updates already
	// accumulate (and coalesce per pod) in statusWorkPending.
	c.statusThrottle.ScheduleFlush()
}

// processAllPendingStatusWork drains the pending status work map and applies
// each pod's latest entry in parallel.
//
// Two design choices, both load-bearing:
//
//  1. Per-pod fan-out. Each pod's status entry lives under its own server-
//     side-apply field manager (`haptic-pod-status-<podName>`), so concurrent
//     Applies to different pods can't conflict at the apiserver. Spawning
//     one goroutine per pod cuts batch latency from `N × per-apply-time` to
//     ~`1 × per-apply-time`. Under heavy parallel-test churn this was the
//     bottleneck that let spec advance faster than status could catch up;
//     symptom on CI pipeline 2559961278 was pods stuck at intermediate
//     checksums for the full 90 s `waitForControllerDeployed` budget.
//
//  2. Latest-at-apply-time per pod. The old implementation snapshotted the
//     entire pending map at batch start, then iterated. If a newer event
//     for a pod arrived during a (serial) apply, the snapshot value was
//     already obsolete — we'd push the stale value to the apiserver and
//     pick up the newer one only on the NEXT throttle window. Instead,
//     each goroutine atomically pops the LATEST entry for its pod at the
//     moment the apply starts. The overwrite-by-pod-key semantics in
//     handleConfigAppliedToPod guarantee that whatever the map holds is
//     the most recent event seen for that pod up to the pop.
func (c *Component) processAllPendingStatusWork(ctx context.Context) {
	c.statusWorkPendingMu.Lock()
	if len(c.statusWorkPending) == 0 {
		c.statusWorkPendingMu.Unlock()
		return
	}
	podKeys := make([]string, 0, len(c.statusWorkPending))
	for podKey := range c.statusWorkPending {
		podKeys = append(podKeys, podKey)
	}
	c.statusWorkPendingMu.Unlock()

	c.logger.Debug("processing coalesced status updates",
		"pending_pod_count", len(podKeys),
	)

	var wg sync.WaitGroup
	for _, podKey := range podKeys {
		wg.Add(1)
		go func(podKey string) {
			defer wg.Done()

			c.statusWorkPendingMu.Lock()
			work, ok := c.statusWorkPending[podKey]
			if ok {
				delete(c.statusWorkPending, podKey)
			}
			c.statusWorkPendingMu.Unlock()
			if !ok {
				// Another goroutine raced and popped first — only possible
				// if a future change introduces a second drain path. Today
				// this fan-out is the sole reader, so the branch is
				// defensive rather than expected.
				return
			}
			c.processStatusWork(ctx, work)
		}(podKey)
	}
	wg.Wait()

	// Record write time for throttle refractory.
	c.statusThrottle.MarkFired()
}

// processStatusWork performs the actual pod status update.
func (c *Component) processStatusWork(ctx context.Context, work *statusWorkItem) {
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
	updateCtx, cancel := context.WithTimeout(ctx, timeouts.KubernetesAPITimeout)
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

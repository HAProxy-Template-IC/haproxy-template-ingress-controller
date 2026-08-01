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
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

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
		// Deploy-driven work first, and drain ALL of it before touching
		// validation work. A select picks uniformly at random among ready
		// cases, so with validation publishes arriving continuously a deployed
		// item could sit unread for seconds — measured 5.2s, with three
		// validation publishes going out ahead of it. For exactly that long the
		// CR contradicts itself: status.deployedToPods already advertises a
		// checksum whose content spec has not published, so no reader can
		// resolve it. flushPendingPublish already orders deploy work first (see
		// below); this makes the queue-level order match.
		// ...but ONLY while the throttle gate is open. Under a closed gate
		// processPublishWork puts the item straight back at the front of the
		// queue, so draining here would pop it again on the very next
		// iteration: a tight loop with nothing to block on, pinning a core for
		// the whole refractory window. That is precisely the burst this queue
		// exists for (the first item closes the gate, the rest arrive during
		// refractory), so it would fire in the common case, not a corner.
		//
		// Falling through to the select instead lets FiredCh drive
		// flushPendingPublish, which drains deploy work first and re-arms
		// itself while the queue is non-empty — same order, no spin. The
		// ScheduleFlush below is what makes that safe: enqueueDeployed only
		// signals deployedTrigger, so without it a queued item under a closed
		// gate would have nothing left to wake it and would stall until an
		// unrelated event arrived.
		if c.publishThrottle.Available() {
			if work := c.takeDeployed(); work != nil {
				c.processPublishWork(ctx, work)
				continue
			}
		} else if c.deployedQueueDepth() > 0 {
			c.publishThrottle.ScheduleFlush()
		}

		select {
		case work := <-c.publishWork:
			c.processPublishWork(ctx, work)
		case <-c.deployedTrigger:
			// Wake only; the drain at the top of the loop takes the item.
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
	c.logger.Debug("Processing publish work",
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
		// Deploy-driven work goes BACK on the deployed queue, at the front, so
		// it keeps its place. A one-slot buffer here would undo the queue's
		// whole guarantee: draining a burst closes the gate on the first
		// publish, and every remaining deployed checksum would then overwrite
		// that slot, leaving only the newest — exactly the drop this queue
		// exists to prevent. Validation work keeps its latest-wins slot, which
		// is correct for it: only the newest render matters there.
		var superseded *publishWorkItem
		if work.deployDriven {
			c.requeueDeployedFront(work)
		} else {
			c.pendingMu.Lock()
			superseded = c.pendingPublish
			c.pendingPublish = work
			c.pendingMu.Unlock()
		}

		// Drop the superseded item's cached render AFTER releasing pendingMu:
		// discardCachedConfig takes mu, and handleLostLeadership takes mu THEN
		// pendingMu — nesting pendingMu->mu here would invert that order and
		// deadlock. Keeping the two mutexes non-nested at this site makes the
		// mu->pendingMu in handleLostLeadership the only nesting, so no AB-BA
		// lock inversion is possible.
		if superseded != nil {
			c.discardCachedConfig(superseded.correlationID)
		}

		c.logger.Debug("Throttling CRD publish, buffering for later",
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
	deployWork := c.takeDeployed()

	c.pendingMu.Lock()
	work := c.pendingPublish
	c.pendingPublish = nil
	c.pendingMu.Unlock()

	// One deployed item per throttle window: the rest stay queued and the
	// worker's own drain picks them up as the gate reopens, so the interval is
	// still honoured and nothing is discarded.
	if deployWork != nil && c.deployedQueueDepth() > 0 {
		c.publishThrottle.ScheduleFlush()
	}

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
		c.logger.Debug("Flushing throttled CRD publish",
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
		c.logger.Error("Failed to publish runtime configuration",
			"error", err,
			"config_name", work.templateConfig.Name,
			"correlation_id", work.correlationID,
		)
		c.discardCachedConfig(work.correlationID)
		return
	}

	checksumHex := request.Checksum
	c.logger.Debug("Runtime configuration published successfully",
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
	c.logger.Debug("Processing validation failed work",
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
		c.logger.Error("Failed to publish invalid runtime configuration",
			"error", err,
			"config_name", work.templateConfig.Name,
			"correlation_id", work.correlationID,
		)
		c.discardCachedConfig(work.correlationID)
		return
	}

	c.logger.Warn("Invalid runtime configuration published",
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
	podKeys := slices.Collect(maps.Keys(c.statusWorkPending))
	c.statusWorkPendingMu.Unlock()

	c.logger.Debug("Processing coalesced status updates",
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

	c.logger.Debug("Processing status update for pod",
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
		if errors.Is(err, configpublisher.ErrRuntimeConfigNotPublished) {
			// Startup race: the first deployment to the pods completes a few
			// hundred milliseconds before the initial HAProxyCfg publish
			// lands, so this pod's status SSA found no resource. Dropping the
			// update here loses the pod's deployedToPods entry until the next
			// config change or drift check (deduped no-op deploys never
			// re-queue it) — observed as an e2e initial-sync timeout with one
			// of two pods permanently missing. Requeue and retry shortly.
			c.logger.Debug("HAProxyCfg not published yet, requeuing pod status update",
				"runtime_config_name", event.RuntimeConfigName,
				"pod_name", event.PodName,
				"retries", work.retries,
			)
			c.requeueStatusWork(work)
			return
		}
		c.logger.Warn("Failed to update deployment status",
			"error", err,
			"runtime_config_name", event.RuntimeConfigName,
			"pod_name", event.PodName,
		)
		return
	}

	c.logger.Debug("Deployment status updated successfully",
		"runtime_config_name", event.RuntimeConfigName,
		"pod_name", event.PodName,
	)
}

// statusWorkRetryDelay paces requeued status updates whose target HAProxyCfg
// wasn't published yet. The publish normally lands within milliseconds of the
// first deployment, so the first retry succeeds; the delay only bounds the
// spin when it doesn't.
const statusWorkRetryDelay = time.Second

// statusWorkMaxRetries caps requeues for one work item. 30 × 1s covers any
// realistic publish latency; past that the HAProxyCfg is gone for good (e.g.
// the config was deleted mid-flight) and the update is moot.
const statusWorkMaxRetries = 30

// requeueStatusWork puts a not-yet-appliable status update back into the
// pending map — unless a newer update for the same pod arrived meanwhile —
// and arms a short timer to wake the status worker again.
func (c *Component) requeueStatusWork(work *statusWorkItem) {
	if work.retries >= statusWorkMaxRetries {
		c.logger.Warn("Dropping pod status update after max retries, HAProxyCfg still not published",
			"runtime_config_name", work.event.RuntimeConfigName,
			"pod_name", work.event.PodName,
			"retries", work.retries,
		)
		return
	}
	work.retries++

	key := statusWorkKey(work.event)
	c.statusWorkPendingMu.Lock()
	if _, exists := c.statusWorkPending[key]; exists {
		// A newer update for this pod is already pending; it supersedes this one.
		c.statusWorkPendingMu.Unlock()
		return
	}
	c.statusWorkPending[key] = work
	c.statusWorkPendingMu.Unlock()

	time.AfterFunc(statusWorkRetryDelay, func() {
		select {
		case c.statusWorkTrigger <- struct{}{}:
		default:
		}
	})
}

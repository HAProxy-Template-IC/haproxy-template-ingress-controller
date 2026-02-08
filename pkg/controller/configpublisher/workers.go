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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

// publishWorker processes publish work items asynchronously.
// This worker runs in a separate goroutine to prevent blocking the event loop
// on slow K8S API calls.
func (c *Component) publishWorker(ctx context.Context) {
	for {
		select {
		case work := <-c.publishWork:
			c.processPublishWork(work)
		case <-ctx.Done():
			return
		}
	}
}

// processPublishWork performs the actual config publishing.
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

	// Convert auxiliary files
	auxFiles := c.convertAuxiliaryFiles(work.entry.auxFiles)

	// Create publish request
	request := &configpublisher.PublishRequest{
		TemplateConfigName:      work.templateConfig.Name,
		TemplateConfigNamespace: work.templateConfig.Namespace,
		TemplateConfigUID:       work.templateConfig.UID,
		Config:                  work.entry.config,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		AuxiliaryFiles:          auxFiles,
		RenderedAt:              work.entry.renderedAt,
		ValidatedAt:             work.event.Timestamp(),
		Checksum:                checksumHex,
		CompressionThreshold:    c.getCompressionThreshold(work.templateConfig),
	}

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
		// Clean up the cached entry
		c.mu.Lock()
		delete(c.renderedConfigs, work.correlationID)
		c.mu.Unlock()
		return
	}

	c.logger.Debug("runtime configuration published successfully",
		"runtime_config_name", result.RuntimeConfigName,
		"runtime_config_namespace", result.RuntimeConfigNamespace,
		"checksum", checksumHex,
		"correlation_id", work.correlationID,
	)

	// Update last published checksum and clean up the cached entry after successful publish
	c.mu.Lock()
	c.lastPublishedChecksum = checksumHex
	delete(c.renderedConfigs, work.correlationID)
	c.mu.Unlock()

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

	// Use pre-computed checksum from pipeline (propagated via TemplateRenderedEvent)
	checksumHex := work.entry.contentChecksum

	// Convert auxiliary files
	auxFiles := c.convertAuxiliaryFiles(work.entry.auxFiles)

	// Build validation error summary
	var validationError string
	if len(work.event.Errors) > 0 {
		validationError = work.event.Errors[0]
		if len(work.event.Errors) > 1 {
			validationError = fmt.Sprintf("%s (+%d more errors)", validationError, len(work.event.Errors)-1)
		}
	}

	// Create publish request with invalid state (uses "-invalid" suffix)
	request := &configpublisher.PublishRequest{
		TemplateConfigName:      work.templateConfig.Name,
		TemplateConfigNamespace: work.templateConfig.Namespace,
		TemplateConfigUID:       work.templateConfig.UID,
		Config:                  work.entry.config,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		AuxiliaryFiles:          auxFiles,
		RenderedAt:              work.entry.renderedAt,
		Checksum:                checksumHex,
		NameSuffix:              "-invalid",
		ValidationError:         validationError,
		CompressionThreshold:    c.getCompressionThreshold(work.templateConfig),
	}

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
		// Clean up the cached entry
		c.mu.Lock()
		delete(c.renderedConfigs, work.correlationID)
		c.mu.Unlock()
		return
	}

	c.logger.Warn("invalid runtime configuration published",
		"runtime_config_name", result.RuntimeConfigName,
		"runtime_config_namespace", result.RuntimeConfigNamespace,
		"validation_error", validationError,
		"correlation_id", work.correlationID,
	)

	// Clean up the cached entry after successful publish
	c.mu.Lock()
	delete(c.renderedConfigs, work.correlationID)
	c.mu.Unlock()
}

// statusWorker processes pod status update work items asynchronously with coalescing.
//
// Instead of processing each update as it arrives, this worker waits for a trigger
// signal and then processes all pending updates at once. This ensures that when
// multiple updates arrive for the same pod, only the latest one is applied.
func (c *Component) statusWorker(ctx context.Context) {
	for {
		select {
		case <-c.statusWorkTrigger:
			// Drain all pending work items and process them
			c.processAllPendingStatusWork()
		case <-ctx.Done():
			return
		}
	}
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
	timestamp := event.Timestamp()
	update := configpublisher.DeploymentStatusUpdate{
		RuntimeConfigName:      event.RuntimeConfigName,
		RuntimeConfigNamespace: event.RuntimeConfigNamespace,
		PodName:                event.PodName,
		Checksum:               event.Checksum,
		IsDriftCheck:           event.IsDriftCheck,
	}

	// Extract sync metadata if available
	if event.SyncMetadata != nil {
		// Only set deployedAt when actual operations were performed successfully
		if event.SyncMetadata.Error == "" && event.SyncMetadata.OperationCounts.TotalAPIOperations > 0 {
			update.DeployedAt = timestamp
		}

		// Set performance metrics when actual work happened (operations performed or error occurred)
		// Skip only for drift checks that found no drift (no operations, no error)
		if event.SyncMetadata.OperationCounts.TotalAPIOperations > 0 || event.SyncMetadata.Error != "" {
			update.SyncDuration = &event.SyncMetadata.SyncDuration
			update.VersionConflictRetries = event.SyncMetadata.VersionConflictRetries
			update.FallbackUsed = event.SyncMetadata.FallbackUsed
		}

		// Set reload information if reload was triggered
		if event.SyncMetadata.ReloadTriggered {
			update.LastReloadAt = &timestamp
			update.LastReloadID = event.SyncMetadata.ReloadID
		}

		// Copy operation summary
		if event.SyncMetadata.OperationCounts.TotalAPIOperations > 0 {
			update.OperationSummary = &configpublisher.OperationSummary{
				TotalAPIOperations: event.SyncMetadata.OperationCounts.TotalAPIOperations,
				BackendsAdded:      event.SyncMetadata.OperationCounts.BackendsAdded,
				BackendsRemoved:    event.SyncMetadata.OperationCounts.BackendsRemoved,
				BackendsModified:   event.SyncMetadata.OperationCounts.BackendsModified,
				ServersAdded:       event.SyncMetadata.OperationCounts.ServersAdded,
				ServersRemoved:     event.SyncMetadata.OperationCounts.ServersRemoved,
				ServersModified:    event.SyncMetadata.OperationCounts.ServersModified,
				FrontendsAdded:     event.SyncMetadata.OperationCounts.FrontendsAdded,
				FrontendsRemoved:   event.SyncMetadata.OperationCounts.FrontendsRemoved,
				FrontendsModified:  event.SyncMetadata.OperationCounts.FrontendsModified,
			}
		}

		// Copy error information
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

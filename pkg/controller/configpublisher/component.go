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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "config-publisher"

	// EventBufferSize is the buffer size for the event subscription channel.
	EventBufferSize = 50
)

// Component is the event adapter for the config publisher.
// It wraps the pure Publisher component and coordinates it with the event bus.
//
// This component caches information from multiple events (ConfigValidatedEvent,
// TemplateRenderedEvent) and publishes runtime config resources only after
// successful HAProxy validation (ValidationCompletedEvent).
type Component struct {
	publisher *configpublisher.Publisher
	eventBus  *busevents.EventBus
	logger    *slog.Logger

	// Subscribed in constructor for proper startup synchronization
	eventChan <-chan busevents.Event

	// Cached state from events (protected by mutex)
	mu                sync.RWMutex
	templateConfig    *v1alpha1.HAProxyTemplateConfig
	renderedConfig    string
	renderedAuxFiles  *dataplane.AuxiliaryFiles
	renderedAt        time.Time
	hasTemplateConfig bool
	hasRenderedConfig bool
}

// New creates a new config publisher component.
func New(
	publisher *configpublisher.Publisher,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *Component {
	if logger == nil {
		logger = slog.Default()
	}

	// Use SubscribeLeaderOnly because this component only runs on the leader.
	// It subscribes after EventBus.Start() when leadership is acquired.
	// All-replica components replay their state on BecameLeaderEvent to ensure
	// leader-only components don't miss critical state.
	return &Component{
		publisher: publisher,
		eventBus:  eventBus,
		logger:    logger.With("component", ComponentName),
		eventChan: eventBus.SubscribeLeaderOnly(EventBufferSize),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the config publisher's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// It processes events from the subscription channel established in the constructor.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("config publisher starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("Config publisher shutting down", "reason", ctx.Err())
			return ctx.Err()
		}
	}
}

// handleEvent processes events from the event bus.
func (c *Component) handleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		c.handleConfigValidated(e)

	case *events.TemplateRenderedEvent:
		c.handleTemplateRendered(e)

	case *events.ValidationCompletedEvent:
		c.handleValidationCompleted(e)

	case *events.ValidationFailedEvent:
		c.handleValidationFailed(e)

	case *events.ConfigAppliedToPodEvent:
		c.handleConfigAppliedToPod(e)

	case *events.HAProxyPodTerminatedEvent:
		c.handlePodTerminated(e)

	case *events.LostLeadershipEvent:
		c.handleLostLeadership(e)
	}
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
func (c *Component) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	c.logger.Debug("caching rendered config for publishing",
		"config_bytes", event.ConfigBytes,
		"auxiliary_file_count", event.AuxiliaryFileCount,
	)

	// Extract auxiliary files using typed accessor for compile-time type safety
	var auxFiles *dataplane.AuxiliaryFiles
	if event.AuxiliaryFiles != nil {
		if files, ok := event.GetAuxiliaryFiles(); ok {
			auxFiles = files
		} else {
			c.logger.Warn("template rendered event contains unexpected auxiliary files type - expected *dataplane.AuxiliaryFiles",
				"actual_type", fmt.Sprintf("%T", event.AuxiliaryFiles),
				"config_bytes", event.ConfigBytes)
		}
	}

	// Cache the rendered config
	c.mu.Lock()
	c.renderedConfig = event.HAProxyConfig
	c.renderedAuxFiles = auxFiles
	c.renderedAt = event.Timestamp()
	c.hasRenderedConfig = true
	c.mu.Unlock()
}

// handleValidationCompleted publishes the configuration after successful validation.
func (c *Component) handleValidationCompleted(_ *events.ValidationCompletedEvent) {
	// Get cached state
	c.mu.RLock()
	hasTemplateConfig := c.hasTemplateConfig
	hasRenderedConfig := c.hasRenderedConfig
	templateConfig := c.templateConfig
	renderedConfig := c.renderedConfig
	renderedAuxFiles := c.renderedAuxFiles
	renderedAt := c.renderedAt
	c.mu.RUnlock()

	// Check if we have all required data
	if !hasTemplateConfig || !hasRenderedConfig {
		c.logger.Warn("cannot publish configuration, missing cached state",
			"has_template_config", hasTemplateConfig,
			"has_rendered_config", hasRenderedConfig,
		)
		return
	}

	c.logger.Debug("publishing configuration after successful validation",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
	)

	// Calculate checksum of rendered config
	hash := sha256.Sum256([]byte(renderedConfig))
	checksum := hex.EncodeToString(hash[:])

	// Convert event to publish request
	req := configpublisher.PublishRequest{
		TemplateConfigName:      templateConfig.Name,
		TemplateConfigNamespace: templateConfig.Namespace,
		TemplateConfigUID:       templateConfig.UID,
		Config:                  renderedConfig,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		AuxiliaryFiles:          c.convertAuxiliaryFiles(renderedAuxFiles),
		RenderedAt:              renderedAt,
		ValidatedAt:             time.Now(),
		Checksum:                checksum,
	}

	// Call pure publisher (non-blocking - log errors but don't fail)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := c.publisher.PublishConfig(ctx, &req)
	if err != nil {
		c.logger.Warn("failed to publish configuration",
			"error", err,
			"config_name", templateConfig.Name,
			"config_namespace", templateConfig.Namespace,
		)

		// Publish failure event (non-blocking)
		c.eventBus.Publish(events.NewConfigPublishFailedEvent(
			fmt.Errorf("failed to publish configuration for %s/%s: %w", templateConfig.Namespace, templateConfig.Name, err),
		))
		return
	}

	c.logger.Debug("configuration published successfully",
		"runtime_config_name", result.RuntimeConfigName,
		"runtime_config_namespace", result.RuntimeConfigNamespace,
		"map_file_count", len(result.MapFileNames),
		"secret_count", len(result.SecretNames),
	)

	// Cleanup invalid config resource if it exists
	invalidConfigName := result.RuntimeConfigName + "-invalid"
	if err := c.publisher.DeleteRuntimeConfig(ctx, result.RuntimeConfigNamespace, invalidConfigName); err != nil {
		c.logger.Debug("failed to cleanup invalid config resource (may not exist)",
			"invalid_config_name", invalidConfigName,
			"error", err,
		)
	} else {
		c.logger.Debug("cleaned up invalid config resource",
			"invalid_config_name", invalidConfigName,
		)
	}

	// Publish success event
	c.eventBus.Publish(events.NewConfigPublishedEvent(
		result.RuntimeConfigName,
		result.RuntimeConfigNamespace,
		len(result.MapFileNames),
		len(result.SecretNames),
	))
}

// handleValidationFailed publishes the invalid configuration for observability.
func (c *Component) handleValidationFailed(event *events.ValidationFailedEvent) {
	// Get cached state
	c.mu.RLock()
	hasTemplateConfig := c.hasTemplateConfig
	hasRenderedConfig := c.hasRenderedConfig
	templateConfig := c.templateConfig
	renderedConfig := c.renderedConfig
	renderedAuxFiles := c.renderedAuxFiles
	renderedAt := c.renderedAt
	c.mu.RUnlock()

	// Check if we have all required data
	if !hasTemplateConfig || !hasRenderedConfig {
		c.logger.Warn("cannot publish invalid configuration, missing cached state",
			"has_template_config", hasTemplateConfig,
			"has_rendered_config", hasRenderedConfig,
		)
		return
	}

	// Join validation errors into a single string
	validationError := ""
	if len(event.Errors) > 0 {
		validationError = event.Errors[0]
		if len(event.Errors) > 1 {
			validationError = fmt.Sprintf("%s (and %d more errors)", event.Errors[0], len(event.Errors)-1)
		}
	}

	c.logger.Info("publishing invalid configuration for observability",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
		"error_count", len(event.Errors),
		"first_error", validationError,
	)

	// Calculate checksum of rendered config
	hash := sha256.Sum256([]byte(renderedConfig))
	checksum := hex.EncodeToString(hash[:])

	// Create publish request with -invalid suffix
	req := configpublisher.PublishRequest{
		TemplateConfigName:      templateConfig.Name,
		TemplateConfigNamespace: templateConfig.Namespace,
		TemplateConfigUID:       templateConfig.UID,
		Config:                  renderedConfig,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		AuxiliaryFiles:          c.convertAuxiliaryFiles(renderedAuxFiles),
		RenderedAt:              renderedAt,
		Checksum:                checksum,
		NameSuffix:              "-invalid",
		ValidationError:         validationError,
	}

	// Call pure publisher (non-blocking - log errors but don't fail)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := c.publisher.PublishConfig(ctx, &req)
	if err != nil {
		c.logger.Warn("failed to publish invalid configuration",
			"error", err,
			"config_name", templateConfig.Name,
			"config_namespace", templateConfig.Namespace,
		)
		return
	}

	c.logger.Info("invalid configuration published successfully for debugging",
		"config_name", templateConfig.Name,
		"config_namespace", templateConfig.Namespace,
	)
}

// handleConfigAppliedToPod updates the deployment status when a config is applied to a pod.
func (c *Component) handleConfigAppliedToPod(event *events.ConfigAppliedToPodEvent) {
	c.logger.Debug("updating deployment status for pod",
		"runtime_config_name", event.RuntimeConfigName,
		"runtime_config_namespace", event.RuntimeConfigNamespace,
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
		"checksum", event.Checksum,
		"is_drift_check", event.IsDriftCheck,
	)

	// Convert event to status update
	timestamp := event.Timestamp()
	update := configpublisher.DeploymentStatusUpdate{
		RuntimeConfigName:      event.RuntimeConfigName,
		RuntimeConfigNamespace: event.RuntimeConfigNamespace,
		PodName:                event.PodName,
		LastCheckedAt:          &timestamp, // Always set - every sync updates this
		Checksum:               event.Checksum,
		IsDriftCheck:           event.IsDriftCheck,
	}

	// Extract sync metadata if available
	if event.SyncMetadata != nil {
		// Only set deployedAt when actual operations were performed
		if event.SyncMetadata.Error == "" && event.SyncMetadata.OperationCounts.TotalAPIOperations > 0 {
			update.DeployedAt = timestamp
		}

		// Set reload information if reload was triggered
		if event.SyncMetadata.ReloadTriggered {
			update.LastReloadAt = &timestamp
			update.LastReloadID = event.SyncMetadata.ReloadID
		}

		// Copy performance metrics
		update.SyncDuration = &event.SyncMetadata.SyncDuration
		update.VersionConflictRetries = event.SyncMetadata.VersionConflictRetries
		update.FallbackUsed = event.SyncMetadata.FallbackUsed

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

	// Call pure publisher (non-blocking - log errors but don't fail)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.publisher.UpdateDeploymentStatus(ctx, &update); err != nil {
		c.logger.Warn("failed to update deployment status",
			"error", err,
			"runtime_config_name", event.RuntimeConfigName,
			"pod_name", event.PodName,
		)
		// Non-blocking - just log the error
		return
	}

	c.logger.Debug("deployment status updated successfully",
		"runtime_config_name", event.RuntimeConfigName,
		"pod_name", event.PodName,
	)
}

// handlePodTerminated cleans up pod references when a pod is terminated.
func (c *Component) handlePodTerminated(event *events.HAProxyPodTerminatedEvent) {
	c.logger.Info("cleaning up pod references after termination",
		"pod_name", event.PodName,
		"pod_namespace", event.PodNamespace,
	)

	// Convert event to cleanup request
	cleanupReq := configpublisher.PodCleanupRequest{
		PodName: event.PodName,
	}

	// Call pure publisher (non-blocking - log errors but don't fail)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// convertAuxiliaryFiles converts dataplane auxiliary files to publisher auxiliary files.
func (c *Component) convertAuxiliaryFiles(dataplaneFiles *dataplane.AuxiliaryFiles) *configpublisher.AuxiliaryFiles {
	if dataplaneFiles == nil {
		return nil
	}

	return &configpublisher.AuxiliaryFiles{
		MapFiles:        dataplaneFiles.MapFiles,
		SSLCertificates: dataplaneFiles.SSLCertificates,
		GeneralFiles:    dataplaneFiles.GeneralFiles,
	}
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

	if c.hasTemplateConfig || c.hasRenderedConfig {
		c.logger.Info("lost leadership, clearing cached configuration state",
			"had_template_config", c.hasTemplateConfig,
			"had_rendered_config", c.hasRenderedConfig,
		)
	}

	// Clear all cached state
	c.templateConfig = nil
	c.renderedConfig = ""
	c.renderedAuxFiles = nil
	c.renderedAt = time.Time{}
	c.hasTemplateConfig = false
	c.hasRenderedConfig = false
}

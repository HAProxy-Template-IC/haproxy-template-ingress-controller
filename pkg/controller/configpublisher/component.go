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
	"log/slog"
	"sync"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "config-publisher"

	// EventBufferSize is the buffer size for the event subscription channel.
	// Large buffer (200) to handle burst traffic during startup.
	// ConfigPublisher makes synchronous k8s API calls, so it processes events slowly
	// compared to the rate at which all-replica components publish them.
	EventBufferSize = 200

	// publishWorkChannelSize is the buffer size for the publish work channel.
	// A size of 1 provides natural coalescing - if new work arrives while
	// previous work is being processed, the old pending work is replaced.
	publishWorkChannelSize = 1

	// statusWorkTriggerSize is the buffer size for the status work trigger channel.
	// A size of 1 is sufficient since we use a separate map for coalescing.
	// The trigger just wakes up the worker to process pending updates.
	statusWorkTriggerSize = 1
)

// renderedConfigEntry holds cached rendered config data indexed by correlation ID.
// This ensures we match the correct TemplateRenderedEvent with its corresponding
// ValidationCompletedEvent, preventing stale data from being published when
// events from multiple reconciliation cycles are interleaved.
type renderedConfigEntry struct {
	config     string
	auxFiles   *dataplane.AuxiliaryFiles
	renderedAt time.Time
}

// publishWorkItem represents a config publish task for the async worker.
type publishWorkItem struct {
	correlationID  string
	event          *events.ValidationCompletedEvent
	templateConfig *v1alpha1.HAProxyTemplateConfig
	entry          *renderedConfigEntry
}

// validationFailedWorkItem represents a failed config publish task for the async worker.
type validationFailedWorkItem struct {
	correlationID  string
	event          *events.ValidationFailedEvent
	templateConfig *v1alpha1.HAProxyTemplateConfig
	entry          *renderedConfigEntry
}

// statusWorkItem represents a pod status update task for the async worker.
type statusWorkItem struct {
	event *events.ConfigAppliedToPodEvent
}

// Component is the event adapter for the config publisher.
// It wraps the pure Publisher component and coordinates it with the event bus.
//
// This component caches information from multiple events (ConfigValidatedEvent,
// TemplateRenderedEvent) and publishes runtime config resources only after
// successful HAProxy validation (ValidationCompletedEvent).
//
// Rendered configs are cached by correlation ID to ensure we match the correct
// TemplateRenderedEvent with its corresponding ValidationCompletedEvent, even when
// events from multiple reconciliation cycles are interleaved.
//
// The component uses async workers for K8S API operations to prevent blocking
// the event loop. This ensures new events are processed promptly even when
// K8S API calls are slow.
type Component struct {
	publisher *configpublisher.Publisher
	eventBus  *busevents.EventBus
	logger    *slog.Logger

	// Subscribed in Start() when leadership is acquired
	eventChan <-chan busevents.Event

	// Cached state from events (protected by mutex)
	mu                sync.RWMutex
	templateConfig    *v1alpha1.HAProxyTemplateConfig
	hasTemplateConfig bool

	// renderedConfigs maps correlation ID to rendered config data.
	// This ensures we match the correct TemplateRenderedEvent with its corresponding
	// ValidationCompletedEvent when events from multiple cycles are interleaved.
	renderedConfigs map[string]*renderedConfigEntry

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}

	// Work channels for async K8S API operations.
	// Using channels with small buffers provides natural coalescing:
	// newer work replaces older pending work when the worker is busy.
	publishWork          chan *publishWorkItem
	validationFailedWork chan *validationFailedWorkItem

	// Status update coalescing.
	// Instead of queueing every status update individually, we coalesce updates
	// for the same pod. When multiple updates arrive for the same pod before the
	// worker processes them, only the latest update is applied. This prevents
	// channel overflow during high-frequency reconciliation cycles.
	statusWorkPending   map[string]*statusWorkItem // Key: namespace/runtimeConfig/podName
	statusWorkPendingMu sync.Mutex
	statusWorkTrigger   chan struct{} // Signals worker to process pending updates

	// lastPublishedChecksum tracks the checksum of the last successfully published config.
	// Used to skip redundant CRD updates when config content is unchanged.
	// Protected by mu.
	lastPublishedChecksum string
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

	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &Component{
		publisher:            publisher,
		eventBus:             eventBus,
		logger:               logger.With("component", ComponentName),
		renderedConfigs:      make(map[string]*renderedConfigEntry),
		subscriptionReady:    make(chan struct{}),
		publishWork:          make(chan *publishWorkItem, publishWorkChannelSize),
		validationFailedWork: make(chan *validationFailedWorkItem, publishWorkChannelSize),
		statusWorkPending:    make(map[string]*statusWorkItem),
		statusWorkTrigger:    make(chan struct{}, statusWorkTriggerSize),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
func (c *Component) SubscriptionReady() <-chan struct{} {
	return c.subscriptionReady
}

// Start begins the config publisher's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// It subscribes to events when called (after leadership is acquired).
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (c *Component) Start(ctx context.Context) error {
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeTemplateRendered,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeConfigAppliedToPod,
		events.EventTypeHAProxyPodTerminated,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeLostLeadership,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(c.subscriptionReady)

	c.logger.Debug("config publisher starting")

	// Start async workers for K8S API operations.
	// These workers process work items from their channels, allowing the main
	// event loop to continue processing events without blocking on slow API calls.
	go c.publishWorker(ctx)
	go c.validationFailedWorker(ctx)
	go c.statusWorker(ctx)

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

	case *events.HAProxyPodsDiscoveredEvent:
		c.handlePodsDiscovered(e)

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

	// Cache the rendered config indexed by correlation ID
	c.mu.Lock()
	c.renderedConfigs[correlationID] = &renderedConfigEntry{
		config:     event.HAProxyConfig,
		auxFiles:   auxFiles,
		renderedAt: event.Timestamp(),
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
	if correlationID == "" {
		c.logger.Warn("ValidationCompletedEvent missing correlation ID, cannot match rendered config",
			"event_id", event.EventID())
		return
	}

	// Get cached state using correlation ID for proper event matching
	c.mu.RLock()
	hasTemplateConfig := c.hasTemplateConfig
	templateConfig := c.templateConfig
	entry, hasRenderedConfig := c.renderedConfigs[correlationID]
	c.mu.RUnlock()

	// Check if we have all required data
	if !hasTemplateConfig || !hasRenderedConfig {
		c.logger.Warn("cannot publish configuration, missing cached state",
			"has_template_config", hasTemplateConfig,
			"has_rendered_config", hasRenderedConfig,
			"correlation_id", correlationID,
		)
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

	select {
	case c.publishWork <- workItem:
		// Work queued successfully
	default:
		// Channel full - drain old work and queue new work (coalescing)
		select {
		case oldWork := <-c.publishWork:
			c.logger.Debug("coalescing publish work, replacing stale event",
				"old_correlation_id", oldWork.correlationID,
				"new_correlation_id", correlationID,
			)
			// Cleanup the old entry since we're skipping it
			c.mu.Lock()
			delete(c.renderedConfigs, oldWork.correlationID)
			c.mu.Unlock()
		default:
			// Channel was drained by worker between our checks - try again
		}
		// Now try to queue the new work
		select {
		case c.publishWork <- workItem:
			// Work queued successfully after coalescing
		default:
			// Very unlikely - worker grabbed our slot, just log and skip
			c.logger.Debug("publish work channel busy, will retry on next event",
				"correlation_id", correlationID)
		}
	}
}

// handleValidationFailed queues the invalid configuration for async publishing.
// Uses correlation ID to match with the corresponding TemplateRenderedEvent.
//
// This method is non-blocking - it queues work for the validationFailedWorker instead
// of making K8S API calls directly.
func (c *Component) handleValidationFailed(event *events.ValidationFailedEvent) {
	correlationID := event.CorrelationID()
	if correlationID == "" {
		c.logger.Warn("ValidationFailedEvent missing correlation ID, cannot match rendered config",
			"event_id", event.EventID())
		return
	}

	// Get cached state using correlation ID for proper event matching
	c.mu.RLock()
	hasTemplateConfig := c.hasTemplateConfig
	templateConfig := c.templateConfig
	entry, hasRenderedConfig := c.renderedConfigs[correlationID]
	c.mu.RUnlock()

	// Check if we have all required data
	if !hasTemplateConfig || !hasRenderedConfig {
		c.logger.Warn("cannot publish invalid configuration, missing cached state",
			"has_template_config", hasTemplateConfig,
			"has_rendered_config", hasRenderedConfig,
			"correlation_id", correlationID,
		)
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

	select {
	case c.validationFailedWork <- workItem:
		// Work queued successfully
	default:
		// Channel full - coalesce
		select {
		case oldWork := <-c.validationFailedWork:
			c.logger.Debug("coalescing validation failed work",
				"old_correlation_id", oldWork.correlationID,
				"new_correlation_id", correlationID,
			)
			c.mu.Lock()
			delete(c.renderedConfigs, oldWork.correlationID)
			c.mu.Unlock()
		default:
		}
		select {
		case c.validationFailedWork <- workItem:
		default:
			c.logger.Debug("validation failed work channel busy",
				"correlation_id", correlationID)
		}
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
		if endpoint, ok := ep.(dataplane.Endpoint); ok {
			podNames = append(podNames, endpoint.PodName)
		}
	}

	// Create timeout context (same pattern as handlePodTerminated)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		GeneralFiles:    dataplaneFiles.GeneralFiles,
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

	// Generate checksum covering main config and all auxiliary files
	checksumHex := dataplane.ComputeContentChecksum(work.entry.config, work.entry.auxFiles)

	// Skip publish if checksum unchanged (content deduplication).
	// This prevents redundant CRD updates when config content hasn't changed,
	// which commonly happens during high-frequency EndpointSlice reconciliations.
	c.mu.RLock()
	lastChecksum := c.lastPublishedChecksum
	c.mu.RUnlock()

	if checksumHex == lastChecksum {
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
	publishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	c.logger.Info("runtime configuration published successfully",
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

	// Generate checksum covering main config and all auxiliary files
	checksumHex := dataplane.ComputeContentChecksum(work.entry.config, work.entry.auxFiles)

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
	publishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		LastCheckedAt:          &timestamp,
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

	// Call pure publisher with timeout context
	updateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

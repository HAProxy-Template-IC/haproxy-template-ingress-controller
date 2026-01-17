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

// Package deployer implements the Deployer component that deploys validated
// HAProxy configurations to discovered HAProxy pod endpoints.
//
// The Deployer is a stateless executor that receives DeploymentScheduledEvent
// and executes deployments to the specified endpoints. All deployment scheduling,
// rate limiting, and queueing logic is handled by the DeploymentScheduler component.
package deployer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "deployer"

	// EventBufferSize is the size of the event subscription buffer.
	// Size 50: Low-volume component (~1-2 deployment events per reconciliation cycle).
	// Larger buffers reduce event drops during bursts but consume more memory.
	EventBufferSize = 50
)

// Component implements the deployer component.
//
// It subscribes to DeploymentScheduledEvent and deploys configurations to
// HAProxy instances. This is a stateless executor - all scheduling logic
// is handled by the DeploymentScheduler component.
//
// Event subscriptions:
//   - DeploymentScheduledEvent: Execute deployment to specified endpoints
//   - DeploymentCancelRequestEvent: Cancel in-progress deployment
//
// The component publishes deployment result events for observability.
type Component struct {
	eventBus             *busevents.EventBus
	eventChan            <-chan busevents.Event // Event subscription channel (subscribed in Start())
	logger               *slog.Logger
	deploymentInProgress atomic.Bool // Defensive: prevents concurrent deployments if scheduler has bugs

	// maxParallel limits concurrent Dataplane API operations during sync.
	// 0 means unlimited (not recommended for large configs).
	maxParallel int

	// rawPushThreshold triggers raw config push when change count exceeds this value.
	// 0 means disabled (fine-grained sync always used, except version=1).
	rawPushThreshold int

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}

	// Deployment cancellation support
	cancelMu            sync.Mutex
	activeCorrelationID string             // Correlation ID of active deployment
	activeCancelFunc    context.CancelFunc // Cancel function for active deployment
	deploymentDone      chan struct{}      // Signals when deployment goroutine completes
}

// New creates a new Deployer component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - logger: Structured logger for component logging
//   - maxParallel: Maximum concurrent Dataplane API operations (0 = unlimited)
//   - rawPushThreshold: Change count threshold for raw config push (0 = disabled)
//
// Returns:
//   - A new Component instance ready to be started
func New(eventBus *busevents.EventBus, logger *slog.Logger, maxParallel, rawPushThreshold int) *Component {
	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &Component{
		eventBus:          eventBus,
		logger:            logger.With("component", ComponentName),
		maxParallel:       maxParallel,
		rawPushThreshold:  rawPushThreshold,
		healthTracker:     lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		subscriptionReady: make(chan struct{}),
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

// Start begins the deployer's event loop.
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
	// Subscribe to both DeploymentScheduledEvent and DeploymentCancelRequestEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeDeploymentScheduled,
		events.EventTypeDeploymentCancelRequest)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(c.subscriptionReady)

	c.logger.Debug("deployer starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(ctx, event)

		case <-ctx.Done():
			c.logger.Info("Deployer shutting down", "reason", ctx.Err())
			// Cancel any active deployment on shutdown
			c.cancelActiveDeployment("shutdown")
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(ctx context.Context, event busevents.Event) {
	switch e := event.(type) {
	case *events.DeploymentScheduledEvent:
		c.handleDeploymentScheduled(ctx, e)
	case *events.DeploymentCancelRequestEvent:
		c.handleDeploymentCancelRequest(e)
	}
}

// handleDeploymentScheduled implements "latest wins" coalescing for deployment scheduled events.
//
// When multiple coalescible DeploymentScheduledEvents arrive while deployment is in progress,
// intermediate events are superseded - only the latest pending event is processed.
// This prevents queue backlog where deployments can't keep up with high-frequency validation.
//
// Non-coalescible events (e.g., from drift_prevention, validation_fallback) are always
// processed and never skipped.
//
// Uses the centralized coalesce.DrainLatest utility for consistent behavior across components.
func (c *Component) handleDeploymentScheduled(ctx context.Context, event *events.DeploymentScheduledEvent) {
	// Process current event
	c.performDeployment(ctx, event)

	// After deployment completes, drain the event channel for any pending coalescible events.
	// Since the event loop is single-threaded, events buffer in eventChan while performDeployment executes.
	// We process only the latest coalescible event.
	for {
		latest, supersededCount := coalesce.DrainLatest[*events.DeploymentScheduledEvent](
			c.eventChan,
			func(e busevents.Event) { c.handleEvent(ctx, e) }, // Handle non-coalescible events
		)
		if latest == nil {
			return
		}

		if supersededCount > 0 {
			c.logger.Debug("Coalesced deployment scheduled events",
				"superseded_count", supersededCount,
				"processing", latest.CorrelationID())
		}
		c.performDeployment(ctx, latest)
	}
}

// performDeployment executes a single deployment.
// This method is called by handleDeploymentScheduled after coalescing logic.
//
// Defensive: drops duplicate events if a deployment is already in progress.
func (c *Component) performDeployment(ctx context.Context, event *events.DeploymentScheduledEvent) {
	// Track processing for health check stall detection
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	correlationID := event.CorrelationID()

	// Defensive check: atomically set deploymentInProgress from false to true
	// This prevents concurrent deployments if scheduler has bugs
	if !c.deploymentInProgress.CompareAndSwap(false, true) {
		c.logger.Error("dropping duplicate DeploymentScheduledEvent - deployment already in progress",
			"reason", event.Reason,
			"endpoint_count", len(event.Endpoints),
			"correlation_id", correlationID)
		return
	}
	// Note: flag will be cleared by deployToEndpoints after deployment completes

	// Create cancellable context for this deployment
	deployCtx, cancel := context.WithCancel(ctx)

	// Store cancel function so it can be called on timeout
	c.cancelMu.Lock()
	c.activeCorrelationID = correlationID
	c.activeCancelFunc = cancel
	c.deploymentDone = make(chan struct{})
	c.cancelMu.Unlock()

	// Ensure we clean up cancel state when deployment completes
	defer func() {
		c.cancelMu.Lock()
		c.activeCorrelationID = ""
		c.activeCancelFunc = nil
		if c.deploymentDone != nil {
			close(c.deploymentDone)
			c.deploymentDone = nil
		}
		c.cancelMu.Unlock()
	}()

	c.logger.Debug("Deployment scheduled, starting execution",
		"reason", event.Reason,
		"endpoint_count", len(event.Endpoints),
		"config_bytes", len(event.Config),
		"correlation_id", correlationID)

	// Execute deployment with cancellable context
	c.deployToEndpoints(deployCtx, event.Config, event.AuxiliaryFiles, event.Endpoints, event.RuntimeConfigName, event.RuntimeConfigNamespace, event.Reason, correlationID)
}

// convertEndpoints converts []interface{} to []dataplane.Endpoint.
func (c *Component) convertEndpoints(endpointsRaw []interface{}) []dataplane.Endpoint {
	endpoints := make([]dataplane.Endpoint, 0, len(endpointsRaw))
	for i, ep := range endpointsRaw {
		endpoint, ok := ep.(dataplane.Endpoint)
		if !ok {
			c.logger.Error("invalid endpoint type",
				"index", i,
				"expected", "dataplane.Endpoint",
				"actual", fmt.Sprintf("%T", ep))
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

// convertAuxFiles converts interface{} to *dataplane.AuxiliaryFiles.
func (c *Component) convertAuxFiles(auxFilesRaw interface{}) *dataplane.AuxiliaryFiles {
	if auxFilesRaw == nil {
		return nil
	}

	auxFiles, ok := auxFilesRaw.(*dataplane.AuxiliaryFiles)
	if !ok {
		c.logger.Warn("invalid auxiliary files type, proceeding without aux files",
			"expected", "*dataplane.AuxiliaryFiles",
			"actual", fmt.Sprintf("%T", auxFilesRaw))
		return nil
	}
	return auxFiles
}

// deployToEndpoints deploys configuration to all HAProxy endpoints in parallel.
//
// This method:
//  1. Publishes DeploymentStartedEvent
//  2. Deploys to all endpoints in parallel
//  3. Publishes InstanceDeployedEvent or InstanceDeploymentFailedEvent for each endpoint
//  4. Publishes ConfigAppliedToPodEvent for successful deployments
//  5. Publishes DeploymentCompletedEvent with summary
func (c *Component) deployToEndpoints(
	ctx context.Context,
	config string,
	auxFilesRaw interface{},
	endpointsRaw []interface{},
	runtimeConfigName string,
	runtimeConfigNamespace string,
	reason string,
	correlationID string,
) {
	// Clear deployment flag after this function completes (after wg.Wait())
	defer c.deploymentInProgress.Store(false)

	startTime := time.Now()

	// Convert endpoints and auxiliary files
	endpoints := c.convertEndpoints(endpointsRaw)
	if len(endpoints) == 0 {
		c.logger.Error("no valid endpoints to deploy to")
		// Publish completion event so downstream components know deployment didn't happen
		c.eventBus.Publish(events.NewDeploymentCompletedEvent(
			events.DeploymentResult{},
			events.WithCorrelation(correlationID, correlationID),
		))
		return
	}

	auxFiles := c.convertAuxFiles(auxFilesRaw)

	// Calculate config checksum for ConfigAppliedToPodEvent
	hash := sha256.Sum256([]byte(config))
	checksum := hex.EncodeToString(hash[:])

	c.logger.Debug("Starting deployment",
		"reason", reason,
		"endpoint_count", len(endpoints),
		"config_bytes", len(config),
		"has_aux_files", auxFiles != nil,
		"correlation_id", correlationID)

	// Publish DeploymentStartedEvent with correlation
	c.eventBus.Publish(events.NewDeploymentStartedEvent(
		endpointsRaw,
		events.WithCorrelation(correlationID, correlationID),
	))

	// Deploy to all endpoints in parallel
	var wg sync.WaitGroup

	// deploymentState holds aggregated metrics protected for concurrent access
	state := &deploymentState{
		operationBreakdown: make(map[string]int),
	}

	for i := range endpoints {
		wg.Add(1)
		go func(ep *dataplane.Endpoint) {
			defer wg.Done()
			c.processEndpointDeployment(ctx, ep, config, auxFiles, checksum, reason,
				runtimeConfigName, runtimeConfigNamespace, correlationID, state)
		}(&endpoints[i])
	}

	// Wait for all deployments to complete
	wg.Wait()

	totalDurationMs := time.Since(startTime).Milliseconds()

	c.logger.Debug("Deployment completed",
		"total_endpoints", len(endpoints),
		"succeeded", state.successCount,
		"failed", state.failureCount,
		"reloads_triggered", state.reloadsTriggered,
		"total_operations", state.totalOperations,
		"duration_ms", totalDurationMs,
		"correlation_id", correlationID)

	// Publish DeploymentCompletedEvent with correlation
	c.eventBus.Publish(events.NewDeploymentCompletedEvent(
		events.DeploymentResult{
			Total:              len(endpoints),
			Succeeded:          int(state.successCount),
			Failed:             int(state.failureCount),
			DurationMs:         totalDurationMs,
			ReloadsTriggered:   int(state.reloadsTriggered),
			TotalAPIOperations: int(state.totalOperations),
			OperationBreakdown: state.operationBreakdown,
		},
		events.WithCorrelation(correlationID, correlationID),
	))
}

// deploymentState holds aggregated metrics protected for concurrent access.
type deploymentState struct {
	successCount       int32
	failureCount       int32
	reloadsTriggered   int32
	totalOperations    int32
	breakdownMu        sync.Mutex
	operationBreakdown map[string]int
}

// processEndpointDeployment handles deployment to a single endpoint and updates shared state.
// This method is called from goroutines and must be thread-safe.
func (c *Component) processEndpointDeployment(
	ctx context.Context,
	ep *dataplane.Endpoint,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	checksum string,
	reason string,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	correlationID string,
	state *deploymentState,
) {
	// Check if context is already cancelled (e.g., timeout fired)
	if ctx.Err() != nil {
		c.logger.Debug("Skipping endpoint deployment - context cancelled",
			"endpoint", ep.URL,
			"pod", ep.PodName,
			"error", ctx.Err(),
			"correlation_id", correlationID)
		atomic.AddInt32(&state.failureCount, 1)
		return
	}

	instanceStart := time.Now()
	syncResult, err := c.deployToSingleEndpoint(ctx, config, auxFiles, ep)
	durationMs := time.Since(instanceStart).Milliseconds()

	// Determine if this is a drift check based on deployment reason
	isDriftCheck := reason == "drift_prevention"

	if err != nil {
		c.handleEndpointFailure(ep, err, durationMs, checksum, isDriftCheck,
			runtimeConfigName, runtimeConfigNamespace, correlationID, state)
	} else {
		c.handleEndpointSuccess(ep, syncResult, durationMs, checksum, isDriftCheck,
			runtimeConfigName, runtimeConfigNamespace, correlationID, state)
	}
}

// handleEndpointFailure processes a failed endpoint deployment.
func (c *Component) handleEndpointFailure(
	ep *dataplane.Endpoint,
	err error,
	durationMs int64,
	checksum string,
	isDriftCheck bool,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	correlationID string,
	state *deploymentState,
) {
	c.logger.Error("deployment failed for endpoint",
		"endpoint", ep.URL,
		"pod", ep.PodName,
		"error", err,
		"duration_ms", durationMs,
		"correlation_id", correlationID)

	// Publish InstanceDeploymentFailedEvent with correlation
	c.eventBus.Publish(events.NewInstanceDeploymentFailedEvent(
		ep,
		err.Error(),
		true, // retryable
		events.WithCorrelation(correlationID, correlationID),
	))

	// Publish ConfigAppliedToPodEvent with error info (for status tracking)
	if runtimeConfigName != "" && runtimeConfigNamespace != "" {
		syncMetadata := &events.SyncMetadata{
			Error: err.Error(),
		}
		c.eventBus.Publish(events.NewConfigAppliedToPodEvent(
			runtimeConfigName,
			runtimeConfigNamespace,
			ep.PodName,
			ep.PodNamespace,
			checksum,
			isDriftCheck,
			syncMetadata,
		))
	}

	atomic.AddInt32(&state.failureCount, 1)
}

// handleEndpointSuccess processes a successful endpoint deployment.
func (c *Component) handleEndpointSuccess(
	ep *dataplane.Endpoint,
	syncResult *dataplane.SyncResult,
	durationMs int64,
	checksum string,
	isDriftCheck bool,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	correlationID string,
	state *deploymentState,
) {
	c.logger.Debug("Deployment succeeded for endpoint",
		"endpoint", ep.URL,
		"pod", ep.PodName,
		"duration_ms", durationMs,
		"reload_triggered", syncResult.ReloadTriggered,
		"correlation_id", correlationID)

	// Publish InstanceDeployedEvent with correlation
	c.eventBus.Publish(events.NewInstanceDeployedEvent(
		ep,
		durationMs,
		syncResult.ReloadTriggered,
		events.WithCorrelation(correlationID, correlationID),
	))

	// Publish ConfigAppliedToPodEvent (for runtime config status updates)
	// Skip for no-op drift checks to reduce Kubernetes API load
	if runtimeConfigName != "" && runtimeConfigNamespace != "" {
		if isDriftCheck && c.isNoOpDriftCheck(syncResult) {
			c.logger.Debug("Skipping ConfigAppliedToPodEvent for no-op drift check",
				"pod", ep.PodName,
				"endpoint", ep.URL)
		} else {
			syncMetadata := c.convertSyncResultToMetadata(syncResult)
			c.eventBus.Publish(events.NewConfigAppliedToPodEvent(
				runtimeConfigName,
				runtimeConfigNamespace,
				ep.PodName,
				ep.PodNamespace,
				checksum,
				isDriftCheck,
				syncMetadata,
			))
		}
	}

	atomic.AddInt32(&state.successCount, 1)

	// Track reloads and operations for aggregate metrics
	if syncResult.ReloadTriggered {
		atomic.AddInt32(&state.reloadsTriggered, 1)
	}

	// Details is always populated per dataplane.SyncResult contract
	atomic.AddInt32(&state.totalOperations, safeIntToInt32(syncResult.Details.TotalOperations))

	// Accumulate operation breakdown from AppliedOperations
	// All operations (config + aux files) are now in AppliedOperations
	state.breakdownMu.Lock()
	for _, op := range syncResult.AppliedOperations {
		key := op.Section + "_" + op.Type
		state.operationBreakdown[key]++
	}
	state.breakdownMu.Unlock()
}

// deployToSingleEndpoint deploys configuration to a single HAProxy endpoint.
//
// Returns the sync result containing detailed operation metadata, or an error if the sync failed.
func (c *Component) deployToSingleEndpoint(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	endpoint *dataplane.Endpoint,
) (*dataplane.SyncResult, error) {
	// Create client for this endpoint
	client, err := dataplane.NewClient(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Close()

	// Use default sync options and apply configuration limits
	opts := dataplane.DefaultSyncOptions()
	opts.MaxParallel = c.maxParallel
	opts.RawPushThreshold = c.rawPushThreshold

	// Sync configuration
	result, err := client.Sync(ctx, config, auxFiles, opts)
	if err != nil {
		return nil, fmt.Errorf("sync failed: %w", err)
	}

	c.logger.Debug("sync completed for endpoint",
		"endpoint", endpoint.URL,
		"pod", endpoint.PodName,
		"applied_operations", len(result.AppliedOperations),
		"reload_triggered", result.ReloadTriggered,
		"duration", result.Duration)

	return result, nil
}

// safeIntToInt32 converts int to int32 with bounds checking to prevent overflow.
func safeIntToInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

// convertSyncResultToMetadata converts dataplane.SyncResult to events.SyncMetadata.
func (c *Component) convertSyncResultToMetadata(result *dataplane.SyncResult) *events.SyncMetadata {
	if result == nil {
		return nil
	}

	// Count total servers added/removed/modified across all backends
	totalServersAdded := 0
	for _, servers := range result.Details.ServersAdded {
		totalServersAdded += len(servers)
	}
	totalServersRemoved := 0
	for _, servers := range result.Details.ServersDeleted {
		totalServersRemoved += len(servers)
	}
	totalServersModified := 0
	for _, servers := range result.Details.ServersModified {
		totalServersModified += len(servers)
	}

	return &events.SyncMetadata{
		ReloadTriggered:        result.ReloadTriggered,
		ReloadID:               result.ReloadID,
		SyncDuration:           result.Duration,
		VersionConflictRetries: result.Retries,
		FallbackUsed:           result.UsedRawPush(),
		OperationCounts: events.OperationCounts{
			TotalAPIOperations: result.Details.TotalOperations,
			BackendsAdded:      len(result.Details.BackendsAdded),
			BackendsRemoved:    len(result.Details.BackendsDeleted),
			BackendsModified:   len(result.Details.BackendsModified),
			ServersAdded:       totalServersAdded,
			ServersRemoved:     totalServersRemoved,
			ServersModified:    totalServersModified,
			FrontendsAdded:     len(result.Details.FrontendsAdded),
			FrontendsRemoved:   len(result.Details.FrontendsDeleted),
			FrontendsModified:  len(result.Details.FrontendsModified),
		},
		Error: "", // Empty on success
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

// handleDeploymentCancelRequest cancels an in-progress deployment if the correlation ID matches.
func (c *Component) handleDeploymentCancelRequest(event *events.DeploymentCancelRequestEvent) {
	correlationID := event.CorrelationID()

	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	// Check if there's an active deployment with matching correlation ID
	if c.activeCorrelationID == "" || c.activeCancelFunc == nil {
		c.logger.Debug("Received cancel request but no deployment in progress",
			"requested_correlation_id", correlationID,
			"reason", event.Reason)
		return
	}

	if c.activeCorrelationID != correlationID {
		c.logger.Debug("Received cancel request but correlation ID does not match",
			"requested_correlation_id", correlationID,
			"active_correlation_id", c.activeCorrelationID,
			"reason", event.Reason)
		return
	}

	c.logger.Info("Cancelling in-progress deployment",
		"correlation_id", correlationID,
		"reason", event.Reason)

	// Cancel the deployment context
	c.activeCancelFunc()
}

// cancelActiveDeployment cancels any active deployment regardless of correlation ID.
// Used for graceful shutdown.
func (c *Component) cancelActiveDeployment(reason string) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()

	if c.activeCancelFunc == nil {
		return
	}

	c.logger.Info("Cancelling active deployment",
		"correlation_id", c.activeCorrelationID,
		"reason", reason)

	c.activeCancelFunc()

	// Wait for deployment to complete if deploymentDone channel exists
	if c.deploymentDone != nil {
		c.cancelMu.Unlock()
		<-c.deploymentDone
		c.cancelMu.Lock()
	}
}

// isNoOpDriftCheck returns true if this drift check made no meaningful changes
// that warrant publishing a ConfigAppliedToPodEvent.
//
// We skip event publishing when ALL of the following are true:
//   - No operations were performed (TotalOperations=0)
//   - No HAProxy reload was triggered.
func (c *Component) isNoOpDriftCheck(syncResult *dataplane.SyncResult) bool {
	if syncResult == nil {
		return true
	}

	// Always publish if operations were performed
	if syncResult.Details.TotalOperations > 0 {
		return false
	}

	// Always publish if reload was triggered
	if syncResult.ReloadTriggered {
		return false
	}

	// No meaningful changes - safe to skip
	return true
}

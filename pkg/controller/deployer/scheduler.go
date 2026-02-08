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

// Package deployer implements deployment scheduling and execution components.
package deployer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// SchedulerComponentName is the unique identifier for the DeploymentScheduler component.
	SchedulerComponentName = "deployment-scheduler"

	// SchedulerEventBufferSize is the size of the event subscription buffer for the scheduler.
	// Size 50: Moderate-volume component handling template, validation, and discovery events.
	// Note: Named with "Scheduler" prefix to avoid conflict with EventBufferSize in this package.
	SchedulerEventBufferSize = 50
)

// deploymentPhase represents the current phase of the deployment scheduler state machine.
type deploymentPhase int

const (
	// phaseIdle means no deployment is in progress or pending scheduling.
	phaseIdle deploymentPhase = iota

	// phaseRateLimiting means a deployment is waiting for the minimum interval to elapse.
	// The timeout checker should NOT fire during this phase.
	phaseRateLimiting

	// phaseDeploying means a deployment event has been published and we're waiting for completion.
	// The timeout checker SHOULD fire if this phase lasts too long.
	phaseDeploying
)

// String returns a human-readable representation of the deployment phase.
func (p deploymentPhase) String() string {
	switch p {
	case phaseIdle:
		return "idle"
	case phaseRateLimiting:
		return "rate_limiting"
	case phaseDeploying:
		return "deploying"
	default:
		return "unknown"
	}
}

// schedulerState groups the deployment scheduling state into a single struct.
// All fields are protected by DeploymentScheduler.schedulerMutex.
type schedulerState struct {
	phase                 deploymentPhase
	activeCorrelationID   string
	deploymentStartTime   time.Time
	pending               *scheduledDeployment
	lastDeploymentEndTime time.Time
}

// scheduledDeployment represents a deployment that was triggered while another
// deployment was in progress. Only the latest scheduled deployment is kept (latest wins).
type scheduledDeployment struct {
	config        string
	auxFiles      *dataplane.AuxiliaryFiles
	parsedConfig  *parser.StructuredConfig
	endpoints     []dataplane.Endpoint
	reason        string
	correlationID string // Correlation ID for event tracing
	coalescible   bool   // Whether this deployment can be coalesced (skipped if newer available)
}

// DeploymentScheduler implements deployment scheduling with rate limiting.
//
// It subscribes to events that trigger deployments, maintains the state of
// rendered and validated configurations, and enforces minimum deployment intervals.
//
// Event subscriptions:
//   - TemplateRenderedEvent: Track rendered config and auxiliary files
//   - ValidationCompletedEvent: Cache validated config and schedule deployment
//   - ValidationFailedEvent: Deploy cached config for drift prevention fallback
//   - HAProxyPodsDiscoveredEvent: Update endpoints and schedule deployment
//
// The component publishes DeploymentScheduledEvent when a deployment should execute.
type DeploymentScheduler struct {
	eventBus              *busevents.EventBus
	eventChan             <-chan busevents.Event // Event subscription channel (subscribed in Start())
	logger                *slog.Logger
	minDeploymentInterval time.Duration
	ctx                   context.Context // Main event loop context for scheduling

	// State protected by mutex
	mu                      sync.RWMutex
	lastRenderedConfig      string                    // Last rendered HAProxy config (before validation)
	lastAuxiliaryFiles      *dataplane.AuxiliaryFiles // Last rendered auxiliary files
	lastContentChecksum     string                    // Pre-computed content checksum from pipeline
	lastValidatedConfig     string                    // Last validated HAProxy config
	lastValidatedAux        *dataplane.AuxiliaryFiles // Last validated auxiliary files
	lastParsedConfig        *parser.StructuredConfig  // Pre-parsed desired config
	lastCorrelationID       string                    // Correlation ID from last validation event
	lastCoalescible         bool                      // Coalescibility flag from last validation event
	currentEndpoints        []dataplane.Endpoint      // Current HAProxy pod endpoints
	hasValidConfig          bool                      // Whether we have a validated config to deploy
	runtimeConfigName       string                    // Name of HAProxyCfg resource (set by ConfigPublishedEvent)
	runtimeConfigNamespace  string                    // Namespace of HAProxyCfg resource (set by ConfigPublishedEvent)
	templateConfigName      string                    // Name from ConfigValidatedEvent.TemplateConfig (for early runtimeConfigName computation)
	templateConfigNamespace string                    // Namespace from ConfigValidatedEvent.TemplateConfig

	// Deployment scheduling and rate limiting
	schedulerMutex    sync.Mutex
	state             schedulerState
	deploymentTimeout time.Duration

	// Cache for deployment optimization - skip if config unchanged
	lastDeployedConfigHash string    // SHA-256 hash of last successfully deployed config
	lastDeployedPodSetHash string    // Hash of pod endpoints for the last deployment
	lastDeployedTime       time.Time // When the last successful deployment occurred

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}
}

// computePodSetHash computes a hash of the current pod endpoints.
// Used to detect if pod set changed (new/removed HAProxy pods).
func computePodSetHash(endpoints []dataplane.Endpoint) string {
	h := sha256.New()

	// Extract and sort URLs for deterministic hashing
	urls := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		urls = append(urls, ep.URL)
	}
	sort.Strings(urls)

	for _, url := range urls {
		h.Write([]byte(url))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// NewDeploymentScheduler creates a new DeploymentScheduler component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing scheduled deployments
//   - logger: Structured logger for component logging
//   - minDeploymentInterval: Minimum time between consecutive deployments (rate limiting)
//   - deploymentTimeout: Maximum time to wait for a deployment to complete before retrying
//
// Returns:
//   - A new DeploymentScheduler instance ready to be started
func NewDeploymentScheduler(eventBus *busevents.EventBus, logger *slog.Logger, minDeploymentInterval, deploymentTimeout time.Duration) *DeploymentScheduler {
	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &DeploymentScheduler{
		eventBus:              eventBus,
		logger:                logger.With("component", SchedulerComponentName),
		minDeploymentInterval: minDeploymentInterval,
		deploymentTimeout:     deploymentTimeout,
		healthTracker:         lifecycle.NewProcessingTracker(SchedulerComponentName, lifecycle.DefaultProcessingTimeout),
		subscriptionReady:     make(chan struct{}),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (s *DeploymentScheduler) Name() string {
	return SchedulerComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
func (s *DeploymentScheduler) SubscriptionReady() <-chan struct{} {
	return s.subscriptionReady
}

// Start begins the deployment scheduler's event loop.
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
func (s *DeploymentScheduler) Start(ctx context.Context) error {
	s.ctx = ctx // Save context for scheduling operations

	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	s.eventChan = s.eventBus.SubscribeTypesLeaderOnly(SchedulerComponentName, SchedulerEventBufferSize,
		events.EventTypeTemplateRendered,
		events.EventTypeConfigValidated,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeDeploymentCompleted,
		events.EventTypeConfigPublished,
		events.EventTypeLostLeadership,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(s.subscriptionReady)

	s.logger.Debug("deployment scheduler starting",
		"min_deployment_interval_ms", s.minDeploymentInterval.Milliseconds(),
		"deployment_timeout_ms", s.deploymentTimeout.Milliseconds())

	// Ticker to check for deployment timeouts
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-s.eventChan:
			s.handleEvent(ctx, event)

		case <-ticker.C:
			s.checkDeploymentTimeout(ctx)

		case <-ctx.Done():
			s.logger.Info("DeploymentScheduler shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (s *DeploymentScheduler) handleEvent(ctx context.Context, event busevents.Event) {
	// Track processing for health check stall detection
	s.healthTracker.StartProcessing()
	defer s.healthTracker.EndProcessing()

	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		s.handleTemplateRendered(e)

	case *events.ConfigValidatedEvent:
		s.handleConfigValidated(e)

	case *events.ValidationCompletedEvent:
		s.handleValidationCompleted(ctx, e)

	case *events.ValidationFailedEvent:
		s.handleValidationFailed(ctx, e)

	case *events.HAProxyPodsDiscoveredEvent:
		s.handlePodsDiscovered(ctx, e)

	case *events.DeploymentCompletedEvent:
		s.handleDeploymentCompleted(e)

	case *events.ConfigPublishedEvent:
		s.handleConfigPublished(e)

	case *events.LostLeadershipEvent:
		s.handleLostLeadership(e)
	}
}

// handleTemplateRendered handles template rendering completion.
//
// This caches the rendered configuration and auxiliary files for later deployment
// after validation completes.
func (s *DeploymentScheduler) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastRenderedConfig = event.HAProxyConfig
	s.lastAuxiliaryFiles = event.AuxiliaryFiles
	s.lastContentChecksum = event.ContentChecksum

	s.logger.Debug("cached rendered config for deployment after validation",
		"config_bytes", event.ConfigBytes,
		"aux_files", event.AuxiliaryFileCount)
}

// handleConfigValidated handles ConfigValidatedEvent to cache template config metadata.
//
// This caches the template config name and namespace early in the pipeline, allowing
// runtimeConfigName to be computed deterministically without waiting for ConfigPublishedEvent.
// This fixes the race condition where deployment was scheduled before ConfigPublishedEvent arrived.
func (s *DeploymentScheduler) handleConfigValidated(event *events.ConfigValidatedEvent) {
	tc, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		s.logger.Debug("ConfigValidatedEvent.TemplateConfig is not HAProxyTemplateConfig, skipping")
		return
	}

	s.mu.Lock()
	s.templateConfigName = tc.Name
	s.templateConfigNamespace = tc.Namespace
	s.mu.Unlock()

	s.logger.Debug("cached template config metadata for runtime config name computation",
		"template_config_name", tc.Name,
		"template_config_namespace", tc.Namespace)
}

// handleValidationCompleted handles successful configuration validation.
//
// This caches the validated configuration and schedules deployment to current endpoints.
// This is called during full reconciliation cycles (config or resource changes).
func (s *DeploymentScheduler) handleValidationCompleted(ctx context.Context, event *events.ValidationCompletedEvent) {
	correlationID := event.CorrelationID()
	s.logger.Debug("Validation completed, preparing deployment",
		"warnings", len(event.Warnings),
		"duration_ms", event.DurationMs,
		"has_parsed_config", event.ParsedConfig != nil,
		"correlation_id", correlationID)

	// Log warnings if any
	for _, warning := range event.Warnings {
		s.logger.Warn("validation warning", "warning", warning)
	}

	// Get current state and cache validated config BEFORE scheduling
	// This prevents race where pod discovery reads stale config
	s.mu.Lock()
	config := s.lastRenderedConfig
	auxFiles := s.lastAuxiliaryFiles
	endpoints := s.currentEndpoints
	// Cache validated config immediately to prevent race condition
	s.lastValidatedConfig = config
	s.lastValidatedAux = auxFiles
	s.lastParsedConfig = event.ParsedConfig // Cache pre-parsed config for sync optimization
	s.lastCorrelationID = correlationID
	s.lastCoalescible = event.Coalescible()
	s.hasValidConfig = true
	s.mu.Unlock()

	if config == "" {
		s.logger.Error("no rendered config available for deployment")
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("no endpoints available yet, config cached for later deployment")
		return
	}

	// Use pre-computed content checksum from pipeline (propagated via TemplateRenderedEvent)
	s.mu.RLock()
	configHash := s.lastContentChecksum
	s.mu.RUnlock()
	podSetHash := computePodSetHash(endpoints)

	// Drift prevention deployments must ALWAYS execute (bypass cache)
	isDriftPrevention := event.TriggerReason == events.TriggerReasonDriftPrevention

	// Check if deployment can be skipped (config unchanged for same pod set)
	s.mu.RLock()
	canSkip := !isDriftPrevention &&
		configHash == s.lastDeployedConfigHash &&
		podSetHash == s.lastDeployedPodSetHash &&
		!s.lastDeployedTime.IsZero()
	s.mu.RUnlock()

	if canSkip {
		s.logger.Debug("skipping deployment - config unchanged since last deploy",
			"config_hash", configHash[:8],
			"pod_set_hash", podSetHash[:8],
			"last_deployed", s.lastDeployedTime.Format(time.RFC3339))
		return
	}

	// Get parsed config for sync optimization
	s.mu.RLock()
	parsedConfig := s.lastParsedConfig
	s.mu.RUnlock()

	// Schedule deployment to current endpoints (or queue if deployment in progress)
	// Propagate coalescibility from validation event through the deployment pipeline
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, endpoints, "config_validation", correlationID, event.Coalescible())
}

// handlePodsDiscovered handles HAProxy pod discovery/changes with coalescing.
//
// This schedules deployment of the last validated configuration to the new set of endpoints.
// This is called when HAProxy pods are added/removed/updated without config changes.
//
// After processing the initial event, it drains the event channel for any additional
// coalescible HAProxyPodsDiscoveredEvents and processes only the latest one. This prevents
// queue buildup during high-frequency pod churn (scaling events, rolling updates).
func (s *DeploymentScheduler) handlePodsDiscovered(ctx context.Context, event *events.HAProxyPodsDiscoveredEvent) {
	s.performPodsDiscovered(ctx, event)

	// After processing completes, drain for latest coalescible event
	for {
		latest, supersededCount := coalesce.DrainLatest[*events.HAProxyPodsDiscoveredEvent](
			s.eventChan,
			func(e busevents.Event) { s.handleEvent(ctx, e) },
		)
		if latest == nil {
			return
		}
		if supersededCount > 0 {
			s.logger.Debug("Coalesced HAProxy pods discovered events",
				"superseded_count", supersededCount)
		}
		s.performPodsDiscovered(ctx, latest)
	}
}

// performPodsDiscovered executes the actual pod discovery handling logic.
func (s *DeploymentScheduler) performPodsDiscovered(ctx context.Context, event *events.HAProxyPodsDiscoveredEvent) {
	s.mu.Lock()
	s.currentEndpoints = event.Endpoints
	endpointCount := len(event.Endpoints)
	config := s.lastValidatedConfig
	auxFiles := s.lastValidatedAux
	parsedConfig := s.lastParsedConfig
	correlationID := s.lastCorrelationID
	coalescible := s.lastCoalescible
	hasValidConfig := s.hasValidConfig
	s.mu.Unlock()

	s.logger.Debug("HAProxy pods discovered",
		"count", endpointCount)

	if !hasValidConfig {
		s.logger.Debug("no validated config available yet, skipping deployment")
		return
	}

	if endpointCount == 0 {
		s.logger.Debug("no endpoints available, skipping deployment")
		return
	}

	// Schedule deployment of last validated config to new endpoints (or queue if in progress)
	// Use the correlation ID and coalescibility from the last validation for traceability
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, event.Endpoints, "pod_discovery", correlationID, coalescible)
}

// handleValidationFailed handles validation failure events.
//
// When validation fails for any reason, we deploy the cached last known good config
// as a fallback. This ensures HAProxy pods stay in sync with a valid configuration
// even when the latest config is invalid (e.g., due to template syntax errors,
// HTTP fetch failures, or invalid HAProxy configuration).
//
// This is critical for resilience: the controller must NOT accept a broken config
// and must continue using the last known good config until a valid one is provided.
func (s *DeploymentScheduler) handleValidationFailed(ctx context.Context, event *events.ValidationFailedEvent) {
	correlationID := event.CorrelationID()

	s.mu.RLock()
	config := s.lastValidatedConfig
	auxFiles := s.lastValidatedAux
	parsedConfig := s.lastParsedConfig
	endpoints := s.currentEndpoints
	hasValidConfig := s.hasValidConfig
	s.mu.RUnlock()

	s.logger.Warn("validation failed, deploying cached config as fallback",
		"trigger_reason", event.TriggerReason,
		"errors", event.Errors,
		"correlation_id", correlationID)

	if !hasValidConfig {
		s.logger.Error("validation fallback failed: no cached config available",
			"correlation_id", correlationID)
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("validation fallback skipped: no endpoints available",
			"correlation_id", correlationID)
		return
	}

	// Schedule fallback deployment with last known good config
	// Fallback deployments are NOT coalescible - they must execute to ensure consistency
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, endpoints, "validation_fallback", correlationID, false)
}

// handleDeploymentCompleted handles deployment completion events.
//
// This marks the deployment as complete, updates the deployment end time,
// caches the deployed config hash for optimization, and processes any
// pending deployment via scheduleOrQueue.
func (s *DeploymentScheduler) handleDeploymentCompleted(_ *events.DeploymentCompletedEvent) {
	// Cache the deployed content checksum for future comparison (skip unchanged deployments)
	s.mu.Lock()
	s.lastDeployedConfigHash = s.lastContentChecksum
	s.lastDeployedPodSetHash = computePodSetHash(s.currentEndpoints)
	s.lastDeployedTime = time.Now()
	s.mu.Unlock()

	s.schedulerMutex.Lock()

	// Transition to idle and record completion time
	s.state.phase = phaseIdle
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.lastDeploymentEndTime = time.Now()

	// Check if there's a pending deployment to process
	pending := s.state.pending
	if pending != nil {
		s.state.pending = nil
		s.schedulerMutex.Unlock()

		s.logger.Debug("Deployment completed, processing queued deployment",
			"pending_reason", pending.reason,
			"pending_endpoint_count", len(pending.endpoints),
			"correlation_id", pending.correlationID)

		// Use scheduleOrQueue for proper mutex management and goroutine control
		// This ensures only one scheduling goroutine runs at a time
		s.scheduleOrQueue(s.ctx, pending.config, pending.auxFiles, pending.parsedConfig, pending.endpoints, pending.reason, pending.correlationID, pending.coalescible)
		return
	}

	s.schedulerMutex.Unlock()
}

// scheduleOrQueue either queues a deployment if one is in progress, or schedules it immediately.
//
// This prevents concurrent deployments which can cause version conflicts.
// Uses a "latest wins" pattern where pending deployments overwrite each other.
func (s *DeploymentScheduler) scheduleOrQueue(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	endpoints []dataplane.Endpoint,
	reason string,
	correlationID string,
	coalescible bool,
) {
	s.schedulerMutex.Lock()

	if s.state.phase != phaseIdle {
		// Deployment already in progress (rate limiting or deploying) - overwrite pending (latest wins)
		s.state.pending = &scheduledDeployment{
			config:        config,
			auxFiles:      auxFiles,
			parsedConfig:  parsedConfig,
			endpoints:     endpoints,
			reason:        reason,
			correlationID: correlationID,
			coalescible:   coalescible,
		}
		s.schedulerMutex.Unlock()
		s.logger.Debug("Deployment in progress, queued for later",
			"reason", reason,
			"endpoint_count", len(endpoints),
			"phase", s.state.phase.String(),
			"correlation_id", correlationID)
		return
	}

	// Transition to rate limiting phase, track start time and correlation ID
	s.state.phase = phaseRateLimiting
	s.state.deploymentStartTime = time.Now()
	s.state.activeCorrelationID = correlationID
	s.schedulerMutex.Unlock()

	// Schedule deployment asynchronously to avoid blocking event loop
	// This allows new events to be received and queued while we handle rate limiting
	go s.scheduleWithRateLimitUnlocked(ctx, config, auxFiles, parsedConfig, endpoints, reason, correlationID, coalescible)
}

// scheduleWithRateLimitUnlocked schedules a deployment, enforcing rate limiting.
//
// This method should only be called from scheduleOrQueue() which manages the scheduler mutex.
// It enforces the minimum deployment interval and recursively processes pending deployments.
func (s *DeploymentScheduler) scheduleWithRateLimitUnlocked(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	endpoints []dataplane.Endpoint,
	reason string,
	correlationID string,
	coalescible bool,
) {
	// Get last deployment time for rate limiting
	s.schedulerMutex.Lock()
	lastDeploymentEnd := s.state.lastDeploymentEndTime
	s.schedulerMutex.Unlock()

	// Enforce minimum deployment interval (rate limiting)
	// Only enforce if we have a previous deployment time (not zero)
	if !lastDeploymentEnd.IsZero() && s.minDeploymentInterval > 0 {
		timeSinceLastDeployment := time.Since(lastDeploymentEnd)
		if timeSinceLastDeployment < s.minDeploymentInterval {
			sleepDuration := s.minDeploymentInterval - timeSinceLastDeployment
			s.logger.Debug("Enforcing minimum deployment interval",
				"sleep_duration_ms", sleepDuration.Milliseconds(),
				"min_interval_ms", s.minDeploymentInterval.Milliseconds(),
				"time_since_last_ms", timeSinceLastDeployment.Milliseconds())

			// Sleep with context awareness
			timer := time.NewTimer(sleepDuration)
			select {
			case <-timer.C:
				// Sleep completed
			case <-ctx.Done():
				timer.Stop()
				s.schedulerMutex.Lock()
				s.state.phase = phaseIdle
				s.state.activeCorrelationID = ""
				s.schedulerMutex.Unlock()
				s.logger.Info("Deployment scheduling cancelled during rate limit sleep",
					"reason", reason)
				return
			}
		}
	}

	// Transition from rate limiting to deploying
	s.schedulerMutex.Lock()
	s.state.phase = phaseDeploying
	s.state.deploymentStartTime = time.Now() // Reset start time to when actual deployment begins
	s.schedulerMutex.Unlock()

	// Get runtime config metadata and content checksum under lock
	s.mu.RLock()
	runtimeConfigName := s.runtimeConfigName
	runtimeConfigNamespace := s.runtimeConfigNamespace
	templateConfigName := s.templateConfigName
	templateConfigNamespace := s.templateConfigNamespace
	contentChecksum := s.lastContentChecksum
	s.mu.RUnlock()

	// Compute runtime config name if not set via ConfigPublishedEvent.
	// This uses the deterministic naming convention to avoid waiting for the
	// K8s API call that publishes the HAProxyCfg resource (fixes race condition).
	if runtimeConfigName == "" && templateConfigName != "" {
		runtimeConfigName = configpublisher.GenerateRuntimeConfigName(templateConfigName)
		runtimeConfigNamespace = templateConfigNamespace
		s.logger.Debug("computed runtime config name from template config",
			"runtime_config_name", runtimeConfigName,
			"template_config_name", templateConfigName)
	}

	// Publish DeploymentScheduledEvent with correlation
	s.logger.Debug("Scheduling deployment",
		"reason", reason,
		"endpoint_count", len(endpoints),
		"config_bytes", len(config),
		"has_parsed_config", parsedConfig != nil,
		"correlation_id", correlationID)

	s.eventBus.Publish(events.NewDeploymentScheduledEvent(
		config, auxFiles, parsedConfig, endpoints, runtimeConfigName, runtimeConfigNamespace, reason, contentChecksum, coalescible,
		events.WithCorrelation(correlationID, correlationID),
	))

	// Note: We wait for DeploymentCompletedEvent to update lastDeploymentEndTime
	// This is handled in handleDeploymentCompleted()

	// Check for pending deployment and process it
	s.schedulerMutex.Lock()
	pending := s.state.pending
	s.state.pending = nil

	if pending == nil {
		// No pending work - wait for DeploymentCompletedEvent to transition to idle
		// (phase stays phaseDeploying until handleDeploymentCompleted)
		s.schedulerMutex.Unlock()
		return
	}

	// Pending deployment exists - transition back to rate limiting for next cycle
	s.state.phase = phaseRateLimiting
	s.schedulerMutex.Unlock()

	// Check context before processing pending
	select {
	case <-ctx.Done():
		s.schedulerMutex.Lock()
		s.state.phase = phaseIdle
		s.state.activeCorrelationID = ""
		s.schedulerMutex.Unlock()
		s.logger.Info("Deployment scheduling cancelled, discarding pending deployment",
			"reason", pending.reason)
		return
	default:
	}

	s.logger.Debug("Processing queued deployment",
		"reason", pending.reason,
		"endpoint_count", len(pending.endpoints),
		"correlation_id", pending.correlationID)

	// Recursive: schedule pending (we're still in non-idle state)
	s.scheduleWithRateLimitUnlocked(ctx, pending.config, pending.auxFiles, pending.parsedConfig,
		pending.endpoints, pending.reason, pending.correlationID, pending.coalescible)
}

// handleConfigPublished handles ConfigPublishedEvent by caching runtime config metadata.
//
// This caches the runtime config name and namespace for use when publishing
// ConfigAppliedToPodEvent after successful deployments.
func (s *DeploymentScheduler) handleConfigPublished(event *events.ConfigPublishedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.runtimeConfigName = event.RuntimeConfigName
	s.runtimeConfigNamespace = event.RuntimeConfigNamespace

	s.logger.Debug("cached runtime config metadata for deployment events",
		"runtime_config_name", event.RuntimeConfigName,
		"runtime_config_namespace", event.RuntimeConfigNamespace)
}

// handleLostLeadership handles LostLeadershipEvent by clearing deployment state.
//
// When a replica loses leadership, leader-only components (including this scheduler)
// are stopped via context cancellation. However, we defensively clear state to prevent
// potential deadlocks if there's a race condition during shutdown.
//
// This prevents scenarios where:
//   - phase is stuck at non-idle, blocking future deployments
//   - pending contains stale deployments that shouldn't execute
func (s *DeploymentScheduler) handleLostLeadership(_ *events.LostLeadershipEvent) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()

	if s.state.phase != phaseIdle || s.state.pending != nil {
		s.logger.Info("Lost leadership, clearing deployment state",
			"phase", s.state.phase.String(),
			"has_pending", s.state.pending != nil)
	}

	// Transition to idle and clear all transient state
	s.state.phase = phaseIdle
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.pending = nil

	// Note: state.lastDeploymentEndTime is NOT cleared - this historical data is safe to keep
	// and helps prevent rapid deployments if leadership is quickly reacquired

	// Clear deployment cache - new leader should verify config state
	s.mu.Lock()
	s.lastDeployedConfigHash = ""
	s.lastDeployedPodSetHash = ""
	s.lastDeployedTime = time.Time{}
	s.mu.Unlock()
}

// checkDeploymentTimeout checks if the current deployment has exceeded the timeout.
//
// If a deployment is in progress and has exceeded the configured timeout, this method
// publishes a cancellation event to stop the running deployment, resets the stuck state,
// and triggers a new reconciliation. This is a safety net for race conditions during
// leadership transitions where DeploymentCompletedEvent may be lost.
func (s *DeploymentScheduler) checkDeploymentTimeout(ctx context.Context) {
	s.schedulerMutex.Lock()
	// Only check timeout in deploying phase - rate limiting has its own timeout via context
	if s.state.phase != phaseDeploying {
		s.schedulerMutex.Unlock()
		return
	}
	startTime := s.state.deploymentStartTime
	activeCorrelationID := s.state.activeCorrelationID
	pending := s.state.pending
	s.schedulerMutex.Unlock()

	// Skip if deployment hasn't started yet (startTime is zero)
	if startTime.IsZero() {
		return
	}

	elapsed := time.Since(startTime)
	if elapsed <= s.deploymentTimeout {
		return
	}

	s.logger.Warn("Deployment timeout - cancelling and resetting stuck state",
		"duration_ms", elapsed.Milliseconds(),
		"timeout_ms", s.deploymentTimeout.Milliseconds(),
		"correlation_id", activeCorrelationID)

	// Publish cancellation event to stop the running deployment
	// This must be done BEFORE resetting state so the deployer can match the correlation ID
	if activeCorrelationID != "" {
		s.eventBus.Publish(events.NewDeploymentCancelRequestEvent(
			"deployment_timeout",
			events.WithCorrelation(activeCorrelationID, activeCorrelationID),
		))
	}

	s.schedulerMutex.Lock()
	s.state.phase = phaseIdle
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.pending = nil
	s.schedulerMutex.Unlock()

	// If there was a pending deployment, process it now
	if pending != nil {
		s.logger.Info("Processing pending deployment after timeout recovery",
			"reason", pending.reason+"_timeout_retry",
			"correlation_id", pending.correlationID)
		s.scheduleOrQueue(ctx, pending.config, pending.auxFiles, pending.parsedConfig,
			pending.endpoints, pending.reason+"_timeout_retry", pending.correlationID, pending.coalescible)
	}

	// Trigger a new reconciliation to recover from the stuck state
	// Timeout recovery is NOT coalescible - it must be processed to recover from stuck state
	s.eventBus.Publish(events.NewReconciliationTriggeredEvent("deployment_timeout_recovery", false, events.WithNewCorrelation()))
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (s *DeploymentScheduler) HealthCheck() error {
	return s.healthTracker.Check()
}

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

package deployer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
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
	ticker := time.NewTicker(timeouts.TickerPollInterval)
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

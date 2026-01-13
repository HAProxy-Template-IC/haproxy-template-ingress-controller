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

// Package reconciler implements the Reconciler component that debounces resource
// changes and triggers reconciliation events.
//
// The Reconciler is a key component in Stage 5 of the controller startup sequence.
// It subscribes to resource change events, applies debouncing to batch rapid changes,
// and publishes reconciliation trigger events when the system reaches a quiet state.
package reconciler

import (
	"context"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// EventBufferSize is the size of the event subscription buffer.
	// Size 100: Medium-volume component that receives resource change events from
	// multiple watchers (Ingress, HTTPRoute, Service, Endpoints, Secrets, ConfigMaps).
	// Higher than deployer to handle bursts when many resources change simultaneously.
	EventBufferSize = 100
)

// DefaultDebounceInterval is re-exported from types for backward compatibility.
// New code should use types.DefaultDebounceInterval directly.
var DefaultDebounceInterval = types.DefaultDebounceInterval

// ComponentName is the unique identifier for this component.
const ComponentName = "reconciler"

// Reconciler implements the reconciliation debouncer component.
//
// It subscribes to resource change events and index synchronization events,
// applies debouncing logic to prevent excessive reconciliations, and triggers
// reconciliation when appropriate.
//
// Debouncing behavior (leading-edge with refractory period):
//   - First change after refractory: Trigger immediately (0ms delay)
//   - Changes during refractory: Queued (timer NOT reset, fires at refractory end)
//   - Index synchronized: Trigger immediately (initial reconciliation after all resources synced)
//
// This guarantees:
//   - Minimum interval: At least debounceInterval between any two triggers
//   - Maximum latency: Every change synced within debounceInterval
//
// The component publishes ReconciliationTriggeredEvent to signal the Executor
// to begin a reconciliation cycle.
type Reconciler struct {
	eventBus          *busevents.EventBus
	eventChan         <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger            *slog.Logger
	debounceInterval  time.Duration
	debounceTimer     *time.Timer
	pendingTrigger    bool
	lastTriggerReason string
	lastTriggerTime   time.Time // Tracks when we last triggered for refractory period

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker
}

// Config configures the Reconciler component.
type Config struct {
	// DebounceInterval is the minimum time between reconciliation triggers (refractory period).
	// The first change triggers immediately, subsequent changes within this interval are batched.
	// If not set, DefaultDebounceInterval is used.
	DebounceInterval time.Duration
}

// New creates a new Reconciler component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing triggers
//   - logger: Structured logger for component logging
//   - config: Optional configuration (nil for defaults)
//
// Returns:
//   - A new Reconciler instance ready to be started
func New(eventBus *busevents.EventBus, logger *slog.Logger, config *Config) *Reconciler {
	debounceInterval := DefaultDebounceInterval
	if config != nil && config.DebounceInterval > 0 {
		debounceInterval = config.DebounceInterval
	}

	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeResourceIndexUpdated,
		events.EventTypeIndexSynchronized,
		events.EventTypeHTTPResourceUpdated,
		events.EventTypeHTTPResourceAccepted,
		events.EventTypeDriftPreventionTriggered,
		events.EventTypeBecameLeader,
	)

	return &Reconciler{
		eventBus:         eventBus,
		eventChan:        eventChan,
		logger:           logger,
		debounceInterval: debounceInterval,
		// Timer is created on first use to avoid firing immediately
		debounceTimer:     nil,
		pendingTrigger:    false,
		lastTriggerReason: "",
		healthTracker:     lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (r *Reconciler) Name() string {
	return ComponentName
}

// Start begins the reconciler's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// The component is already subscribed to the EventBus (subscription happens in New()),
// so this method only processes events:
//   - ResourceIndexUpdatedEvent: Leading-edge trigger or queue for refractory timer
//   - IndexSynchronizedEvent: Triggers initial reconciliation when all resources synced
//   - Refractory timer expiration: Publishes ReconciliationTriggeredEvent if pending changes
//
// The component runs until the context is cancelled, at which point it
// performs cleanup and returns.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (r *Reconciler) Start(ctx context.Context) error {
	r.logger.Debug("reconciler starting",
		"debounce_interval", r.debounceInterval)

	for {
		select {
		case event := <-r.eventChan:
			r.handleEvent(event)

		case <-r.getDebounceTimerChan():
			// Timer fired - clear reference so new timer can be created if needed
			r.debounceTimer = nil

			// Only trigger if there are pending changes
			if r.pendingTrigger {
				r.triggerReconciliation("debounce_timer")
			}

		case <-ctx.Done():
			r.logger.Info("Reconciler shutting down", "reason", ctx.Err())
			r.cleanup()
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (r *Reconciler) handleEvent(event busevents.Event) {
	// Track processing for health check stall detection
	r.healthTracker.StartProcessing()
	defer r.healthTracker.EndProcessing()

	switch e := event.(type) {
	case *events.ResourceIndexUpdatedEvent:
		r.handleResourceChange(e)

	case *events.IndexSynchronizedEvent:
		r.handleIndexSynchronized(e)

	case *events.HTTPResourceUpdatedEvent:
		r.handleHTTPResourceChange(e)

	case *events.HTTPResourceAcceptedEvent:
		r.handleHTTPResourceAccepted(e)

	case *events.DriftPreventionTriggeredEvent:
		r.handleDriftPrevention(e)

	case *events.BecameLeaderEvent:
		r.handleBecameLeader(e)
	}
}

// handleResourceChange processes resource index update events.
//
// Uses leading-edge debouncing: the first change triggers immediately if outside
// the refractory period, subsequent changes are batched until refractory expires.
// This guarantees both minimum interval between triggers and maximum latency for changes.
//
// HAProxy pods are filtered out since they are deployment targets, not configuration sources.
// Changes to HAProxy pods trigger deployment-only reconciliation via the Deployer component.
func (r *Reconciler) handleResourceChange(event *events.ResourceIndexUpdatedEvent) {
	// Skip initial sync events - we don't want to trigger reconciliation
	// until the initial sync is complete
	if event.ChangeStats.IsInitialSync {
		r.logger.Debug("Skipping initial sync event",
			"resource_type", event.ResourceTypeName,
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted)
		return
	}

	// Skip HAProxy pod changes - they are deployment targets, not configuration sources
	// Pod changes trigger deployment via HAProxyPodsDiscoveredEvent → Deployer component
	if event.ResourceTypeName == "haproxy-pods" {
		r.logger.Debug("Skipping HAProxy pod change (deployment target, not config source)",
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted)
		return
	}

	now := time.Now()
	timeSinceLastTrigger := now.Sub(r.lastTriggerTime)

	if timeSinceLastTrigger >= r.debounceInterval {
		// Outside refractory period - trigger immediately (leading edge)
		r.logger.Debug("Resource change detected, triggering immediately (outside refractory)",
			"resource_type", event.ResourceTypeName,
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted,
			"time_since_last_trigger", timeSinceLastTrigger)
		r.triggerReconciliation("resource_change")
	} else {
		// Inside refractory period - queue for later, DO NOT reset timer
		r.logger.Debug("Resource change detected, queuing (inside refractory)",
			"resource_type", event.ResourceTypeName,
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted,
			"remaining_refractory", r.debounceInterval-timeSinceLastTrigger,
			"debounce_interval", r.debounceInterval)
		r.pendingTrigger = true
		r.lastTriggerReason = "resource_change"
		// Only start timer if not already running (key difference from old behavior!)
		r.ensureRefractoryTimer(now)
	}
}

// handleIndexSynchronized processes index synchronized events.
//
// When all resource watchers have completed their initial sync, this triggers
// the initial reconciliation. This ensures the first render happens only after
// all resources are indexed, providing a complete view of the cluster state.
func (r *Reconciler) handleIndexSynchronized(event *events.IndexSynchronizedEvent) {
	r.logger.Info("All indices synchronized, triggering initial reconciliation",
		"resource_counts", event.ResourceCounts)

	// Stop any pending debounce timer - initial sync takes priority
	r.stopDebounceTimer()

	// Trigger reconciliation immediately
	r.triggerReconciliation("index_synchronized")
}

// handleHTTPResourceChange processes HTTP resource update events.
//
// HTTP resource changes use the same leading-edge debouncing as other resource changes.
// When external HTTP content changes (e.g., IP blocklists, API responses),
// this triggers a re-render to incorporate the new content.
func (r *Reconciler) handleHTTPResourceChange(event *events.HTTPResourceUpdatedEvent) {
	now := time.Now()
	timeSinceLastTrigger := now.Sub(r.lastTriggerTime)

	if timeSinceLastTrigger >= r.debounceInterval {
		// Outside refractory period - trigger immediately (leading edge)
		r.logger.Debug("HTTP resource change detected, triggering immediately (outside refractory)",
			"url", event.URL,
			"content_size", event.ContentSize,
			"time_since_last_trigger", timeSinceLastTrigger)
		r.triggerReconciliation("http_resource_change")
	} else {
		// Inside refractory period - queue for later
		r.logger.Debug("HTTP resource change detected, queuing (inside refractory)",
			"url", event.URL,
			"content_size", event.ContentSize,
			"remaining_refractory", r.debounceInterval-timeSinceLastTrigger,
			"debounce_interval", r.debounceInterval)
		r.pendingTrigger = true
		r.lastTriggerReason = "http_resource_change"
		r.ensureRefractoryTimer(now)
	}
}

// handleHTTPResourceAccepted processes HTTP resource accepted events.
//
// When HTTP content is promoted from pending to accepted (after validation succeeds),
// we need to trigger a new reconciliation to re-render the production configuration
// with the new accepted content. Without this, the production config would stay
// with the old content until the next external trigger.
func (r *Reconciler) handleHTTPResourceAccepted(event *events.HTTPResourceAcceptedEvent) {
	r.logger.Info("HTTP resource accepted, triggering immediate reconciliation",
		"url", event.URL,
		"content_size", event.ContentSize)

	// Stop pending debounce timer - accepted content should be deployed immediately
	r.stopDebounceTimer()

	// Trigger reconciliation immediately
	r.triggerReconciliation("http_resource_accepted")
}

// handleDriftPrevention processes drift prevention triggered events.
//
// Drift prevention triggers immediate full reconciliation (render → validate → deploy).
// This ensures HTTP store entries get their LastAccessTime updated during template rendering,
// preventing premature eviction. If validation fails, the DeploymentScheduler will fall back
// to deploying the cached last known good config.
func (r *Reconciler) handleDriftPrevention(_ *events.DriftPreventionTriggeredEvent) {
	// Stop pending debounce timer - drift prevention takes priority
	r.stopDebounceTimer()

	// Trigger reconciliation immediately with drift_prevention reason
	// The TriggerReason will be propagated through the event chain and used by
	// DeploymentScheduler to deploy cached config if validation fails
	r.triggerReconciliation(events.TriggerReasonDriftPrevention)
}

// handleBecameLeader triggers immediate reconciliation when leadership is acquired.
//
// This ensures leader-only components (renderer, drift monitor) receive fresh state.
// The renderer is leader-only and starts when we become leader, so this reconciliation
// provides the initial config rendering for the new leader.
func (r *Reconciler) handleBecameLeader(_ *events.BecameLeaderEvent) {
	r.logger.Info("Became leader, triggering immediate reconciliation")

	// Stop pending debounce timer - leader transition takes priority
	r.stopDebounceTimer()

	// Trigger reconciliation immediately
	r.triggerReconciliation("became_leader")
}

// ensureRefractoryTimer ensures a timer is running for the remainder of the refractory period.
// Unlike the old resetDebounceTimer, this does NOT reset an existing timer - it only
// starts one if none exists. This is critical for the leading-edge debounce behavior.
func (r *Reconciler) ensureRefractoryTimer(now time.Time) {
	if r.debounceTimer != nil {
		// Timer already running - do not reset (critical for leading-edge behavior)
		return
	}

	// Calculate remaining time until refractory period ends
	remaining := r.debounceInterval - now.Sub(r.lastTriggerTime)
	if remaining <= 0 {
		// Refractory already expired - should not happen, but handle gracefully
		remaining = r.debounceInterval
	}

	r.debounceTimer = time.NewTimer(remaining)
}

// stopDebounceTimer stops the debounce timer if it's running and clears the reference.
func (r *Reconciler) stopDebounceTimer() {
	if r.debounceTimer != nil {
		if !r.debounceTimer.Stop() {
			// Timer already fired, drain the channel
			select {
			case <-r.debounceTimer.C:
			default:
			}
		}
		r.debounceTimer = nil
	}
	r.pendingTrigger = false
}

// getDebounceTimerChan returns the debounce timer's channel or a nil channel
// if the timer hasn't been created yet.
//
// This allows the select statement to work correctly - a nil channel blocks forever,
// which is the desired behavior when there's no active debounce timer.
func (r *Reconciler) getDebounceTimerChan() <-chan time.Time {
	if r.debounceTimer == nil {
		return nil
	}
	return r.debounceTimer.C
}

// triggerReconciliation publishes a ReconciliationTriggeredEvent with a new correlation ID.
//
// The correlation ID is generated here and will be propagated through the entire
// reconciliation pipeline (Renderer → Validator → Scheduler → Deployer) enabling
// end-to-end tracing of all events in a single reconciliation cycle.
//
// This also updates lastTriggerTime to start a new refractory period.
func (r *Reconciler) triggerReconciliation(reason string) {
	// Update last trigger time for refractory period tracking
	r.lastTriggerTime = time.Now()

	// Create event with new correlation ID to trace this reconciliation cycle
	event := events.NewReconciliationTriggeredEvent(reason, events.WithNewCorrelation())

	r.logger.Debug("Triggering reconciliation",
		"reason", reason,
		"correlation_id", event.CorrelationID())

	r.eventBus.Publish(event)
	r.pendingTrigger = false
}

// cleanup performs cleanup when the component is shutting down.
func (r *Reconciler) cleanup() {
	r.stopDebounceTimer()

	// If there was a pending trigger when we shut down, log it
	if r.pendingTrigger {
		r.logger.Debug("Reconciler shutting down with pending trigger",
			"last_reason", r.lastTriggerReason)
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (r *Reconciler) HealthCheck() error {
	return r.healthTracker.Check()
}

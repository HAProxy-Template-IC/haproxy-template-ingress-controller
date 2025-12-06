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

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
)

const (
	// DefaultDebounceInterval is the default time to wait after the last resource
	// change before triggering reconciliation.
	DefaultDebounceInterval = 500 * time.Millisecond

	// EventBufferSize is the size of the event subscription buffer.
	// Size 100: Medium-volume component that receives resource change events from
	// multiple watchers (Ingress, HTTPRoute, Service, Endpoints, Secrets, ConfigMaps).
	// Higher than deployer to handle bursts when many resources change simultaneously.
	EventBufferSize = 100
)

// ComponentName is the unique identifier for this component.
const ComponentName = "reconciler"

// Reconciler implements the reconciliation debouncer component.
//
// It subscribes to resource change events and configuration change events,
// applies debouncing logic to prevent excessive reconciliations, and triggers
// reconciliation when appropriate.
//
// Debouncing behavior:
//   - Resource changes: Wait for quiet period (debounce interval) before triggering
//   - Config changes: Trigger immediately (no debouncing)
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
}

// Config configures the Reconciler component.
type Config struct {
	// DebounceInterval is the time to wait after the last resource change
	// before triggering reconciliation. If not set, DefaultDebounceInterval is used.
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
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &Reconciler{
		eventBus:         eventBus,
		eventChan:        eventChan,
		logger:           logger,
		debounceInterval: debounceInterval,
		// Timer is created on first use to avoid firing immediately
		debounceTimer:     nil,
		pendingTrigger:    false,
		lastTriggerReason: "",
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
//   - ResourceIndexUpdatedEvent: Starts/resets debounce timer
//   - ConfigValidatedEvent: Triggers immediate reconciliation
//   - Debounce timer expiration: Publishes ReconciliationTriggeredEvent
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
	r.logger.Info("Reconciler starting",
		"debounce_interval", r.debounceInterval)

	for {
		select {
		case event := <-r.eventChan:
			r.handleEvent(event)

		case <-r.getDebounceTimerChan():
			// Debounce timer expired - trigger reconciliation
			r.triggerReconciliation("debounce_timer")

		case <-ctx.Done():
			r.logger.Info("Reconciler shutting down", "reason", ctx.Err())
			r.cleanup()
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (r *Reconciler) handleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ResourceIndexUpdatedEvent:
		r.handleResourceChange(e)

	case *events.ConfigValidatedEvent:
		r.handleConfigChange(e)

	case *events.HTTPResourceUpdatedEvent:
		r.handleHTTPResourceChange(e)

	case *events.HTTPResourceAcceptedEvent:
		r.handleHTTPResourceAccepted(e)
	}
}

// handleResourceChange processes resource index update events.
//
// Resource changes are debounced to batch rapid successive changes.
// The debounce timer is reset on each change, and reconciliation is
// only triggered after a quiet period.
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

	r.logger.Debug("Resource change detected, resetting debounce timer",
		"resource_type", event.ResourceTypeName,
		"created", event.ChangeStats.Created,
		"modified", event.ChangeStats.Modified,
		"deleted", event.ChangeStats.Deleted,
		"debounce_interval", r.debounceInterval)

	r.pendingTrigger = true
	r.lastTriggerReason = "resource_change"
	r.resetDebounceTimer()
}

// handleConfigChange processes config validated events.
//
// Config changes trigger immediate reconciliation without debouncing.
// Any pending debounce timer is cancelled to prioritize config changes.
func (r *Reconciler) handleConfigChange(event *events.ConfigValidatedEvent) {
	r.logger.Debug("Config change detected, triggering immediate reconciliation",
		"config_version", event.Version)

	// Stop pending debounce timer - config changes take priority
	r.stopDebounceTimer()

	// Trigger reconciliation immediately
	r.triggerReconciliation("config_change")
}

// handleHTTPResourceChange processes HTTP resource update events.
//
// HTTP resource changes are debounced like other resource changes.
// When external HTTP content changes (e.g., IP blocklists, API responses),
// this triggers a re-render to incorporate the new content.
func (r *Reconciler) handleHTTPResourceChange(event *events.HTTPResourceUpdatedEvent) {
	r.logger.Debug("HTTP resource change detected, resetting debounce timer",
		"url", event.URL,
		"content_size", event.ContentSize,
		"debounce_interval", r.debounceInterval)

	r.pendingTrigger = true
	r.lastTriggerReason = "http_resource_change"
	r.resetDebounceTimer()
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

// resetDebounceTimer resets the debounce timer to the configured interval.
func (r *Reconciler) resetDebounceTimer() {
	if r.debounceTimer == nil {
		// Create timer on first use
		r.debounceTimer = time.NewTimer(r.debounceInterval)
	} else {
		// Stop and drain existing timer before resetting
		if !r.debounceTimer.Stop() {
			// Timer already fired, drain the channel
			select {
			case <-r.debounceTimer.C:
			default:
			}
		}
		r.debounceTimer.Reset(r.debounceInterval)
	}
}

// stopDebounceTimer stops the debounce timer if it's running.
func (r *Reconciler) stopDebounceTimer() {
	if r.debounceTimer != nil {
		if !r.debounceTimer.Stop() {
			// Timer already fired, drain the channel
			select {
			case <-r.debounceTimer.C:
			default:
			}
		}
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
func (r *Reconciler) triggerReconciliation(reason string) {
	// Create event with new correlation ID to trace this reconciliation cycle
	event := events.NewReconciliationTriggeredEvent(reason, events.WithNewCorrelation())

	r.logger.Info("Triggering reconciliation",
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

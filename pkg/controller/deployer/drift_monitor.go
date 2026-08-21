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
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timers"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// DriftMonitorComponentName is the unique identifier for the drift prevention monitor component.
	DriftMonitorComponentName = "drift-monitor"
)

// DriftPreventionMonitor triggers periodic reconciliation to prevent configuration
// drift in HAProxy pods.
//
// When no deployment has occurred within the configured interval, it publishes
// a DriftPreventionTriggeredEvent to trigger reconciliation. This helps detect
// and correct configuration drift caused by other Dataplane API clients or
// manual changes.
//
// This is a leader-only component that starts when leadership is acquired.
// Only the leader needs drift prevention since only the leader deploys.
// The Reconciler triggers fresh reconciliation on BecameLeaderEvent to provide current state.
//
// Event subscriptions:
//   - DeploymentCompletedEvent: Reset drift prevention timer
//   - LostLeadershipEvent: Stop drift timer when losing leadership
//
// The component publishes DriftPreventionTriggeredEvent when drift prevention
// is needed.
type DriftPreventionMonitor struct {
	*component.ReadySignal

	eventBus                *busevents.EventBus
	eventChan               <-chan busevents.Event // Subscribed in Start() for leader-only pattern
	logger                  *slog.Logger
	driftPreventionInterval time.Duration

	driftTimer         timers.SafeTimer
	lastDeploymentTime time.Time

	// Health check: stall detection for timer-based component
	healthTracker *lifecycle.HealthTracker
}

// NewDriftPreventionMonitor creates a new DriftPreventionMonitor component.
//
// As a leader-only component, subscription happens in Start() after leadership
// is acquired, not during construction.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing triggers
//   - logger: Structured logger for component logging
//   - driftPreventionInterval: Interval after which to trigger drift prevention deployment
//
// Returns:
//   - A new DriftPreventionMonitor instance ready to be started
func NewDriftPreventionMonitor(eventBus *busevents.EventBus, logger *slog.Logger, driftPreventionInterval time.Duration) *DriftPreventionMonitor {
	// Create health tracker with stall timeout = interval × 1.5 to allow for jitter
	// For default 60s interval, this gives 90s stall timeout
	healthTracker := lifecycle.NewActivityTracker(
		DriftMonitorComponentName,
		lifecycle.ActivityStallTimeout(driftPreventionInterval),
	)

	return &DriftPreventionMonitor{
		ReadySignal: component.NewReadySignal(),
		eventBus:    eventBus,
		// eventChan is subscribed in Start() for leader-only pattern
		logger:                  logger.With("component", DriftMonitorComponentName),
		driftPreventionInterval: driftPreventionInterval,
		healthTracker:           healthTracker,
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (m *DriftPreventionMonitor) Name() string {
	return DriftMonitorComponentName
}

// Start begins the drift prevention monitor's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// As a leader-only component, it subscribes to events when started (after leadership is acquired).
//
// Event handling:
//   - DeploymentCompletedEvent: Resets the drift prevention timer
//   - LostLeadershipEvent: Stops drift timer when losing leadership
//   - Drift timer expiration: Publishes DriftPreventionTriggeredEvent
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
func (m *DriftPreventionMonitor) Start(ctx context.Context) error {
	defer m.Rearm()
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// Use Critical buffer: fast timer-reset operations
	m.eventChan = m.eventBus.SubscribeTypesLeaderOnly(DriftMonitorComponentName, buffers.Critical,
		events.EventTypeDeploymentCompleted,
		events.EventTypeLostLeadership,
	)
	// Unsubscribe on loop exit: without this, every leadership re-acquisition on
	// the same instance would stack another subscription whose orphaned channel
	// fills up and logs critical drops forever (mirrors Coordinator).
	defer m.eventBus.UnsubscribeTyped(m.eventChan)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	m.MarkReady()

	m.logger.Debug("Drift monitor starting",
		"drift_prevention_interval_ms", m.driftPreventionInterval.Milliseconds())

	// Start initial drift prevention timer
	m.resetDriftTimer()

	for {
		select {
		case event := <-m.eventChan:
			m.handleEvent(event)

		case <-m.driftTimer.Chan():
			m.driftTimer.Fired()
			m.handleDriftTimerExpired()

		case <-ctx.Done():
			m.logger.Info("DriftPreventionMonitor shutting down", "reason", ctx.Err())
			m.driftTimer.Stop()
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (m *DriftPreventionMonitor) handleEvent(event busevents.Event) {
	switch event.(type) {
	case *events.DeploymentCompletedEvent:
		m.handleDeploymentCompleted()
	case *events.LostLeadershipEvent:
		m.handleLostLeadership()
	}
}

// handleDeploymentCompleted handles deployment completion events.
//
// This resets the drift prevention timer since a deployment has occurred.
func (m *DriftPreventionMonitor) handleDeploymentCompleted() {
	// Record activity for health check - handling events counts as activity
	m.healthTracker.RecordActivity()

	m.logger.Debug("Deployment completed, resetting drift prevention timer")
	m.resetDriftTimer()
}

// handleLostLeadership handles leadership loss events.
//
// This stops the drift timer since only the leader needs drift prevention.
// The new leader will start their own drift timer when they acquire leadership.
func (m *DriftPreventionMonitor) handleLostLeadership() {
	m.driftTimer.Stop()
	m.logger.Info("Lost leadership, stopping drift timer")
}

// handleDriftTimerExpired handles drift timer expiration.
//
// This publishes a DriftPreventionTriggeredEvent to trigger a deployment.
func (m *DriftPreventionMonitor) handleDriftTimerExpired() {
	// Record activity for health check stall detection
	m.healthTracker.RecordActivity()

	timeSinceLastDeployment := time.Since(m.lastDeploymentTime)

	m.logger.Debug("Drift prevention timer expired, triggering deployment",
		"time_since_last_deployment", timeSinceLastDeployment)

	// Publish drift prevention trigger event
	m.eventBus.Publish(events.NewDriftPreventionTriggeredEvent(timeSinceLastDeployment))

	// Reset timer for next interval
	// Note: The deployment will complete and trigger handleDeploymentCompleted
	// which will also reset the timer, but we reset here to ensure the timer
	// keeps running even if the deployment fails
	m.resetDriftTimer()
}

// resetDriftTimer resets the drift prevention timer.
//
// This should be called whenever a deployment completes or when the timer expires.
func (m *DriftPreventionMonitor) resetDriftTimer() {
	m.lastDeploymentTime = time.Now()
	m.driftTimer.Reset(m.driftPreventionInterval)

	m.logger.Debug("Drift prevention timer reset",
		"next_trigger_in_ms", m.driftPreventionInterval.Milliseconds())
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (no timer tick for > stallTimeout).
// For a timer-based component like DriftPreventionMonitor, a healthy state means
// the timer is firing at the expected interval.
func (m *DriftPreventionMonitor) HealthCheck() error {
	return m.healthTracker.Check()
}

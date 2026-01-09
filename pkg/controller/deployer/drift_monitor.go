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
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
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
	eventBus                *busevents.EventBus
	eventChan               <-chan busevents.Event // Subscribed in Start() for leader-only pattern
	logger                  *slog.Logger
	driftPreventionInterval time.Duration

	// Timer management protected by mutex
	mu                 sync.Mutex
	driftTimer         *time.Timer
	driftTimerChan     <-chan time.Time
	lastDeploymentTime time.Time
	timerActive        bool

	// Health check: stall detection for timer-based component
	healthTracker *lifecycle.HealthTracker

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}
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
		eventBus: eventBus,
		// eventChan is subscribed in Start() for leader-only pattern
		logger:                  logger.With("component", DriftMonitorComponentName),
		driftPreventionInterval: driftPreventionInterval,
		healthTracker:           healthTracker,
		subscriptionReady:       make(chan struct{}),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (m *DriftPreventionMonitor) Name() string {
	return DriftMonitorComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
func (m *DriftPreventionMonitor) SubscriptionReady() <-chan struct{} {
	return m.subscriptionReady
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
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// Use Critical buffer: fast timer-reset operations
	m.eventChan = m.eventBus.SubscribeTypesLeaderOnly(buffers.Critical(),
		events.EventTypeDeploymentCompleted,
		events.EventTypeLostLeadership,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(m.subscriptionReady)

	m.logger.Debug("drift monitor starting",
		"drift_prevention_interval_ms", m.driftPreventionInterval.Milliseconds())

	// Start initial drift prevention timer
	m.resetDriftTimer()

	for {
		select {
		case event := <-m.eventChan:
			m.handleEvent(event)

		case <-m.getDriftTimerChan():
			m.handleDriftTimerExpired()

		case <-ctx.Done():
			m.logger.Info("DriftPreventionMonitor shutting down", "reason", ctx.Err())
			m.stopDriftTimer()
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

	m.logger.Debug("deployment completed, resetting drift prevention timer")
	m.resetDriftTimer()
}

// handleLostLeadership handles leadership loss events.
//
// This stops the drift timer since only the leader needs drift prevention.
// The new leader will start their own drift timer when they acquire leadership.
func (m *DriftPreventionMonitor) handleLostLeadership() {
	m.logger.Info("lost leadership, stopping drift timer")
	m.stopDriftTimer()
}

// handleDriftTimerExpired handles drift timer expiration.
//
// This publishes a DriftPreventionTriggeredEvent to trigger a deployment.
func (m *DriftPreventionMonitor) handleDriftTimerExpired() {
	// Record activity for health check stall detection
	m.healthTracker.RecordActivity()

	m.mu.Lock()
	timeSinceLastDeployment := time.Since(m.lastDeploymentTime)
	m.mu.Unlock()

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
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastDeploymentTime = time.Now()

	// Stop existing timer if any
	if m.driftTimer != nil {
		m.driftTimer.Stop()
	}

	// Create new timer
	m.driftTimer = time.NewTimer(m.driftPreventionInterval)
	m.driftTimerChan = m.driftTimer.C
	m.timerActive = true

	m.logger.Debug("drift prevention timer reset",
		"next_trigger_in_ms", m.driftPreventionInterval.Milliseconds())
}

// stopDriftTimer stops the drift prevention timer.
//
// This should be called during shutdown.
func (m *DriftPreventionMonitor) stopDriftTimer() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.driftTimer != nil {
		m.driftTimer.Stop()
		m.timerActive = false
	}
}

// getDriftTimerChan returns the drift timer channel for select statements.
//
// Returns a closed channel if no timer is active to prevent blocking.
func (m *DriftPreventionMonitor) getDriftTimerChan() <-chan time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.timerActive && m.driftTimerChan != nil {
		return m.driftTimerChan
	}

	// Return closed channel to prevent blocking
	closed := make(chan time.Time)
	close(closed)
	return closed
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (no timer tick for > stallTimeout).
// For a timer-based component like DriftPreventionMonitor, a healthy state means
// the timer is firing at the expected interval.
func (m *DriftPreventionMonitor) HealthCheck() error {
	return m.healthTracker.Check()
}

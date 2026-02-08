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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

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

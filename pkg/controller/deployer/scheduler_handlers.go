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

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

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

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
	s.lastValidatedStatusPatches = event.StatusPatches

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
	statusPatches := s.lastValidatedStatusPatches
	configChecksum := s.lastContentChecksum
	// Cache validated config immediately to prevent race condition.
	// `lastValidatedContentChecksum` must be captured AT THE SAME POINT as
	// `lastValidatedConfig` — otherwise pod-discovery reads (which fall
	// through to this cache) can re-read a stale or newer
	// `lastContentChecksum` and the resulting deploy records the wrong
	// hash. See scheduler.go's scheduledDeployment.contentChecksum doc.
	s.lastValidatedConfig = config
	s.lastValidatedAux = auxFiles
	s.lastValidatedContentChecksum = configChecksum
	s.lastParsedConfig = event.ParsedConfig // Cache pre-parsed config for sync optimization
	s.lastCorrelationID = correlationID
	s.lastCoalescible = event.Coalescible()
	s.hasValidConfig = true
	parsedConfig := s.lastParsedConfig
	s.mu.Unlock()

	if config == "" {
		s.logger.Error("no rendered config available for deployment")
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("no endpoints available yet, config cached for later deployment")
		return
	}

	// Use the content checksum captured WITH the config that was just
	// validated — `lastValidatedContentChecksum`, set above under the same
	// lock as `lastValidatedConfig`. Reading `s.lastContentChecksum`
	// directly here would let a fresh reconcile (which mutates that
	// field in handleTemplateRendered) substitute a newer hash than the
	// config we're about to deploy actually carries.
	configHash := configChecksum
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
		// Publish a DeploymentSkippedEvent so consumers that need to know
		// "the data plane is converged on this config" can react. The
		// status-applier uses this to write the template's "deployed"
		// status variant on resources whose addition didn't change the
		// rendered HAProxy config (a status-only delta) — without this
		// signal the status would stay at the CRD default forever.
		s.eventBus.Publish(events.NewDeploymentSkippedEvent(
			len(endpoints),
			"config_unchanged",
			configHash,
			podSetHash,
			statusPatches,
			events.PropagateCorrelation(event),
		))
		return
	}

	// Schedule deployment to current endpoints (or queue if deployment in progress).
	// scheduleOrQueue classifies the render into a lane (runtime-raw vs structural)
	// against the last-dispatched config; the deploy loop applies it accordingly.
	// Propagate coalescibility from validation event through the deployment pipeline.
	//
	// `configHash` was captured above from `s.lastContentChecksum` at the same
	// point `config` was captured (line 112). Thread it through scheduleOrQueue
	// so the eventual deploy records THIS hash, not whatever
	// `s.lastContentChecksum` holds at deploy-time (which a later reconcile
	// will have overwritten under sustained parallel-test load).
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, endpoints, "config_validation", correlationID, statusPatches, event.Coalescible(), configHash)
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
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
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

	// Schedule deployment of last validated config to new endpoints (or queue if in progress).
	// Use the correlation ID, coalescibility, AND content checksum captured
	// when the config was validated — same lock window in
	// handleValidationCompleted — so the deploy records the hash that
	// matches the config it actually carries, not whatever
	// `lastContentChecksum` holds now (later renders' values).
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, event.Endpoints, "pod_discovery", correlationID, statusPatches, coalescible, contentChecksum)
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
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
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

	// Schedule fallback deployment with last known good config. Fallback
	// deployments are NOT coalescible — they must execute to ensure
	// consistency. The contentChecksum threaded here is the hash of the
	// last-validated config (NOT the failed-validation render), so the
	// deploy records the correct hash for what's actually being applied.
	s.scheduleOrQueue(ctx, config, auxFiles, parsedConfig, endpoints, "validation_fallback", correlationID, statusPatches, false, contentChecksum)
}

// handleDeploymentCompleted handles deployment completion events.
//
// This marks the deployment as complete, updates the deployment end time, and
// caches the deployed config hash for optimization. It does NOT re-schedule —
// it only clears deployInFlight and signals the loop, which picks up any pending
// deployment on its next cycle.
//
// The "deployed config hash" must come from event.ContentChecksum — the
// hash threaded through the DeploymentScheduledEvent that triggered this
// deployment — NOT from s.lastContentChecksum (the latest render's hash).
// A reconcile that lands between deployment-start and deployment-complete
// overwrites s.lastContentChecksum with the newer render, and using that
// value here mis-records THIS deployment's checksum as the newer one. The
// next reconcile that produces the newer hash then matches lastDeployedConfigHash
// and incorrectly skips deployment — the newer render's content (e.g. a
// fresh Ingress's redirect directive) never reaches HAProxy. See CI
// pipeline 2551671212 / TestIngressHaproxyRedirectTo for a real
// reproduction.
func (s *DeploymentScheduler) handleDeploymentCompleted(event *events.DeploymentCompletedEvent) {
	// Cache the deployed content checksum for future comparison (skip unchanged deployments).
	// Empty ContentChecksum means the zero-endpoint code path — nothing deployed, don't
	// touch the cache (otherwise we'd record "" as "last deployed" and force the next
	// real deployment to run, which is the safer side of the failure mode but is also
	// a needless deploy).
	//
	// Only cache the hash when the deployment FULLY succeeded (event.Failed == 0).
	// lastDeployedConfigHash is the "last SUCCESSFULLY deployed" hash that the
	// skip-unchanged gate compares against; recording a failed/partial deploy here
	// would make the gate refuse to re-push to the still-stale pods until the
	// config changes or the drift timer fires, delaying self-heal. A failure leaves
	// the cache at the last good hash so the next reconcile re-attempts immediately.
	s.mu.Lock()
	if event.ContentChecksum != "" && event.Failed == 0 {
		s.lastDeployedConfigHash = event.ContentChecksum
		s.lastDeployedPodSetHash = computePodSetHash(s.currentEndpoints)
		s.lastDeployedTime = time.Now()
	}
	s.mu.Unlock()

	s.schedulerMutex.Lock()

	// Mark the deploy complete and record the end time (the loop rate-limits the
	// next deploy from here). The deploy loop is blocked in awaitCompletion; the
	// signal below releases it and it picks up any pending deployment on its own
	// next cycle. We do NOT re-schedule here — that second scheduling path was
	// the source of concurrent rate-limit goroutines and the reload storm.
	s.state.deployInFlight = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()

	s.signalCompleted()
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
//   - deployInFlight is stuck true, blocking future deployments
//   - pending contains stale deployments that shouldn't execute
func (s *DeploymentScheduler) handleLostLeadership(_ *events.LostLeadershipEvent) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()

	if s.state.deployInFlight || s.state.pending != nil {
		s.logger.Info("Lost leadership, clearing deployment state",
			"deploy_in_flight", s.state.deployInFlight,
			"has_pending", s.state.pending != nil)
	}

	// Clear all transient deploy state. The deploy loop itself exits via ctx
	// cancellation on leadership loss; its channels are recreated on next Start.
	s.state.deployInFlight = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.pending = nil

	// Drop the dispatch diff baseline (the new leader hasn't dispatched, so its
	// first render must be classified structural — nil baseline — and deploy the
	// whole config) and close the bypass's persistent clients.
	s.lastDispatchedParsed = nil
	s.lastDispatchedConfig = ""
	s.runtimeBypass.Close()

	// Note: state.lastDeploymentEndTime is NOT cleared - this historical data is safe to keep
	// and helps prevent rapid deployments if leadership is quickly reacquired

	// Clear deployment cache - new leader should verify config state
	s.mu.Lock()
	s.lastDeployedConfigHash = ""
	s.lastDeployedPodSetHash = ""
	s.lastDeployedTime = time.Time{}
	s.mu.Unlock()
}

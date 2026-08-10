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

const (
	// maxDeployFailureRetries bounds the fast self-reschedule of a retryable
	// deploy failure. Beyond this many consecutive fast retries for the SAME
	// render (checksum), the scheduler stops the hot retry and hands off to the
	// 60s DriftPreventionMonitor backstop — so a permanently-wedged deploy (e.g. a
	// config HAProxy keeps rejecting) can't spin. A NEW render resets the budget.
	maxDeployFailureRetries = 5

	// maxFailureRetryBackoff caps the exponential fast-retry backoff. It matches
	// config.DefaultDriftPreventionInterval (60s): once a single retry would wait
	// as long as the drift backstop anyway, there's nothing to gain over letting
	// drift drive it.
	maxFailureRetryBackoff = 60 * time.Second
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
	s.lastRenderedEventID = event.EventID()
	s.lastValidatedStatusPatches = event.StatusPatches

	s.logger.Debug("Cached rendered config for deployment after validation",
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

	s.logger.Debug("Cached template config metadata for runtime config name computation",
		"template_config_name", tc.Name,
		"template_config_namespace", tc.Namespace)
}

// rendererMatchesValidation reports whether the cached render is the one this
// validation verdict describes.
//
// The Coordinator publishes TemplateRenderedEvent and ValidationCompletedEvent as
// two separate Publish calls and the bus drops per subscriber, so losing only the
// first leaves this cache holding the PREVIOUS render while the verdict describes
// the current one. Deploying that pair sends render N-1's bytes together with
// render N's ParsedConfig, so lane classification and the runtime-server diff are
// computed against a config that is not the one being pushed.
//
// The verdict's causation ID is the render event's ID (the Coordinator propagates
// it), which makes the pairing checkable. A mismatch discards the verdict instead
// of deploying it; the next reconcile or the drift backstop redeploys from a
// matching pair.
//
// An empty cached ID means no render has been received at all, and is a mismatch
// rather than a match — otherwise a verdict carrying no causation would pair with
// the empty cache and the guard would depend on how the verdict was constructed.
func (s *DeploymentScheduler) rendererMatchesValidation(event *events.ValidationCompletedEvent) bool {
	s.mu.RLock()
	renderedEventID := s.lastRenderedEventID
	s.mu.RUnlock()

	if renderedEventID != "" && event.CausationID() == renderedEventID {
		return true
	}

	s.logger.Warn("Discarding validation verdict for a render this scheduler never received",
		"validated_render", event.CausationID(),
		"cached_render", renderedEventID,
		"correlation_id", event.CorrelationID())
	return false
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
		s.logger.Warn("Validation warning", "warning", warning)
	}

	if !s.rendererMatchesValidation(event) {
		return
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
		s.logger.Error("No rendered config available for deployment")
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("No endpoints available yet, config cached for later deployment")
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
		s.logger.Debug("Skipping deployment - config unchanged since last deploy",
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

	// After processing completes, drain queued events: consecutive coalescible
	// pods-discovered events collapse to their latest; any other event type
	// flushes the held pods-discovered event first and is then handled in
	// arrival order, so neither side can starve the other.
	coalesce.DrainLatest(
		s.eventChan,
		func(e busevents.Event) { s.handleEvent(ctx, e) },
		func(latest *events.HAProxyPodsDiscoveredEvent, supersededCount int) {
			if supersededCount > 0 {
				s.logger.Debug("Coalesced HAProxy pods discovered events",
					"superseded_count", supersededCount)
			}
			s.performPodsDiscovered(ctx, latest)
		},
	)
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
		s.logger.Debug("No validated config available yet, skipping deployment")
		return
	}

	if endpointCount == 0 {
		s.logger.Debug("No endpoints available, skipping deployment")
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

	s.logger.Warn("Validation failed, deploying cached config as fallback",
		"trigger_reason", event.TriggerReason,
		"errors", event.Errors,
		"correlation_id", correlationID)

	if !hasValidConfig {
		s.logger.Error("Validation fallback failed: no cached config available",
			"correlation_id", correlationID)
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("Validation fallback skipped: no endpoints available",
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

	// A deploy that reported failures did NOT land on every pod: drop the
	// dispatch baseline so the next dispatch — including the armed fast
	// retry — classifies structural and full-syncs against the pods' real
	// state (see invalidateDispatchBaselineLocked for the issue #76 incident
	// this prevents).
	//
	// A deploy that landed everywhere is the moment the dispatched render
	// becomes the RUNNING one, so it is the only place the structural lane may
	// advance the activated baseline. The deploy loop is blocked in
	// awaitCompletion until this fires, so no newer dispatch can have
	// overwritten lastDispatchedConfig in between (#112).
	switch {
	case event.Total > 0 && event.Failed > 0:
		s.invalidateDispatchBaselineLocked()
	case event.Total > 0:
		s.lastActivatedConfig = s.lastDispatchedConfig
	}
	s.schedulerMutex.Unlock()

	s.signalCompleted()

	// Fast self-reschedule: a retryable per-pod failure (e.g. a transient DPA
	// transaction-version conflict) is otherwise only re-driven by the 60s drift
	// backstop, so the first retry can wait a full minute — a Gateway can sit
	// Programmed!=True that whole time. Re-drive it promptly through the SINGLE
	// coalescing path with bounded backoff. A fully-successful deploy cancels any
	// armed retry (the config converged). Both branches gate on Total>0 so the
	// zero-endpoint "nothing deployed" completion is a no-op.
	switch {
	case event.Total > 0 && event.Failed > 0:
		s.scheduleFailureRetry(event)
	case event.Total > 0 && event.Failed == 0:
		s.cancelFailureRetry()
	}
}

// scheduleFailureRetry arms (or re-arms) the single fast-retry timer after a
// deploy completed with failures. It re-dispatches the last-validated render
// through the existing scheduleOrQueue path after a bounded exponential backoff,
// so a transiently-failed deploy self-heals in seconds instead of waiting up to a
// full DriftPreventionInterval for the 60s backstop.
//
// The ONLY async primitive is one time.AfterFunc timer: its callback
// (rescheduleLastValidated) writes the single state.pending slot, so all timing
// stays in the one runDeployLoop and no second scheduling path (the reload-storm
// regression the handleDeploymentCompleted comment warns about) is created.
//
// Budget: a NEW render (different ContentChecksum) earns a fresh budget; the same
// failing render gets at most maxDeployFailureRetries fast retries, after which
// the 60s drift backstop takes over — no hot loop on a permanently-wedged deploy.
func (s *DeploymentScheduler) scheduleFailureRetry(event *events.DeploymentCompletedEvent) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()

	if event.ContentChecksum != s.lastFailedRetryChecksum {
		// A different render is failing now — start a fresh budget for it.
		s.lastFailedRetryChecksum = event.ContentChecksum
		s.deployFailureRetries = 0
	}

	if s.deployFailureRetries >= maxDeployFailureRetries {
		s.logger.Warn("Deploy fast-retry budget exhausted; 60s drift backstop continues",
			"attempts", s.deployFailureRetries,
			"checksum", event.ContentChecksum)
		return
	}

	s.deployFailureRetries++
	backoff := s.failureRetryBackoff(s.deployFailureRetries)
	if s.retryTimer != nil {
		s.retryTimer.Stop()
	}
	s.retryTimer = time.AfterFunc(backoff, s.rescheduleLastValidated)

	s.logger.Info("Deploy failed; scheduling fast retry",
		"attempt", s.deployFailureRetries,
		"backoff_ms", backoff.Milliseconds(),
		"checksum", event.ContentChecksum)
}

// failureRetryBackoff returns the fast-retry backoff for the given 1-based
// attempt: an exponential base<<(attempt-1) doubling, clamped to
// maxFailureRetryBackoff. The base is minDeploymentInterval (default 2s), so the
// sequence is 2s, 4s, 8s, 16s, 32s. The clamp also catches any shift overflow
// (backoff<=0) defensively.
func (s *DeploymentScheduler) failureRetryBackoff(attempt int) time.Duration {
	base := s.minDeploymentInterval
	if base <= 0 {
		base = 2 * time.Second
	}
	backoff := base << (attempt - 1)
	if backoff <= 0 || backoff > maxFailureRetryBackoff {
		backoff = maxFailureRetryBackoff
	}
	return backoff
}

// rescheduleLastValidated is the fast-retry timer callback. It re-dispatches the
// last-validated render through the existing coalescing path, MIRRORING
// performPodsDiscovered's snapshot: it never spawns a deploy — it only sets the
// single state.pending slot, which the one runDeployLoop drains under its own
// rate limit (waitDeployInterval still enforces minDeploymentInterval between
// reloads).
//
// coalescible=false: when the timer fires this retry IS the newest thing for the
// slot. A newer real render overwrites state.pending via latest-wins anyway
// (and, being a fresh ValidationCompleted, resets the failure budget), so the
// retry never clobbers newer work.
//
// Lock ordering: snapshot under s.mu, RELEASE it, then call scheduleOrQueue
// (which takes schedulerMutex internally). s.mu and schedulerMutex are never held
// at the same time here.
func (s *DeploymentScheduler) rescheduleLastValidated() {
	// If leadership was lost (or we're shutting down) between the timer arming and
	// firing, s.ctx is cancelled and the deploy loop has exited — don't repopulate
	// state.pending for a term that's already over. handleLostLeadership stops the
	// timer, but Stop() can't cancel a callback already running, so this closes
	// that last window. (s.ctx is nil only in direct-call unit tests that never
	// call Start; there the hasValidConfig guard below no-ops safely.)
	if s.ctx != nil && s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	config := s.lastValidatedConfig
	auxFiles := s.lastValidatedAux
	parsedConfig := s.lastParsedConfig
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
	correlationID := s.lastCorrelationID
	endpoints := s.currentEndpoints
	hasValidConfig := s.hasValidConfig
	s.mu.Unlock()

	if !hasValidConfig || len(endpoints) == 0 {
		s.logger.Debug("Fast retry skipped: no validated config or no endpoints")
		return
	}

	s.scheduleOrQueue(s.ctx, config, auxFiles, parsedConfig, endpoints, "deploy_failure_retry", correlationID, statusPatches, false /* coalescible */, contentChecksum)
}

// cancelFailureRetry stops any armed fast-retry timer and resets the budget.
// Called when a deploy completes with zero failures (the config converged) so a
// stale retry from an earlier failure can't fire against an already-good state.
func (s *DeploymentScheduler) cancelFailureRetry() {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	if s.retryTimer != nil {
		s.retryTimer.Stop()
	}
	s.deployFailureRetries = 0
	s.lastFailedRetryChecksum = ""
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

	s.logger.Debug("Cached runtime config metadata for deployment events",
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

	// Stop any armed fast-retry timer and reset its budget — a new leadership
	// term must not fire a retry queued by the previous one. Inlined rather than
	// calling cancelFailureRetry() (which would re-lock the schedulerMutex we
	// already hold).
	if s.retryTimer != nil {
		s.retryTimer.Stop()
	}
	s.deployFailureRetries = 0
	s.lastFailedRetryChecksum = ""

	// Drop the dispatch diff baseline (the new leader hasn't dispatched, so its
	// first render must be classified structural — nil baseline — and deploy the
	// whole config) and close the bypass's persistent clients.
	s.lastDispatchedParsed = nil
	s.lastDispatchedConfig = ""
	s.lastActivatedConfig = ""
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

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
	"sync"
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

// handleTemplateRendered arms deployment for a completed render.
//
// The render itself is the trigger now: HAProxy's verdict runs asynchronously in
// the render gate (ADR-0022), so waiting for it here would put the check back on
// the wall clock. While the gate holds renders — it refused the previous one —
// the render is only cached, and the gate's pass for this plan releases it.
func (s *DeploymentScheduler) handleTemplateRendered(ctx context.Context, event *events.TemplateRenderedEvent) {
	s.mu.Lock()
	s.lastRenderedConfig = event.HAProxyConfig
	s.lastAuxiliaryFiles = event.AuxiliaryFiles
	s.lastContentChecksum = event.ContentChecksum
	s.lastRenderedPlan = event.Plan
	s.lastRenderedPlanID = event.PlanID
	s.lastRenderedStatusPatches = event.StatusPatches
	pinned := s.gatePinned
	s.mu.Unlock()

	if pinned {
		s.logger.Warn("Render gate is holding renders; waiting for its verdict before deploying",
			"plan", event.PlanID,
			"correlation_id", event.CorrelationID())
		return
	}

	s.dispatchRender(ctx, event.CorrelationID(), event.Coalescible(), deployReason(event.TriggerReason))
}

// handleRenderGateCompleted moves the deployment side of the gate's latch: a
// refusal holds every later render until a verdict passes, and that pass
// dispatches the render being held — but only when the verdict names the newest
// render, so a verdict for a superseded plan never dispatches an unchecked one.
func (s *DeploymentScheduler) handleRenderGateCompleted(ctx context.Context, event *events.RenderGateCompletedEvent) {
	// A verdict for a plan the fleet has moved past scopes the gate's revert;
	// it says nothing about the render this scheduler is converging on, so it
	// must not move the latch, the deployable render or a queued deployment.
	if !event.Newest {
		s.logger.Debug("Ignoring a render gate verdict for a superseded plan",
			"plan", event.PlanID, "ok", event.OK)
		return
	}

	if !event.OK {
		s.holdAfterRefusal(event)
		return
	}

	s.mu.Lock()
	wasPinned := s.gatePinned
	// The bus delivers a render before its verdict (the gate learns of the
	// render from the same event), so the only way this misses is a dropped
	// render — and then the drift pass produces a fresh one within its
	// interval, which the gate judges in turn.
	namesHeldRender := s.lastRenderedPlanID != "" && s.lastRenderedPlanID == event.PlanID
	alreadyDispatched := s.lastValidatedPlanID == event.PlanID
	if alreadyDispatched {
		s.acceptRenderLocked()
	}
	if !wasPinned || namesHeldRender {
		s.gatePinned = false
	}
	s.mu.Unlock()

	if !wasPinned || !namesHeldRender || alreadyDispatched {
		return
	}

	s.logger.Info("Render gate passed the held render, deploying it", "plan", event.PlanID)
	s.dispatchRender(ctx, event.CorrelationID(), false, "rendergate_release")

	// The released render is now what the fleet runs, so it is what a later
	// refusal must roll back to. Without this the rollback would reach past it
	// to the render accepted before the incident, dropping everything HAProxy
	// validated in between.
	s.mu.Lock()
	s.acceptRenderLocked()
	s.mu.Unlock()
}

// holdAfterRefusal closes the latch on a failed verdict for the newest render.
//
// Only HAProxy's own refusal moves the deployable render back: a check that
// could not run is not evidence about the config, and rolling back on it would
// undo a live, working render because a temp directory was unwritable — the
// same rule revert.go applies to the pods. Either way the gate holds: nothing
// has judged the render, so nothing new may be dispatched.
func (s *DeploymentScheduler) holdAfterRefusal(event *events.RenderGateCompletedEvent) {
	s.mu.Lock()
	s.gatePinned = true
	restored := ""
	if event.Refused {
		restored = s.rollBackToAcceptedRenderLocked(event.PlanID)
	}
	s.mu.Unlock()

	dropped := s.dropPendingPlan(event.PlanID)

	s.logger.Warn("Render gate refused a render; holding further renders",
		"plan", event.PlanID,
		"pinned", event.Pinned,
		"refused_by_haproxy", event.Refused,
		"rolled_back_to", restored,
		"dropped_pending", dropped,
		"error", event.Message)
}

// dropPendingPlan retires a deployment of the refused plan that is queued
// behind an in-flight one, reporting whether it dropped anything.
//
// The latch alone does not cover this: a render dispatched before the verdict
// may still be sitting in the pending slot when the refusal lands, and the
// deploy loop would publish it as soon as the current deployment finishes —
// after the scoped revert has already run and found no pod carrying it. Scoped
// to the named plan, because a refusal for one render must not cancel another.
func (s *DeploymentScheduler) dropPendingPlan(planID string) bool {
	if planID == "" {
		return false
	}
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	if s.state.pending == nil || s.state.pending.planID != planID {
		return false
	}
	s.workRevision++
	s.state.pending = nil
	return true
}

// acceptRenderLocked snapshots the render the gate just passed. Caller holds mu.
//
// The three paths that re-send a render to pods rather than dispatch a new one
// — pod discovery, the validation fallback and the retry timers — all read the
// `lastValidated*` fields. Those hold whatever was dispatched last, which under
// the optimistic gate includes a render HAProxy has not judged yet, so this
// snapshot is what a refusal rolls them back to.
func (s *DeploymentScheduler) acceptRenderLocked() {
	s.acceptedRender = &acceptedRender{
		config:          s.lastValidatedConfig,
		auxFiles:        s.lastValidatedAux,
		contentChecksum: s.lastValidatedContentChecksum,
		plan:            s.lastValidatedPlan,
		planID:          s.lastValidatedPlanID,
		statusPatches:   s.lastValidatedStatusPatches,
		correlationID:   s.lastCorrelationID,
	}
}

// rollBackToAcceptedRenderLocked undoes the optimistic promotion of a render
// the gate then refused, so every path that re-sends "the config the fleet
// runs" sends the last one HAProxy accepted — the same set the pods were just
// reverted to — instead of fighting that revert with the refused render.
// Caller holds mu. Returns the plan it rolled back to, for the log.
//
// A refusal naming a plan the scheduler has already superseded leaves the
// deployable render alone: the newer one is simply unjudged, and the gate
// checks it next. With nothing accepted yet — the term's first render was
// refused — there is nothing to roll back to, and the refused render stays the
// only config this controller has; HAProxy refuses it per pod at apply, which
// is visible as a NACK rather than a silent stall.
func (s *DeploymentScheduler) rollBackToAcceptedRenderLocked(refusedPlanID string) string {
	if s.acceptedRender == nil || refusedPlanID == "" || s.lastValidatedPlanID != refusedPlanID {
		return ""
	}
	if s.acceptedRender.planID == refusedPlanID {
		return "" // the gate contradicting itself; keep what a pass established
	}
	s.lastValidatedConfig = s.acceptedRender.config
	s.lastValidatedAux = s.acceptedRender.auxFiles
	s.lastValidatedContentChecksum = s.acceptedRender.contentChecksum
	s.lastValidatedPlan = s.acceptedRender.plan
	s.lastValidatedPlanID = s.acceptedRender.planID
	s.lastValidatedStatusPatches = s.acceptedRender.statusPatches
	s.lastCorrelationID = s.acceptedRender.correlationID
	s.lastCoalescible = false
	return s.acceptedRender.planID
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

// dispatchRender promotes the cached render to the deployable one and schedules
// it to the current endpoint set.
//
// Called from the render's own event while the gate is open, and from the gate's
// verdict while it is holding — the two paths differ only in what proved the
// render dispatchable, never in what is dispatched.
func (s *DeploymentScheduler) dispatchRender(ctx context.Context, correlationID string, coalescible bool, reason string) {
	// Get current state and cache the deployable config BEFORE scheduling
	// This prevents race where pod discovery reads stale config
	s.mu.Lock()
	config := s.lastRenderedConfig
	auxFiles := s.lastAuxiliaryFiles
	endpoints := s.currentEndpoints
	statusPatches := s.lastRenderedStatusPatches
	configChecksum := s.lastContentChecksum
	plan := s.lastRenderedPlan
	planID := s.lastRenderedPlanID
	// Cache validated config immediately to prevent race condition.
	// `lastValidatedContentChecksum` must be captured AT THE SAME POINT as
	// `lastValidatedConfig` — otherwise pod-discovery reads (which fall
	// through to this cache) can re-read a stale or newer
	// `lastContentChecksum` and the resulting deploy records the wrong
	// hash. See scheduler.go's scheduledDeployment.contentChecksum doc.
	s.lastValidatedConfig = config
	s.lastValidatedAux = auxFiles
	s.lastValidatedContentChecksum = configChecksum
	s.lastValidatedPlan = plan
	s.lastValidatedPlanID = planID
	s.lastValidatedStatusPatches = statusPatches
	s.lastCorrelationID = correlationID
	s.lastCoalescible = coalescible
	s.hasValidConfig = true
	s.mu.Unlock()

	if config == "" {
		s.logger.Error("No rendered config available for deployment")
		return
	}

	if len(endpoints) == 0 {
		s.logger.Debug("No endpoints available yet, config cached for later deployment")
		return
	}

	// Use the content checksum captured WITH the config being dispatched —
	// `lastValidatedContentChecksum`, set above under the same lock as
	// `lastValidatedConfig`. Reading `s.lastContentChecksum` directly here
	// would let a fresh reconcile (which mutates that field in
	// handleTemplateRendered) substitute a newer hash than the config we're
	// about to deploy actually carries.
	configHash := configChecksum
	podSetHash := computePodSetHash(endpoints)

	// Drift prevention deployments must ALWAYS execute (bypass cache)
	isDriftPrevention := reason == events.TriggerReasonDriftPrevention

	// Check if deployment can be skipped (config unchanged for same pod set).
	// The fleet must be settled on the last deployed config — not merely have it
	// as the last one that fully deployed while a later render has since moved it
	// on. Otherwise a content-addressed render recurring after a paced deploy
	// (add then delete, both hashing to the same recurring plan) matches the
	// stale hash and the fleet never reaches it.
	s.mu.RLock()
	canSkip := !isDriftPrevention &&
		configHash == s.lastDeployedConfigHash &&
		s.lastDispatchedConfigHash == s.lastDeployedConfigHash &&
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
			events.SkipReasonConfigUnchanged,
			configHash,
			podSetHash,
			statusPatches,
			events.WithCorrelation(correlationID, correlationID),
		))
		return
	}

	// Schedule deployment to current endpoints (or queue if deployment in progress).
	// scheduleOrQueue classifies the render into a lane (runtime-raw vs structural)
	// against the last-dispatched config; the deploy loop applies it accordingly.
	//
	// `configHash` was captured above under the same lock as `config`. Thread it
	// through scheduleOrQueue so the eventual deploy records THIS hash, not
	// whatever `s.lastContentChecksum` holds at deploy-time (which a later
	// reconcile will have overwritten under sustained parallel-test load).
	s.scheduleOrQueue(ctx, config, auxFiles, endpoints, reason, correlationID, statusPatches, coalescible, configHash, plan, planID)
}

// deployReason names why the deploy runs. The drift pass must stay
// distinguishable all the way to the deployer: it verifies each pod's tree
// instead of trusting the digests the agent last recorded.
func deployReason(triggerReason string) string {
	if triggerReason == events.TriggerReasonDriftPrevention {
		return events.TriggerReasonDriftPrevention
	}
	return "config_validation"
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
	// An endpoint-authority change retires the in-flight deploy: its pods are
	// not the fleet any more, and the replacement set must be deployed to as a
	// whole.
	var cancelledDeploymentID, cancelledCorrelationID string
	podSetHash := computePodSetHash(event.Endpoints)
	s.schedulerMutex.Lock()
	if s.lastPodSetHash != "" && s.lastPodSetHash != podSetHash {
		if s.state.deployInFlight {
			cancelledDeploymentID = s.state.activeDeploymentID
			cancelledCorrelationID = s.state.activeCorrelationID
		}
		s.workRevision++
		s.state.pending = nil
	}
	s.schedulerMutex.Unlock()
	s.publishFleetCapabilities(event.Endpoints)
	if cancelledDeploymentID != "" {
		s.eventBus.Publish(events.NewDeploymentCancelRequestEvent(
			cancelledDeploymentID,
			"endpoint_authority_changed",
			events.WithCorrelation(cancelledCorrelationID, cancelledDeploymentID),
		))
	}

	s.mu.Lock()
	s.currentEndpoints = event.Endpoints
	endpointCount := len(event.Endpoints)
	config := s.lastValidatedConfig
	auxFiles := s.lastValidatedAux
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
	plan := s.lastValidatedPlan
	planID := s.lastValidatedPlanID
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
	s.scheduleOrQueue(ctx, config, auxFiles, event.Endpoints, "pod_discovery", correlationID, statusPatches, coalescible, contentChecksum, plan, planID)
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
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
	plan := s.lastValidatedPlan
	planID := s.lastValidatedPlanID
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
	s.scheduleOrQueue(ctx, config, auxFiles, endpoints, "validation_fallback", correlationID, statusPatches, false, contentChecksum, plan, planID)
}

// handleDeploymentCompleted handles deployment completion events.
//
// This marks the deployment as complete, updates the deployment end time, and
// caches the deployed config hash for optimization. It does NOT re-schedule —
// it only clears deployInFlight and signals the loop, which picks up any pending
// deployment on its next cycle.
//
// The deployed config and pod-set hashes must come from the completion event —
// the proofs captured from the DeploymentScheduledEvent that triggered this
// deployment — NOT from mutable scheduler state.
// A reconcile that lands between deployment-start and deployment-complete
// overwrites s.lastContentChecksum with the newer render, and using that
// value here mis-records THIS deployment's checksum as the newer one. The
// next reconcile that produces the newer hash then matches lastDeployedConfigHash
// and incorrectly skips deployment — the newer render's content (e.g. a
// fresh Ingress's redirect directive) never reaches HAProxy. See CI
// pipeline 2551671212 / TestIngressHaproxyRedirectTo for a real
// reproduction.
func (s *DeploymentScheduler) handleDeploymentCompleted(event *events.DeploymentCompletedEvent) {
	s.schedulerMutex.Lock()
	if !s.state.deployInFlight || event.DeploymentID == "" || event.DeploymentID != s.state.activeDeploymentID {
		activeDeploymentID := s.state.activeDeploymentID
		s.schedulerMutex.Unlock()
		s.logger.Warn("Ignoring completion for a deployment that is not active",
			"completed_deployment_id", event.DeploymentID,
			"active_deployment_id", activeDeploymentID,
			"correlation_id", event.CorrelationID())
		return
	}

	timedOut := s.state.deploymentTimedOut
	s.state.deployInFlight = false
	s.state.deploymentTimedOut = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeDeploymentID = ""
	s.state.activeCorrelationID = ""

	if timedOut {
		s.schedulerMutex.Unlock()
		s.logger.Info("Timed-out deployment terminated",
			"deployment_id", event.DeploymentID,
			"correlation_id", event.CorrelationID())
		s.signalCompleted()
		return
	}

	s.schedulerMutex.Unlock()

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
	// A pod holding the render behind a paced reload has not deployed it
	// yet either: caching the hash now would make the skip-unchanged gate
	// refuse the follow-up that observes the reload firing.
	fullyDeployed := event.Failed == 0 && event.PendingReloads == 0
	s.mu.Lock()
	if event.ContentChecksum != "" && event.PodSetHash != "" {
		// Record what the fleet was last sent even when it holds it behind a
		// paced reload: the skip-unchanged gate reads this to tell "the fleet is
		// settled on lastDeployedConfigHash" from "a later render moved it off,
		// and lastDeployedConfigHash is stale". A recurring content-addressed
		// render must not be skipped just because its checksum matches that
		// stale value.
		s.lastDispatchedConfigHash = event.ContentChecksum
		if fullyDeployed {
			s.lastDeployedConfigHash = event.ContentChecksum
			s.lastDeployedPodSetHash = event.PodSetHash
			s.lastDeployedTime = time.Now()
		}
	}
	s.mu.Unlock()

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
	case event.Total > 0 && event.PendingReloads > 0:
		s.schedulePendingReloadFollowUp(event)
	case event.Total > 0 && event.Failed == 0:
		s.cancelFailureRetry()
	}

	s.signalCompleted()
}

// pendingReloadFollowUpMargin is added to the agent's scheduled_at so the
// follow-up finds the reload done, not about to run. Renders that arrive while
// reloads are pending are dispatched at once: the pods coalesce their files
// into the pending reload and run the in-place subset — an endpoint change
// must never wait for a reload window.
const pendingReloadFollowUpMargin = 250 * time.Millisecond

// schedulePendingReloadFollowUp re-drives the last validated render once the
// pods' paced reloads have fired. The agent never cancels a scheduled reload
// and never calls back; the controller polls at the scheduled time (plan
// §0.c). The re-diff is a noop for every pod that has reloaded and
// `scheduled` again for one that has not, so the chain ends by itself. It
// rides the single retry timer outside the failure budget: waiting for a
// reload window is not a failure.
func (s *DeploymentScheduler) schedulePendingReloadFollowUp(event *events.DeploymentCompletedEvent) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	if s.retryStopped {
		return
	}
	wait := pendingReloadFollowUpMargin
	if !event.PendingReloadUntil.IsZero() {
		wait = time.Until(event.PendingReloadUntil) + pendingReloadFollowUpMargin
	}
	if wait < pendingReloadFollowUpMargin {
		wait = pendingReloadFollowUpMargin
	}
	if wait > maxFailureRetryBackoff {
		wait = maxFailureRetryBackoff
	}
	s.stopRetryTimerLocked()
	s.retryGeneration++
	generation := s.retryGeneration
	workRevision := s.workRevision
	s.retryCallbacks.Add(1)
	var doneOnce sync.Once
	done := func() {
		doneOnce.Do(s.retryCallbacks.Done)
	}
	s.retryTimerDone = done
	s.retryTimer = time.AfterFunc(wait, func() {
		defer done()
		s.runRetry(generation, workRevision, "pending_reload_follow_up")
	})

	s.logger.Info("Reloads pending on the fleet; following up when they fire",
		"pending_pods", event.PendingReloads,
		"wait_ms", wait.Milliseconds(),
		"checksum", event.ContentChecksum)
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
	if s.retryStopped {
		return
	}

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
	s.stopRetryTimerLocked()
	s.retryGeneration++
	generation := s.retryGeneration
	workRevision := s.workRevision
	s.retryCallbacks.Add(1)
	var doneOnce sync.Once
	done := func() {
		doneOnce.Do(s.retryCallbacks.Done)
	}
	s.retryTimerDone = done
	s.retryTimer = time.AfterFunc(backoff, func() {
		defer done()
		s.runFailureRetry(generation, workRevision)
	})

	s.logger.Info("Deploy failed; scheduling fast retry",
		"attempt", s.deployFailureRetries,
		"backoff_ms", backoff.Milliseconds(),
		"checksum", event.ContentChecksum)
}

func (s *DeploymentScheduler) runFailureRetry(generation, workRevision uint64) {
	s.runRetry(generation, workRevision, "deploy_failure_retry")
}

// runRetry re-dispatches the last validated render under a reason, if the
// timer that fired is still the armed one and the term did not move on.
func (s *DeploymentScheduler) runRetry(generation, workRevision uint64, reason string) {
	s.schedulerMutex.Lock()
	if generation != s.retryGeneration {
		s.schedulerMutex.Unlock()
		return
	}
	s.retryTimer = nil
	s.retryTimerDone = nil
	if s.retryStopped || workRevision != s.workRevision {
		s.schedulerMutex.Unlock()
		return
	}
	s.schedulerMutex.Unlock()

	s.rescheduleLastValidated(generation, workRevision, reason)
}

func (s *DeploymentScheduler) stopRetryTimerLocked() {
	s.retryGeneration++
	if s.retryTimer == nil {
		return
	}
	if s.retryTimer.Stop() && s.retryTimerDone != nil {
		s.retryTimerDone()
	}
	s.retryTimer = nil
	s.retryTimerDone = nil
}

func (s *DeploymentScheduler) stopFailureRetries() {
	s.schedulerMutex.Lock()
	s.retryStopped = true
	s.stopRetryTimerLocked()
	if s.state.pending != nil && s.state.pending.retryGeneration != 0 {
		s.state.pending = nil
	}
	s.deployFailureRetries = 0
	s.lastFailedRetryChecksum = ""
	s.schedulerMutex.Unlock()

	s.retryCallbacks.Wait()
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

// rescheduleLastValidated snapshots the last validated render, then installs it
// only if the work and retry revisions captured when its timer was armed remain
// current. It never starts a second deploy path.
func (s *DeploymentScheduler) rescheduleLastValidated(generation, workRevision uint64, reason string) {
	s.schedulerMutex.Lock()
	newerPending := s.state.pending != nil && s.state.pending.retryGeneration == 0
	s.schedulerMutex.Unlock()
	if newerPending {
		// A newer render is already waiting; dispatching it covers this one.
		return
	}
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
	statusPatches := s.lastValidatedStatusPatches
	contentChecksum := s.lastValidatedContentChecksum
	plan := s.lastValidatedPlan
	planID := s.lastValidatedPlanID
	correlationID := s.lastCorrelationID
	endpoints := s.currentEndpoints
	hasValidConfig := s.hasValidConfig
	s.mu.Unlock()

	if !hasValidConfig || len(endpoints) == 0 {
		s.logger.Debug("Fast retry skipped: no validated config or no endpoints")
		return
	}

	s.installPending(s.ctx, &scheduledDeployment{
		workRevision:    workRevision,
		retryGeneration: generation,
		config:          config,
		auxFiles:        auxFiles,
		plan:            plan,
		planID:          planID,
		endpoints:       endpoints,
		reason:          reason,
		correlationID:   correlationID,
		statusPatches:   statusPatches,
		contentChecksum: contentChecksum,
	})
}

// cancelFailureRetry stops any armed fast-retry timer and resets the budget.
// Called when a deploy completes with zero failures (the config converged) so a
// stale retry from an earlier failure can't fire against an already-good state.
func (s *DeploymentScheduler) cancelFailureRetry() {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	s.stopRetryTimerLocked()
	if s.state.pending != nil && s.state.pending.retryGeneration != 0 {
		s.state.pending = nil
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
	s.workRevision++
	s.state.deployInFlight = false
	s.state.deploymentTimedOut = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeDeploymentID = ""
	s.state.activeCorrelationID = ""
	s.state.pending = nil

	s.retryStopped = true
	s.stopRetryTimerLocked()
	s.deployFailureRetries = 0
	s.lastFailedRetryChecksum = ""

	s.lastPodSetHash = ""

	// Clear deployment cache - new leader should verify config state.
	// The render gate's latch is per leadership term: a new leader starts
	// optimistic because the agents' own last-known-good set protects the fleet.
	s.mu.Lock()
	s.lastDeployedConfigHash = ""
	s.lastDispatchedConfigHash = ""
	s.lastDeployedPodSetHash = ""
	s.lastDeployedTime = time.Time{}
	s.gatePinned = false
	s.acceptedRender = nil
	s.mu.Unlock()
}

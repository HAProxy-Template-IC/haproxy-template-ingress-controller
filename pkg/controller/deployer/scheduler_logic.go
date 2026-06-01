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
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// scheduleOrQueue either queues a deployment if one is in progress, or schedules it immediately.
//
// This prevents concurrent deployments which can cause version conflicts.
// Uses a "latest wins" pattern where pending deployments overwrite each other.
//
// `contentChecksum` MUST be captured by the caller at the same point `config`
// is captured (i.e. from the same TemplateRenderedEvent / ValidationCompletedEvent
// snapshot) and threaded through unchanged. Re-reading it from
// `s.lastContentChecksum` at deploy-time creates a race window where a fresh
// reconcile mutates the field, the wrong hash gets recorded as "deployed",
// and the next reconcile producing that hash incorrectly skips.
func (s *DeploymentScheduler) scheduleOrQueue(
	_ context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	endpoints []dataplane.Endpoint,
	reason string,
	correlationID string,
	statusPatches []templating.StatusPatch,
	coalescible bool,
	contentChecksum string,
) {
	// Snapshot the diff baseline (the last-DISPATCHED render) under a short lock.
	s.schedulerMutex.Lock()
	prevParsed := s.lastDispatchedParsed
	s.schedulerMutex.Unlock()

	// Compute the render diff LOCK-FREE — it is O(config), pod-independent, and
	// the same for every endpoint. The lane is decided from this diff: a render
	// whose diff vs the last-dispatched config is purely runtime-eligible server
	// fields takes the runtime-raw lane; anything structural takes the structural
	// lane.
	updates, err := dataplane.ComputeRuntimeServerUpdates(prevParsed, parsedConfig)

	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()

	// If the baseline advanced while we computed the diff (another dispatch landed
	// concurrently), recompute against the now-current baseline so the lane
	// reflects what THIS render actually changes relative to what's in flight.
	if s.lastDispatchedParsed != prevParsed {
		prevParsed = s.lastDispatchedParsed
		updates, err = dataplane.ComputeRuntimeServerUpdates(prevParsed, parsedConfig)
	}

	// Latest-wins: overwrite the single pending slot with this render and its lane.
	// The deploy loop grabs the newest pending; the single slot + latest-wins is
	// the entire mechanism by which the two lanes never coexist — a structural
	// render supersedes a pending runtime-raw, and while a structural is pending
	// every later render is structural too (its diff vs the unchanged baseline
	// still contains the structural op). There is no idle/in-flight branching and
	// no goroutine spawn here — the ONE runDeployLoop goroutine owns all timing.
	s.state.pending = &scheduledDeployment{
		config:          config,
		auxFiles:        auxFiles,
		parsedConfig:    parsedConfig,
		endpoints:       endpoints,
		reason:          reason,
		correlationID:   correlationID,
		statusPatches:   statusPatches,
		coalescible:     coalescible,
		contentChecksum: contentChecksum,
		lane:            classifyLane(prevParsed, updates, err),
		runtimeUpdates:  updates,
	}

	// Wake the deploy loop to (re)evaluate pending. Signalling under the lock is
	// fine — signalLoop is a non-blocking cap-1 send.
	s.signalLoop()
}

// classifyLane decides the apply lane for a render given its diff (updates)
// against the last-dispatched config. It is laneRuntimeRaw iff there is a
// non-nil baseline, the diff computed cleanly, and the diff is purely
// runtime-eligible server fields (IsRuntimeEligible). Every other case —
// nil baseline (cold start: nothing to diff against, so the whole config must be
// deployed), a diff error, or any structural op — is laneStructural.
func classifyLane(prevParsed *parser.StructuredConfig, updates *dataplane.RuntimeServerUpdates, err error) lane {
	if prevParsed != nil && err == nil && updates.IsRuntimeEligible() {
		return laneRuntimeRaw
	}
	return laneStructural
}

// runDeployLoop is the single goroutine that owns all rate-limit timing. It is
// the ONLY place that waits out minDeploymentInterval and publishes
// DeploymentScheduledEvent. Event handlers only set state.pending (latest-wins)
// and wake it via signalLoop; this loop coalesces to the newest pending and
// emits at most one deploy per minDeploymentInterval — so concurrent rate-limit
// sleeps (and the resulting reload bursts under churn) are structurally
// impossible. Runs for the whole leadership term; exits on ctx.Done().
func (s *DeploymentScheduler) runDeployLoop(ctx context.Context) {
	defer close(s.loopDone)

	for {
		// 1. Wait for pending work.
		s.schedulerMutex.Lock()
		hasPending := s.state.pending != nil
		s.schedulerMutex.Unlock()
		if !hasPending {
			select {
			case <-s.pendingSignal:
			case <-ctx.Done():
				return
			}
			continue // re-check pending under lock
		}

		// 2. Wait out minDeploymentInterval before a structural reload — while
		// keeping the LATEST render's runtime-eligible server subset (a pod-IP
		// rotation) applied to the live workers throughout the wait, so endpoint
		// changes converge in ~ms instead of being trapped behind the interval
		// when coalesced with another tenant's structural change (the
		// rolling-restart 503 gap). A runtime-raw pending skips the wait entirely
		// (dispatchPending applies it). See waitDeployInterval.
		if !s.waitDeployInterval(ctx) {
			return // ctx cancelled
		}

		// 3. Grab+clear the latest pending (the coalescing point) and dispatch it
		// on its lane. The grab re-reads pending under the lock, so a render that
		// arrived during the structural interval wait (possibly flipping the lane)
		// is the one acted on.
		s.schedulerMutex.Lock()
		dep := s.state.pending
		s.state.pending = nil
		s.schedulerMutex.Unlock()
		if dep == nil {
			continue // Cleared by lost-leadership while we waited.
		}
		if !s.dispatchPending(ctx, dep) {
			return // ctx cancelled
		}
	}
}

// applyRuntimePreInterval applies a STRUCTURAL render's runtime-eligible server
// subset (pod IP / port / admin-state changes) to the live workers immediately,
// via the same runtime-raw push the runtime-raw lane uses (a skip_reload body
// push carrying `set server` actions — no reload). waitDeployInterval calls it
// for the pending render before it sleeps out the interval, and again for each
// newer render that arrives mid-wait, so a pod-IP rotation reaches HAProxy in ~ms
// instead of waiting out the structural reload's minDeploymentInterval. Without
// it, a runtime-eligible server change coalesced into a render that also carries
// an unrelated tenant's structural op is trapped on the rate-limited lane (the
// rolling-restart 503 gap).
//
// No-op for the runtime-raw lane (dispatchPending applies that with no interval,
// so applying here too would merely double it) and for renders whose diff
// carries no runtime-eligible server change (ServerOpCount 0, nil-safe). The
// baseline (lastDispatchedParsed) is deliberately NOT advanced here: the
// structural deploy still diffs against it, re-applies the same `set server`
// (idempotent), and force-reloads the body that already carries the new address,
// so no reload can ever load a config without it. Best-effort — applyRuntimeRaw
// swallows per-endpoint failures and the structural deploy converges the pods.
func (s *DeploymentScheduler) applyRuntimePreInterval(ctx context.Context, dep *scheduledDeployment) {
	// dep may be nil: the mid-wait pendingSignal path reads s.state.pending,
	// which handleLostLeadership can clear to nil concurrently (a buffered signal
	// can still be selected before the loop sees ctx.Done()). Treat a nil pending
	// as nothing-to-apply rather than dereferencing it.
	if dep == nil || dep.lane != laneStructural || dep.runtimeUpdates.ServerOpCount() == 0 {
		return
	}
	s.runtimeBypass.applyRuntimeRaw(ctx, dep)
}

// waitDeployInterval enforces minDeploymentInterval before a STRUCTURAL deploy,
// measured from the last structural deploy's end, while keeping endpoint changes
// flowing to the live workers throughout the wait.
//
// A runtime-raw pending SKIPS the wait entirely (wait==0) — its server changes
// apply via dispatchPending with no reload. For a STRUCTURAL pending it sleeps
// the remaining interval, but FIRST applies that render's runtime-eligible server
// subset (set server + skip_reload push, no reload) and then RE-applies the
// latest render's subset every time a newer pending arrives mid-wait. This is
// what stops a pod-IP rotation coalesced with another tenant's structural change
// from waiting out the whole interval (the rolling-restart 503 gap): the address
// reaches the live worker in ~ms while the reload stays gated. The original timer
// is never reset, so the structural reload still fires at the interval deadline
// and reloads can't burst. Flap-safe: no deploy is in flight during the wait, so
// no reload can revert the just-applied address before the gated structural
// deploy (which re-renders the same address) runs.
//
// Returns false only on ctx cancellation.
func (s *DeploymentScheduler) waitDeployInterval(ctx context.Context) bool {
	s.schedulerMutex.Lock()
	pending := s.state.pending
	var wait time.Duration
	if pending != nil && pending.lane == laneStructural {
		wait = s.remainingInterval(s.state.lastDeploymentEndTime)
	}
	s.schedulerMutex.Unlock()
	if wait <= 0 {
		return true
	}
	s.logger.Debug("Enforcing minimum deployment interval",
		"sleep_duration_ms", wait.Milliseconds(),
		"min_interval_ms", s.minDeploymentInterval.Milliseconds())

	// Apply the current structural render's runtime subset before sleeping, so
	// the address is live for the whole interval rather than only after it.
	s.applyRuntimePreInterval(ctx, pending)

	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case <-s.pendingSignal:
			// A newer render arrived mid-interval (latest-wins already swapped
			// state.pending). Apply its runtime subset now so an endpoint change
			// landing during the wait converges in ~ms; keep waiting the original
			// timer (the reload stays gated, no burst).
			s.schedulerMutex.Lock()
			latest := s.state.pending
			s.schedulerMutex.Unlock()
			s.applyRuntimePreInterval(ctx, latest)
		case <-ctx.Done():
			return false
		}
	}
}

// dispatchPending dispatches one grabbed pending deployment on its lane. Returns
// false only on ctx cancellation (so the loop should exit).
//
//   - laneStructural: mark in-flight, advance the dispatch baseline, publish one
//     DeploymentScheduledEvent, and block until completion/timeout/shutdown.
//   - laneRuntimeRaw: advance the dispatch baseline and apply inline+synchronously
//     via the runtime bypass. Does NOT set deployInFlight or touch the interval
//     anchor (it reloads nothing), and does not publish or await.
//
// Both lanes write lastDispatchedParsed + lastDispatchedConfig together under
// schedulerMutex.
func (s *DeploymentScheduler) dispatchPending(ctx context.Context, dep *scheduledDeployment) bool {
	if dep.lane == laneRuntimeRaw {
		s.schedulerMutex.Lock()
		s.lastDispatchedParsed = dep.parsedConfig
		s.lastDispatchedConfig = dep.config
		s.schedulerMutex.Unlock()

		// A pure runtime-raw deploy reloads nothing, so this apply IS the complete
		// deploy: resolve the HAProxyCfg identity so the bypass can advance the
		// pod's status.deployedToPods[].Checksum on success. (The pre-interval
		// apply of a STRUCTURAL render's runtime subset leaves these empty — its
		// reload is still pending, so the Component publishes the status after it.)
		dep.runtimeConfigName, dep.runtimeConfigNamespace = s.resolveRuntimeConfigName()

		s.runtimeBypass.applyRuntimeRaw(ctx, dep)
		return true
	}

	// laneStructural: deployInFlight gates the timeout checker and pairs with
	// awaitCompletion; lastDeploymentEndTime advances only on this lane's
	// completion (the interval anchor).
	s.schedulerMutex.Lock()
	s.state.deployInFlight = true
	s.state.deploymentStartTime = time.Now()
	s.state.activeCorrelationID = dep.correlationID
	s.lastDispatchedParsed = dep.parsedConfig
	s.lastDispatchedConfig = dep.config
	s.schedulerMutex.Unlock()

	// Drain any stale completion (e.g. a late completion of a previously
	// timed-out deploy) so awaitCompletion waits for THIS deploy's signal.
	select {
	case <-s.completed:
	default:
	}

	// Publish exactly one DeploymentScheduledEvent, then wait for its completion /
	// timeout (both signal s.completed) / shutdown.
	s.publishScheduled(dep)
	return s.awaitCompletion(ctx)
}

// awaitCompletion blocks until the in-flight deploy completes or times out
// (handleDeploymentCompleted and checkDeploymentTimeout both record the
// end state and signal s.completed), or the context is cancelled. Returns
// false only on shutdown.
func (s *DeploymentScheduler) awaitCompletion(ctx context.Context) bool {
	select {
	case <-s.completed:
		return true
	case <-ctx.Done():
		return false
	}
}

// remainingInterval returns how long the loop must still wait before the next
// deploy, given when the last deploy ended. Zero if no prior deploy or the
// interval has already elapsed. The caller holds schedulerMutex while reading
// state.lastDeploymentEndTime; the computation touches only the immutable
// minDeploymentInterval.
func (s *DeploymentScheduler) remainingInterval(lastDeploymentEnd time.Time) time.Duration {
	if lastDeploymentEnd.IsZero() || s.minDeploymentInterval <= 0 {
		return 0
	}
	if d := s.minDeploymentInterval - time.Since(lastDeploymentEnd); d > 0 {
		return d
	}
	return 0
}

// signalLoop wakes the deploy loop after state.pending was set. Non-blocking:
// a signal already buffered IS the wakeup (coalesces redundant signals).
func (s *DeploymentScheduler) signalLoop() {
	select {
	case s.pendingSignal <- struct{}{}:
	default:
	}
}

// signalCompleted wakes the loop's awaitCompletion. Non-blocking; cap-1 buffered.
func (s *DeploymentScheduler) signalCompleted() {
	select {
	case s.completed <- struct{}{}:
	default:
	}
}

// publishScheduled emits the DeploymentScheduledEvent for the grabbed pending
// deployment, resolving the runtime-config name at dispatch time.
// `dep.contentChecksum` was captured at schedule-time with the config, so it
// labels THIS deploy's content correctly regardless of newer reconciles.
// resolveRuntimeConfigName returns the HAProxyCfg resource name + namespace whose
// status.deployedToPods a deploy advances. It prefers the value set by
// ConfigPublishedEvent and falls back to the deterministic name derived from the
// template-config name when that event hasn't landed yet — avoiding a wait on the
// K8s API call that publishes the HAProxyCfg resource. Used by both the structural
// DeploymentScheduledEvent (publishScheduled) and the runtime-raw lane dispatch.
func (s *DeploymentScheduler) resolveRuntimeConfigName() (name, namespace string) {
	s.mu.RLock()
	name = s.runtimeConfigName
	namespace = s.runtimeConfigNamespace
	templateConfigName := s.templateConfigName
	templateConfigNamespace := s.templateConfigNamespace
	s.mu.RUnlock()

	if name == "" && templateConfigName != "" {
		name = configpublisher.GenerateRuntimeConfigName(templateConfigName)
		namespace = templateConfigNamespace
	}
	return name, namespace
}

func (s *DeploymentScheduler) publishScheduled(dep *scheduledDeployment) {
	runtimeConfigName, runtimeConfigNamespace := s.resolveRuntimeConfigName()

	s.logger.Debug("Scheduling deployment",
		"reason", dep.reason,
		"endpoint_count", len(dep.endpoints),
		"config_bytes", len(dep.config),
		"has_parsed_config", dep.parsedConfig != nil,
		"correlation_id", dep.correlationID)

	s.eventBus.Publish(events.NewDeploymentScheduledEvent(
		dep.config, dep.auxFiles, dep.parsedConfig, dep.endpoints, runtimeConfigName, runtimeConfigNamespace, dep.reason, dep.contentChecksum, dep.statusPatches, dep.coalescible,
		events.WithCorrelation(dep.correlationID, dep.correlationID),
	))
}

// checkDeploymentTimeout checks if the current deployment has exceeded the timeout.
//
// If a deployment is in progress and has exceeded the configured timeout, this method
// publishes a cancellation event to stop the running deployment, resets the stuck state,
// and triggers a new reconciliation. This is a safety net for race conditions during
// leadership transitions where DeploymentCompletedEvent may be lost.
func (s *DeploymentScheduler) checkDeploymentTimeout(_ context.Context) {
	s.schedulerMutex.Lock()
	// Only a published-but-unconfirmed deploy can time out. The rate-limit wait
	// now lives in runDeployLoop and is bounded by minDeploymentInterval, so
	// there's no rate-limiting phase to exclude here.
	if !s.state.deployInFlight {
		s.schedulerMutex.Unlock()
		return
	}
	startTime := s.state.deploymentStartTime
	activeCorrelationID := s.state.activeCorrelationID
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

	// Clear the in-flight deploy and count the timeout as a deploy-end, so the
	// loop rate-limits the next deploy from here. Keep state.pending — the loop
	// picks it up on its next cycle once awaitCompletion unblocks. Then signal
	// completion to release the loop.
	s.schedulerMutex.Lock()
	s.state.deployInFlight = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeCorrelationID = ""
	s.state.lastDeploymentEndTime = time.Now()
	s.schedulerMutex.Unlock()
	s.signalCompleted()

	// Trigger a new reconciliation to recover from the stuck state (e.g. a lost
	// DeploymentCompletedEvent). Not coalescible — it must be processed.
	s.eventBus.Publish(events.NewReconciliationTriggeredEvent("deployment_timeout_recovery", false, events.WithNewCorrelation()))
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (s *DeploymentScheduler) HealthCheck() error {
	return s.healthTracker.Check()
}

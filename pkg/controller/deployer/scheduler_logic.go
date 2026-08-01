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

// applyRuntimeSubset applies dep's runtime-eligible server subset (pod IP / port /
// admin-state `set server` actions) to the live HAProxy workers immediately, via
// the runtime-raw skip_reload push (no reload) — WITHOUT claiming the deploy is
// complete. The apply is PARTIAL: it suppresses the deploy-owning publishes
// (DeployedConfigPublishRequest / ConfigAppliedToPodEvent); whoever owns the deploy
// (the eventual authoritative dispatchPending of this pending, or an in-flight
// structural deploy) publishes the CR/status. Both deploy-loop wait points call it
// so a newly-Ready pod's reserved-slot address reaches HAProxy in ~ms regardless of
// what is currently gating the loop:
//
//   - waitDeployInterval: while sleeping out minDeploymentInterval before/between
//     structural deploys. The pending here may be runtime-raw — once a structural
//     deploy has completed, lastDispatchedParsed has advanced to it, so a
//     newly-Ready pod's render diffs PURELY runtime-eligible against that baseline
//     and classifies laneRuntimeRaw. This MUST be lane-independent: gating on
//     laneStructural (as a prior version did) left exactly that runtime-raw render
//     skipped during the interval sleep, so the new pod's slot fill waited out the
//     whole interval and an in-flight request exhausted `option redispatch` on the
//     dead old slot — the residual rolling-restart 503 (more frequent on slower
//     HAProxy builds, e.g. the 3.0 image, which lose this race more often).
//   - awaitCompletion: while an unrelated tenant's structural deploy executes.
//
// Lane-INDEPENDENT, gated only on a non-empty runtime subset (ServerOpCount,
// nil-safe). dep may be nil: handleLostLeadership can clear s.state.pending
// concurrently with a buffered pendingSignal, so treat nil as nothing-to-apply.
//
// The baseline (lastDispatchedParsed) is deliberately NOT advanced here: the
// authoritative dispatch still diffs against it and re-applies the same `set server`
// (idempotent), and any force-reload ships the body that already carries the
// address. Clobber-safe: a concurrent reload may replace the worker the `set server`
// landed on, but the push retries across the reload onto the new worker, and the
// next structural deploy re-renders the body WITH the new address — never permanently
// lost (config-driven; no server-state-file — ADR-0011). Best-effort: applyRuntimeRaw
// swallows per-endpoint failures and the scheduled deploy converges the pods.
//
// The push body is NOT the pending render (issue #84): dep may be structural
// (a pod rotation coalesced with another tenant's new routes), and the
// dataplane writes the skip_version body to disk verbatim without a reload —
// where it can clobber an in-flight force_reload deploy's write between the
// write and the master's re-exec read (mode A: fresh workers activating
// pre-route configs) or park un-activated structural content that a later
// sync's runtime-only diff "successfully" skips the reload for (mode B: routes
// 404 until an unrelated reload). The body is the last-DISPATCHED config —
// which dep.runtimeUpdates was diffed against — patched with ONLY the
// runtime-eligible server lines from the pending render, so disk always stays
// "last activated config + runtime updates". Computed once per apply, shared
// across pods. Same wire behavior as before (one skip_version push + actions);
// only the body content differs.
func (s *DeploymentScheduler) applyRuntimeSubset(ctx context.Context, dep *scheduledDeployment) {
	if dep == nil || dep.runtimeUpdates.ServerOpCount() == 0 {
		return
	}

	s.schedulerMutex.Lock()
	baseline := s.lastActivatedConfig
	// A structural deploy in flight has already written its render to disk. Patching
	// the older ACTIVATED config would roll that write back — the deploy's read-back
	// then sees its whole render missing and fails post_reload_divergence (issue #84
	// mode A). Patch what it wrote instead, so the only on-disk difference is the
	// runtime-eligible server line, which the read-back tolerates by design. This is
	// also the config lastDispatchedParsed describes — the baseline runtimeUpdates
	// was diffed against — so body and diff finally share one base.
	inFlight := s.state.deployInFlight
	if inFlight {
		baseline = s.lastDispatchedConfig
	}
	s.schedulerMutex.Unlock()
	if baseline == "" {
		// Nothing proven activated (cold start, or invalidated after a failed
		// deploy): there is no running config to patch, and the pending is (or
		// will be re-classified) structural — the scheduled deploy converges.
		return
	}

	s.runtimeBypass.applyRuntimeRaw(ctx, dep, bypassPush{
		body:    dep.runtimeUpdates.BuildRuntimeBypassBody(baseline, dep.config),
		partial: true,
		// The structural half of this body is on disk but NOT loaded by any worker
		// until the in-flight deploy's reload lands, so the push proves nothing about
		// the running state. Recording it as activated is what let a later empty diff
		// skip the reload over parked content (issue #76).
		unproven: inFlight,
		// Abandon retry storms once a newer render replaced this pending: its
		// own apply (or the authoritative dispatch) carries fresher state.
		superseded: func() bool {
			s.schedulerMutex.Lock()
			defer s.schedulerMutex.Unlock()
			return s.state.pending != dep
		},
	})
}

// waitDeployInterval enforces minDeploymentInterval before a STRUCTURAL deploy,
// measured from the last structural deploy's end, while keeping endpoint changes
// flowing to the live workers throughout the wait.
//
// Only a STRUCTURAL pending sleeps (wait>0); a runtime-raw INITIAL pending has
// wait==0 and is dispatched immediately by the loop. Throughout — before the
// wait<=0 short-circuit AND on every newer pending that arrives mid-sleep — it
// applies the latest render's runtime-eligible server subset to the live workers
// via applyRuntimeSubset (set server + skip_reload push, no reload). That apply is
// LANE-INDEPENDENT: once a structural deploy has completed, lastDispatchedParsed
// has advanced, so a newly-Ready pod's render that arrives during the interval
// sleep classifies laneRuntimeRaw — and its slot fill must NOT wait out the
// interval (gating the mid-sleep apply on laneStructural was the residual
// rolling-restart 503: the runtime-raw render was skipped and the new pod's
// address only reached HAProxy when the interval elapsed, by which time the dying
// old slot had exhausted `option redispatch`). When it does sleep, the original
// timer is never reset, so the structural reload still fires at the interval
// deadline and reloads can't burst. Flap-safe: no deploy is in flight when the
// subset is applied, and the apply is carried across any later reload by
// retry-across-reload + the re-rendered body.
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

	// Fast-track the pending render's runtime-eligible server subset to the live
	// workers immediately — BEFORE the wait<=0 short-circuit below — so a pod-IP
	// rotation reaches HAProxy in ~ms whether or not the deploy is interval-gated.
	// Lane-independent (see applyRuntimeSubset): the pending may be runtime-raw (a
	// newly-Ready pod diffed against an already-dispatched structural baseline), and
	// that is exactly the render whose slot fill must not be trapped behind the
	// interval. No-op for renders with no runtime-eligible server change.
	s.applyRuntimeSubset(ctx, pending)

	if wait <= 0 {
		return true
	}
	s.logger.Debug("Enforcing minimum deployment interval",
		"sleep_duration_ms", wait.Milliseconds(),
		"min_interval_ms", s.minDeploymentInterval.Milliseconds())

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
			s.applyRuntimeSubset(ctx, latest)
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
		// This lane reloads nothing: the push body plus its runtime actions ARE
		// the activation, so dispatched and activated coincide here. The
		// structural lane advances activated only on completion.
		s.lastActivatedConfig = dep.config
		s.schedulerMutex.Unlock()

		// A pure runtime-raw deploy reloads nothing, so this apply IS the complete
		// deploy: resolve the HAProxyCfg identity so the bypass can advance the
		// pod's status.deployedToPods[].Checksum on success. (The pre-interval
		// apply of a STRUCTURAL render's runtime subset leaves these empty — its
		// reload is still pending, so the Component publishes the status after it.)
		dep.runtimeConfigName, dep.runtimeConfigNamespace = s.resolveRuntimeConfigName()

		// Not partial: a pure runtime-raw deploy reloads nothing and is the complete
		// deploy, so it publishes the deployed config + per-pod status. The body is
		// the render itself — by lane construction it differs from the activated
		// baseline ONLY in runtime-eligible server fields, so it already IS
		// "baseline + runtime patches" (the issue #84 bypass-body invariant), and
		// pushing the full fresh render lets the restamp prove disk == running.
		s.runtimeBypass.applyRuntimeRaw(ctx, dep, bypassPush{
			body: dep.config,
			// Abandon retry storms once a newer render is pending: it will be
			// dispatched right after this apply returns.
			superseded: func() bool {
				s.schedulerMutex.Lock()
				defer s.schedulerMutex.Unlock()
				return s.state.pending != nil
			},
		})
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

// awaitCompletion blocks until the in-flight structural deploy completes or times
// out (handleDeploymentCompleted and checkDeploymentTimeout both record the end
// state and signal s.completed), or the context is cancelled. Returns false only
// on shutdown.
//
// While it waits it stays responsive to newer renders: when a render arrives
// mid-deploy (pendingSignal), it applies that render's runtime-eligible server
// subset to the live workers immediately via a PARTIAL runtime apply, so a pod that
// goes Ready during an unrelated tenant's structural reload converges in ~ms instead
// of waiting out the whole in-flight deploy (the residual rolling-restart 503 gap).
// The pending is NOT consumed here — the loop grabs it on its next cycle after
// completion and dispatches it authoritatively; this only fast-tracks the server
// addresses onto the workers in the meantime.
//
// It deliberately does NOT drain s.completed in the pendingSignal branch: the
// pre-dispatch drain in dispatchPending is the only one, and a second drain here
// could swallow THIS deploy's completion signal and hang the loop forever.
func (s *DeploymentScheduler) awaitCompletion(ctx context.Context) bool {
	for {
		select {
		case <-s.completed:
			return true
		case <-s.pendingSignal:
			// A newer render arrived while the structural deploy is in flight
			// (latest-wins already swapped state.pending). Apply its runtime subset
			// to the live workers now (partial); keep waiting for completion.
			s.schedulerMutex.Lock()
			latest := s.state.pending
			s.schedulerMutex.Unlock()
			s.applyRuntimeSubset(ctx, latest)
		case <-ctx.Done():
			return false
		}
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

// invalidateDispatchBaselineLocked drops the lane-classification baseline and
// downgrades any parked pending to the structural lane, clearing its stale
// runtime diff. Call it (under schedulerMutex) whenever a dispatched deploy is
// not known to have landed on every pod — a completion with failures, or a
// timeout. The baseline (last-DISPATCHED render) then no longer reflects the
// pods' running state: without the reset, a pending whose runtime-raw lane was
// frozen against the unlanded render dispatches silently, restamps the config
// version header over disk content the workers never loaded, and the fast
// retry trusts the empty diff — a 0-op "success" that leaves structural config
// parked unreloaded until the next unrelated change (issue #76).
// classifyLane(nil, …) is always structural, so no runtime-raw dispatch (and
// no restamp) can occur until a deploy completes cleanly — exactly the
// restamp's safety precondition (disk == running).
func (s *DeploymentScheduler) invalidateDispatchBaselineLocked() {
	s.lastDispatchedParsed = nil
	s.lastDispatchedConfig = ""
	// The activated baseline goes with it: a deploy that did not land
	// everywhere leaves at least one pod running something else, so there is no
	// single config that is proven to be running (#112).
	s.lastActivatedConfig = ""
	if s.state.pending != nil {
		s.state.pending.lane = laneStructural
		s.state.pending.runtimeUpdates = nil
	}
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
	// A timed-out deploy is not known to have landed on the pods — same
	// invalidation as a failed deploy (issue #76).
	s.invalidateDispatchBaselineLocked()
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

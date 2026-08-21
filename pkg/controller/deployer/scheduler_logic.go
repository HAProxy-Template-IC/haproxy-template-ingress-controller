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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// scheduleOrQueue either queues a deployment if one is in progress, or schedules it immediately.
//
// This prevents concurrent deployments which can cause version conflicts.
// Uses a "latest wins" pattern where pending deployments overwrite each other.
//
// `contentChecksum` MUST be captured by the caller at the same point `config`
// is captured (i.e. from the same TemplateRenderedEvent snapshot) and threaded
// through unchanged. Re-reading it from
// `s.lastContentChecksum` at deploy-time creates a race window where a fresh
// reconcile mutates the field, the wrong hash gets recorded as "deployed",
// and the next reconcile producing that hash incorrectly skips.
func (s *DeploymentScheduler) scheduleOrQueue(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	endpoints []dataplane.Endpoint,
	reason string,
	correlationID string,
	statusPatches []templating.StatusPatch,
	coalescible bool,
	contentChecksum string,
	plan *renderplan.Plan,
	planID string,
) {
	s.schedulerMutex.Lock()
	if contextCancelled(ctx) {
		s.schedulerMutex.Unlock()
		return
	}
	s.workRevision++
	workRevision := s.workRevision
	s.schedulerMutex.Unlock()

	s.installPending(ctx, &scheduledDeployment{
		workRevision:    workRevision,
		config:          config,
		auxFiles:        auxFiles,
		plan:            plan,
		planID:          planID,
		endpoints:       endpoints,
		reason:          reason,
		correlationID:   correlationID,
		statusPatches:   statusPatches,
		coalescible:     coalescible,
		contentChecksum: contentChecksum,
	})
}

// installPending overwrites the single pending slot with this render and wakes
// the deploy loop. Latest-wins: a render that arrives while another is pending
// supersedes it, so the loop always dispatches the newest desired state.
func (s *DeploymentScheduler) installPending(ctx context.Context, dep *scheduledDeployment) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	if !s.pendingRevisionCurrentLocked(ctx, dep) {
		return
	}
	s.state.pending = dep

	// Signalling under the lock is fine — signalLoop is a non-blocking cap-1 send.
	s.signalLoop()
}

func (s *DeploymentScheduler) pendingRevisionCurrentLocked(ctx context.Context, dep *scheduledDeployment) bool {
	if contextCancelled(ctx) || dep.workRevision != s.workRevision {
		return false
	}
	return dep.retryGeneration == 0 || (!s.retryStopped && dep.retryGeneration == s.retryGeneration)
}

func contextCancelled(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil
}

// runDeployLoop is the single goroutine that dispatches deployments. Event
// handlers only set state.pending (latest-wins) and wake it via signalLoop, so
// two deployments can never be in flight at once and the newest render is the
// one that goes out. Runs for the whole leadership term; exits on ctx.Done().
//
// Reload pacing belongs to the agent (--reload-interval-min), which coalesces
// reloads without holding back the applies that need none; this loop never
// waits for a window, so an endpoint change reaches the running workers as
// soon as it is rendered.
func (s *DeploymentScheduler) runDeployLoop(ctx context.Context) {
	defer func() {
		s.schedulerMutex.Lock()
		s.workRevision++
		s.state.pending = nil
		s.schedulerMutex.Unlock()
		close(s.loopDone)
	}()

	for {
		if contextCancelled(ctx) {
			return
		}
		s.schedulerMutex.Lock()
		dep := s.state.pending
		s.state.pending = nil
		s.schedulerMutex.Unlock()

		if dep == nil {
			select {
			case <-s.pendingSignal:
			case <-ctx.Done():
				return
			}
			continue
		}
		if !s.dispatchPending(ctx, dep) {
			return // ctx cancelled
		}
	}
}

// dispatchPending publishes one DeploymentScheduledEvent and blocks until its
// completion, timeout or shutdown. Returns false only on ctx cancellation.
func (s *DeploymentScheduler) dispatchPending(ctx context.Context, dep *scheduledDeployment) bool {
	if contextCancelled(ctx) {
		return false
	}

	scheduledEvent := s.newScheduledEvent(dep)
	s.schedulerMutex.Lock()
	if !s.pendingRevisionCurrentLocked(ctx, dep) {
		s.schedulerMutex.Unlock()
		return !contextCancelled(ctx)
	}
	s.state.deployInFlight = true
	s.state.deploymentTimedOut = false
	s.state.deploymentStartTime = time.Now()
	s.state.activeDeploymentID = scheduledEvent.EventID()
	s.state.activeCorrelationID = dep.correlationID
	s.lastPodSetHash = computePodSetHash(dep.endpoints)
	s.schedulerMutex.Unlock()

	if contextCancelled(ctx) {
		s.clearDispatchedPending(scheduledEvent.EventID())
		return false
	}

	s.publishScheduled(scheduledEvent)
	return s.awaitCompletion(ctx)
}

func (s *DeploymentScheduler) clearDispatchedPending(deploymentID string) {
	s.schedulerMutex.Lock()
	defer s.schedulerMutex.Unlock()
	if s.state.activeDeploymentID != deploymentID {
		return
	}
	s.state.deployInFlight = false
	s.state.deploymentTimedOut = false
	s.state.deploymentStartTime = time.Time{}
	s.state.activeDeploymentID = ""
	s.state.activeCorrelationID = ""
}

// awaitCompletion blocks until the exact in-flight deploy reports termination
// or the context is cancelled. A timeout requests cancellation but keeps the
// slot owned until that acknowledgement arrives.
//
// A render arriving mid-deploy only sets state.pending; the loop picks it up on
// its next cycle. Reading s.completed is deliberately the only way out: a
// pendingSignal branch could swallow this deploy's accepted completion and hang
// the loop.
func (s *DeploymentScheduler) awaitCompletion(ctx context.Context) bool {
	select {
	case <-s.completed:
		return true
	case <-ctx.Done():
		return false
	}
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

// resolveRuntimeConfigName returns the HAProxyCfg resource name + namespace whose
// status.deployedToPods a deploy advances. It prefers the value set by
// ConfigPublishedEvent and falls back to the deterministic name derived from the
// template-config name when that event hasn't landed yet — avoiding a wait on the
// K8s API call that publishes the HAProxyCfg resource.
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

// newScheduledEvent builds the deploy event. `dep.contentChecksum` was captured
// at schedule-time with the config, so it labels THIS deploy's content
// correctly regardless of newer reconciles.
func (s *DeploymentScheduler) newScheduledEvent(dep *scheduledDeployment) *events.DeploymentScheduledEvent {
	runtimeConfigName, runtimeConfigNamespace := s.resolveRuntimeConfigName()
	// ParsedConfig is the Dataplane API's desired-state input; the agent path
	// reads the render plan instead.
	return events.NewDeploymentScheduledEvent(
		dep.config, dep.auxFiles, nil, dep.endpoints, runtimeConfigName, runtimeConfigNamespace,
		dep.reason, dep.contentChecksum, dep.plan, dep.planID, dep.statusPatches, dep.coalescible,
		events.WithCorrelation(dep.correlationID, dep.correlationID),
	)
}

func (s *DeploymentScheduler) publishScheduled(event *events.DeploymentScheduledEvent) {
	s.logger.Debug("Scheduling deployment",
		"reason", event.Reason,
		"endpoint_count", len(event.Endpoints),
		"config_bytes", len(event.Config),
		"plan", event.PlanID,
		"deployment_id", event.EventID(),
		"correlation_id", event.CorrelationID())

	s.eventBus.Publish(event)
}

// checkDeploymentTimeout checks if the current deployment has exceeded the timeout.
//
// If a deployment exceeds the configured timeout, this method keeps publishing
// cancellation for that attempt and triggers one recovery reconciliation. The
// slot remains owned until the matching completion acknowledges termination.
func (s *DeploymentScheduler) checkDeploymentTimeout(_ context.Context) {
	s.schedulerMutex.Lock()
	if !s.state.deployInFlight {
		s.schedulerMutex.Unlock()
		return
	}
	startTime := s.state.deploymentStartTime
	activeDeploymentID := s.state.activeDeploymentID
	activeCorrelationID := s.state.activeCorrelationID

	// Skip if deployment hasn't started yet (startTime is zero)
	if startTime.IsZero() {
		s.schedulerMutex.Unlock()
		return
	}

	elapsed := time.Since(startTime)
	if elapsed <= s.deploymentTimeout {
		s.schedulerMutex.Unlock()
		return
	}
	firstTimeout := !s.state.deploymentTimedOut
	if firstTimeout {
		s.state.deploymentTimedOut = true
	}
	s.schedulerMutex.Unlock()

	if firstTimeout {
		s.logger.Warn("Deployment timed out; waiting for termination",
			"duration_ms", elapsed.Milliseconds(),
			"timeout_ms", s.deploymentTimeout.Milliseconds(),
			"deployment_id", activeDeploymentID,
			"correlation_id", activeCorrelationID)
	}

	// Re-publish until the exact deployment acknowledges termination. Cancellation
	// is idempotent and a dropped control event must not release newer work.
	if activeDeploymentID != "" {
		s.eventBus.Publish(events.NewDeploymentCancelRequestEvent(
			activeDeploymentID,
			"deployment_timeout",
			events.WithCorrelation(activeCorrelationID, activeDeploymentID),
		))
	}

	// Trigger a new reconciliation to recover from the stuck state (e.g. a lost
	// DeploymentCompletedEvent). Not coalescible — it must be processed.
	if firstTimeout {
		s.eventBus.Publish(events.NewReconciliationTriggeredEvent("deployment_timeout_recovery", false, events.WithNewCorrelation()))
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (s *DeploymentScheduler) HealthCheck() error {
	return s.healthTracker.Check()
}

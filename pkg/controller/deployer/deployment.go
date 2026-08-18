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
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// deployRequest is one deployment's desired state, identical for every pod.
type deployRequest struct {
	plan            *renderplan.Plan
	planID          string
	contents        map[string]string // file content by digest, the manifest's join key
	blob            []byte            // the plan, zstd-compressed, as the agent stores it
	token           api.Token
	validatedPlanID string
	// verify makes each pod re-hash its tree before it reports: the drift pass
	// asks what is on disk, not what the agent last wrote.
	verify bool
	diffs  *diffMemo
}

// performDeployment executes a single deployment.
//
// This method is called from HandleEvent for each dispatched
// DeploymentScheduledEvent. "Latest wins" coalescing of pending coalescible
// DeploymentScheduledEvents is provided by the embedded component.Base via
// the CoalescesOn hook (see component.go).
//
// Defensive: drops duplicate events if a deployment is already in progress.
func (c *Component) performDeployment(ctx context.Context, event *events.DeploymentScheduledEvent) {
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	correlationID := event.CorrelationID()
	deploymentID := event.EventID()

	// Defensive check: atomically set deploymentInProgress from false to true.
	// This prevents concurrent deployments if the scheduler has bugs.
	if !c.deploymentInProgress.CompareAndSwap(false, true) {
		c.Logger().Error("Dropping duplicate DeploymentScheduledEvent - deployment already in progress",
			"reason", event.Reason,
			"endpoint_count", len(event.Endpoints),
			"correlation_id", correlationID)
		return
	}

	deployCtx, cancel := c.beginDeployment(ctx, deploymentID, correlationID)
	defer cancel()

	defer func() {
		c.cancelMu.Lock()
		c.activeDeploymentID = ""
		c.activeCorrelationID = ""
		c.activeCancelFunc = nil
		if c.deploymentDone != nil {
			close(c.deploymentDone)
			c.deploymentDone = nil
		}
		c.cancelMu.Unlock()
	}()

	c.Logger().Debug("Deployment scheduled, starting execution",
		"reason", event.Reason,
		"endpoint_count", len(event.Endpoints),
		"config_bytes", len(event.Config),
		"plan", event.PlanID,
		"deployment_id", deploymentID,
		"correlation_id", correlationID)

	c.deployToEndpoints(deployCtx, cancel, event, deploymentID)
}

// deployToEndpoints applies the render to every pod, at most maxConcurrentPods
// at a time, and reports the fleet's answer.
func (c *Component) deployToEndpoints(
	ctx context.Context,
	standDown context.CancelFunc,
	event *events.DeploymentScheduledEvent,
	deploymentID string,
) {
	defer c.deploymentInProgress.Store(false)

	startTime := time.Now()
	correlationID := event.CorrelationID()
	if len(event.Endpoints) == 0 || event.Plan == nil {
		c.reportUndeployable(event, deploymentID)
		return
	}

	// contentChecksum covers config plus auxiliary files, so it is the value
	// HAProxyCfg.spec.Checksum is comparable to. Plan ids do not replace it:
	// aux bytes outside the plan still ride it.
	podSetHash := computePodSetHash(event.Endpoints)
	request := c.newDeployRequest(event)

	c.EventBus().Publish(events.NewDeploymentStartedEvent(
		len(event.Endpoints),
		events.WithCorrelation(correlationID, deploymentID),
	))

	state := &deploymentState{standDown: standDown, operationBreakdown: map[string]int{}}
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConcurrentPods)
	for i := range event.Endpoints {
		wg.Add(1)
		go func(endpoint *dataplane.Endpoint) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			c.deployToPod(ctx, endpoint, request, event, state)
		}(&event.Endpoints[i])
	}
	wg.Wait()

	c.plans.Retain(c.fleetPlanRefs(event.Endpoints))
	c.clients.Retain(event.Endpoints)
	c.recordFleetAck(event.Plan, atomic.LoadInt32(&state.ackCount))

	c.Logger().Debug("Deployment completed",
		"total_endpoints", len(event.Endpoints),
		"converged", atomic.LoadInt32(&state.convergedCount),
		"failed", atomic.LoadInt32(&state.failureCount),
		"reloads_triggered", atomic.LoadInt32(&state.reloadsTriggered),
		"duration_ms", time.Since(startTime).Milliseconds(),
		"correlation_id", correlationID)

	c.publishCompleted(event, deploymentID, podSetHash, state, time.Since(startTime).Milliseconds())
	c.publishDeployedConfig(event, int(atomic.LoadInt32(&state.ackCount)))
	c.observeConvergence(event, podSetHash, state)
}

// reportUndeployable completes a deployment that cannot be executed: a render
// without a plan carries no file set, and a fleet without pods has no target.
// Both report through the normal completion so the scheduler is never wedged.
func (c *Component) reportUndeployable(event *events.DeploymentScheduledEvent, deploymentID string) {
	result := &events.DeploymentResult{DeploymentID: deploymentID, StatusPatches: event.StatusPatches}
	if event.Plan == nil && len(event.Endpoints) > 0 {
		c.Logger().Error("Render carries no plan; the agent needs one to apply it",
			"endpoint_count", len(event.Endpoints),
			"correlation_id", event.CorrelationID())
		result.Total = len(event.Endpoints)
		result.Failed = len(event.Endpoints)
	} else {
		c.Logger().Error("No valid endpoints to deploy to")
	}
	c.EventBus().Publish(events.NewDeploymentCompletedEvent(result,
		events.WithCorrelation(event.CorrelationID(), deploymentID)))
}

func (c *Component) newDeployRequest(event *events.DeploymentScheduledEvent) *deployRequest {
	c.plans.Put(event.Plan)
	blob, err := planblob.Encode(event.Plan)
	if err != nil {
		// Without the blob a pod that outlives this controller reports a
		// baseline nobody can decode, which costs it one reload — never
		// correctness, so the deployment goes ahead.
		c.Logger().Error("Encoding the plan blob failed; pods will not retain this baseline",
			"plan", event.PlanID, "error", err)
	}
	return &deployRequest{
		plan:            event.Plan,
		planID:          event.PlanID,
		contents:        contentsByDigest(event.Config, event.AuxiliaryFiles),
		blob:            blob,
		token:           api.Token{LeaderEpoch: c.leaderEpoch(), RenderSeq: c.nextRenderSeq()},
		validatedPlanID: c.validatedPlan(),
		verify:          event.Reason == events.TriggerReasonDriftPrevention,
		diffs:           newDiffMemo(),
	}
}

// contentsByDigest indexes every rendered byte string by its plan digest. The
// digest is the manifest's join key, so no consumer re-derives the path
// conventions the render used.
func contentsByDigest(config string, aux *dataplane.AuxiliaryFiles) map[string]string {
	contents := map[string]string{renderplan.DigestString(config): config}
	if aux == nil {
		return contents
	}
	add := func(content string) { contents[renderplan.DigestString(content)] = content }
	for i := range aux.MapFiles {
		add(aux.MapFiles[i].Content)
	}
	for i := range aux.SSLCertificates {
		add(aux.SSLCertificates[i].Content)
	}
	for i := range aux.SSLCaFiles {
		add(aux.SSLCaFiles[i].Content)
	}
	for i := range aux.CRTListFiles {
		add(aux.CRTListFiles[i].Content)
	}
	for i := range aux.GeneralFiles {
		add(aux.GeneralFiles[i].Content)
	}
	return contents
}

// recordFleetAck hands the renderer the plan the fleet now runs — one pod that
// took it is enough, the rest converge on the same render. Called directly, not
// over the bus: the next render must read it (ADR-0001).
func (c *Component) recordFleetAck(plan *renderplan.Plan, acked int32) {
	if plan == nil || c.ackedPlans == nil || acked == 0 {
		return
	}
	c.ackedPlans.SetAckedPlan(plan)
}

// deploymentState aggregates what the pods answered. Counts are atomic; the
// mutex guards the maps and slices.
type deploymentState struct {
	standDown context.CancelFunc

	ackCount         int32 // pods that accepted the apply
	convergedCount   int32 // pods now running the render
	failureCount     int32
	reloadsTriggered int32
	pendingReloads   int32 // pods holding the render behind a paced reload

	mu                 sync.Mutex
	totalOperations    int
	operationBreakdown map[string]int
	stoodDown          bool
	pendingReloadUntil time.Time
	running            map[string]string // pod → the plan its worker runs, from its ACK
}

// noteRunning records which plan a pod's worker runs after its apply.
func (s *deploymentState) noteRunning(endpoint *dataplane.Endpoint, planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]string{}
	}
	s.running[podKey(endpoint)] = planID
}

// fleetRunningPlan is the plan every pod reported running, or "" when a pod
// did not answer or the fleet disagrees.
func (s *deploymentState) fleetRunningPlan(total int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if total == 0 || len(s.running) != total {
		return ""
	}
	consensus := ""
	for _, planID := range s.running {
		switch {
		case planID == "":
			return ""
		case consensus == "":
			consensus = planID
		case consensus != planID:
			return ""
		}
	}
	return consensus
}

// notePendingReload records a pod that scheduled its reload for later.
func (s *deploymentState) notePendingReload(scheduledAt string) {
	atomic.AddInt32(&s.pendingReloads, 1)
	due, err := time.Parse(time.RFC3339, scheduledAt)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if due.After(s.pendingReloadUntil) {
		s.pendingReloadUntil = due
	}
}

// deployToPod applies the render to one pod and folds its answer into state.
func (c *Component) deployToPod(
	ctx context.Context,
	endpoint *dataplane.Endpoint,
	request *deployRequest,
	event *events.DeploymentScheduledEvent,
	state *deploymentState,
) {
	if ctx.Err() != nil {
		c.Logger().Debug("Skipping endpoint deployment - context cancelled",
			"pod", endpoint.PodName, "error", ctx.Err())
		atomic.AddInt32(&state.failureCount, 1)
		return
	}

	start := time.Now()
	outcome, err := c.applyToPod(ctx, endpoint, request)
	durationMs := time.Since(start).Milliseconds()

	switch {
	case errors.Is(err, errStaleEpoch):
		c.standDown(state, endpoint, err)
	case err != nil:
		c.handleEndpointFailure(endpoint, err, durationMs, event, state)
	// An apply that reports an error did not do what it was asked, whether or
	// not the agent called the transaction a success: a refused in-place batch
	// answers OK with an error and no applied plan.
	case !outcome.result.OK || outcome.result.Error != nil:
		c.handleEndpointRejection(endpoint, outcome, event, state)
	default:
		c.handleEndpointSuccess(endpoint, outcome, durationMs, event, state)
	}
}

// standDown stops dispatching: another controller holds a higher leader epoch,
// so this one is not the fleet's writer any more. Losing the epoch race is
// losing leadership, and it is given up for real — a replica that only stopped
// dispatching keeps renewing its Lease, and nothing would ever re-arm it.
func (c *Component) standDown(state *deploymentState, endpoint *dataplane.Endpoint, err error) {
	atomic.AddInt32(&state.failureCount, 1)

	state.mu.Lock()
	first := !state.stoodDown
	state.stoodDown = true
	state.mu.Unlock()
	if !first {
		return
	}

	c.Logger().Error("A newer leader epoch owns the fleet, standing down",
		"pod", endpoint.PodName, "identity", c.identity(), "error", err)
	state.standDown()
	if c.fence == nil {
		// No Lease to release: the leadership this reports is nominal, and the
		// event is all the leader-only components have to stop on.
		c.EventBus().Publish(events.NewLostLeadershipEvent(standaloneIdentity, "stale_leader_epoch"))
		return
	}
	c.fence.StandDown("stale_leader_epoch")
}

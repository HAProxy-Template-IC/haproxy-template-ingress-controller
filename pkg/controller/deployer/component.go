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

// Package deployer applies a validated render to the HAProxy fleet through the
// HAPTIC agent that owns each pod's file tree.
//
// The Deployer is a stateless executor: it receives DeploymentScheduledEvent
// and, per pod, reads the agent's baseline, diffs the render against it
// (pkg/dataplane/deployplan) and sends the resulting fenced applies. All
// scheduling, rate limiting and queueing lives in the DeploymentScheduler.
package deployer

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "deployer"

	// EventBufferSize is the size of the event subscription buffer.
	// Low-volume component (~1-2 deployment events per reconciliation cycle).
	EventBufferSize = busevents.StandardSubscriberBuffer

	cancellationSubscriberName = ComponentName + "-cancellation"

	// standaloneIdentity names this controller when leader election is off.
	standaloneIdentity = "standalone"
)

// LeadershipFence is the current leadership term: who this controller is, the
// epoch every apply it sends is fenced by, and the two answers to a pod that
// refuses that epoch. A pod that has seen a higher epoch refuses this
// controller's writes.
type LeadershipFence interface {
	Identity() string
	LeaderEpoch() uint64
	// Reclaim lifts the epoch past one a pod already accepted. It errors when
	// a newer leader — not a regressed counter — is behind the refusal, which
	// is the one case where the pod is right and this controller must stop.
	Reclaim(ctx context.Context, floor uint64) (uint64, error)
	// StandDown gives leadership up so a fresh term claims a fresh epoch.
	StandDown(reason string)
}

// Component implements the deployer component.
//
// Event subscriptions:
//   - DeploymentScheduledEvent: apply the render to the scheduled endpoints
//   - DeploymentCancelRequestEvent: cancel an in-progress deployment through a
//     separate control loop that stays responsive while execution blocks
type Component struct {
	*component.Base
	*component.ReadySignal

	deploymentInProgress atomic.Bool // Defensive: prevents concurrent deployments if the scheduler has bugs

	// ctx is the event-loop context captured by Start. Handlers run only on the
	// loop goroutine and use it for agent calls, so applies abort on shutdown.
	ctx context.Context

	clients *agentClients
	plans   *planCache
	fence   LeadershipFence

	// renderSeq orders this leadership term's applies inside its epoch. It
	// restarts per term, which the epoch makes unambiguous.
	renderSeq atomic.Uint64

	healthTracker *lifecycle.HealthTracker

	// metrics records the per-pod apply counters directly rather than over the
	// bus: each has exactly one subscriber that only increments (ADR-0001 — no
	// event hop without a second participant). Nil in tests.
	metrics *metrics.Metrics

	// ackedPlans receives the plan a deployment landed on at least one pod, so
	// the renderer reads the fleet's state instead of its own last render. Nil
	// in tests.
	ackedPlans AckedPlanSink

	// baselineSeeded is set once this term has adopted (or given up on) the
	// plan the fleet was already running. See seedBaseline.
	baselineSeeded atomic.Bool

	// stateMu guards the per-fleet facts the apply path reads: which pods must
	// be re-sent their complete state, which plans are proven good, which plans
	// each pod last reported it holds, and the pods this controller last wrote
	// to (the revert's search space).
	stateMu         sync.Mutex
	invalidBaseline map[string]struct{}
	validatedPlans  *validatedPlanSet
	observedPlans   map[string][]string
	fleet           []dataplane.Endpoint
	// awaiting are the renders the fleet accepted behind a paced reload, in
	// dispatch order, until a later deployment observes the fleet running one.
	awaiting []awaitingRender

	// Deployment cancellation support
	cancelMu            sync.Mutex
	activeDeploymentID  string                 // Event ID of the active DeploymentScheduledEvent
	activeCorrelationID string                 // Trace correlation of the active deployment
	activeCancelFunc    context.CancelFunc     // Cancel function for active deployment
	deploymentDone      chan struct{}          // Signals when deployment goroutine completes
	pendingCancellation string                 // Exact deployment cancelled before its handler starts
	cancelEventChan     <-chan busevents.Event // Out-of-band control subscription
}

// New creates a new Deployer component.
//
// syncTimeout bounds one pod's apply; the agent's own reload deadline is a
// chart-templated agent flag, not a client timeout. domainMetrics may be nil in
// tests; the per-pod counters are then not recorded.
func New(eventBus *busevents.EventBus, logger *slog.Logger, syncTimeout time.Duration, domainMetrics *metrics.Metrics) *Component {
	c := &Component{
		ReadySignal:     component.NewReadySignal(),
		clients:         newAgentClients(agentStateTimeout, syncTimeout),
		plans:           newPlanCache(),
		invalidBaseline: map[string]struct{}{},
		observedPlans:   map[string][]string{},
		validatedPlans:  newValidatedPlanSet(),
		healthTracker:   lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		metrics:         domainMetrics,
	}
	// Subscription happens here, at construction (component.Base), before
	// EventBus.Start(). The Deployer remains a leader-only component: its event
	// loop only runs once Start() is called after leadership is acquired, and
	// the subscribed event types are published only by the leader-only
	// DeploymentScheduler.
	c.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeDeploymentScheduled,
			events.EventTypeRenderGateCompleted,
			events.EventTypeHAProxyPodsDiscovered,
		},
	})
	c.cancelEventChan = eventBus.SubscribeTypes(cancellationSubscriberName, EventBufferSize,
		events.EventTypeDeploymentCancelRequest)
	return c
}

// agentStateTimeout bounds a /v1/state read. The agent answers it from memory
// (or one tree hash on the drift pass), so a slow answer is a sick pod.
const agentStateTimeout = 10 * time.Second

// Start begins the deployer's event loop and blocks until ctx is cancelled.
func (c *Component) Start(ctx context.Context) error {
	defer c.Rearm()
	// Discard events buffered before this leadership term. The construction-
	// time subscription persists across terms, so DeploymentScheduledEvents
	// queued when leadership was lost would otherwise replay a stale deployment
	// into the new term. Must run before MarkReady.
	c.FlushPending()
	c.flushPendingCancellationRequests()
	c.cancelMu.Lock()
	c.pendingCancellation = ""
	c.cancelMu.Unlock()

	// Signal that subscription is complete for the SubscriptionReadySignaler
	// interface. Subscription itself happened at construction (component.Base),
	// so the signal can fire before the loop starts.
	c.MarkReady()

	// A new term dials fresh: the previous leader's pooled connections carry no
	// state worth keeping, and its apply sequence restarts under a new epoch.
	c.clients.Close()
	c.renderSeq.Store(0)
	c.baselineSeeded.Store(false)
	c.clearBaselineInvalidations()
	c.forgetAwaitingConvergence()

	c.ctx = ctx
	controlCtx, stopControl := context.WithCancel(ctx)
	controlDone := make(chan struct{})
	go c.runCancellationLoop(controlCtx, controlDone)
	err := c.Base.Start(ctx)

	stopControl()
	c.cancelActiveDeployment("shutdown")
	<-controlDone
	c.clients.Close()
	return err
}

// HandleEvent implements component.EventHandler: it routes events to the
// appropriate handler.
func (c *Component) HandleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.DeploymentScheduledEvent:
		c.performDeployment(c.ctx, e)
	case *events.RenderGateCompletedEvent:
		c.handleRenderGateCompleted(c.ctx, e)
	case *events.HAProxyPodsDiscoveredEvent:
		c.seedBaseline(c.ctx, e.Endpoints)
	}
}

// CoalescesOn implements component.CoalescingHandler: after each dispatch, the
// embedded component.Base drains the subscription channel and processes only
// the LATEST pending coalescible DeploymentScheduledEvent ("latest wins").
//
// This coalescing is load-bearing: deployment is single-threaded, but the
// validator + scheduler upstream can fire many DeploymentScheduledEvents during
// a single deployment. Without it, the deployer would process every queued
// event in FIFO order — deploying the OLDEST pending config first and falling
// further and further behind under load.
func (c *Component) CoalescesOn() []string {
	return []string{events.EventTypeDeploymentScheduled}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle - idle is always healthy for event-driven components.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

// SetValidatedPlan records a plan the controller proved good.
//
// It is a set, not a single value: the gate checks superseded plans that pods
// still run as well as the newest one, and their verdicts arrive in check
// order, not render order. A pod promotes its rollback baseline only when the
// manifest names the plan that pod applied (agent statemachine), so recording
// one id and overwriting it would leave every pod on another plan unable to
// promote — the fleet's oldest straggler deciding when the newest pod's
// last-known-good may advance.
func (c *Component) SetValidatedPlan(planID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.validatedPlans.add(planID)
}

// validatedPlanFor is what a pod's manifest carries: the pod's own applied plan
// when that passed — which is the only value that promotes its baseline — and
// otherwise the newest passed plan, which is inert for this pod but keeps the
// field meaningful for a pod that catches up between the state read and the
// apply.
func (c *Component) validatedPlanFor(appliedPlanID string) string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.validatedPlans.resolve(appliedPlanID)
}

func (c *Component) leaderEpoch() uint64 {
	if c.fence == nil {
		return 0
	}
	return c.fence.LeaderEpoch()
}

func (c *Component) identity() string {
	if c.fence == nil {
		return standaloneIdentity
	}
	return c.fence.Identity()
}

func (c *Component) nextRenderSeq() uint64 {
	return c.renderSeq.Add(1)
}

// podKey identifies one pod across a container restart: a new container is a
// new tree, and a new address is a new agent.
func podKey(endpoint *dataplane.Endpoint) string {
	return endpoint.PodUID + "\x00" + endpoint.PodRuntimeID + "\x00" + endpoint.URL
}

// invalidateBaseline makes this pod's next apply carry the complete file set
// and a reload. A refused apply may have left the pod somewhere the render
// plans cannot describe, so no ops may be composed against it.
func (c *Component) invalidateBaseline(endpoint *dataplane.Endpoint) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.invalidBaseline[podKey(endpoint)] = struct{}{}
}

func (c *Component) clearBaselineInvalidation(endpoint *dataplane.Endpoint) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	delete(c.invalidBaseline, podKey(endpoint))
}

func (c *Component) baselineInvalid(endpoint *dataplane.Endpoint) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	_, invalid := c.invalidBaseline[podKey(endpoint)]
	return invalid
}

func (c *Component) clearBaselineInvalidations() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	clear(c.invalidBaseline)
	clear(c.observedPlans)
}

// notePodPlans records the plans one pod reports it holds — its state read
// before the apply, then its ACK after it. Every pod that answered at all
// contributes, not only the ones that ACKed: a pod whose apply failed still
// runs its plans, and evicting them costs it a full-state reload on the retry
// seconds later.
func (c *Component) notePodPlans(endpoint *dataplane.Endpoint, ids ...string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.observedPlans[podKey(endpoint)] = planIDs(ids)
}

// fleetPlanRefs is every plan the fleet still refers to, forgetting the pods
// that are gone. A pod this deployment could not read keeps its last answer:
// a blip that failed every pod would otherwise evict the whole cache and
// reload the fleet on the next round.
func (c *Component) fleetPlanRefs(endpoints []dataplane.Endpoint) []string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	live := make(map[string]struct{}, len(endpoints))
	for i := range endpoints {
		live[podKey(&endpoints[i])] = struct{}{}
	}
	refs := make([]string, 0, len(c.observedPlans))
	for key, ids := range c.observedPlans {
		if _, wanted := live[key]; !wanted {
			delete(c.observedPlans, key)
			continue
		}
		refs = append(refs, ids...)
	}
	return refs
}

// planIDs drops the empty and repeated ids, so one pod's entry stays the few
// plans it actually holds.
func planIDs(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}
	return kept
}

// applyPosture decides how much of the desired state one pod gets, and what to
// tell an operator about it. A contract skew is never a refusal: a
// fleet-correlated refusal would fence the repair path.
func (c *Component) applyPosture(endpoint *dataplane.Endpoint, state *api.State) (full bool, notes []string) {
	majorMismatch, missingOps := agentclient.CheckSkew(state)
	if majorMismatch || len(missingOps) > 0 {
		if c.metrics != nil {
			c.metrics.RecordAgentVersionSkew()
		}
		c.Logger().Warn("Agent speaks a different contract; sending the complete state and a reload",
			"pod", endpoint.PodName,
			"agent_api_version", state.APIVersion,
			"controller_api_version", api.Version,
			"missing_ops", len(missingOps))
		notes = append(notes, skewReason(state, majorMismatch, missingOps))
		full = true
	}
	if c.baselineInvalid(endpoint) {
		notes = append(notes, "the previous apply was rejected, resending the complete state")
		full = true
	}
	return full, notes
}

func skewReason(state *api.State, majorMismatch bool, missingOps []string) string {
	if majorMismatch {
		return fmt.Sprintf("agent speaks API version %d, this controller version %d", state.APIVersion, api.Version)
	}
	return fmt.Sprintf("agent does not execute %d of the composed op kinds", len(missingOps))
}

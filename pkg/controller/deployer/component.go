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

// Package deployer implements the Deployer component that deploys validated
// HAProxy configurations to discovered HAProxy pod endpoints.
//
// The Deployer is a stateless executor that receives DeploymentScheduledEvent
// and executes deployments to the specified endpoints. All deployment scheduling,
// rate limiting, and queueing logic is handled by the DeploymentScheduler component.
package deployer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
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
)

// Component implements the deployer component.
//
// It subscribes to DeploymentScheduledEvent and deploys configurations to
// HAProxy instances. This is a stateless executor - all scheduling logic
// is handled by the DeploymentScheduler component.
//
// Event subscriptions:
//   - DeploymentScheduledEvent: Execute deployment to specified endpoints
//   - DeploymentCancelRequestEvent: Cancel in-progress deployment through a
//     separate control loop that stays responsive while execution blocks
//
// The component publishes deployment result events for observability.
type Component struct {
	*component.Base
	*component.ReadySignal

	deploymentInProgress atomic.Bool // Defensive: prevents concurrent deployments if scheduler has bugs

	// ctx is the event-loop context captured by Start. Handlers run only
	// on the loop goroutine and use it for Dataplane API calls so syncs
	// abort on shutdown.
	ctx context.Context

	// reloadVerificationTimeout bounds how long the Dataplane sync waits for a
	// graceful reload to be reported as completed before failing the sync.
	reloadVerificationTimeout time.Duration

	// syncTimeout is the overall timeout for one Dataplane sync to a single endpoint.
	syncTimeout time.Duration

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// metrics records the two runtime-divergence counters directly rather than
	// over the bus: each had exactly one subscriber, and that subscriber only
	// incremented a counter (ADR-0001 — no event hop without a second
	// participant). Nil in tests.
	metrics *metrics.Metrics

	// versionCache caches the last-synced config version per endpoint authority.
	// Allows skipping expensive GetRawConfiguration() + parse on subsequent syncs
	// when the pod's config version hasn't changed.
	versionCache *configVersionCache

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
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - logger: Structured logger for component logging
//   - reloadVerificationTimeout: bounds how long each sync waits for HAProxy to
//     report a graceful reload as completed
//   - syncTimeout: overall per-endpoint sync timeout (parse + diff + apply +
//     optional reload-verify)
//
// Returns:
//   - A new Component instance ready to be started
//
// domainMetrics may be nil in tests; the divergence counters are then not recorded.
func New(eventBus *busevents.EventBus, logger *slog.Logger, reloadVerificationTimeout, syncTimeout time.Duration, domainMetrics *metrics.Metrics) *Component {
	c := &Component{
		ReadySignal:               component.NewReadySignal(),
		reloadVerificationTimeout: reloadVerificationTimeout,
		syncTimeout:               syncTimeout,
		versionCache:              newConfigVersionCache(),
		healthTracker:             lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		metrics:                   domainMetrics,
	}
	// Subscription happens here, at construction (component.Base), before
	// EventBus.Start(). The Deployer remains a leader-only component: its
	// event loop only runs once Start() is called after leadership is
	// acquired, and the subscribed event types (DeploymentScheduledEvent,
	// DeploymentCancelRequestEvent) are published only by the leader-only
	// DeploymentScheduler.
	c.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeDeploymentScheduled,
		},
	})
	c.cancelEventChan = eventBus.SubscribeTypes(cancellationSubscriberName, EventBufferSize,
		events.EventTypeDeploymentCancelRequest)
	return c
}

// Start begins the deployer's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (c *Component) Start(ctx context.Context) error {
	// Discard events buffered before this leadership term. The construction-
	// time subscription persists across terms, so DeploymentScheduledEvents
	// queued when leadership was lost would otherwise replay a stale
	// deployment into the new term (the old subscribe-per-term code discarded
	// them implicitly). Must run before MarkReady: current-term events only
	// start flowing once this term's scheduler renders, which is gated behind
	// the readiness signal.
	c.FlushPending()
	c.flushPendingCancellationRequests()
	c.cancelMu.Lock()
	c.pendingCancellation = ""
	c.cancelMu.Unlock()

	// Signal that subscription is complete for the SubscriptionReadySignaler
	// interface. Subscription itself happened at construction (component.Base),
	// so the signal can fire before the loop starts.
	c.MarkReady()

	// Clear version cache on start (handles leadership transitions - fresh state)
	c.versionCache.clear()

	c.ctx = ctx
	controlCtx, stopControl := context.WithCancel(ctx)
	controlDone := make(chan struct{})
	go c.runCancellationLoop(controlCtx, controlDone)
	err := c.Base.Start(ctx)

	stopControl()
	c.cancelActiveDeployment("shutdown")
	<-controlDone
	return err
}

// HandleEvent implements component.EventHandler: it routes events to the
// appropriate handler.
func (c *Component) HandleEvent(event busevents.Event) {
	if e, ok := event.(*events.DeploymentScheduledEvent); ok {
		c.performDeployment(c.ctx, e)
	}
}

// CoalescesOn implements component.CoalescingHandler: after each dispatch,
// the embedded component.Base drains the subscription channel and processes
// only the LATEST pending coalescible DeploymentScheduledEvent ("latest
// wins"), superseding intermediates. Non-coalescible events (e.g. from
// drift_prevention, validation_fallback) are always processed and never
// skipped.
//
// This coalescing is load-bearing: deployment is single-threaded, but the
// validator + scheduler upstream can fire many DeploymentScheduledEvents
// during a single deployment. Without it, the deployer would process every
// queued event in FIFO order — deploying the OLDEST pending config first
// and falling further and further behind under load.
func (c *Component) CoalescesOn() []string {
	return []string{events.EventTypeDeploymentScheduled}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
// RecordActivation records (or, with an empty proof, clears) what an apply
// proved about an endpoint's running config.
//
// Exported so the runtime-bypass path — which writes to the same pods through a
// different component — updates the same per-endpoint state the structural sync
// reads. Two independent writers to one pod with two independent notions of
// "what is running" is precisely how a config goes parked unnoticed (#112).
func (c *Component) RecordActivation(endpoint *dataplane.Endpoint, proof string) {
	c.versionCache.setActivated(endpoint, proof)
}

// RetainEndpointAuthorities evicts observations that belong to endpoints no
// longer present in the scheduler's authoritative fleet view.
func (c *Component) RetainEndpointAuthorities(endpoints []dataplane.Endpoint) {
	c.versionCache.retain(endpoints)
}

func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

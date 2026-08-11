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
	"crypto/sha256"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// SchedulerComponentName is the unique identifier for the DeploymentScheduler component.
	SchedulerComponentName = "deployment-scheduler"

	// SchedulerEventBufferSize is the size of the event subscription buffer for the scheduler.
	// Moderate-volume component handling template, validation, and discovery events.
	// Named with "Scheduler" prefix to avoid conflict with EventBufferSize in this package.
	SchedulerEventBufferSize = busevents.StandardSubscriberBuffer
)

// schedulerState groups the deployment scheduling state into a single struct.
// All fields are protected by DeploymentScheduler.schedulerMutex.
//
// The single deploy-loop goroutine (runDeployLoop) owns all rate-limit timing;
// these fields are the shared state it coordinates with the event handlers.
// `deployInFlight` replaces the old phase state machine: it is true from the
// moment the loop publishes a DeploymentScheduledEvent until the matching
// DeploymentCompletedEvent clears it. A timeout marks that deployment as
// retiring until the same attempt terminates. The single loop makes the
// deployment interval authoritative.
type schedulerState struct {
	deployInFlight        bool
	deploymentTimedOut    bool
	activeDeploymentID    string
	activeCorrelationID   string
	deploymentStartTime   time.Time
	pending               *scheduledDeployment
	lastDeploymentEndTime time.Time
}

// lane is the apply lane a scheduled render is classified into, decided by
// diffing the render against the last-DISPATCHED config (the in-flight/pending
// deploy's render, NOT last-completed).
type lane int

const (
	// laneStructural: the diff has at least one non-runtime-eligible op. Apply via
	// a full raw push + reload, rate-limited by the deployment interval
	// (latest-wins, ≤1 enqueued).
	laneStructural lane = iota
	// laneRuntimeRaw: the diff is purely runtime-eligible server fields (no
	// structural/reload op). Apply via a single skip_reload raw push +
	// X-Runtime-Actions, NO reload, IGNORING the deployment interval.
	laneRuntimeRaw
)

// scheduledDeployment represents a deployment that was triggered while another
// deployment was in progress. Only the latest scheduled deployment is kept (latest wins).
//
// `lane` is the apply lane this render was classified into (decided per latest
// render by diffing against the last-dispatched config). `runtimeUpdates` is the
// precomputed runtime-eligible render diff for the runtime-raw lane (nil/ignored
// for the structural lane), threaded through so applyRuntimeRaw does NOT recompute
// it.
//
// `contentChecksum` is captured at schedule-time (the hash of THIS scheduled
// deployment's config+auxFiles). It MUST travel with the struct rather than
// being re-read from `DeploymentScheduler.lastContentChecksum` at deploy-time
// — otherwise a reconcile that lands between scheduleOrQueue and the actual
// deploy publishes a new render with a different checksum, the scheduler
// re-reads the now-newer value, threads that into DeploymentScheduledEvent,
// and the deployer records it as "what was just deployed". The next
// reconcile producing that same hash then matches `lastDeployedConfigHash`
// and incorrectly skips deployment. The fix in commit
// fix(deployer): cache pod's actual post-sync state (6d4d921e) addressed
// the analogous race for the per-pod version cache; this field closes the
// scheduler-side leg.
type scheduledDeployment struct {
	workRevision    uint64
	retryGeneration uint64
	config          string
	auxFiles        *dataplane.AuxiliaryFiles
	parsedConfig    *parser.StructuredConfig
	endpoints       []dataplane.Endpoint
	reason          string
	correlationID   string                          // Correlation ID for event tracing
	statusPatches   []templating.StatusPatch        // Patches to forward to DeploymentScheduledEvent
	coalescible     bool                            // Whether this deployment can be coalesced (skipped if newer available)
	contentChecksum string                          // Hash of THIS deployment's config+aux — captured at schedule-time
	lane            lane                            // Apply lane (structural vs runtime-raw)
	runtimeUpdates  *dataplane.RuntimeServerUpdates // Precomputed runtime diff for the runtime-raw lane

	// runtimeConfigName / runtimeConfigNamespace identify the HAProxyCfg resource
	// whose status.deployedToPods this deploy advances. Resolved at dispatch
	// (resolveRuntimeConfigName) and set ONLY for the runtime-raw lane, where the
	// apply reloads nothing and is therefore the complete deploy — so the bypass
	// publishes ConfigAppliedToPodEvent. Left empty for the structural lane (its
	// runtime subset may be applied pre-interval, but the Component publishes the
	// truthful per-pod status only after the reload completes).
	runtimeConfigName      string
	runtimeConfigNamespace string
}

// DeploymentScheduler implements deployment scheduling with rate limiting.
//
// It subscribes to events that trigger deployments, maintains the state of
// rendered and validated configurations, and enforces minimum deployment intervals.
//
// Event subscriptions:
//   - TemplateRenderedEvent: Track rendered config and auxiliary files
//   - ValidationCompletedEvent: Cache validated config and schedule deployment
//   - ValidationFailedEvent: Deploy cached config for drift prevention fallback
//   - HAProxyPodsDiscoveredEvent: Update endpoints and schedule deployment
//
// The component publishes DeploymentScheduledEvent when a deployment should execute.
type DeploymentScheduler struct {
	*component.ReadySignal

	eventBus              *busevents.EventBus
	eventChan             <-chan busevents.Event // Event subscription channel (subscribed in Start())
	logger                *slog.Logger
	minDeploymentInterval time.Duration
	ctx                   context.Context // Main event loop context for scheduling

	// State protected by mutex
	mu                           sync.RWMutex
	lastRenderedConfig           string                    // Last rendered HAProxy config (before validation)
	lastAuxiliaryFiles           *dataplane.AuxiliaryFiles // Last rendered auxiliary files
	lastContentChecksum          string                    // Pre-computed content checksum from pipeline
	lastRenderedEventID          string                    // EventID of the render that wrote the four fields above — see handleValidationCompleted
	lastValidatedStatusPatches   []templating.StatusPatch  // Patches from the last successful render — forwarded to deploy events for StatusApplier
	lastValidatedConfig          string                    // Last validated HAProxy config
	lastValidatedAux             *dataplane.AuxiliaryFiles // Last validated auxiliary files
	lastValidatedContentChecksum string                    // Hash captured WITH lastValidatedConfig — must travel together, never reconstructed
	lastParsedConfig             *parser.StructuredConfig  // Pre-parsed desired config
	lastCorrelationID            string                    // Correlation ID from last validation event
	lastCoalescible              bool                      // Coalescibility flag from last validation event
	currentEndpoints             []dataplane.Endpoint      // Current HAProxy pod endpoints
	hasValidConfig               bool                      // Whether we have a validated config to deploy
	runtimeConfigName            string                    // Name of HAProxyCfg resource (set by ConfigPublishedEvent)
	runtimeConfigNamespace       string                    // Namespace of HAProxyCfg resource (set by ConfigPublishedEvent)
	templateConfigName           string                    // Name from ConfigValidatedEvent.TemplateConfig (for early runtimeConfigName computation)
	templateConfigNamespace      string                    // Namespace from ConfigValidatedEvent.TemplateConfig

	// Deployment scheduling and rate limiting
	schedulerMutex    sync.Mutex
	state             schedulerState
	workRevision      uint64
	deploymentTimeout time.Duration

	// Fast deploy-failure retries requeue the last validated render through the
	// normal scheduler. schedulerMutex protects the timer owner and retry budget.
	retryTimer              *time.Timer
	retryTimerDone          func()
	retryCallbacks          sync.WaitGroup
	retryGeneration         uint64
	retryStopped            bool
	deployFailureRetries    int
	lastFailedRetryChecksum string

	// lastDispatchedParsed / lastDispatchedConfig are the render that the most
	// recent DISPATCH committed to (the in-flight/pending deploy's render), used
	// as the SINGLE diff baseline for classifying the next render's lane
	// (baseline->current => the undeployed server changes). They advance at
	// DISPATCH for BOTH lanes — structural: when the deploy is published;
	// runtime-raw: right before the inline applyRuntimeRaw — and are reset on lost
	// leadership. lastDispatchedConfig is the matching raw config STRING (the
	// desired body the runtime-raw push carries). Both MUST be written together
	// under schedulerMutex. Protected by schedulerMutex.
	lastDispatchedParsed *parser.StructuredConfig
	lastDispatchedConfig string

	// lastActivatedConfig is the raw config last proven to be RUNNING on the
	// fleet — a structural deploy that completed with zero failures, or a
	// runtime-raw apply, whose push body and runtime actions are themselves the
	// activation. It is deliberately NOT the dispatched render: a structural
	// dispatch advances lastDispatchedConfig BEFORE the deploy lands, so during
	// the flight the two differ, and that window is exactly when
	// applyRuntimeSubset runs.
	//
	// Patching the dispatched-but-unlanded render instead wrote the pending
	// structural content to disk under skip_reload — content HAProxy never
	// loaded — after which the next sync's empty diff reported success and the
	// render was never activated (#112). Protected by schedulerMutex; written
	// together with lastDispatchedConfig where both advance.
	lastActivatedConfig string

	// Cache for deployment optimization - skip if config unchanged
	lastDeployedConfigHash string    // SHA-256 hash of last successfully deployed config
	lastDeployedPodSetHash string    // Hash of pod endpoints for the last deployment
	lastDeployedTime       time.Time // When the last successful deployment occurred

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// runtimeBypass applies pure-runtime server changes (e.g. a pod-IP rotation)
	// to the live HAProxy workers via the runtime-raw lane. The deploy loop calls
	// it SYNCHRONOUSLY, serialized AFTER any in-flight structural deploy's reload,
	// so a runtime `set server` can never land on a worker that reload replaces.
	// Best-effort; the scheduled deploy is the correctness floor.
	runtimeBypass *runtimeBypass

	// Deploy-loop coordination. The single long-lived runDeployLoop goroutine
	// (started in Start) owns rate-limit timing. Created in Start so each
	// leadership term gets fresh channels.
	//   - pendingSignal (cap 1): event handlers wake the loop after setting pending.
	//   - completed (cap 1): an accepted DeploymentCompletedEvent wakes the
	//     loop's awaitCompletion.
	//   - loopDone: closed when the loop exits, so Start joins it on shutdown.
	pendingSignal chan struct{}
	completed     chan struct{}
	loopDone      chan struct{}
}

// computePodSetHash computes a hash of the current pod endpoints.
// Used to detect if pod set changed (new/removed HAProxy pods).
func computePodSetHash(endpoints []dataplane.Endpoint) string {
	h := sha256.New()

	// Extract and sort URLs for deterministic hashing
	urls := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		urls = append(urls, ep.URL)
	}
	slices.Sort(urls)

	for _, url := range urls {
		h.Write([]byte(url))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// NewDeploymentScheduler creates a new DeploymentScheduler component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing scheduled deployments
//   - logger: Structured logger for component logging
//   - minDeploymentInterval: Minimum time between consecutive deployments (rate limiting)
//   - deploymentTimeout: Maximum time to wait for a deployment to complete before retrying
//
// Returns:
//   - A new DeploymentScheduler instance ready to be started
func NewDeploymentScheduler(eventBus *busevents.EventBus, logger *slog.Logger, minDeploymentInterval, deploymentTimeout time.Duration) *DeploymentScheduler {
	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &DeploymentScheduler{
		ReadySignal:           component.NewReadySignal(),
		eventBus:              eventBus,
		logger:                logger.With("component", SchedulerComponentName),
		minDeploymentInterval: minDeploymentInterval,
		deploymentTimeout:     deploymentTimeout,
		healthTracker:         lifecycle.NewProcessingTracker(SchedulerComponentName, lifecycle.DefaultProcessingTimeout),
		runtimeBypass:         newRuntimeBypass(logger, eventBus),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (s *DeploymentScheduler) Name() string {
	return SchedulerComponentName
}

// Start begins the deployment scheduler's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// It subscribes to events when called (after leadership is acquired).
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (s *DeploymentScheduler) Start(ctx context.Context) error {
	s.ctx = ctx // Save context for scheduling operations
	s.schedulerMutex.Lock()
	s.retryStopped = false
	s.retryGeneration++
	s.schedulerMutex.Unlock()

	// Create deploy-loop channels fresh for this leadership term (see struct).
	s.pendingSignal = make(chan struct{}, 1)
	s.completed = make(chan struct{}, 1)
	s.loopDone = make(chan struct{})

	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	s.eventChan = s.eventBus.SubscribeTypesLeaderOnly(SchedulerComponentName, SchedulerEventBufferSize,
		events.EventTypeTemplateRendered,
		events.EventTypeConfigValidated,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeDeploymentCompleted,
		events.EventTypeConfigPublished,
		events.EventTypeLostLeadership,
	)
	// Unsubscribe on loop exit: without this, every leadership re-acquisition on
	// the same instance would stack another subscription whose orphaned channel
	// fills up and logs critical drops forever (mirrors Coordinator).
	defer s.eventBus.UnsubscribeTyped(s.eventChan)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	s.MarkReady()

	// Start the single deploy loop that owns rate-limit timing. All event
	// handlers only set state.pending (latest-wins) and signal it; this is the
	// ONLY goroutine that waits out minDeploymentInterval and publishes
	// DeploymentScheduledEvent, so reloads can never burst under churn.
	go s.runDeployLoop(ctx)

	s.logger.Debug("Deployment scheduler starting",
		"min_deployment_interval_ms", s.minDeploymentInterval.Milliseconds(),
		"deployment_timeout_ms", s.deploymentTimeout.Milliseconds())

	// Ticker to check for deployment timeouts
	ticker := time.NewTicker(timeouts.TickerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case event := <-s.eventChan:
			s.handleEvent(ctx, event)

		case <-ticker.C:
			s.checkDeploymentTimeout(ctx)

		case <-ctx.Done():
			s.logger.Info("DeploymentScheduler shutting down", "reason", ctx.Err())
			s.stopFailureRetries()
			s.runtimeBypass.Close()
			<-s.loopDone // join the deploy loop (it returns on ctx.Done())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (s *DeploymentScheduler) handleEvent(ctx context.Context, event busevents.Event) {
	// Track processing for health check stall detection
	s.healthTracker.StartProcessing()
	defer s.healthTracker.EndProcessing()

	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		s.handleTemplateRendered(e)

	case *events.ConfigValidatedEvent:
		s.handleConfigValidated(e)

	case *events.ValidationCompletedEvent:
		s.handleValidationCompleted(ctx, e)

	case *events.ValidationFailedEvent:
		s.handleValidationFailed(ctx, e)

	case *events.HAProxyPodsDiscoveredEvent:
		s.handlePodsDiscovered(ctx, e)

	case *events.DeploymentCompletedEvent:
		s.handleDeploymentCompleted(e)

	case *events.ConfigPublishedEvent:
		s.handleConfigPublished(e)

	case *events.LostLeadershipEvent:
		s.handleLostLeadership(e)
	}
}

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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
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

var podSetHashKey = newPodSetHashKey()

func newPodSetHashKey() [sha256.Size]byte {
	var key [sha256.Size]byte
	if _, err := rand.Read(key[:]); err != nil {
		panic(fmt.Errorf("creating endpoint-authority hash key: %w", err))
	}
	return key
}

// schedulerState groups the deployment scheduling state into a single struct.
// All fields are protected by DeploymentScheduler.schedulerMutex.
//
// The single deploy-loop goroutine (runDeployLoop) dispatches; these fields are
// the shared state it coordinates with the event handlers. `deployInFlight`
// replaces the old phase state machine: it is true from the moment the loop
// publishes a DeploymentScheduledEvent until the matching
// DeploymentCompletedEvent clears it. A timeout marks that deployment as
// retiring until the same attempt terminates.
type schedulerState struct {
	deployInFlight      bool
	deploymentTimedOut  bool
	activeDeploymentID  string
	activeCorrelationID string
	deploymentStartTime time.Time
	pending             *scheduledDeployment
}

// scheduledDeployment represents a deployment that was triggered while another
// deployment was in progress. Only the latest scheduled deployment is kept (latest wins).
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
	plan            *renderplan.Plan // Structure of THIS deployment's render — captured with `config`, same rule as contentChecksum
	planID          string
	endpoints       []dataplane.Endpoint
	reason          string
	correlationID   string                   // Correlation ID for event tracing
	statusPatches   []templating.StatusPatch // Patches to forward to DeploymentScheduledEvent
	coalescible     bool                     // Whether this deployment can be coalesced (skipped if newer available)
	contentChecksum string                   // Hash of THIS deployment's config+aux — captured at schedule-time
}

// acceptedRender is one render the render gate passed, kept so a later refusal
// has somewhere to roll back to. Every field is captured together with the
// config it belongs to, same rule as scheduledDeployment.contentChecksum.
type acceptedRender struct {
	config          string
	auxFiles        *dataplane.AuxiliaryFiles
	contentChecksum string
	plan            *renderplan.Plan
	planID          string
	statusPatches   []templating.StatusPatch
	correlationID   string
}

// DeploymentScheduler decides which render is deployed next, and when.
//
// It subscribes to events that trigger deployments, maintains the state of
// rendered and validated configurations, and holds a new render while the
// fleet's paced reloads are still pending. One deployment in flight is the
// only rate limit it applies; reload pacing lives in the agent.
//
// Event subscriptions:
//   - TemplateRenderedEvent: Cache the render and schedule deployment
//   - RenderGateCompletedEvent: Move the gate's latch; a pass releases a held render
//   - ValidationFailedEvent: Deploy cached config for drift prevention fallback
//   - HAProxyPodsDiscoveredEvent: Update endpoints and schedule deployment
//
// The component publishes DeploymentScheduledEvent when a deployment should execute.
type DeploymentScheduler struct {
	*component.ReadySignal

	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Event subscription channel (subscribed in Start())
	logger    *slog.Logger
	// minDeploymentInterval seeds the fast-retry backoff. Reload pacing is the
	// agent's (--reload-interval-min), which the chart templates from the same
	// value; nothing here waits it out.
	minDeploymentInterval time.Duration
	ctx                   context.Context // Main event loop context for scheduling

	// State protected by mutex
	mu                           sync.RWMutex
	lastRenderedConfig           string                    // Last rendered HAProxy config (before validation)
	lastAuxiliaryFiles           *dataplane.AuxiliaryFiles // Last rendered auxiliary files
	lastContentChecksum          string                    // Pre-computed content checksum from pipeline
	lastRenderedPlan             *renderplan.Plan          // Plan of the last rendered config
	lastRenderedPlanID           string                    // Digest of lastRenderedPlan
	lastRenderedStatusPatches    []templating.StatusPatch  // Patches of the last render — promoted with the config it belongs to
	lastValidatedStatusPatches   []templating.StatusPatch  // Patches of the dispatched render — captured with it, forwarded to deploy events for StatusApplier
	lastValidatedConfig          string                    // Last validated HAProxy config
	lastValidatedAux             *dataplane.AuxiliaryFiles // Last validated auxiliary files
	lastValidatedContentChecksum string                    // Hash captured WITH lastValidatedConfig — must travel together, never reconstructed
	lastValidatedPlan            *renderplan.Plan          // Plan captured WITH lastValidatedConfig
	lastValidatedPlanID          string                    // Digest of lastValidatedPlan
	lastCorrelationID            string                    // Correlation ID from last validation event
	lastCoalescible              bool                      // Coalescibility flag from last validation event
	currentEndpoints             []dataplane.Endpoint      // Current HAProxy pod endpoints
	hasValidConfig               bool                      // Whether we have a validated config to deploy
	runtimeConfigName            string                    // Name of HAProxyCfg resource (set by ConfigPublishedEvent)
	runtimeConfigNamespace       string                    // Namespace of HAProxyCfg resource (set by ConfigPublishedEvent)
	templateConfigName           string                    // Name from ConfigValidatedEvent.TemplateConfig (for early runtimeConfigName computation)
	templateConfigNamespace      string                    // Namespace from ConfigValidatedEvent.TemplateConfig

	// gatePinned holds every render until the render gate passes one. Set from
	// RenderGateCompletedEvent; the gate itself owns the latch (ADR-0022).
	gatePinned bool

	// acceptedRender is the newest render the gate passed. A refusal rolls the
	// lastValidated* fields back to it, so pod discovery, the validation
	// fallback and the retry timers re-send the config HAProxy accepted rather
	// than the one it just refused. Nil until the gate has passed a render.
	acceptedRender *acceptedRender

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

	// lastPodSetHash is the endpoint authority set the last dispatch targeted.
	// A change to it retires any in-flight deploy: its pods are not the fleet
	// any more. Protected by schedulerMutex.
	lastPodSetHash string

	// Cache for deployment optimization - skip if config unchanged
	lastDeployedConfigHash string    // SHA-256 hash of last successfully deployed config
	lastDeployedPodSetHash string    // Hash of endpoint authorities for the last deployment
	lastDeployedTime       time.Time // When the last successful deployment occurred

	// lastDispatchedConfigHash is the checksum of the last render whose
	// deployment completed, whether it fully deployed or left reloads pending.
	// The skip-unchanged gate may only fire when it equals
	// lastDeployedConfigHash: a paced deploy advances this but not the "deployed"
	// hash, so the fleet has moved off lastDeployedConfigHash while it still
	// names an earlier full deploy. Content-addressed renders recur (an add and
	// its delete hash to the same plan every churn cycle), so without this a
	// recurring render whose checksum matches that stale hash is dismissed as
	// unchanged and the fleet never reaches it.
	lastDispatchedConfigHash string

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// capabilities receives the capability set derived from the fleet's lowest
	// reported HAProxy version. Nil in tests; guarded by mu with lastFleetVersion.
	capabilities     FleetCapabilitiesSink
	lastFleetVersion string

	// Deploy-loop coordination. The single long-lived runDeployLoop goroutine
	// (started in Start) is the only dispatcher. Created in Start so each
	// leadership term gets fresh channels.
	//   - pendingSignal (cap 1): event handlers wake the loop after setting pending.
	//   - completed (cap 1): an accepted DeploymentCompletedEvent wakes the
	//     loop's awaitCompletion.
	//   - loopDone: closed when the loop exits, so Start joins it on shutdown.
	pendingSignal chan struct{}
	completed     chan struct{}
	loopDone      chan struct{}
}

// computePodSetHash computes an order-independent hash of endpoint authorities.
func computePodSetHash(endpoints []dataplane.Endpoint) string {
	digests := make([][sha256.Size]byte, 0, len(endpoints))
	for i := range endpoints {
		digests = append(digests, hashEndpointAuthority(&endpoints[i]))
	}
	slices.SortFunc(digests, func(left, right [sha256.Size]byte) int {
		return bytes.Compare(left[:], right[:])
	})

	h := sha256.New()
	for i := range digests {
		_, _ = h.Write(digests[i][:])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func hashEndpointAuthority(endpoint *dataplane.Endpoint) [sha256.Size]byte {
	return hashEndpointAuthorityWithKey(endpoint, podSetHashKey[:])
}

func hashEndpointAuthorityWithKey(endpoint *dataplane.Endpoint, key []byte) [sha256.Size]byte {
	values := []string{
		endpoint.URL,
		endpoint.Username,
		endpoint.Password,
		endpoint.PodName,
		endpoint.PodNamespace,
		endpoint.PodUID,
		endpoint.PodRuntimeID,
		strconv.Itoa(endpoint.DetectedMajorVersion),
		strconv.Itoa(endpoint.DetectedMinorVersion),
		endpoint.DetectedFullVersion,
	}
	// Keying prevents the logged hash prefix from becoming a password verifier.
	h := hmac.New(sha256.New, key)
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	return [sha256.Size]byte(h.Sum(nil))
}

func newDeploymentScheduler(eventBus *busevents.EventBus, logger *slog.Logger, minDeploymentInterval, deploymentTimeout time.Duration) *DeploymentScheduler {
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
	defer s.Rearm()
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
		events.EventTypeRenderGateCompleted,
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

	// Start the single deploy loop. All event handlers only set state.pending
	// (latest-wins) and signal it; this is the ONLY goroutine that publishes
	// DeploymentScheduledEvent, so two deployments are never in flight at once.
	go s.runDeployLoop(ctx)

	s.logger.Debug("Deployment scheduler starting",
		"failure_retry_base_ms", s.minDeploymentInterval.Milliseconds(),
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
		s.handleTemplateRendered(ctx, e)

	case *events.ConfigValidatedEvent:
		s.handleConfigValidated(e)

	case *events.RenderGateCompletedEvent:
		s.handleRenderGateCompleted(ctx, e)

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

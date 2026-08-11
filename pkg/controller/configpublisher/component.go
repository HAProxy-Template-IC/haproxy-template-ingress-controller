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

package configpublisher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/throttle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "config-publisher"

	// EventBufferSize is the buffer size for the event subscription channel.
	// Large buffer to handle burst traffic during startup: ConfigPublisher makes
	// synchronous k8s API calls, so it processes events slowly compared to the
	// rate at which all-replica components publish them.
	EventBufferSize = busevents.PublishingSubscriberBuffer

	// publishWorkChannelSize is the buffer size for the publish work channel.
	// A size of 1 provides natural coalescing - if new work arrives while
	// previous work is being processed, the old pending work is replaced.
	publishWorkChannelSize = 1

	// statusWorkTriggerSize is the buffer size for the status work trigger channel.
	// A size of 1 is sufficient since we use a separate map for coalescing.
	// The trigger just wakes up the worker to process pending updates.
	statusWorkTriggerSize = 1
)

// renderedConfigEntry holds cached rendered config data indexed by correlation ID.
// This ensures we match the correct TemplateRenderedEvent with its corresponding
// ValidationCompletedEvent, preventing stale data from being published when
// events from multiple reconciliation cycles are interleaved.
type renderedConfigEntry struct {
	config          string
	auxFiles        *dataplane.AuxiliaryFiles
	contentChecksum string
	renderedAt      time.Time
}

// publishWorkItem represents a config publish task for the async worker.
type publishWorkItem struct {
	correlationID  string
	templateConfig *v1alpha1.HAProxyTemplateConfig
	entry          *renderedConfigEntry
	request        *configpublisher.PublishRequest
	generation     uint64
	term           uint64
	superseded     <-chan struct{}
	// deployDriven marks an item that carries the bytes the deployer just
	// applied (from a DeployedConfigPublishRequest), as opposed to the
	// validation-driven publish. Deploy-driven items use their own pending slot
	// so a validation publish can't coalesce them away, keeping every deployed
	// checksum observable as a published spec.Checksum.
	deployDriven bool
}

// validationFailedWorkItem represents a failed config publish task for the async worker.
type validationFailedWorkItem struct {
	correlationID   string
	templateConfig  *v1alpha1.HAProxyTemplateConfig
	entry           *renderedConfigEntry
	request         *configpublisher.PublishRequest
	validationError string
	generation      uint64
	term            uint64
	superseded      <-chan struct{}
}

// statusWorkItem represents a pod status update task for the async worker.
type statusWorkItem struct {
	event *events.ConfigAppliedToPodEvent
	// retries counts requeues of this item while its HAProxyCfg wasn't
	// published yet (see requeueStatusWork). Only touched by the status
	// worker goroutines after the item leaves the pending map.
	retries int
}

type podAuthorityKey struct {
	namespace string
	name      string
}

type podAuthority struct {
	uid       string
	runtimeID string
}

// Component is the event adapter for the config publisher.
// It wraps the pure Publisher component and coordinates it with the event bus.
//
// This component caches information from multiple events (ConfigValidatedEvent,
// TemplateRenderedEvent) and publishes runtime config resources only after
// successful HAProxy validation (ValidationCompletedEvent).
//
// Rendered configs are cached by correlation ID to ensure we match the correct
// TemplateRenderedEvent with its corresponding ValidationCompletedEvent, even when
// events from multiple reconciliation cycles are interleaved.
//
// The component uses async workers for K8S API operations to prevent blocking
// the event loop. This ensures new events are processed promptly even when
// K8S API calls are slow.
type Component struct {
	*component.ReadySignal

	publisher *configpublisher.Publisher
	eventBus  *busevents.EventBus
	logger    *slog.Logger

	// Subscribed in Start() when leadership is acquired
	eventChan <-chan busevents.Event

	// Cached state from events (protected by mutex)
	mu                sync.RWMutex
	templateConfig    *v1alpha1.HAProxyTemplateConfig
	hasTemplateConfig bool

	// renderedConfigs maps correlation ID to rendered config data.
	// This ensures we match the correct TemplateRenderedEvent with its corresponding
	// ValidationCompletedEvent when events from multiple cycles are interleaved.
	renderedConfigs map[string]*renderedConfigEntry

	// Work channels for async K8S API operations.
	// Using channels with small buffers provides natural coalescing:
	// newer work replaces older pending work when the worker is busy.
	publishWork          chan *publishWorkItem
	validationFailedWork chan *validationFailedWorkItem

	// Status update coalescing.
	// Instead of queueing every status update individually, we coalesce updates
	// for the same pod. When multiple updates arrive for the same pod before the
	// worker processes them, only the latest update is applied. This prevents
	// channel overflow during high-frequency reconciliation cycles.
	statusWorkPending   map[string]*statusWorkItem // Key: namespace/runtimeConfig/podName
	statusWorkPendingMu sync.Mutex
	statusWorkTrigger   chan struct{} // Signals worker to process pending updates
	statusRetrySignals  *delayedSignals

	endpointAuthorityMu    sync.RWMutex
	endpointAuthorities    map[podAuthorityKey]podAuthority
	endpointAuthoritiesSet bool

	// deployedPending is every deployed render still awaiting publication, in
	// arrival order and deduplicated by content checksum.
	//
	// A size-1 channel with latest-wins coalescing was wrong here, unlike for
	// the validation path: `status.deployedToPods[].checksum` is written by an
	// independent path, so a dropped deployed checksum leaves the CR
	// advertising a config that `spec.content` never carried — a checksum no
	// reader, and no watcher, can resolve. Measured on a real run: 1 checksum
	// in 31 dropped.
	//
	// What a reader can observe is the bound: a checksum only needs to reach
	// `spec` while some pod is still reported at it. pruneSupersededDeployed
	// therefore drops a queued entry once the fleet has demonstrably moved
	// past it, which caps the queue at the number of distinct checksums live
	// across the fleet rather than at the deploy rate. Each entry holds a full
	// rendered config, and the drain is one entry per configPublishInterval,
	// so without that prune the runtime-raw lane — which skips
	// minDeploymentInterval by design — grows this without bound under
	// endpoint churn. Protected by deployedPendingMu.
	deployedPending   []*publishWorkItem
	deployedPendingMu sync.Mutex

	// deployedChecksumByPod is the checksum each pod last reported running,
	// fed by ConfigAppliedToPodEvent — the same events that write
	// status.deployedToPods, so it mirrors exactly what a reader can see.
	// Protected by deployedPendingMu, which also guards the queue it prunes.
	deployedChecksumByPod map[podAuthorityKey]string
	deployedTrigger       chan struct{} // Wakes publishWorker; cap 1, same as statusWorkTrigger

	// lastPublishedChecksum tracks the checksum of the last successfully published config.
	// Used to skip redundant CRD updates when config content is unchanged.
	// Protected by mu.
	lastPublishedChecksum   string
	publicationTerm         uint64
	nextPublishGeneration   uint64
	latestPublishGeneration uint64
	nextInvalidGeneration   uint64
	latestInvalidGeneration uint64
	publishSuperseded       chan struct{}
	invalidSuperseded       chan struct{}
	publicationRetryWait    func(context.Context, time.Duration, <-chan struct{}) bool
	publicationCallMu       sync.Mutex

	// publishInterval configures the leading-edge refractory period for both
	// publish and status throttles. Decouples CRD writes from reconciliation
	// frequency to reduce etcd write pressure; deployments to HAProxy pods
	// (event-driven) are unaffected.
	publishInterval time.Duration

	// publishThrottle gates spec writes; statusThrottle gates status
	// subresource writes. Each UpdateStatus writes the full ~509 KB object to
	// etcd even though only the status changed, so throttling them at the
	// same cadence as spec publishes is essential for etcd write pressure.
	publishThrottle *throttle.LeadingEdge
	statusThrottle  *throttle.LeadingEdge

	// pendingPublish buffers the latest publish work item that arrived while
	// inside the publish-throttle refractory window. The publish worker
	// flushes it on publishThrottle.FiredCh(). Protected by pendingMu.
	pendingPublish *publishWorkItem
	pendingMu      sync.Mutex
}

// Option configures the Component.
type Option func(*Component)

// WithPublishInterval sets the throttle interval for CRD publishes.
// During endpoint churn each reconciliation produces a new config, but writing
// ~500 KB to etcd every 5 s is excessive. This interval limits CRD updates while
// deployments to HAProxy pods (event-driven) remain unaffected.
// A value of 0 disables throttling (every config is published immediately).
func WithPublishInterval(d time.Duration) Option {
	return func(c *Component) {
		c.publishInterval = d
	}
}

// New creates a new config publisher component.
func New(
	publisher *configpublisher.Publisher,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	opts ...Option,
) *Component {
	if logger == nil {
		logger = slog.Default()
	}

	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	c := &Component{
		ReadySignal:          component.NewReadySignal(),
		publisher:            publisher,
		eventBus:             eventBus,
		logger:               logger.With("component", ComponentName),
		renderedConfigs:      make(map[string]*renderedConfigEntry),
		publishWork:          make(chan *publishWorkItem, publishWorkChannelSize),
		deployedTrigger:      make(chan struct{}, statusWorkTriggerSize),
		validationFailedWork: make(chan *validationFailedWorkItem, publishWorkChannelSize),
		statusWorkPending:    make(map[string]*statusWorkItem),
		statusWorkTrigger:    make(chan struct{}, statusWorkTriggerSize),
		statusRetrySignals:   newDelayedSignals(),
		publishSuperseded:    make(chan struct{}),
		invalidSuperseded:    make(chan struct{}),
		publicationRetryWait: waitForPublicationRetry,
		endpointAuthorities:  make(map[podAuthorityKey]podAuthority),

		deployedChecksumByPod: make(map[podAuthorityKey]string),
	}

	for _, opt := range opts {
		opt(c)
	}

	// Throttles are constructed after options so publishInterval is set.
	// A zero interval disables them (Available() always returns true).
	c.publishThrottle = throttle.New(c.publishInterval)
	c.statusThrottle = throttle.New(c.publishInterval)

	return c
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the config publisher's event loop.
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
func (c *Component) Start(ctx context.Context) error {
	c.preparePublicationTerm()
	defer c.Rearm()
	c.endpointAuthorityMu.Lock()
	c.endpointAuthorities = make(map[podAuthorityKey]podAuthority)
	c.endpointAuthoritiesSet = false
	c.endpointAuthorityMu.Unlock()

	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeTemplateRendered,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeConfigAppliedToPod,
		events.EventTypeDeployedConfigPublishRequest,
		events.EventTypeHAProxyPodTerminated,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeLostLeadership,
	)
	// Unsubscribe on loop exit: without this, every leadership re-acquisition on
	// the same instance would stack another subscription whose orphaned channel
	// fills up and logs critical drops forever (mirrors Coordinator).
	defer c.eventBus.UnsubscribeTyped(c.eventChan)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	c.MarkReady()

	c.logger.Debug("Config publisher starting")

	// Start async workers for K8S API operations.
	// These workers process work items from their channels, allowing the main
	// event loop to continue processing events without blocking on slow API calls.
	var workers sync.WaitGroup
	workers.Go(func() { c.publishWorker(ctx) })
	workers.Go(func() { c.validationFailedWorker(ctx) })
	workers.Go(func() { c.statusWorker(ctx) })
	defer func() {
		c.publishThrottle.Stop()
		c.statusThrottle.Stop()
		c.statusRetrySignals.Stop()
		workers.Wait()
	}()

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(ctx, event)

		case <-ctx.Done():
			c.logger.Info("Config publisher shutting down", "reason", ctx.Err())
			return ctx.Err()
		}
	}
}

func (c *Component) preparePublicationTerm() {
	c.mu.Lock()
	c.publicationTerm++
	c.templateConfig = nil
	c.hasTemplateConfig = false
	c.renderedConfigs = make(map[string]*renderedConfigEntry)
	c.lastPublishedChecksum = ""
	c.latestPublishGeneration = 0
	c.latestInvalidGeneration = 0
	c.publishSuperseded = supersedePublication(c.publishSuperseded)
	c.invalidSuperseded = supersedePublication(c.invalidSuperseded)
	c.mu.Unlock()

	c.publishWork = make(chan *publishWorkItem, publishWorkChannelSize)
	c.validationFailedWork = make(chan *validationFailedWorkItem, publishWorkChannelSize)

	c.deployedPendingMu.Lock()
	c.deployedPending = nil
	c.deployedTrigger = make(chan struct{}, statusWorkTriggerSize)
	c.deployedPendingMu.Unlock()

	c.pendingMu.Lock()
	c.pendingPublish = nil
	c.pendingMu.Unlock()

	c.statusWorkPendingMu.Lock()
	c.statusWorkPending = make(map[string]*statusWorkItem)
	c.statusWorkTrigger = make(chan struct{}, statusWorkTriggerSize)
	c.statusWorkPendingMu.Unlock()

	c.statusRetrySignals = newDelayedSignals()
	c.publishThrottle = throttle.New(c.publishInterval)
	c.statusThrottle = throttle.New(c.publishInterval)
}

// handleEvent processes events from the event bus. ctx is the component's
// lifecycle context, forwarded to handlers that issue Kubernetes API calls so
// those calls are cancelled on shutdown.
func (c *Component) handleEvent(ctx context.Context, event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		c.handleConfigValidated(e)

	case *events.TemplateRenderedEvent:
		c.handleTemplateRendered(e)

	case *events.ValidationCompletedEvent:
		c.handleValidationCompleted(e)

	case *events.ValidationFailedEvent:
		c.handleValidationFailed(e)

	case *events.ConfigAppliedToPodEvent:
		c.handleConfigAppliedToPod(e)

	case *events.DeployedConfigPublishRequest:
		c.handleDeployedConfigPublishRequest(e)

	case *events.HAProxyPodTerminatedEvent:
		c.handlePodTerminated(ctx, e)

	case *events.HAProxyPodsDiscoveredEvent:
		c.handlePodsDiscovered(ctx, e)

	case *events.LostLeadershipEvent:
		c.handleLostLeadership(e)
	}
}

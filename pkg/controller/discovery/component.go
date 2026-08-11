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

// Package discovery provides the Discovery event adapter component.
//
// It tracks the set of HAProxy pods reported by the resource watcher (via the
// auto-injected haproxy-pods watcher), enriches each pod with credentials and
// a HAProxy version probe through pkg/dataplane, and publishes
// HAProxyPodsDiscoveredEvent / HAProxyPodTerminatedEvent so the deployer and
// other consumers know which endpoints to talk to.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "discovery"

	// EventBufferSize is the buffer size for event subscriptions.
	// High-volume to absorb pod churn bursts during scaling and rolling updates.
	EventBufferSize = busevents.HighVolumeSubscriberBuffer

	// Version check retry configuration.
	initialRetryInterval = 5 * time.Second
	maxRetryInterval     = 1 * time.Minute
	retryBackoffFactor   = 2
)

// retryState tracks retry information for pods pending version check.
type retryState struct {
	lastAttempt time.Time
	retryCount  int
}

// Component is the Discovery event adapter.
//
// This component:
//   - Subscribes to ConfigValidatedEvent, CredentialsUpdatedEvent, ResourceIndexUpdatedEvent, and BecameLeaderEvent
//   - Maintains current state (dataplanePort, credentials, podStore)
//   - Calls Discovery.DiscoverEndpoints() when relevant events occur
//   - Publishes HAProxyPodsDiscoveredEvent with discovered endpoints
//   - Publishes HAProxyPodTerminatedEvent when pods are removed
//
// Event Flow:
//  1. ConfigValidatedEvent → Update dataplanePort → Trigger discovery
//  2. CredentialsUpdatedEvent → Update credentials → Trigger discovery
//  3. ResourceIndexUpdatedEvent (haproxy-pods) → Trigger discovery
//  4. BecameLeaderEvent → Re-trigger discovery for new leader's DeploymentScheduler
//  5. Discovery completes → Compare with previous endpoints → Publish HAProxyPodTerminatedEvent for removed pods → Publish HAProxyPodsDiscoveredEvent
type Component struct {
	*component.Base

	discovery *Discovery

	// State replay for leadership transitions
	discoveredReplayer *leadership.StateReplayer[*events.HAProxyPodsDiscoveredEvent]

	// State protected by mutex
	mu                   sync.RWMutex
	dataplanePort        int
	credentials          *coreconfig.Credentials
	podStore             types.Store
	lastEndpoints        map[string]string // Map of PodName → PodNamespace for tracking removals
	hasCredentials       bool
	hasDataplanePort     bool
	initialSyncComplete  bool // Set when ResourceSyncCompleteEvent for haproxy-pods is received
	initialDiscoveryDone bool // Set after the first discovery is performed
	lifecycleCtx         context.Context

	// Version filtering state
	localVersion   *dataplane.Version             // Local HAProxy version detected at startup
	admittedPods   map[string]*dataplane.Endpoint // Map of PodName → admitted Endpoint with cached version
	pendingRetries map[string]*retryState         // Map of PodName → retry state for pending pods

	// Retry timer for pending pods
	retryTimer        *time.Timer
	retryTimerDone    func()
	retryTimerMu      sync.Mutex
	retryCallbacks    sync.WaitGroup
	retryGeneration   uint64
	retryTimerStopped bool
}

// New creates a new Discovery event adapter component.
//
// Parameters:
//   - eventBus: The event bus for subscribing to and publishing events
//   - logger: Structured logger for observability
//
// Returns a configured Component ready to be started, or an error if
// local HAProxy version detection fails (which is fatal - the controller
// cannot start without knowing its local version for compatibility checking).
//
// Note: The Discovery pure component is created lazily when the dataplane port
// is configured via ConfigValidatedEvent. This constructor only detects the
// local HAProxy version for future compatibility checking.
func New(eventBus *busevents.EventBus, logger *slog.Logger) (*Component, error) {
	// Detect local HAProxy version at startup (fatal if fails). Happens
	// before the Base subscribes so a failed constructor doesn't leak a
	// subscription.
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("detecting local HAProxy version: %w", err)
	}

	c := &Component{
		discoveredReplayer: leadership.NewStateReplayer[*events.HAProxyPodsDiscoveredEvent](eventBus),
		lastEndpoints:      make(map[string]string),
		localVersion:       localVersion,
		admittedPods:       make(map[string]*dataplane.Endpoint),
		pendingRetries:     make(map[string]*retryState),
	}
	// The Base subscribes to the EventBus during construction (before
	// EventBus.Start()). This ensures proper startup synchronization without
	// timing-based sleeps. Typed subscription (EventTypes, not a catch-all)
	// so we only receive events we handle (reduces buffer pressure).
	c.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeConfigValidated,
			events.EventTypeCredentialsUpdated,
			events.EventTypeResourceIndexUpdated,
			events.EventTypeResourceSyncComplete,
			events.EventTypeBecameLeader,
			events.EventTypeDriftPreventionTriggered,
		},
	})

	c.Logger().Debug("Detected local HAProxy version",
		"version", localVersion.Full,
		"major", localVersion.Major,
		"minor", localVersion.Minor)

	return c, nil
}

// Start runs the embedded component.Base event loop until the context is
// cancelled.
//
// Note: Event subscription occurs in the constructor (New()) to ensure proper
// startup synchronization. ResourceSyncCompleteEvent is buffered until EventBus.Start()
// is called, so no events are missed.
func (c *Component) Start(ctx context.Context) error {
	c.mu.Lock()
	c.lifecycleCtx = ctx
	c.mu.Unlock()
	defer c.stopRetryTimer()

	return c.Base.Start(ctx)
}

func (c *Component) lifecycleContext() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lifecycleCtx
}

// stopRetryTimer prevents new retries and joins callbacks already in progress.
func (c *Component) stopRetryTimer() {
	c.retryTimerMu.Lock()
	c.retryTimerStopped = true
	c.retryGeneration++
	if c.retryTimer != nil {
		if c.retryTimer.Stop() && c.retryTimerDone != nil {
			c.retryTimerDone()
		}
		c.retryTimer = nil
		c.retryTimerDone = nil
	}
	c.retryTimerMu.Unlock()

	c.retryCallbacks.Wait()
}

// HandleEvent implements component.EventHandler: it processes incoming
// events and triggers discovery as needed.
func (c *Component) HandleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		c.handleConfigValidated(e)

	case *events.CredentialsUpdatedEvent:
		c.handleCredentialsUpdated(e)

	case *events.ResourceIndexUpdatedEvent:
		c.handleResourceIndexUpdated(e)

	case *events.ResourceSyncCompleteEvent:
		c.handleResourceSyncComplete(e)

	case *events.BecameLeaderEvent:
		c.handleBecameLeader(e)

	case *events.DriftPreventionTriggeredEvent:
		c.handleDriftPrevention(e)
	}
}

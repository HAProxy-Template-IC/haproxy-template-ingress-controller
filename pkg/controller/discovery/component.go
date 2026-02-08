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
// This package wraps the pure Discovery component (pkg/dataplane/discovery)
// with event-driven coordination. It subscribes to configuration, credentials,
// and pod change events, and publishes discovered HAProxy endpoints.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	EventBufferSize = 100

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
	discovery *Discovery
	eventBus  *busevents.EventBus
	logger    *slog.Logger

	// Subscribed in constructor for proper startup synchronization
	eventChan <-chan busevents.Event

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

	// Version filtering state
	localVersion   *dataplane.Version             // Local HAProxy version detected at startup
	admittedPods   map[string]*dataplane.Endpoint // Map of PodName → admitted Endpoint with cached version
	pendingRetries map[string]*retryState         // Map of PodName → retry state for pending pods

	// Retry timer for pending pods
	retryTimer   *time.Timer
	retryTimerMu sync.Mutex
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
	componentLogger := logger.With("component", ComponentName)

	// Detect local HAProxy version at startup (fatal if fails)
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to detect local HAProxy version: %w", err)
	}

	componentLogger.Debug("detected local HAProxy version",
		"version", localVersion.Full,
		"major", localVersion.Major,
		"minor", localVersion.Minor)

	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeCredentialsUpdated,
		events.EventTypeResourceIndexUpdated,
		events.EventTypeResourceSyncComplete,
		events.EventTypeBecameLeader,
	)

	return &Component{
		eventBus:           eventBus,
		logger:             componentLogger,
		eventChan:          eventChan,
		discoveredReplayer: leadership.NewStateReplayer[*events.HAProxyPodsDiscoveredEvent](eventBus),
		lastEndpoints:      make(map[string]string),
		localVersion:       localVersion,
		admittedPods:       make(map[string]*dataplane.Endpoint),
		pendingRetries:     make(map[string]*retryState),
	}, nil
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the Discovery component's event processing loop.
//
// This method:
//   - Maintains state from config and credential updates
//   - Triggers discovery when HAProxy pods change (via ResourceSyncCompleteEvent)
//   - Publishes discovered endpoints
//   - Runs until context is cancelled
//
// Returns an error if the event loop fails.
//
// Note: Event subscription occurs in the constructor (New()) to ensure proper
// startup synchronization. ResourceSyncCompleteEvent is buffered until EventBus.Start()
// is called, so no events are missed.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("discovery starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("Discovery shutting down", "reason", ctx.Err())
			return ctx.Err()
		}
	}
}

// handleEvent processes incoming events and triggers discovery as needed.
func (c *Component) handleEvent(event interface{}) {
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
	}
}

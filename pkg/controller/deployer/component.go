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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "deployer"

	// EventBufferSize is the size of the event subscription buffer.
	// Size 50: Low-volume component (~1-2 deployment events per reconciliation cycle).
	// Larger buffers reduce event drops during bursts but consume more memory.
	EventBufferSize = 50
)

// Component implements the deployer component.
//
// It subscribes to DeploymentScheduledEvent and deploys configurations to
// HAProxy instances. This is a stateless executor - all scheduling logic
// is handled by the DeploymentScheduler component.
//
// Event subscriptions:
//   - DeploymentScheduledEvent: Execute deployment to specified endpoints
//   - DeploymentCancelRequestEvent: Cancel in-progress deployment
//
// The component publishes deployment result events for observability.
type Component struct {
	eventBus             *busevents.EventBus
	eventChan            <-chan busevents.Event // Event subscription channel (subscribed in Start())
	logger               *slog.Logger
	deploymentInProgress atomic.Bool // Defensive: prevents concurrent deployments if scheduler has bugs

	// maxParallel limits concurrent Dataplane API operations during sync.
	// 0 means unlimited (not recommended for large configs).
	maxParallel int

	// rawPushThreshold triggers raw config push when change count exceeds this value.
	// 0 means disabled (fine-grained sync always used, except version=1).
	rawPushThreshold int

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}

	// versionCache caches the last-synced config version per endpoint URL.
	// Allows skipping expensive GetRawConfiguration() + parse on subsequent syncs
	// when the pod's config version hasn't changed.
	versionCache *configVersionCache

	// Deployment cancellation support
	cancelMu            sync.Mutex
	activeCorrelationID string             // Correlation ID of active deployment
	activeCancelFunc    context.CancelFunc // Cancel function for active deployment
	deploymentDone      chan struct{}      // Signals when deployment goroutine completes
}

// New creates a new Deployer component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - logger: Structured logger for component logging
//   - maxParallel: Maximum concurrent Dataplane API operations (0 = unlimited)
//   - rawPushThreshold: Change count threshold for raw config push (0 = disabled)
//
// Returns:
//   - A new Component instance ready to be started
func New(eventBus *busevents.EventBus, logger *slog.Logger, maxParallel, rawPushThreshold int) *Component {
	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &Component{
		eventBus:          eventBus,
		logger:            logger.With("component", ComponentName),
		maxParallel:       maxParallel,
		rawPushThreshold:  rawPushThreshold,
		versionCache:      newConfigVersionCache(),
		healthTracker:     lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		subscriptionReady: make(chan struct{}),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
func (c *Component) SubscriptionReady() <-chan struct{} {
	return c.subscriptionReady
}

// Start begins the deployer's event loop.
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
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// Subscribe to both DeploymentScheduledEvent and DeploymentCancelRequestEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeDeploymentScheduled,
		events.EventTypeDeploymentCancelRequest)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(c.subscriptionReady)

	// Clear version cache on start (handles leadership transitions - fresh state)
	c.versionCache.clear()

	c.logger.Debug("deployer starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(ctx, event)

		case <-ctx.Done():
			c.logger.Info("Deployer shutting down", "reason", ctx.Err())
			// Cancel any active deployment on shutdown
			c.cancelActiveDeployment("shutdown")
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(ctx context.Context, event busevents.Event) {
	switch e := event.(type) {
	case *events.DeploymentScheduledEvent:
		c.handleDeploymentScheduled(ctx, e)
	case *events.DeploymentCancelRequestEvent:
		c.handleDeploymentCancelRequest(e)
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

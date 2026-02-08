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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "config-publisher"

	// EventBufferSize is the buffer size for the event subscription channel.
	// Large buffer (200) to handle burst traffic during startup.
	// ConfigPublisher makes synchronous k8s API calls, so it processes events slowly
	// compared to the rate at which all-replica components publish them.
	EventBufferSize = 200

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
	event          *events.ValidationCompletedEvent
	templateConfig *v1alpha1.HAProxyTemplateConfig
	entry          *renderedConfigEntry
}

// validationFailedWorkItem represents a failed config publish task for the async worker.
type validationFailedWorkItem struct {
	correlationID  string
	event          *events.ValidationFailedEvent
	templateConfig *v1alpha1.HAProxyTemplateConfig
	entry          *renderedConfigEntry
}

// statusWorkItem represents a pod status update task for the async worker.
type statusWorkItem struct {
	event *events.ConfigAppliedToPodEvent
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

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}

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

	// lastPublishedChecksum tracks the checksum of the last successfully published config.
	// Used to skip redundant CRD updates when config content is unchanged.
	// Protected by mu.
	lastPublishedChecksum string
}

// New creates a new config publisher component.
func New(
	publisher *configpublisher.Publisher,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *Component {
	if logger == nil {
		logger = slog.Default()
	}

	// Note: eventChan is NOT subscribed here - subscription happens in Start().
	// This is a leader-only component that subscribes when Start() is called
	// (after leadership is acquired). All-replica components replay their state
	// on BecameLeaderEvent to ensure leader-only components receive current state.
	return &Component{
		publisher:            publisher,
		eventBus:             eventBus,
		logger:               logger.With("component", ComponentName),
		renderedConfigs:      make(map[string]*renderedConfigEntry),
		subscriptionReady:    make(chan struct{}),
		publishWork:          make(chan *publishWorkItem, publishWorkChannelSize),
		validationFailedWork: make(chan *validationFailedWorkItem, publishWorkChannelSize),
		statusWorkPending:    make(map[string]*statusWorkItem),
		statusWorkTrigger:    make(chan struct{}, statusWorkTriggerSize),
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
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeTemplateRendered,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeConfigAppliedToPod,
		events.EventTypeHAProxyPodTerminated,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeLostLeadership,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(c.subscriptionReady)

	c.logger.Debug("config publisher starting")

	// Start async workers for K8S API operations.
	// These workers process work items from their channels, allowing the main
	// event loop to continue processing events without blocking on slow API calls.
	go c.publishWorker(ctx)
	go c.validationFailedWorker(ctx)
	go c.statusWorker(ctx)

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("Config publisher shutting down", "reason", ctx.Err())
			return ctx.Err()
		}
	}
}

// handleEvent processes events from the event bus.
func (c *Component) handleEvent(event busevents.Event) {
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

	case *events.HAProxyPodTerminatedEvent:
		c.handlePodTerminated(e)

	case *events.HAProxyPodsDiscoveredEvent:
		c.handlePodsDiscovered(e)

	case *events.LostLeadershipEvent:
		c.handleLostLeadership(e)
	}
}

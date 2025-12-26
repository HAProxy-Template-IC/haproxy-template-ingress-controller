package leaderelection

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/client-go/kubernetes"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	k8sleaderelection "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
)

// Component is an event adapter that wraps the pure leader election component
// and publishes events for observability.
//
// This is the coordination layer that connects the pure k8s/leaderelection
// package to the controller's event bus.
type Component struct {
	elector        *k8sleaderelection.Elector
	eventBus       *busevents.EventBus
	logger         *slog.Logger
	identity       string
	leaseName      string
	leaseNamespace string
}

// New creates a new leader election component.
//
// This function wraps the pure leader election elector and adds event publishing
// for observability. The callbacks provided by the caller are wrapped to also
// publish events before/after the callback executes.
func New(
	config *k8sleaderelection.Config,
	clientset kubernetes.Interface,
	eventBus *busevents.EventBus,
	callbacks k8sleaderelection.Callbacks,
	logger *slog.Logger,
) (*Component, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if eventBus == nil {
		return nil, fmt.Errorf("event bus cannot be nil")
	}

	if logger == nil {
		logger = slog.Default()
	}

	c := &Component{
		eventBus:       eventBus,
		logger:         logger,
		identity:       config.Identity,
		leaseName:      config.LeaseName,
		leaseNamespace: config.LeaseNamespace,
	}

	// Wrap callbacks to publish events for observability
	wrappedCallbacks := k8sleaderelection.Callbacks{
		OnStartedLeading: func(ctx context.Context) {
			// Publish event BEFORE executing callback
			c.eventBus.Publish(events.NewBecameLeaderEvent(config.Identity))

			// Execute user callback
			if callbacks.OnStartedLeading != nil {
				callbacks.OnStartedLeading(ctx)
			}
		},
		OnStoppedLeading: func() {
			// Publish event BEFORE executing callback
			// Note: We don't have the reason at this point, so we use a generic message
			c.eventBus.Publish(events.NewLostLeadershipEvent(config.Identity, "lease_lost"))

			// Execute user callback
			if callbacks.OnStoppedLeading != nil {
				callbacks.OnStoppedLeading()
			}
		},
		OnNewLeader: func(identity string) {
			// Publish event
			isSelf := identity == config.Identity
			c.eventBus.Publish(events.NewNewLeaderObservedEvent(identity, isSelf))

			// Execute user callback
			if callbacks.OnNewLeader != nil {
				callbacks.OnNewLeader(identity)
			}
		},
	}

	// Create pure elector with wrapped callbacks
	elector, err := k8sleaderelection.New(config, clientset, wrappedCallbacks, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create elector: %w", err)
	}

	c.elector = elector

	return c, nil
}

// Start starts the leader election loop.
//
// This function blocks until the context is cancelled or an error occurs.
// It should be run in a goroutine.
func (c *Component) Start(ctx context.Context) error {
	// Publish start event with all metadata
	c.eventBus.Publish(events.NewLeaderElectionStartedEvent(c.identity, c.leaseName, c.leaseNamespace))

	// Start pure elector (blocks)
	return c.elector.Start(ctx)
}

// IsLeader returns true if this instance is currently the leader.
func (c *Component) IsLeader() bool {
	return c.elector.IsLeader()
}

// GetLeader returns the identity of the current leader.
func (c *Component) GetLeader() string {
	return c.elector.GetLeader()
}

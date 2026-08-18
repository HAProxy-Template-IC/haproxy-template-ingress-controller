package leaderelection

import (
	"context"
	"errors"
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
	epoch          *LeaseEpoch
}

// New creates a new leader election component.
//
// This function wraps the pure leader election elector and adds event publishing
// for observability. The callbacks provided by the caller are wrapped to also
// publish events before/after the callback executes.
// epoch may be nil, in which case no fencing epoch is claimed; the deployer
// then dispatches at epoch zero, which is correct only for a single writer.
func New(
	config *k8sleaderelection.Config,
	clientset kubernetes.Interface,
	eventBus *busevents.EventBus,
	callbacks k8sleaderelection.Callbacks,
	epoch *LeaseEpoch,
	logger *slog.Logger,
) (*Component, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}

	if eventBus == nil {
		return nil, errors.New("event bus cannot be nil")
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
		epoch:          epoch,
	}

	wrappedCallbacks := c.wrapCallbacks(config.Identity, callbacks)

	elector, err := k8sleaderelection.New(config, clientset, wrappedCallbacks, logger)
	if err != nil {
		return nil, fmt.Errorf("creating elector: %w", err)
	}

	c.elector = elector

	return c, nil
}

func (c *Component) wrapCallbacks(identity string, callbacks k8sleaderelection.Callbacks) k8sleaderelection.Callbacks {
	return k8sleaderelection.Callbacks{
		OnStartedLeading: func(ctx context.Context) {
			// PAUSE - temporarily buffer events during leadership transition
			// This prevents race conditions where leader-only components miss
			// events published before they finish subscribing.
			c.eventBus.Pause()

			// Claim the fencing epoch BEFORE anything leader-only can dispatch:
			// an apply carrying the previous term's epoch is refused by every
			// pod the new leader already wrote to.
			if err := c.epoch.Bump(ctx); err != nil {
				c.logger.Error("Claiming the leader epoch failed; applies stay fenced out until it succeeds",
					"error", err, "lease", c.leaseName)
			}

			// Publish event (buffered while paused)
			c.eventBus.Publish(events.NewBecameLeaderEvent(identity))

			// Execute user callback (starts leader-only components)
			if callbacks.OnStartedLeading != nil {
				callbacks.OnStartedLeading(ctx)
			}

			c.eventBus.StartContext(ctx)
		},
		OnStoppedLeading: func() {
			// Publish event BEFORE executing callback
			// Note: We don't have the reason at this point, so we use a generic message
			c.eventBus.Publish(events.NewLostLeadershipEvent(identity, "lease_lost"))

			// Execute user callback
			if callbacks.OnStoppedLeading != nil {
				callbacks.OnStoppedLeading()
			}
		},
		OnNewLeader: func(identity string) {
			// Publish event
			isSelf := identity == c.identity
			c.eventBus.Publish(events.NewNewLeaderObservedEvent(identity, isSelf))

			// Execute user callback
			if callbacks.OnNewLeader != nil {
				callbacks.OnNewLeader(identity)
			}
		},
	}
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

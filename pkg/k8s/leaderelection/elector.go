package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Config contains configuration for leader election.
type Config struct {
	// Enabled determines if leader election is active
	Enabled bool

	// Identity is the unique identifier of this instance (usually pod name)
	Identity string

	// LeaseName is the name of the Lease resource
	LeaseName string

	// LeaseNamespace is the namespace of the Lease resource
	LeaseNamespace string

	// LeaseDuration is the duration that non-leader candidates will wait to force acquire leadership
	LeaseDuration time.Duration

	// RenewDeadline is the duration that the acting leader will retry refreshing leadership before giving up
	RenewDeadline time.Duration

	// RetryPeriod is the duration the LeaderElector clients should wait between tries of actions
	RetryPeriod time.Duration

	// ReleaseOnCancel should be true to release leadership when the context is cancelled
	ReleaseOnCancel bool
}

// Callbacks contains callback functions for leader election events.
type Callbacks struct {
	// OnStartedLeading is called when the instance becomes the leader
	OnStartedLeading func(ctx context.Context)

	// OnStoppedLeading is called when the instance stops being the leader
	OnStoppedLeading func()

	// OnNewLeader is called when a new leader is observed (may be self or another instance)
	OnNewLeader func(identity string)
}

// Elector manages leader election using Kubernetes Lease resources.
//
// This is a pure component that wraps k8s.io/client-go/tools/leaderelection
// with a clean interface. It has no dependencies on the event bus or controller
// coordination logic.
type Elector struct {
	config    *Config
	clientset kubernetes.Interface
	callbacks Callbacks
	logger    *slog.Logger

	// Internal state
	mu      sync.RWMutex
	elector *leaderelection.LeaderElector
	leader  string
}

// New creates a new leader elector.
//
// The elector is not started until Start() is called.
func New(
	config *Config,
	clientset kubernetes.Interface,
	callbacks Callbacks,
	logger *slog.Logger,
) (*Elector, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}

	if !config.Enabled {
		return nil, errors.New("leader election is not enabled in config")
	}

	if config.Identity == "" {
		return nil, errors.New("identity cannot be empty")
	}

	if config.LeaseName == "" {
		return nil, errors.New("lease name cannot be empty")
	}

	if config.LeaseNamespace == "" {
		return nil, errors.New("lease namespace cannot be empty")
	}

	if clientset == nil {
		return nil, errors.New("clientset cannot be nil")
	}
	if config.LeaseDuration <= 0 {
		return nil, errors.New("lease duration must be greater than zero")
	}
	if config.RenewDeadline <= 0 {
		return nil, errors.New("renew deadline must be greater than zero")
	}
	if config.RetryPeriod <= 0 {
		return nil, errors.New("retry period must be greater than zero")
	}
	if config.LeaseDuration <= config.RenewDeadline {
		return nil, errors.New("lease duration must be greater than renew deadline")
	}
	if float64(config.RenewDeadline) <= leaderelection.JitterFactor*float64(config.RetryPeriod) {
		return nil, errors.New("renew deadline must be greater than retry period with jitter")
	}

	if logger == nil {
		logger = slog.Default()
	}

	e := &Elector{
		config:    config,
		clientset: clientset,
		callbacks: callbacks,
		logger:    logger,
	}

	return e, nil
}

// Start starts the leader election loop.
//
// This function blocks until the context is cancelled or an error occurs.
// It should be run in a goroutine.
//
// Lost-lease semantics: client-go's LeaderElector.Run returns permanently
// when an acquired lease is lost (renewal missed its deadline), after
// invoking OnStoppedLeading — it does NOT re-enter the acquire loop. In that
// case Start returns nil while the caller's context is still alive. Callers
// that need leadership to ever be re-acquired must treat that return as
// abnormal and restart election themselves (the controller does this by
// failing the iteration and reinitializing — see
// pkg/controller/leader.go's superviseElection).
func (e *Elector) Start(ctx context.Context) error {
	e.logger.Debug("Creating leader election lock",
		"lease_name", e.config.LeaseName,
		"lease_namespace", e.config.LeaseNamespace,
		"identity", e.config.Identity)

	// Create resource lock for Lease
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      e.config.LeaseName,
			Namespace: e.config.LeaseNamespace,
		},
		Client: e.clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: e.config.Identity,
		},
	}

	// Create leader election config
	leConfig := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.config.LeaseDuration,
		RenewDeadline:   e.config.RenewDeadline,
		RetryPeriod:     e.config.RetryPeriod,
		ReleaseOnCancel: e.config.ReleaseOnCancel,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				e.mu.Lock()
				e.leader = e.config.Identity
				e.mu.Unlock()

				e.logger.Debug("Started leading",
					"identity", e.config.Identity,
					"lease", e.config.LeaseName)

				if e.callbacks.OnStartedLeading != nil {
					e.callbacks.OnStartedLeading(ctx)
				}
			},
			OnStoppedLeading: func() {
				e.mu.RLock()
				previousLeader := e.leader
				e.mu.RUnlock()

				e.logger.Warn("Stopped leading",
					"identity", e.config.Identity,
					"previous_leader", previousLeader,
					"lease", e.config.LeaseName)

				if e.callbacks.OnStoppedLeading != nil {
					e.callbacks.OnStoppedLeading()
				}
			},
			OnNewLeader: func(identity string) {
				e.mu.Lock()
				e.leader = identity
				isSelf := identity == e.config.Identity
				e.mu.Unlock()

				e.logger.Debug("New leader observed",
					"leader", identity,
					"is_self", isSelf,
					"lease", e.config.LeaseName)

				if e.callbacks.OnNewLeader != nil {
					e.callbacks.OnNewLeader(identity)
				}
			},
		},
	}

	// Create leader elector
	elector, err := leaderelection.NewLeaderElector(leConfig)
	if err != nil {
		return fmt.Errorf("creating leader elector: %w", err)
	}

	e.mu.Lock()
	e.elector = elector
	e.mu.Unlock()

	e.logger.Info("Starting leader election loop",
		"identity", e.config.Identity,
		"lease", e.config.LeaseName,
		"namespace", e.config.LeaseNamespace)

	// Run leader election (blocks until context is cancelled)
	elector.Run(ctx)

	e.logger.Info("Leader election loop stopped",
		"identity", e.config.Identity)

	return nil
}

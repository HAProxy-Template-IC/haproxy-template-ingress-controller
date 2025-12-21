package leaderelection

import (
	"context"
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
	mu       sync.RWMutex
	elector  *leaderelection.LeaderElector
	isLeader bool
	leader   string
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
		return nil, fmt.Errorf("config cannot be nil")
	}

	if !config.Enabled {
		return nil, fmt.Errorf("leader election is not enabled in config")
	}

	if config.Identity == "" {
		return nil, fmt.Errorf("identity cannot be empty")
	}

	if config.LeaseName == "" {
		return nil, fmt.Errorf("lease name cannot be empty")
	}

	if config.LeaseNamespace == "" {
		return nil, fmt.Errorf("lease namespace cannot be empty")
	}

	if clientset == nil {
		return nil, fmt.Errorf("clientset cannot be nil")
	}

	if logger == nil {
		logger = slog.Default()
	}

	e := &Elector{
		config:    config,
		clientset: clientset,
		callbacks: callbacks,
		logger:    logger,
		isLeader:  false,
		leader:    "",
	}

	return e, nil
}

// Start starts the leader election loop.
//
// This function blocks until the context is cancelled or an error occurs.
// It should be run in a goroutine.
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
				e.isLeader = true
				e.leader = e.config.Identity
				e.mu.Unlock()

				e.logger.Info("Started leading",
					"identity", e.config.Identity,
					"lease", e.config.LeaseName)

				if e.callbacks.OnStartedLeading != nil {
					e.callbacks.OnStartedLeading(ctx)
				}
			},
			OnStoppedLeading: func() {
				e.mu.Lock()
				previousLeader := e.leader
				e.isLeader = false
				e.mu.Unlock()

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

				e.logger.Info("New leader observed",
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
		return fmt.Errorf("failed to create leader elector: %w", err)
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

// IsLeader returns true if this instance is currently the leader.
func (e *Elector) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.isLeader
}

// GetLeader returns the identity of the current leader.
//
// Returns empty string if no leader has been observed yet.
func (e *Elector) GetLeader() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

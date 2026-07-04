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

package controller

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	leaderelectionctrl "gitlab.com/haproxy-haptic/haptic/pkg/controller/leaderelection"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	k8sleaderelection "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"

	ctrlconfigpublisher "gitlab.com/haproxy-haptic/haptic/pkg/controller/configpublisher"
)

// leaderOnlyComponents holds components that only the leader should run.
type leaderOnlyComponents struct {
	deployer            *deployer.Component
	deploymentScheduler *deployer.DeploymentScheduler
	configPublisher     *ctrlconfigpublisher.Component
	ctx                 context.Context
	cancel              context.CancelFunc
}

// startReconciliationComponents starts all-replica reconciliation components using the lifecycle registry.
// Leader-only components (Deployer, DeploymentScheduler, ConfigPublisher) are NOT started here.
func startReconciliationComponents(
	iterCtx context.Context,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) {
	// Start all-replica components using the registry (tracked by errgroup for graceful shutdown)
	// The registry handles concurrent startup and error propagation
	startInErrGroup(errGroup, iterCtx, logger, cancel, "reconciliation component", func(ctx context.Context) error {
		return registry.StartAll(ctx, false)
	})

	logger.Info("Reconciliation components started via lifecycle registry (all replicas)",
		"component_count", registry.Count())
}

// startLeaderOnlyComponents starts components that only the leader should run using the lifecycle registry.
// Returns a leaderOnlyComponents struct with cancellation control.
//
// IMPORTANT: This function blocks until all leader-only components have completed their event
// subscription. This ensures the EventBus Pause/Start pattern works correctly - the leaderelection
// callback can safely call eventBus.Start() after this function returns, knowing all leader-only
// components are ready to receive replayed events.
func startLeaderOnlyComponents(
	parentCtx context.Context,
	wiring *reconciliationWiring,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	parentCancel context.CancelFunc,
	errGroup *errgroup.Group,
) *leaderOnlyComponents {
	// Create separate context for leader-only components
	leaderCtx, leaderCancel := context.WithCancel(parentCtx)

	// Start leader-only components using the async registry method.
	// StartLeaderOnlyComponentsAsync blocks until all components have signaled they're
	// subscription-ready, then returns. This ensures all leader-only components are subscribed
	// before the leaderelection callback calls eventBus.Start() to replay buffered events.
	errCh, err := registry.StartLeaderOnlyComponentsAsync(leaderCtx)
	if err != nil {
		logger.Error("Failed to start leader-only components", "error", err)
		parentCancel()
		// Return empty components struct - the error will propagate via errgroup
		return &leaderOnlyComponents{
			ctx:    leaderCtx,
			cancel: leaderCancel,
		}
	}

	// Track component errors in the errgroup for graceful shutdown coordination.
	// This goroutine monitors the error channel and propagates any component failures.
	errGroup.Go(func() error {
		select {
		case err, ok := <-errCh:
			if ok && err != nil && leaderCtx.Err() == nil {
				logger.Error("Leader-only component failed", "error", err)
				parentCancel()
				return err
			}
		case <-leaderCtx.Done():
			// Leadership lost or context cancelled
		}
		return nil
	})

	logger.Debug("Leader-only components started via lifecycle registry",
		"components", "Coordinator, DriftMonitor, Deployer, DeploymentScheduler, ConfigPublisher, StatusUpdater")

	return &leaderOnlyComponents{
		deployer:            wiring.deployer,
		deploymentScheduler: wiring.deploymentScheduler,
		configPublisher:     wiring.configPublisher,
		ctx:                 leaderCtx,
		cancel:              leaderCancel,
	}
}

// stopLeaderOnlyComponents stops leader-only components gracefully.
func stopLeaderOnlyComponents(components *leaderOnlyComponents, logger *slog.Logger) {
	if components == nil || components.cancel == nil {
		return
	}

	logger.Info("Stopping leader-only components")
	components.cancel()

	// Brief pause to allow graceful shutdown
	time.Sleep(timeouts.GracefulStopDelay)

	logger.Info("Leader-only components stopped")
}

// leaderCallbackDeps holds dependencies for leader election callbacks.
// Extracting these to a struct makes the dependencies explicit rather than
// relying on closure scope, improving code clarity and testability.
type leaderCallbackDeps struct {
	iterCtx  context.Context
	wiring   *reconciliationWiring
	registry *lifecycle.Registry
	logger   *slog.Logger
	cancel   context.CancelFunc
	podName  string
	errGroup *errgroup.Group
}

// leaderCallbackState holds mutable state shared across leader callbacks.
// This state is protected by a mutex since callbacks may be invoked concurrently.
type leaderCallbackState struct {
	mu         sync.Mutex
	components *leaderOnlyComponents
}

// makeLeaderCallbacks creates leader election callbacks with explicit dependencies.
// The returned state struct allows the caller to access leader component state.
func makeLeaderCallbacks(deps leaderCallbackDeps) (k8sleaderelection.Callbacks, *leaderCallbackState) {
	state := &leaderCallbackState{}

	callbacks := k8sleaderelection.Callbacks{
		OnStartedLeading: func(_ context.Context) {
			deps.logger.Debug("Became leader, starting deployment components")
			state.mu.Lock()
			defer state.mu.Unlock()
			state.components = startLeaderOnlyComponents(
				deps.iterCtx,
				deps.wiring,
				deps.registry,
				deps.logger,
				deps.cancel,
				deps.errGroup,
			)
		},
		OnStoppedLeading: func() {
			deps.logger.Warn("Lost leadership, stopping deployment components")
			state.mu.Lock()
			defer state.mu.Unlock()
			stopLeaderOnlyComponents(state.components, deps.logger)
			state.components = nil
		},
		OnNewLeader: func(identity string) {
			deps.logger.Debug("New leader observed",
				"leader_identity", identity,
				"is_self", identity == deps.podName,
			)
		},
	}

	return callbacks, state
}

// superviseElection runs the leader-election loop and converts an unexpected
// exit into an iteration-fatal error.
//
// client-go's LeaderElector.Run returns permanently once an acquired lease is
// lost (a renewal missed its deadline — e.g. the apiserver or this pod was
// starved for longer than renewDeadline). It does NOT re-enter the acquire
// loop. Without supervision the replica would keep running its all-replica
// components with a dead elector: never re-acquiring leadership, never
// deploying — a permanent standby zombie (fatal on single-replica
// deployments, issue #57).
//
// Returning an error here cancels the iteration via the errgroup context, and
// the main run loop reinitializes — restarting the election loop with the
// same identity. Re-acquisition without an iteration restart is not possible
// today: the lifecycle registry only starts leader-only components from
// Pending/Standby status, and a stopped term leaves them Stopped/Failed.
//
// A nil return from start with the context still alive is exactly the
// lost-lease case: for a replica that never acquired the lease, Run blocks in
// the acquire loop until the context is cancelled.
func superviseElection(ctx context.Context, start func(context.Context) error, logger *slog.Logger) error {
	err := start(ctx)
	select {
	case <-ctx.Done():
		// Normal teardown (shutdown or config-change reinitialization);
		// any error from the elector here is a cancellation artifact.
		return nil
	default:
	}
	if err == nil {
		err = errors.New("leader election loop exited: lease lost without shutdown")
	}
	logger.Error("Leader election failed, triggering reinitialization to restart election", "error", err)
	return err
}

// setupLeaderElection initializes leader election or starts leader-only components immediately.
//
// Returns leader callback state for lifecycle management. The state contains a mutex-protected
// pointer to the leader-only components, which is nil until leadership is acquired.
func setupLeaderElection(
	setup *componentSetup,
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	wiring *reconciliationWiring,
	logger *slog.Logger,
) *leaderCallbackState {
	if cfg.Controller.LeaderElection.Enabled {
		// Read pod identity from environment
		podName := os.Getenv("POD_NAME")
		podNamespace := os.Getenv("POD_NAMESPACE")

		if podName == "" {
			logger.Warn("POD_NAME environment variable not set, using hostname as identity")
			hostname, err := os.Hostname()
			if err != nil {
				logger.Error("Failed to get hostname for leader election identity", "error", err)
			}
			podName = hostname
		}

		if podNamespace == "" {
			podNamespace = k8sClient.Namespace()
			logger.Debug("POD_NAMESPACE not set, using client namespace", "namespace", podNamespace)
		}

		// Create pure leader election config
		leConfig := &k8sleaderelection.Config{
			Enabled:         true,
			Identity:        podName,
			LeaseName:       cfg.Controller.LeaderElection.LeaseName,
			LeaseNamespace:  podNamespace,
			LeaseDuration:   cfg.Controller.LeaderElection.GetLeaseDuration(),
			RenewDeadline:   cfg.Controller.LeaderElection.GetRenewDeadline(),
			RetryPeriod:     cfg.Controller.LeaderElection.GetRetryPeriod(),
			ReleaseOnCancel: true,
		}

		// Create callbacks with explicit dependencies
		callbacks, state := makeLeaderCallbacks(leaderCallbackDeps{
			iterCtx:  setup.IterCtx,
			wiring:   wiring,
			registry: setup.Registry,
			logger:   logger,
			cancel:   setup.Cancel,
			podName:  podName,
			errGroup: setup.ErrGroup,
		})

		// Create leader election component (event adapter)
		elector, err := leaderelectionctrl.New(leConfig, k8sClient.Clientset(), setup.Bus, callbacks, logger)
		if err != nil {
			logger.Error("Failed to create leader elector", "error", err)
			return state
		}

		// Start leader election loop in errgroup for graceful shutdown
		// This ensures the elector can release the lease on context cancellation
		setup.ErrGroup.Go(func() error {
			return superviseElection(setup.IterCtx, elector.Start, logger)
		})

		logger.Info("Leader election initialized", "identity", podName, "lease_name", leConfig.LeaseName, "lease_namespace", leConfig.LeaseNamespace)
		return state
	}

	// Leader election disabled - start leader-only components immediately.
	// Use the same Pause/Start pattern as leaderelection/component.go to ensure
	// leader-only components subscribe before any buffered events are replayed.
	logger.Info("Leader election disabled, starting all components")
	setup.Bus.Pause()
	setup.Bus.Publish(events.NewBecameLeaderEvent("standalone"))
	state := &leaderCallbackState{
		components: startLeaderOnlyComponents(setup.IterCtx, wiring, setup.Registry, logger, setup.Cancel, setup.ErrGroup),
	}
	setup.Bus.Start()
	return state
}

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
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
	leaderelectionctrl "gitlab.com/haproxy-haptic/haptic/pkg/controller/leaderelection"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
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
	components *reconciliationComponents,
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
		logger.Error("failed to start leader-only components", "error", err)
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
				logger.Error("leader-only component failed", "error", err)
				parentCancel()
				return err
			}
		case <-leaderCtx.Done():
			// Leadership lost or context cancelled
		}
		return nil
	})

	logger.Debug("Leader-only components started via lifecycle registry",
		"components", "Deployer, DeploymentScheduler, ConfigPublisher, Renderer, DriftMonitor")

	return &leaderOnlyComponents{
		deployer:            components.deployer,
		deploymentScheduler: components.deploymentScheduler,
		configPublisher:     components.configPublisher,
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
	time.Sleep(100 * time.Millisecond)

	logger.Info("Leader-only components stopped")
}

// leaderCallbackDeps holds dependencies for leader election callbacks.
// Extracting these to a struct makes the dependencies explicit rather than
// relying on closure scope, improving code clarity and testability.
type leaderCallbackDeps struct {
	iterCtx         context.Context
	reconComponents *reconciliationComponents
	registry        *lifecycle.Registry
	logger          *slog.Logger
	cancel          context.CancelFunc
	podName         string
	errGroup        *errgroup.Group
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
				deps.reconComponents,
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

// setupLeaderElection initializes leader election or starts leader-only components immediately.
//
// Returns leader callback state for lifecycle management. The state contains a mutex-protected
// pointer to the leader-only components, which is nil until leadership is acquired.
func setupLeaderElection(
	iterCtx context.Context,
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	reconComponents *reconciliationComponents,
	registry *lifecycle.Registry,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	cancel context.CancelFunc,
	g *errgroup.Group,
) *leaderCallbackState {
	if cfg.Controller.LeaderElection.Enabled {
		// Read pod identity from environment
		podName := os.Getenv("POD_NAME")
		podNamespace := os.Getenv("POD_NAMESPACE")

		if podName == "" {
			logger.Warn("POD_NAME environment variable not set, using hostname as identity")
			hostname, _ := os.Hostname()
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
			iterCtx:         iterCtx,
			reconComponents: reconComponents,
			registry:        registry,
			logger:          logger,
			cancel:          cancel,
			podName:         podName,
			errGroup:        g,
		})

		// Create leader election component (event adapter)
		elector, err := leaderelectionctrl.New(leConfig, k8sClient.Clientset(), eventBus, callbacks, logger)
		if err != nil {
			logger.Error("Failed to create leader elector", "error", err)
			return state
		}

		// Start leader election loop in errgroup for graceful shutdown
		// This ensures the elector can release the lease on context cancellation
		g.Go(func() error {
			if err := elector.Start(iterCtx); err != nil {
				logger.Error("leader election failed", "error", err)
				return err
			}
			return nil
		})

		logger.Info("Leader election initialized", "identity", podName, "lease_name", leConfig.LeaseName, "lease_namespace", leConfig.LeaseNamespace)
		return state
	}

	// Leader election disabled - start leader-only components immediately
	logger.Info("Leader election disabled, starting all components")
	state := &leaderCallbackState{
		components: startLeaderOnlyComponents(iterCtx, reconComponents, registry, logger, cancel, g),
	}
	return state
}

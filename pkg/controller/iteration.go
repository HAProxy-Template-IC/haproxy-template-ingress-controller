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
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	dryrunvalidator "gitlab.com/haproxy-haptic/haptic/pkg/controller/dryrunvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// buildAndRegisterPluggableValidatorManager constructs the validator
// manager from the configured spec.validators and registers a
// cleanup callback on the iteration setup so the manager's
// connection pools are drained on teardown (every iteration
// restart, every config change, every shutdown).
//
// Returns nil and logs the underlying error when the slice contains
// duplicate names, empty paths, or malformed globs — all CRD-schema
// violations the apiserver should have rejected before this code
// path runs, so the failure is treated as a degraded state rather
// than a fatal one. The /healthz check downstream sees a nil
// manager and skips the "pluggable-validators" entry, surfacing the
// misconfiguration through the absence of the expected entry plus
// the controller log.
func buildAndRegisterPluggableValidatorManager(setup *componentSetup, cfg *coreconfig.Config, logger *slog.Logger) *pluggablevalidator.Manager {
	configs := make([]pluggablevalidator.ManagerConfig, 0, len(cfg.Validators))
	for _, v := range cfg.Validators {
		mc := pluggablevalidator.ManagerConfig{
			Name:           v.Name,
			SocketPath:     v.SocketPath,
			Files:          v.Files,
			MaxConnections: int(v.MaxConnections),
		}
		if v.TimeoutMs > 0 {
			mc.Timeout = time.Duration(v.TimeoutMs) * time.Millisecond
		}
		configs = append(configs, mc)
	}
	mgr, err := pluggablevalidator.NewManager(logger, configs)
	if err != nil {
		logger.Error("pluggable-validator manager construction failed; feature disabled for this iteration",
			slog.Any("error", err))
		return nil
	}
	if mgr.Configured() {
		logger.Info("pluggable validators registered",
			slog.Int("count", len(configs)),
			slog.Any("names", mgr.Names()),
		)
	}
	setup.AddCleanup(func() {
		logger.Debug("closing pluggable-validator connection pools")
		mgr.Close()
	})
	return mgr
}

// runIteration runs a single controller iteration.
//
// This function orchestrates the initialization sequence:
//  1. Fetches and validates the initial HAProxyTemplateConfig CRD and credentials Secret
//  2. Creates and starts all event-driven components
//  3. Creates and starts resource watchers, waits for sync
//  4. Creates and starts SingleWatchers for the CRD and credentials Secret, waits for sync
//  5. Starts the EventBus (releases buffered events)
//  6. Starts reconciliation components (Stage 5)
//  7. Starts debug infrastructure (StateCache, EventBuffer, debug server if enabled)
//  8. Waits for config change signal or context cancellation
//
// Returns:
//   - Error if initialization fails (causes retry)
//   - nil if context is cancelled or config change occurs (normal exit)
func runIteration(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	webhookCertSecretName string,
	debugPort int,
	infra *persistentInfra,
	logger *slog.Logger,
) error {
	logger.Info("Starting controller iteration")

	// Clear the introspection registry to remove stale references from previous iteration
	// The registry persists across iterations to avoid port rebinding issues
	infra.IntrospectionRegistry.Clear()

	// Create config state tracker for health checks
	// This allows the health endpoint to report unhealthy status until config is loaded
	state := &configState{}

	// 0. Setup components BEFORE fetching config so we can start servers early
	setup := setupComponents(ctx, infra.IntrospectionRegistry, logger)
	defer setup.Cancel()

	// 0.25. Create EventBuffer early (subscribes in constructor)
	// Must be created before startEarlyInfrastructureServers() to register /debug/events handler
	// and before EventBus.Start() to ensure proper subscription ordering
	eventBuffer := debug.NewEventBuffer(DebugEventBufferSize, setup.Bus)

	// 0.5. Start infrastructure servers EARLY (before config fetch)
	// This allows debugging startup issues and makes health endpoint available immediately
	// The health checker will report unhealthy until config is loaded
	// Uses two-phase initialization (Setup/Serve) to register /debug/events before serving
	// The introspection server persists across iterations to avoid port rebinding issues
	// We pass the main ctx (not setup.IterCtx) so the server stays alive across iterations
	startEarlyInfrastructureServers(ctx, debugPort, infra, setup, state, eventBuffer, logger)

	// 1. Wait for HAProxyTemplateConfig to exist (polls every 5s)
	// This handles the race condition during fresh installs where the controller pod
	// may start before the HAProxyTemplateConfig CR is fully available
	if err := waitForInitialConfig(ctx, k8sClient, crdName, crdGVR, state, logger); err != nil {
		return err
	}

	// 2. Fetch and validate initial configuration (now guaranteed to exist)
	bundle, err := fetchAndValidateInitialConfig(
		ctx, k8sClient, crdName, secretName, webhookCertSecretName,
		crdGVR, secretGVR, logger,
	)
	if err != nil {
		return err
	}
	cfg, crd, creds, webhookCerts := bundle.Config, bundle.CRD, bundle.Credentials, bundle.WebhookCerts

	// Mark config as loaded and record initial CRD/Secret versions so the
	// bootstrap watcher events don't trigger redundant reinitialization.
	// Later events with different versions still flow through
	// configChangeCh and trigger iteration restart — that's how
	// credentials/webhook-cert rotation reaches the controller.
	finalizeConfigLoad(state, setup, crd.GetResourceVersion(), bundle.CredentialsVersion, bundle.WebhookCertVersion)

	// 3. Setup resource watchers
	resourceWatcher, err := setupResourceWatchers(setup.IterCtx, cfg, k8sClient, setup.Bus, logger, setup.Cancel, setup.ErrGroup)
	if err != nil {
		return err
	}

	// Register stores with ResourceStoreManager for webhook validation
	registerResourceStores(resourceWatcher, setup.StoreManager, logger)

	// 4. Setup config watchers
	if err := setupConfigWatchers(
		setup.IterCtx, k8sClient, crdName, secretName,
		crdGVR, secretGVR, setup.Bus, logger, setup.Cancel, setup.ErrGroup,
	); err != nil {
		return err
	}

	// 4.5. Setup CurrentConfigStore for slot-aware server assignment
	currentConfigStore, err := setupCurrentConfigStore(setup.IterCtx, k8sClient, crdName, haproxyCfgGVR, logger, setup.Cancel, setup.ErrGroup)
	if err != nil {
		return err
	}

	// 5. Initialize StateCache and start background components
	stateCache := NewStateCache(setup.Bus, resourceWatcher, logger)
	startBackgroundComponents(setup.IterCtx, stateCache, setup.MetricsComponent, logger)

	// 6. Create reconciliation components (Stage 5)
	// Components subscribe during construction, before EventBus.Start()
	logger.Info("Stage 5: Creating reconciliation components")
	reconComponents, err := setupReconciliation(setup.IterCtx, cfg, crd, creds, k8sClient, resourceWatcher, currentConfigStore, setup.StoreManager, setup.Bus, setup.Registry, logger, setup.Cancel, setup.ErrGroup)
	if err != nil {
		return err
	}

	// 6.1. EventBuffer was already created early (step 0.25) for /debug/events handler
	// It subscribes in constructor before EventBus.Start() for proper subscription ordering

	// 6.2. Construct the pluggable-validator Manager from spec.validators.
	// Pure synchronous service (no event subs, no goroutines) so order
	// relative to EventBus.Start() doesn't matter. The Manager's
	// Healthy() output is plumbed into /healthz below; admission-time
	// dispatch happens via the DryRunValidator wired up next. The helper
	// registers Close() on the iteration cleanup hook so connection
	// pools drain on teardown.
	pluggableMgr := buildAndRegisterPluggableValidatorManager(setup, cfg, logger)

	// 6.3. Create DryRunValidator for webhook validation.
	// The validator is a synchronous library (ValidateDirect); the proposal
	// validator it wires up subscribes to ProposalValidationRequestedEvent in
	// its constructor, so this must run before EventBus.Start(). The
	// pluggable-validator Manager is injected here so admission-time
	// validation can dispatch the rendered file set to validator sidecars
	// (e.g. SPOA hub --validate-socket) after the standard pipeline
	// passes. A nil Manager is the no-validators-configured case.
	var dryrunValidator *dryrunvalidator.Component
	if webhook.HasWebhookEnabled(cfg) {
		var err error
		dryrunValidator, err = createDryRunValidator(setup.IterCtx, cfg, setup.Bus, setup.StoreManager, reconComponents.capabilities, reconComponents.httpStore, pluggableMgr, logger)
		if err != nil && !errors.Is(err, errNoWebhookRules) {
			return fmt.Errorf("creating dry-run validator: %w", err)
		}
		if errors.Is(err, errNoWebhookRules) {
			logger.Debug("DryRunValidator not created: no webhook validation rules configured")
		}
	}

	// 6.5. Start the EventBus (releases buffered events and begins normal operation)
	// All components have now subscribed during their construction, so we can safely start
	// the bus without race conditions or timing-based sleeps
	logger.Info("Starting EventBus (all components subscribed)")
	setup.Bus.Start()

	// 7. Setup leader election
	logger.Info("Stage 6: Initializing leader election")
	leaderState := setupLeaderElection(
		setup.IterCtx, cfg, k8sClient, reconComponents, setup.Registry, setup.Bus, logger, setup.Cancel, setup.ErrGroup,
	)

	// 8. Setup webhook validation if enabled (start pre-created DryRunValidator)
	maybeSetupWebhook(cfg, webhookCerts, setup, k8sClient, dryrunValidator, logger)

	// 9. Setup debug and metrics infrastructure (start pre-created EventBuffer)
	// Note: The introspection server is already started by startEarlyInfrastructureServers
	// This call registers debug variables and updates the health checker
	setupInfrastructureServers(setup.IterCtx, setup, stateCache, eventBuffer, pluggableMgr, logger)

	// 10. Enable reinitialization signaling now that startup is complete
	// This allows future ConfigValidatedEvents to trigger controller reinitialization.
	// During startup, multiple events occur (watcher sync, status updates) that should
	// NOT trigger reinitialization - those were skipped while this was disabled.
	setup.ConfigChangeHandler.EnableReinitialization()

	logger.Info("Controller iteration initialized successfully - entering event loop")

	// 10. Wait for config change signal or context cancellation
	select {
	case <-setup.IterCtx.Done():
		handleIterationCancellation(leaderState, setup, logger)
		return nil

	case newConfig := <-setup.ConfigChangeCh:
		logger.Info("Configuration change detected, triggering reinitialization",
			"new_config_version", fmt.Sprintf("%p", newConfig))
		handleConfigurationChange(leaderState, setup, logger)
		return nil
	}
}

// handleIterationCancellation handles cleanup when the controller iteration is cancelled.
func handleIterationCancellation(
	leaderState *leaderCallbackState,
	setup *componentSetup,
	logger *slog.Logger,
) {
	logger.Info("Controller iteration cancelled", "reason", setup.IterCtx.Err())

	// Cleanup leader-only components if still running
	leaderState.mu.Lock()
	if leaderState.components != nil {
		stopLeaderOnlyComponents(leaderState.components, logger)
	}
	leaderState.mu.Unlock()

	// Wait for all goroutines to finish gracefully
	waitForGoroutinesToFinish(setup.ErrGroup, logger, "Shutdown")

	// Run registered cleanups (drains connection pools, etc.).
	setup.RunCleanups()
}

// handleConfigurationChange handles cleanup and reinitialization when configuration changes.
func handleConfigurationChange(
	leaderState *leaderCallbackState,
	setup *componentSetup,
	logger *slog.Logger,
) {
	// Stop leader-only components before canceling context
	leaderState.mu.Lock()
	if leaderState.components != nil {
		stopLeaderOnlyComponents(leaderState.components, logger)
	}
	leaderState.mu.Unlock()

	// Cancel iteration context to stop all components and watchers
	setup.Cancel()

	// Wait for all goroutines to finish before reinitializing
	waitForGoroutinesToFinish(setup.ErrGroup, logger, "Reinitialization")

	// Run registered cleanups (drains connection pools, etc.).
	setup.RunCleanups()

	logger.Info("Reinitialization triggered - starting new iteration")
}

// maybeSetupWebhook sets up webhook validation if enabled in the configuration.
// When webhook validation is enabled but no certificate secret is configured,
// it logs a warning and skips setup instead of panicking.
func maybeSetupWebhook(
	cfg *coreconfig.Config,
	webhookCerts *WebhookCertificates,
	setup *componentSetup,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component,
	logger *slog.Logger,
) {
	if !webhook.HasWebhookEnabled(cfg) {
		return
	}
	if webhookCerts == nil {
		logger.Warn("Webhook validation is enabled in config but no webhook certificate secret is configured - skipping webhook setup")
		return
	}
	logger.Info("Stage 7: Setting up webhook validation")
	setupWebhook(setup.IterCtx, cfg, webhookCerts, k8sClient, dryrunValidator, logger, setup.MetricsComponent.Metrics(), setup.Cancel, setup.ErrGroup)
}

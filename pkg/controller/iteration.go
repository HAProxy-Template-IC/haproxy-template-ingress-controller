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
		logger.Error("Pluggable-validator manager construction failed; feature disabled for this iteration",
			slog.Any("error", err))
		return nil
	}
	if mgr.Configured() {
		logger.Info("Pluggable validators registered",
			slog.Int("count", len(configs)),
			slog.Any("names", mgr.Names()),
		)
	}
	setup.AddCleanup(func() {
		logger.Debug("Closing pluggable-validator connection pools")
		mgr.Close()
	})
	return mgr
}

// waitAndLoadInitialConfig polls until the HAProxyTemplateConfig exists (the
// fresh-install race: the controller pod may start before the CR is applied),
// then fetches and structurally validates it together with the credentials
// Secret.
func waitAndLoadInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName, secretName string,
	state *configState,
	logger *slog.Logger,
) (*InitialConfigBundle, error) {
	if err := waitForInitialConfig(ctx, k8sClient, crdName, crdGVR, state, logger); err != nil {
		return nil, err
	}
	return fetchAndValidateInitialConfig(
		ctx, k8sClient, crdName, secretName,
		crdGVR, secretGVR, logger,
	)
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
	webhookCertDir string,
	webhookAdmissionTimeouts WebhookAdmissionTimeouts,
	debugPort int,
	webhookPort int,
	infra *persistentInfra,
	logger *slog.Logger,
) error {
	logger.Info("Starting controller iteration")

	// Reinit-grace accounting (a voluntary restart must not flip /healthz
	// unhealthy for the bounded rebuild window — see InReinitGrace), then
	// clear the persistent introspection registry of the previous
	// iteration's entries, then a fresh per-iteration health state.
	state := beginIteration(infra)

	// 0. Setup components BEFORE fetching config so we can start servers early.
	// The type bootstrapper is also reused by the step-2.5 startup validationTests
	// gate below, so it's hoisted to a local rather than constructed inline.
	typeBootstrapper := newIterationTypeBootstrapper(k8sClient, logger)
	setup := setupComponents(ctx, infra.IntrospectionRegistry, typeBootstrapper, logger)
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

	// 1+2. Wait for the HAProxyTemplateConfig to exist (fresh-install race),
	// then fetch and validate it together with the credentials Secret.
	bundle, err := waitAndLoadInitialConfig(ctx, k8sClient, crdName, secretName, state, logger)
	if err != nil {
		return err
	}
	cfg, crd, creds := bundle.Config, bundle.CRD, bundle.Credentials

	// 2.4. Resolve watched-resource candidate versions against live discovery
	// and derive the EFFECTIVE config: resolved entries carry the served
	// version in APIVersion, unavailable optional resources are dropped, and
	// snippets/tests requiring them are stripped. Everything downstream (the
	// validationTests gate, watchers, typebootstrap, webhook, dry-run,
	// testrunner, render context) consumes the effective config, so the
	// literal-APIVersion consumers need no version awareness of their own.
	// A required-but-unserved resource errors here — failing the iteration
	// fast (retried by the run loop) instead of hanging in informer sync.
	// The CRD watch started alongside re-resolves on relevant CRD changes so
	// late installation, in-place upgrade, and serving removal converge at
	// runtime (no helm operation, no pod restart).
	cfg, err = installEffectiveConfig(ctx, cfg, k8sClient, setup, infra, logger)
	if err != nil {
		return err
	}

	// 2.5. Fail-closed on the initial config's embedded validationTests. A
	// running controller already rejects a live CRD change whose tests fail (the
	// scatter-gather reinit gate), but a fresh pod — every helm upgrade restarts
	// the controllers — loads the config after only structural validation. Run
	// the suite here so a restart/upgrade can't quietly serve a config that fails
	// its own tests. Returning an error keeps the controller un-initialized
	// (/healthz 503) and the liveness probe restarts the pod, so the bad config
	// surfaces as CrashLoopBackOff and a rolling upgrade stalls on the old, good
	// pods. No validationTests in the config → zero-cost pass.
	if err := validateInitialConfigValidationTests(ctx, cfg, typeBootstrapper, logger); err != nil {
		return fmt.Errorf("initial HAProxyTemplateConfig %q failed validationTests on load: %w", crdName, err)
	}

	// Mark config as loaded and record initial CRD/Secret versions so the
	// bootstrap watcher events don't trigger redundant reinitialization.
	// Later events with different versions still flow through
	// configChangeCh and trigger iteration restart — that's how
	// credentials rotation reaches the controller.
	finalizeConfigLoad(state, setup, crd.GetResourceVersion(), bundle.CredentialsVersion)

	// 3. Setup resource watchers
	resourceWatcher, err := setupResourceWatchers(setup, cfg, k8sClient, logger)
	if err != nil {
		return err
	}

	// Build the store provider (used for webhook dry-run validation) from the
	// resource watcher's live stores.
	storeProvider := buildStoreProvider(resourceWatcher)

	// 4. Setup config watchers
	if err := setupConfigWatchers(
		setup, k8sClient, crdName, secretName,
		crdGVR, secretGVR, logger,
	); err != nil {
		return err
	}

	// 4.5. Setup CurrentConfigStore for slot-aware server assignment
	currentConfigStore, err := setupCurrentConfigStore(setup, k8sClient, crdName, haproxyCfgGVR, logger)
	if err != nil {
		return err
	}

	// 5. Initialize StateCache and start background components
	stateCache := NewStateCache(setup.Bus, resourceWatcher, logger)
	startBackgroundComponents(setup.IterCtx, stateCache, setup.MetricsComponent, logger)

	// 6. Create reconciliation components (Stage 5)
	// Components subscribe during construction, before EventBus.Start()
	logger.Info("Stage 5: Creating reconciliation components")
	wiring, err := setupReconciliation(setup, cfg, crd, creds, k8sClient, resourceWatcher, currentConfigStore, storeProvider, logger)
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
	// The webhook server runs whenever the chart mounted a TLS cert directory
	// (the maybeSetupWebhook caller gates on `webhookCertDir != ""`).
	// Construction is NOT gated on whether any watched resource enables
	// validation: the HAProxyTemplateConfig admission webhook is independent of
	// watched resources, and the chart may have provisioned a cert +
	// ValidatingWebhookConfiguration solely for HAProxyTemplateConfig admission.
	// The DryRunValidator is nil when no watched-resource rules exist; the
	// ConfigValidator is always present so HAProxyTemplateConfig admissions land
	// on a real handler instead of the pure server's fail-open path.
	dryrunValidator, configValidator, err := createDryRunValidator(cfg, setup.Bus, storeProvider, wiring, pluggableMgr, k8sClient, logger)
	if err != nil {
		return fmt.Errorf("creating webhook validators: %w", err)
	}

	// 6.5. Start the EventBus (releases buffered events and begins normal operation)
	// All components have now subscribed during their construction, so we can safely start
	// the bus without race conditions or timing-based sleeps
	logger.Info("Starting EventBus (all components subscribed)")
	setup.Bus.Start()

	// 7. Setup leader election
	logger.Info("Stage 6: Initializing leader election")
	leaderState := setupLeaderElection(setup, cfg, k8sClient, wiring, logger)

	// 8. Setup webhook validation if enabled (start pre-created DryRunValidator)
	maybeSetupWebhook(cfg, webhookCertDir, webhookAdmissionTimeouts, webhookPort, setup, k8sClient, dryrunValidator, configValidator, logger)

	// 9. Setup debug and metrics infrastructure (start pre-created EventBuffer)
	// Note: The introspection server is already started by startEarlyInfrastructureServers
	// This call registers debug variables and updates the health checker
	setupInfrastructureServers(setup.IterCtx, setup, state, infra, stateCache, eventBuffer, pluggableMgr, logger)

	// 10. Enable reinitialization signaling now that startup is complete
	// This allows future ConfigValidatedEvents to trigger controller reinitialization.
	// During startup, multiple events occur (watcher sync, status updates) that should
	// NOT trigger reinitialization - those were skipped while this was disabled.
	setup.ConfigChangeHandler.EnableReinitialization()

	// 11. Flip the "initialized" health bit. /healthz returns 503 until
	// this fires and 200 (assuming other components healthy) after.
	// This is the canonical "controller is ready to accept work"
	// signal — see configState.SetInitialized's docstring and the
	// "initialized" entry in the full health checker installed by
	// setupInfrastructureServers.
	state.SetInitialized()
	infra.NoteInitialized()

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

// maybeSetupWebhook sets up the webhook server when the chart has mounted a
// TLS cert directory. The cert-dir path — not the CRD's watchedResources
// section — is the operative gate: the chart's `webhook.enabled` toggle
// controls the cert Secret mount + WEBHOOK_CERT_DIR, and the
// ValidatingWebhookConfiguration may cover HAProxyTemplateConfig admission
// even when no watched-resource has `enableValidationWebhook: true`. Without a
// cert dir, the server can't bind the TLS listener regardless of what we'd
// want to register.
func maybeSetupWebhook(
	cfg *coreconfig.Config,
	webhookCertDir string,
	webhookAdmissionTimeouts WebhookAdmissionTimeouts,
	webhookPort int,
	setup *componentSetup,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component,
	configValidator webhook.ConfigValidatorFunc,
	logger *slog.Logger,
) {
	if webhookCertDir == "" {
		logger.Debug("No webhook TLS cert directory configured; skipping webhook setup")
		return
	}
	if dryrunValidator == nil && configValidator == nil {
		logger.Debug("No webhook validators wired; skipping webhook setup")
		return
	}
	logger.Info("Stage 7: Setting up webhook validation")
	setupWebhook(setup.IterCtx, cfg, webhookCertDir, webhookAdmissionTimeouts, webhookPort, k8sClient, dryrunValidator, configValidator, logger, setup.MetricsComponent.Metrics(), setup.Cancel, setup.ErrGroup)
}

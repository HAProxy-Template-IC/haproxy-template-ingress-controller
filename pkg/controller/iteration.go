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
	"path/filepath"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	dryrunvalidator "gitlab.com/haproxy-haptic/haptic/pkg/controller/dryrunvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// buildAndRegisterPluggableValidatorManager constructs the validator
// manager from the configured spec.validators and registers a
// cleanup callback on the iteration setup so the manager's
// connection pools are drained on teardown (every iteration
// restart, every config change, every shutdown).
func buildAndRegisterPluggableValidatorManager(setup *componentSetup, cfg *coreconfig.Config, logger *slog.Logger) (*pluggablevalidator.Manager, *sharedCleanup, error) {
	configs := make([]pluggablevalidator.ManagerConfig, 0, len(cfg.Validators))
	for _, v := range cfg.Validators {
		mc := pluggablevalidator.ManagerConfig{
			Name:           v.Name,
			SocketPath:     v.SocketPath,
			Files:          v.Files,
			DataFiles:      v.DataFiles,
			MaxConnections: int(v.MaxConnections),
		}
		if v.TimeoutMs > 0 {
			mc.Timeout = time.Duration(v.TimeoutMs) * time.Millisecond
		}
		configs = append(configs, mc)
	}
	// The rendered files land under the parent of the maps directory on the
	// HAProxy pod — the same derivation the renderer uses for the template
	// PathResolver's BaseDir, so what a validator is told matches what the
	// templates emit.
	mgr, err := pluggablevalidator.NewManager(logger, configs,
		pluggablevalidator.WithStagedRoot(filepath.Dir(cfg.Dataplane.MapsDir)))
	if err != nil {
		return nil, nil, err
	}
	if mgr.Configured() {
		logger.Info("Pluggable validators registered",
			slog.Int("count", len(configs)),
			slog.Any("names", mgr.Names()),
		)
	}
	cleanup := newSharedCleanup(func() {
		logger.Debug("Closing pluggable-validator connection pools")
		mgr.Close()
	})
	setup.AddCleanup(cleanup.Release)
	return mgr, cleanup, nil
}

// waitAndLoadInitialConfig polls until the HAProxyTemplateConfig exists (the
// fresh-install race: the controller pod may start before the CR is applied),
// then fetches and structurally validates it together with the credentials
// Secret.
func waitAndLoadInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	state *configState,
	logger *slog.Logger,
) (*InitialConfigBundle, error) {
	if err := waitForInitialConfig(ctx, k8sClient, crdName, crdGVR, libraryGVR, state, logger); err != nil {
		return nil, err
	}
	return fetchAndValidateInitialConfig(
		ctx, k8sClient, crdName, secretName,
		crdGVR, libraryGVR, secretGVR, logger,
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
) (iterationErr error) {
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
	setup := setupComponents(ctx, infra.IntrospectionRegistry, infra.eventDropMetrics, typeBootstrapper, crdName, logger)
	defer func() {
		iterationErr = completeIteration(setup, iterationErr, logger)
	}()

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
	if err := startEarlyInfrastructureServers(ctx, debugPort, infra, setup, state, eventBuffer, logger); err != nil {
		return err
	}

	// 1+2. Wait for the HAProxyTemplateConfig to exist (fresh-install race),
	// then fetch and validate it together with the credentials Secret.
	bundle, err := waitAndLoadInitialConfig(setup.IterCtx, k8sClient, crdName, secretName, state, logger)
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
	cfg, err = installEffectiveConfig(setup.IterCtx, cfg, k8sClient, setup, infra, logger)
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
	// On failure this records WHY on the CRD status (so an operator sees the
	// rejection via `kubectl get/describe` rather than only in this crash-looping
	// pod's logs) and then returns the error — the gate stays fail-closed.
	if err := validateInitialConfigValidationTests(setup.IterCtx, cfg, bundle, k8sClient, typeBootstrapper, logger); err != nil {
		return fmt.Errorf("initial HAProxyTemplateConfig %q failed validationTests on load: %w", crdName, err)
	}

	// Mark config as loaded and record initial CRD/Secret versions so the
	// bootstrap watcher events don't trigger redundant reinitialization.
	// Later events with different versions still flow through
	// configChangeCh and trigger iteration restart — that's how
	// credentials rotation reaches the controller.
	finalizeConfigLoad(state, setup, bundle.ConfigVersion, bundle.CredentialsVersion)

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
		crdGVR, libraryGVR, secretGVR, logger,
	); err != nil {
		return err
	}

	// 4.5. Setup CurrentConfigStore for slot-aware server assignment
	currentConfigStore, err := setupCurrentConfigStore(setup, k8sClient, crdName, haproxyCfgGVR, logger)
	if err != nil {
		return err
	}

	// 5. Initialize the StateCache, its background loop, and the `currentFiles`
	// provider — the last render's general aux files (or, on a cold start, the
	// watched read-back of published HAProxyGeneralFile CRDs) exposed to templates
	// so a template can read its own prior output (e.g. self-rotating TLS
	// session-ticket keys) across restarts and reloads.
	stateCache, currentAuxFiles, err := initRenderStateCache(setup, resourceWatcher, k8sClient, logger)
	if err != nil {
		return err
	}

	// 5.5. Construct the validator used by every render pipeline.
	pluggableMgr, pluggableMgrCleanup, err := buildAndRegisterPluggableValidatorManager(setup, cfg, logger)
	if err != nil {
		return fmt.Errorf("creating pluggable validators: %w", err)
	}

	// 6. Create reconciliation components (Stage 5)
	// Components subscribe during construction, before EventBus.Start()
	logger.Info("Stage 5: Creating reconciliation components")
	wiring, err := setupReconciliation(setup, cfg, crd, bundle.Sources, creds, k8sClient, resourceWatcher, currentConfigStore, currentAuxFiles, storeProvider, pluggableMgr, logger)
	if err != nil {
		return err
	}

	// 6.1. EventBuffer was already created early (step 0.25) for /debug/events handler
	// It subscribes in constructor before EventBus.Start() for proper subscription ordering

	// 6.3. Create DryRunValidator for webhook validation.
	// The validator is a synchronous library (ValidateDirect); the proposal
	// validator it wires up subscribes to ProposalValidationRequestedEvent in
	// its constructor, so this must run before EventBus.Start(). The same output
	// validator used by reconciliation is injected into the
	// admission pipeline so every path applies one validation contract.
	// The webhook server runs whenever the chart mounted a TLS cert directory
	// (the maybeSetupWebhook caller gates on `webhookCertDir != ""`).
	// The DryRunValidator is nil when no watched-resource rules exist.
	dryrunValidator, err := createDryRunValidator(cfg, setup.Bus, storeProvider, wiring, pluggableMgr, logger)
	if err != nil && !errors.Is(err, errNoWebhookRules) {
		return fmt.Errorf("creating webhook validators: %w", err)
	}

	// 6.5. Start the EventBus (releases buffered events and begins normal operation)
	// All components have now subscribed during their construction, so we can safely start
	// the bus without race conditions or timing-based sleeps
	logger.Info("Starting EventBus (all components subscribed)")
	if err := startEventBus(setup); err != nil {
		return err
	}

	if err := setupLeadershipAndWebhook(ctx, setup, infra, cfg, webhookCertDir, webhookAdmissionTimeouts, webhookPort, k8sClient, dryrunValidator, pluggableMgrCleanup, logger); err != nil {
		return err
	}

	// 9. Setup debug and metrics infrastructure (start pre-created EventBuffer)
	// Note: The introspection server is already started by startEarlyInfrastructureServers
	// This call registers debug variables and updates the health checker
	setupInfrastructureServers(setup.IterCtx, setup, state, infra, stateCache, eventBuffer, pluggableMgr, logger)

	// 10. Enable reinitialization signaling now that startup is complete
	// This replays any config or credential update newer than the fetched startup
	// versions, then allows future updates to trigger reinitialization.
	// 11. Flip the "initialized" health bit. /healthz returns 503 until
	// this fires and 200 (assuming other components healthy) after.
	// This is the canonical "controller is ready to accept work"
	// signal — see configState.SetInitialized's docstring and the
	// "initialized" entry in the full health checker installed by
	// setupInfrastructureServers.
	if err := finishIterationStartup(setup, state, infra, logger); err != nil {
		return err
	}
	return waitForIterationExit(setup, logger)
}

func setupLeadershipAndWebhook(
	procCtx context.Context,
	setup *componentSetup,
	infra *persistentInfra,
	cfg *coreconfig.Config,
	webhookCertDir string,
	webhookAdmissionTimeouts WebhookAdmissionTimeouts,
	webhookPort int,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component,
	pluggableMgrCleanup *sharedCleanup,
	logger *slog.Logger,
) error {
	logger.Info("Stage 6: Initializing leader election")
	state, err := setupLeaderElection(setup, cfg, k8sClient, logger)
	setup.LeaderState = state
	if err != nil {
		return err
	}
	return maybeSetupWebhook(procCtx, infra, cfg, webhookCertDir, webhookAdmissionTimeouts, webhookPort, setup, k8sClient, dryrunValidator, pluggableMgrCleanup, logger)
}

func finishIterationStartup(
	setup *componentSetup,
	state *configState,
	infra *persistentInfra,
	logger *slog.Logger,
) error {
	if err := iterationContextError(setup.IterCtx); err != nil {
		return err
	}
	markIterationInitialized(setup, state, infra, logger)
	return nil
}

func waitForIterationExit(
	setup *componentSetup,
	logger *slog.Logger,
) error {
	select {
	case <-setup.IterCtx.Done():
		err := iterationContextError(setup.IterCtx)
		logger.Info("Controller iteration cancelled", "reason", err)
		return err

	case newConfig := <-setup.ConfigChangeCh:
		logger.Info("Configuration change detected, triggering reinitialization",
			"new_config_version", fmt.Sprintf("%p", newConfig))
		logger.Info("Reinitialization triggered - starting new iteration")
		return nil
	}
}

func markIterationInitialized(setup *componentSetup, state *configState, infra *persistentInfra, logger *slog.Logger) {
	setup.ConfigChangeHandler.EnableReinitialization()
	state.SetInitialized()
	infra.NoteInitialized()
	logger.Info("Controller iteration initialized successfully - entering event loop")
}

func teardownIteration(setup *componentSetup, logger *slog.Logger) error {
	setup.Cancel()
	if setup.LeaderState != nil {
		setup.LeaderState.cancel()
	}
	err := waitForGoroutinesToFinish(setup.ErrGroup, logger, "Iteration teardown", ShutdownTimeout)
	var timeoutErr *iterationTeardownTimeoutError
	if errors.As(err, &timeoutErr) {
		return err
	}
	if isContextTermination(setup.IterCtx, err) {
		err = nil
	}
	setup.RunCleanups()
	return err
}

func completeIteration(setup *componentSetup, iterationErr error, logger *slog.Logger) error {
	cause := context.Cause(setup.IterCtx)
	result := errors.Join(iterationErr, teardownIteration(setup, logger))
	if cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(result, cause) {
		result = errors.Join(result, cause)
	}
	return result
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
	procCtx context.Context,
	infra *persistentInfra,
	cfg *coreconfig.Config,
	webhookCertDir string,
	webhookAdmissionTimeouts WebhookAdmissionTimeouts,
	webhookPort int,
	setup *componentSetup,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component,
	pluggableMgrCleanup *sharedCleanup,
	logger *slog.Logger,
) error {
	if webhookCertDir == "" {
		logger.Debug("No webhook TLS cert directory configured; skipping webhook setup")
		return nil
	}
	logger.Info("Stage 7: Setting up webhook validation")
	return setupWebhook(procCtx, setup.IterCtx, infra, cfg, webhookCertDir, webhookAdmissionTimeouts, webhookPort, k8sClient, dryrunValidator, pluggableMgrCleanup, logger, setup.MetricsComponent.Metrics(), setup.Cancel, setup.ErrGroup)
}

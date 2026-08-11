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

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	dryrunvalidator "gitlab.com/haproxy-haptic/haptic/pkg/controller/dryrunvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	pkgwebhook "gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

// webhookWriteTimeoutHeadroom pads the HTTPS write deadline past the slowest
// admission path, so a validation that uses its full budget still gets its
// response written instead of being cut off mid-body.
const webhookWriteTimeoutHeadroom = 2 * time.Second

// WebhookAdmissionTimeouts contains the controller-side deadline for
// watched-resource admission. (HAProxyTemplateConfig admission no longer
// exists — ADR-0016.)
type WebhookAdmissionTimeouts struct {
	Resource time.Duration
}

// setupWebhook creates and starts the webhook component if webhook validation is enabled.
//
// This function:
//  1. Extracts webhook rules from configuration
//  2. Creates template engine for dry-run validation
//  3. Starts DryRunValidator component
//  4. Creates and starts webhook component with mounted certificates
//
// The webhook component validates Kubernetes resources via admission webhook.
// Certificates are expected to be mounted at /etc/webhook/certs/ (provided by Helm).
func setupWebhook(
	procCtx context.Context,
	iterCtx context.Context,
	infra *persistentInfra,
	cfg *coreconfig.Config,
	webhookCertDir string,
	admissionTimeouts WebhookAdmissionTimeouts,
	webhookPort int,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component, // Pre-created validator (may be nil)
	pluggableMgrCleanup *sharedCleanup,
	logger *slog.Logger,
	metricsRecorder webhook.MetricsRecorder,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) error {
	resourceAdmissionTimeout := effectiveResourceAdmissionTimeout(admissionTimeouts.Resource)

	// Extract webhook rules from config
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 {
		infra.webhookMu.Lock()
		sharedServer := infra.WebhookServer
		infra.webhookMu.Unlock()
		if sharedServer == nil {
			logger.Debug("No webhook rules extracted; webhook setup skipped")
			return nil
		}
		logger.Info("No webhook rules extracted; clearing the persistent validator table")
	} else {
		logger.Info("Webhook validation enabled", "rule_count", len(rules))
	}

	var mapper meta.RESTMapper
	if len(rules) > 0 {
		logger.Debug("Creating RESTMapper for resource kind resolution")
		discoveryClient := k8sClient.Clientset().Discovery()
		mapper = restmapper.NewDeferredDiscoveryRESTMapper(
			memory.NewMemCacheClient(discoveryClient),
		)
	}

	// Keep the admission listener bound while iterations replace validator generations.
	sharedServer, err := infra.EnsureWebhookServer(procCtx, &pkgwebhook.ServerConfig{
		Port:         webhookPort,
		Path:         webhook.DefaultWebhookPath,
		CertDir:      webhookCertDir,
		ReadTimeout:  timeouts.HTTPServerTimeout,
		WriteTimeout: max(resourceAdmissionTimeout, timeouts.HTTPServerTimeout) + webhookWriteTimeoutHeadroom,
	}, logger)
	if err != nil {
		return fmt.Errorf("starting webhook listener: %w", err)
	}
	serverRun := infra.currentWebhookRun()
	if serverRun == nil {
		return errors.New("persistent webhook server has no completion owner")
	}
	monitorPersistentWebhookRun(
		procCtx, iterCtx, serverRun,
		errGroup, logger, cancel,
	)
	var onGenerationRetired func()
	if len(rules) > 0 && pluggableMgrCleanup != nil {
		onGenerationRetired = pluggableMgrCleanup.Retain()
	}

	// Create webhook component with DryRunValidator for direct validation (no scatter-gather).
	// It adopts the shared listener above and installs this iteration's
	// validator table onto it.
	webhookComponent := webhook.New(
		logger,
		&webhook.Config{
			Port:                     webhookPort,
			Path:                     webhook.DefaultWebhookPath,
			Rules:                    rules,
			CertDir:                  webhookCertDir,
			DryRunValidator:          dryrunValidator,
			ResourceAdmissionTimeout: resourceAdmissionTimeout,
			Server:                   sharedServer,
			OnGenerationRetired:      onGenerationRetired,
		},
		mapper,
		metricsRecorder,
	)

	// Start webhook component (tracked by errgroup for graceful shutdown)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "webhook component", webhookComponent.Start)

	select {
	case <-webhookComponent.Listening():
		logger.Info("Webhook component listening", "port", webhookPort)
		return nil
	case <-iterCtx.Done():
		return context.Cause(iterCtx)
	}
}

func effectiveResourceAdmissionTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return webhook.DefaultResourceAdmissionTimeout
	}
	return configured
}

func monitorPersistentWebhookRun(
	procCtx context.Context,
	iterCtx context.Context,
	serverRun *persistentServerRun,
	errGroup *errgroup.Group,
	logger *slog.Logger,
	iterationCancel context.CancelFunc,
) {
	startInErrGroup(errGroup, iterCtx, logger, iterationCancel, "persistent webhook server", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-serverRun.Done():
			if err := context.Cause(procCtx); err != nil {
				return err
			}
			if err := context.Cause(ctx); err != nil {
				return err
			}
			err := serverRun.Wait()
			if err == nil {
				err = errors.New("server exited without an error")
			}
			return &persistentWebhookServerError{err: err}
		}
	})
}

// errNoWebhookRules reports that no watched resource enables admission
// validation — the caller continues with a nil DryRunValidator rather than
// treating it as a failure.
var errNoWebhookRules = errors.New("no watched-resource webhook rules")

// createDryRunValidator creates a DryRunValidator component for webhook validation.
//
// This function is called BEFORE EventBus.Start() because the proposal
// validator it constructs subscribes to ProposalValidationRequestedEvent in
// its constructor and must be in place before buffered events are released.
// The DryRunValidator itself is a synchronous library called via
// ValidateDirect; it does not subscribe to anything.
//
// Returns (nil, nil) when no watched resource has enableValidationWebhook=true
// — no GVK is routed to the watched-resource path, and the test runner's temp
// directory + ProposalValidator would be wasted setup. HAProxyTemplateConfig
// admission no longer exists: a per-object webhook cannot judge a multi-object
// change set, so the config gate is the pre-upgrade preflight hook plus the
// fail-closed load gate (ADR-0016).
func createDryRunValidator(
	cfg *coreconfig.Config,
	bus *busevents.EventBus,
	storeProvider stores.StoreProvider,
	wiring *reconciliationWiring,
	outputValidator pipeline.RenderedOutputValidator,
	logger *slog.Logger,
) (*dryrunvalidator.Component, error) {
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 {
		logger.Debug("No watched-resource webhook rules; DryRunValidator skipped")
		return nil, errNoWebhookRules
	}

	logger.Debug("Creating webhook validators", "watched_resource_rules", len(rules))

	// Create template engine using helper (handles template extraction, filters, engine type parsing)
	// Note: DryRunValidator does NOT use currentConfig at runtime - it validates hypothetical future state.
	// However, the templates still need the type declaration to compile successfully.
	//
	// wiring.engineWiring.Declarations carries the typed-resource globals
	// from typebootstrap (and the currentConfig declaration). It's
	// the SAME wiring the reconciliation engine was built with, so
	// chart templates compile identically against either render
	// path. Without this, admission would reject every resource the
	// moment a chart template references a typed global — exactly
	// the failure mode Phase 11.5 CI surfaced.
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, wiring.engineWiring.Declarations, helpers.EngineOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating template engine for dry-run validation: %w", err)
	}

	// Admission wrappers reuse matching HTTP content and stage other sources per render.
	//
	// HAProxyPodStore and CurrentConfigStore intentionally remain nil:
	// the webhook validates hypothetical future state, not what's
	// currently deployed.
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:             engine,
		Config:             cfg,
		Logger:             logger,
		Capabilities:       wiring.capabilities,
		HTTPStoreComponent: wiring.httpStore,
		// TypedResourceTypes mirrors the reconciliation RenderService
		// so dry-run renders bind the same typed `resources` global as
		// production. Without it, the engine compile succeeds (the
		// declarations are present) but rendercontext.BuildResourcesValue
		// would fall back to the untyped map shape and chart code that
		// reaches typed access (`resources.<name>.List()` etc.) would
		// compile-fail under the typed engine declaration.
		TypedResourceTypes: wiring.engineWiring.TypedResourceTypes,
	})

	// Create ValidationService (pure service for validation)
	// Use strict DNS validation for webhook (catch DNS issues before admission)
	dirConfig := extractValidationDirConfig(&cfg.Dataplane)
	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		SkipDNSValidation: false, // Strict mode for webhook validation
		BaseDir:           dirConfig.BaseDir,
		MapsDir:           dirConfig.MapsDir,
		SSLCertsDir:       dirConfig.SSLCertsDir,
		GeneralDir:        dirConfig.GeneralDir,
	})

	return buildDryRunValidator(bus, renderService, validationService, storeProvider, outputValidator, wiring.gvrMapper, cfg.WatchedResources, wiring.publishedCurrentFiles.get, logger)
}

// buildDryRunValidator constructs the watched-resource admission validator.
// Separate from createDryRunValidator so the call-site logic can decide
// whether to build it based on whether any watched resource has
// `enableValidationWebhook: true`. Wraps the sync-only ProposalValidator
// (distinct from the leader-side instance to avoid duplicate event
// subscriptions) and the DryRunValidator itself.
func buildDryRunValidator(
	bus *busevents.EventBus,
	renderService *renderer.RenderService,
	validationService *validation.ValidationService,
	baseStoreProvider stores.StoreProvider,
	outputValidator pipeline.RenderedOutputValidator,
	gvrMapper meta.RESTMapper,
	watchedResources map[string]coreconfig.WatchedResource,
	currentFilesProvider func() (map[string]string, error),
	logger *slog.Logger,
) (*dryrunvalidator.Component, error) {
	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:        renderService,
		Validator:       validationService,
		OutputValidator: outputValidator,
		Logger:          logger,
	})

	// ProposalValidator in sync-only mode (only ValidateSync() is used for
	// webhook). This avoids duplicate event subscriptions since the main
	// ProposalValidator in createReconciliationComponents handles async
	// HTTP content validation events.
	proposalValidatorInstance := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:             bus,
		Pipeline:             pipelineInstance,
		BaseStoreProvider:    baseStoreProvider,
		CurrentFilesProvider: currentFilesProvider,
		Logger:               logger,
		SyncOnly:             true,
	})

	// The admission webhook only validates the *submitted* resource
	// (Ingress / HTTPRoute / etc.) by rendering with an overlay store and
	// checking the result. The chart's embedded `validationTests` are NOT
	// run here — they are chart-author scenarios with their own fixtures,
	// executed in CI via `haptic-controller validate` / `make test-templates`.
	return dryrunvalidator.New(&dryrunvalidator.ComponentConfig{
		ProposalValidator: proposalValidatorInstance,
		RESTMapper:        gvrMapper,
		WatchedResources:  watchedResources,
		Logger:            logger,
	})
}

// setupReconciliation creates and starts the reconciliation components (Stage 5).
//
// The Reconciler debounces resource changes and triggers reconciliation events.
// The Coordinator orchestrates the render-validate pipeline by calling Pipeline.Execute()
// directly and publishing events (TemplateRenderedEvent, ValidationCompletedEvent) for
// downstream components like DeploymentScheduler.
//
// All components are started after initial resource synchronization to ensure we
// have a complete view of the cluster state before beginning reconciliation cycles.
//
// Returns the reconciliation wiring for use in leader election callbacks.
func setupReconciliation(
	setup *componentSetup,
	cfg *coreconfig.Config,
	crd *v1alpha1.HAProxyTemplateConfig,
	sources []events.ConfigSourceRef,
	creds *coreconfig.Credentials,
	k8sClient *client.Client,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	currentConfigStore *currentconfigstore.Store,
	currentFiles *currentFilesAuthority,
	storeProvider stores.StoreProvider,
	outputValidator pipeline.RenderedOutputValidator,
	logger *slog.Logger,
) (*reconciliationWiring, error) {
	wiring, err := createReconciliationComponents(setup, cfg, crd, k8sClient, resourceWatcher, currentConfigStore, currentFiles, storeProvider, outputValidator, logger)
	if err != nil {
		return nil, err
	}

	// Start all-replica components in background
	// Leader-only components (Deployer, DeploymentScheduler, ConfigPublisher) are NOT started here
	// Note: Components already subscribed during construction, so they're ready to receive events
	startReconciliationComponents(setup.IterCtx, setup.Registry, logger, setup.Cancel, setup.ErrGroup)

	// Publish initial config and credentials events
	// These events are buffered by EventBus until Start() is called in the main controller loop
	// This ensures reconciliation components (especially Discovery) receive the initial state
	// even though they were created after the initial CRD/Secret watcher events
	// Note: We pass the actual CRD (not nil) so ConfigPublisher can cache it for creating HAProxyCfg resources
	initialValidated := events.NewConfigValidatedEvent(cfg, crd, "initial", "initial")
	initialValidated.Sources = sources
	setup.Bus.Publish(initialValidated)
	logger.Debug("Published initial ConfigValidatedEvent (buffered until EventBus.Start())")

	setup.Bus.Publish(events.NewCredentialsUpdatedEvent(creds, "initial"))
	logger.Debug("Published initial CredentialsUpdatedEvent (buffered until EventBus.Start())")

	// Trigger initial reconciliation to bootstrap the pipeline
	// This ensures at least one reconciliation cycle runs even with 0 resources
	// A new correlation ID is generated to trace this initial reconciliation cycle
	// Initial sync is NOT coalescible - it must be processed to establish initial state
	initialReconciliation := events.NewReconciliationTriggeredEvent("initial_sync_complete", false, events.WithNewCorrelation())
	setup.Bus.Publish(initialReconciliation)
	logger.Debug("Published initial reconciliation trigger (buffered until EventBus.Start())",
		"correlation_id", initialReconciliation.CorrelationID())

	return wiring, nil
}

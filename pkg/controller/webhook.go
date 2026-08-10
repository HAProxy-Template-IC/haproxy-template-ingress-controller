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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
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
	logger *slog.Logger,
	metricsRecorder webhook.MetricsRecorder,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) {
	// Extract webhook rules from config
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 {
		logger.Debug("No webhook rules extracted (webhook enabled but no matching resources)")
		return
	}

	logger.Info("Webhook validation enabled", "rule_count", len(rules))

	// Create RESTMapper for resolving resource kinds from GVR
	// This uses the Kubernetes API discovery to get authoritative mappings
	logger.Debug("Creating RESTMapper for resource kind resolution")
	discoveryClient := k8sClient.Clientset().Discovery()
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(discoveryClient),
	)

	// Bind the admission listener once per PROCESS, not once per iteration.
	// A component-owned listener closes on every config change, and the API
	// server meanwhile admits unchecked under failurePolicy=Ignore (#110).
	// The certificate is read from the mounted Secret directory per handshake,
	// so cert-manager rotation is picked up without re-binding.
	sharedServer, err := infra.EnsureWebhookServer(procCtx, &pkgwebhook.ServerConfig{
		Port:         webhookPort,
		Path:         webhook.DefaultWebhookPath,
		CertDir:      webhookCertDir,
		ReadTimeout:  timeouts.HTTPServerTimeout,
		WriteTimeout: admissionTimeouts.Resource + webhookWriteTimeoutHeadroom,
	}, logger)
	if err != nil {
		// Match what startInErrGroup does for a component that fails to start:
		// cancel the iteration. Continuing would run the controller with no
		// admission gate at all, which is the failure this whole change exists
		// to close — a bind error must not be quieter than the hole it leaves.
		logger.Error("Webhook listener unavailable; cancelling the iteration rather than running ungated",
			"error", err)
		cancel()
		return
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
			DryRunValidator:          dryrunValidator, // Direct validation, nil = fail-open
			ResourceAdmissionTimeout: admissionTimeouts.Resource,
			Server:                   sharedServer,
		},
		mapper,
		metricsRecorder,
	)

	// Start webhook component (tracked by errgroup for graceful shutdown)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "webhook component", webhookComponent.Start)

	// Block here until the underlying TLS listener has bound. This
	// ensures that by the time iteration setup advances and the
	// controller's readiness probe transitions to healthy, admission
	// requests are actually answerable. Without this gate, the API
	// server's first AdmissionReview races the listener and bounces
	// with "connection refused" — the chart's failurePolicy=Fail then
	// rejects every Ingress create until enough time passes for kubelet
	// retries to land on a bound listener. We bound the wait so a
	// genuine startup failure (cert error, port already in use) doesn't
	// block iteration setup forever; the errgroup still surfaces the
	// underlying error.
	select {
	case <-webhookComponent.Listening():
		logger.Info("Webhook component listening", "port", webhookPort)
	case <-iterCtx.Done():
		logger.Info("Iteration cancelled while waiting for webhook bind")
	case <-time.After(30 * time.Second):
		logger.Warn("Webhook component did not bind within 30s; proceeding (errgroup will surface any error)")
	}
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
	pluggableValidator *pluggablevalidator.Manager,
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

	// Create RenderService (pure service for rendering).
	//
	// HTTPStoreComponent is wired in so chart templates that use
	// `{{ http.Fetch(...) }}` (e.g. an HTTP-store-driven blocklist) render
	// successfully during webhook dry-run. Without it, calling http.Fetch
	// from a template panics on a nil receiver and the webhook rejects
	// every Ingress with a render error — even Ingresses that have nothing
	// to do with HTTP fetching, since the rendering pass happens against
	// the whole merged config. Reusing the cluster's accepted content for
	// dry-run is safe: validation overlays are only applied for the actual
	// proposal-validation pipeline, not for sync webhook calls.
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

	return buildDryRunValidator(bus, renderService, validationService, storeProvider, pluggableValidator, wiring.gvrMapper, logger), nil
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
	pluggableValidator *pluggablevalidator.Manager,
	gvrMapper meta.RESTMapper,
	logger *slog.Logger,
) *dryrunvalidator.Component {
	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	// ProposalValidator in sync-only mode (only ValidateSync() is used for
	// webhook). This avoids duplicate event subscriptions since the main
	// ProposalValidator in createReconciliationComponents handles async
	// HTTP content validation events.
	proposalValidatorInstance := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
		SyncOnly:          true,
	})

	// The admission webhook only validates the *submitted* resource
	// (Ingress / HTTPRoute / etc.) by rendering with an overlay store and
	// checking the result. The chart's embedded `validationTests` are NOT
	// run here — they are chart-author scenarios with their own fixtures,
	// executed in CI via `haptic-controller validate` / `make test-templates`.
	return dryrunvalidator.New(&dryrunvalidator.ComponentConfig{
		ProposalValidator:  proposalValidatorInstance,
		RESTMapper:         gvrMapper,
		Logger:             logger,
		PluggableValidator: pluggableValidator,
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
	currentAuxFiles func() map[string]string,
	storeProvider stores.StoreProvider,
	logger *slog.Logger,
) (*reconciliationWiring, error) {
	wiring, err := createReconciliationComponents(setup, cfg, crd, k8sClient, resourceWatcher, currentConfigStore, currentAuxFiles, storeProvider, logger)
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

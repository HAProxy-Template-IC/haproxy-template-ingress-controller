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

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	dryrunvalidator "gitlab.com/haproxy-haptic/haptic/pkg/controller/dryrunvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	ctrlhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

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
	iterCtx context.Context,
	cfg *coreconfig.Config,
	webhookCertDir string,
	k8sClient *client.Client,
	dryrunValidator *dryrunvalidator.Component, // Pre-created validator (may be nil)
	configValidator webhook.ConfigValidatorFunc, // Pre-created HAProxyTemplateConfig validator (may be nil)
	logger *slog.Logger,
	metricsRecorder webhook.MetricsRecorder,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) {
	// Extract webhook rules from config
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 && configValidator == nil {
		logger.Debug("No webhook rules extracted (webhook enabled but no matching resources)")
		return
	}

	logger.Info("Webhook validation enabled",
		"rule_count", len(rules),
		"haproxytemplateconfig_validator", configValidator != nil)

	// Create RESTMapper for resolving resource kinds from GVR
	// This uses the Kubernetes API discovery to get authoritative mappings
	logger.Debug("Creating RESTMapper for resource kind resolution")
	discoveryClient := k8sClient.Clientset().Discovery()
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(discoveryClient),
	)

	// Create webhook component with DryRunValidator for direct validation (no scatter-gather).
	// The TLS certificate is read from the mounted Secret directory
	// (CertDir) and hot-reloaded on rotation without a restart.
	webhookComponent := webhook.New(
		logger,
		&webhook.Config{
			Port:            9443, // Default webhook port
			Path:            "/validate",
			Rules:           rules,
			CertDir:         webhookCertDir,
			DryRunValidator: dryrunValidator, // Direct validation, nil = fail-open
			ConfigValidator: configValidator, // HAProxyTemplateConfig admission, nil = fail-open
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
		logger.Info("Webhook component listening", "port", 9443)
	case <-iterCtx.Done():
		logger.Info("Iteration cancelled while waiting for webhook bind")
	case <-time.After(30 * time.Second):
		logger.Warn("Webhook component did not bind within 30s; proceeding (errgroup will surface any error)")
	}
}

// createDryRunValidator creates a DryRunValidator component for webhook validation.
//
// This function is called BEFORE EventBus.Start() because the proposal
// validator it constructs subscribes to ProposalValidationRequestedEvent in
// its constructor and must be in place before buffered events are released.
// The DryRunValidator itself is a synchronous library called via
// ValidateDirect; it does not subscribe to anything.
//
// The iterCtx argument is used solely to clean up the per-iteration scratch
// directory (`os.MkdirTemp(...)`) when the iteration ends — without that hook,
// each CRD/credentials/cert rotation that triggers a fresh iteration would leak
// another `haptic-webhook-validation-*` directory into the pod's /tmp.
//
// Returns (nil, nil, error) if engine creation or shared-dependency
// construction fails.
//
// Returns (dryRunValidator, configValidator, nil) on success, where:
//   - dryRunValidator is non-nil iff at least one watched resource has
//     enableValidationWebhook=true (i.e., the watched-resource admission
//     path is active). nil otherwise.
//   - configValidator is ALWAYS non-nil — the HAProxyTemplateConfig
//     admission webhook is independent of watched-resource rules. The
//     chart-side ValidatingWebhookConfiguration may or may not route
//     HAProxyTemplateConfig admission to the controller (chart-side
//     `webhook.haproxyTemplateConfig.enabled`), but the controller is
//     always ready to handle it. If the cluster routes the admission
//     and we returned a nil validator, the request would silently
//     succeed at the pure server's fail-open path — exactly the bug
//     Gitar flagged on commit 8d326660.
func createDryRunValidator(
	cfg *coreconfig.Config,
	bus *busevents.EventBus,
	storeProvider stores.StoreProvider,
	capabilities dataplane.Capabilities,
	httpStoreComponent *ctrlhttpstore.Component,
	pluggableValidator *pluggablevalidator.Manager,
	engineWiring typedRendererWiring,
	gvrMapper meta.RESTMapper,
	k8sClient *client.Client,
	logger *slog.Logger,
) (*dryrunvalidator.Component, webhook.ConfigValidatorFunc, error) {
	rules := webhook.ExtractWebhookRules(cfg)

	logger.Debug("Creating webhook validators", "watched_resource_rules", len(rules))

	// Create template engine using helper (handles template extraction, filters, engine type parsing)
	// Note: DryRunValidator does NOT use currentConfig at runtime - it validates hypothetical future state.
	// However, the templates still need the type declaration to compile successfully.
	//
	// engineWiring.Declarations carries the typed-resource globals
	// from typebootstrap (and the currentConfig declaration). It's
	// the SAME wiring the reconciliation engine was built with, so
	// chart templates compile identically against either render
	// path. Without this, admission would reject every resource the
	// moment a chart template references a typed global — exactly
	// the failure mode Phase 11.5 CI surfaced.
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, engineWiring.Declarations, helpers.EngineOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("creating template engine for dry-run validation: %w", err)
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
		Capabilities:       capabilities,
		HTTPStoreComponent: httpStoreComponent,
		// TypedResourceTypes mirrors the reconciliation RenderService
		// so dry-run renders bind the same typed `resources` global as
		// production. Without it, the engine compile succeeds (the
		// declarations are present) but rendercontext.BuildResourcesValue
		// would fall back to the untyped map shape and chart code that
		// reaches typed access (`resources.<name>.List()` etc.) would
		// compile-fail under the typed engine declaration.
		TypedResourceTypes: engineWiring.TypedResourceTypes,
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

	// ConfigValidator is ALWAYS built when this function runs. The
	// HAProxyTemplateConfig admission webhook is independent of
	// watched-resource webhook rules — even when no Ingress / HTTPRoute
	// admission is configured, the chart's ValidatingWebhookConfiguration
	// may still route HAProxyTemplateConfig admission here, and the
	// controller must be ready to handle it. (Gitar caught this on
	// commit 8d326660 — the previous shape early-returned when
	// `len(rules) == 0` and left ConfigValidator nil.)
	configValidator := webhook.NewConfigValidator(&webhook.ConfigValidatorConfig{
		Logger:             logger,
		StrictValidator:    validationService,
		StoreProvider:      storeProvider,
		Capabilities:       capabilities,
		HTTPStoreComponent: httpStoreComponent,
		Declarations:       engineWiring.Declarations,
		TypedResourceTypes: engineWiring.TypedResourceTypes,
		// Bootstrap schemas from the PROSPECTIVE config at admission, mirroring
		// the daemon load gate, so both gates build the engine from the same
		// type set (engineWiring above is the startup-fixed fallback). The
		// load-gate's TypeBootstrapper and the webhook's SchemaBootstrapper
		// share an underlying signature, so the closure converts directly.
		Bootstrap: webhook.SchemaBootstrapper(newIterationTypeBootstrapper(k8sClient, logger)),
	})

	// DryRunValidator is only needed when at least one watched resource
	// has `enableValidationWebhook: true`. With no such rules, no GVK is
	// routed to the watched-resource path, and the test runner's temp
	// directory + ProposalValidator are wasted setup.
	if len(rules) == 0 {
		logger.Debug("No watched-resource webhook rules; DryRunValidator skipped, only HAProxyTemplateConfig admission wired")
		return nil, configValidator.ValidateDirect, nil
	}

	dryrun := buildDryRunValidator(bus, renderService, validationService, storeProvider, pluggableValidator, gvrMapper, logger)
	return dryrun, configValidator.ValidateDirect, nil
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
// Returns the reconciliation components for use in leader election callbacks.
func setupReconciliation(
	iterCtx context.Context,
	cfg *coreconfig.Config,
	crd *v1alpha1.HAProxyTemplateConfig,
	creds *coreconfig.Credentials,
	k8sClient *client.Client,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	currentConfigStore *currentconfigstore.Store,
	storeProvider stores.StoreProvider,
	bus *busevents.EventBus,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) (*reconciliationComponents, error) {
	// Create all components
	components, err := createReconciliationComponents(iterCtx, cfg, crd, k8sClient, resourceWatcher, currentConfigStore, storeProvider, bus, registry, logger)
	if err != nil {
		return nil, err
	}

	// Start all-replica components in background
	// Leader-only components (Deployer, DeploymentScheduler, ConfigPublisher) are NOT started here
	// Note: Components already subscribed during construction, so they're ready to receive events
	startReconciliationComponents(iterCtx, registry, logger, cancel, errGroup)

	// Publish initial config and credentials events
	// These events are buffered by EventBus until Start() is called in the main controller loop
	// This ensures reconciliation components (especially Discovery) receive the initial state
	// even though they were created after the initial CRD/Secret watcher events
	// Note: We pass the actual CRD (not nil) so ConfigPublisher can cache it for creating HAProxyCfg resources
	bus.Publish(events.NewConfigValidatedEvent(cfg, crd, "initial", "initial"))
	logger.Debug("Published initial ConfigValidatedEvent (buffered until EventBus.Start())")

	bus.Publish(events.NewCredentialsUpdatedEvent(creds, "initial"))
	logger.Debug("Published initial CredentialsUpdatedEvent (buffered until EventBus.Start())")

	// Trigger initial reconciliation to bootstrap the pipeline
	// This ensures at least one reconciliation cycle runs even with 0 resources
	// A new correlation ID is generated to trace this initial reconciliation cycle
	// Initial sync is NOT coalescible - it must be processed to establish initial state
	initialReconciliation := events.NewReconciliationTriggeredEvent("initial_sync_complete", false, events.WithNewCorrelation())
	bus.Publish(initialReconciliation)
	logger.Debug("Published initial reconciliation trigger (buffered until EventBus.Start())",
		"correlation_id", initialReconciliation.CorrelationID())

	return components, nil
}

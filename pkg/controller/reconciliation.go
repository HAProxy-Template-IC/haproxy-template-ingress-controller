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
	"fmt"
	"log/slog"
	"os"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	ctrlconfigpublisher "gitlab.com/haproxy-haptic/haptic/pkg/controller/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/discovery"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/eventemitter"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/reconciler"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceapplier"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	informers "gitlab.com/haproxy-haptic/haptic/pkg/generated/informers/externalversions"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// reconciliationWiring carries the few construction outputs later startup
// stages consume; the components themselves live on through the lifecycle
// registry and their event-bus subscriptions — everything not referenced
// again after construction is deliberately NOT carried here.
//
// Consumers:
//   - deployer/deploymentScheduler/configPublisher: read by
//     startLeaderOnlyComponents to build leaderOnlyComponents.
//   - httpStore/capabilities/engineWiring/gvrMapper: read by
//     createDryRunValidator when the webhook validators are wired up.
type reconciliationWiring struct {
	deployer            *deployer.Component
	deploymentScheduler *deployer.DeploymentScheduler
	configPublisher     *ctrlconfigpublisher.Component
	httpStore           *httpstore.Component   // HTTP resource fetcher for dynamic content
	capabilities        dataplane.Capabilities // HAProxy/DataPlane API capabilities

	// engineWiring carries the type-bootstrap output shared between
	// the reconciliation engine (the coordinator path constructed here) and
	// the dry-run validator engine constructed later in iteration
	// startup. Both engines need the SAME typed-global declarations
	// so chart templates compile identically against either render
	// path — without this sharing, the webhook engine would see the
	// typed globals as undefined and admission would reject every
	// resource.
	engineWiring typedRendererWiring

	// gvrMapper is the cluster RESTMapper shared by every component that
	// resolves apiVersion+kind → GroupVersionResource (status/resource
	// appliers here, and the dry-run validator constructed later in iteration
	// startup). Sharing one deferred mapper avoids duplicate discovery caches
	// and keeps GVR resolution resource-agnostic (RULE #1).
	gvrMapper meta.RESTMapper
}

// createReconciliationComponents creates all reconciliation components and
// registers them with the lifecycle registry (setup.Registry). It returns
// only the slim reconciliationWiring — every component not referenced again
// after construction lives on through the registry and its event-bus
// subscriptions.
func createReconciliationComponents(
	setup *componentSetup,
	cfg *coreconfig.Config,
	crd *v1alpha1.HAProxyTemplateConfig,
	k8sClient *client.Client,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	currentConfigStore *currentconfigstore.Store,
	currentAuxFiles func() map[string]string,
	storeProvider stores.StoreProvider,
	outputValidator pipeline.RenderedOutputValidator,
	logger *slog.Logger,
) (*reconciliationWiring, error) {
	// Create Reconciler. It fires immediately on every resource/HTTP event;
	// there is no reconciler-level refractory. Batching is per-watcher
	// (debounceInterval), reload throttling is the deployer's
	// minDeploymentInterval (which the runtime-eligible fast path bypasses).
	reconcilerComponent := reconciler.New(setup.Bus, logger)

	// Detect local HAProxy version and compute capabilities
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("detecting local HAProxy version: %w", err)
	}
	capabilities := dataplane.CapabilitiesFromVersion(localVersion)

	logger.Info("Detected local HAProxy version",
		"version", localVersion.Full,
		"supports_crt_list", capabilities.SupportsCrtList,
		"supports_map_storage", capabilities.SupportsMapStorage,
		"supports_general_storage", capabilities.SupportsGeneralStorage)

	// Get haproxy-pods store for pod-maxconn calculations in templates
	haproxyPodStore := resourceWatcher.GetStore(names.HAProxyPodsResourceType)
	if haproxyPodStore == nil {
		return nil, fmt.Errorf("%s store not found (should be auto-injected)", names.HAProxyPodsResourceType)
	}

	// Create HTTPStore component for dynamic HTTP content fetching
	// This component manages periodic refreshes and content validation coordination
	// Eviction maxAge is 2x drift prevention interval to catch stale URLs
	driftInterval := cfg.Dataplane.GetDriftPreventionInterval()
	httpStoreEvictionMaxAge := 2 * driftInterval
	httpStoreComponent := httpstore.New(setup.Bus, logger, httpStoreEvictionMaxAge)

	wiring, err := buildEngineWiring(setup.IterCtx, cfg, k8sClient, logger)
	if err != nil {
		return nil, err
	}
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, wiring.Declarations, helpers.EngineOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating template engine for reconciliation: %w", err)
	}

	// Create RenderService with full dependencies for production rendering.
	// TypedResourceTypes feeds the render-time path that wraps each store
	// snapshot into the *[]*<generated-struct> shape Scriggo's typed globals
	// are declared against.
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:                  engine,
		Config:                  cfg,
		Logger:                  logger,
		Capabilities:            capabilities,
		HAProxyPodStore:         haproxyPodStore,
		HTTPStoreComponent:      httpStoreComponent,
		CurrentConfigStore:      currentConfigStore,
		CurrentAuxFilesProvider: currentAuxFiles,
		TypedResourceTypes:      wiring.TypedResourceTypes,
	})

	validationPipeline := buildValidationPipeline(cfg, localVersion, renderService, outputValidator, logger)

	// Coordinator: leader-side render + validate + deploy.
	coordinatorComponent := reconciler.NewCoordinator(&reconciler.CoordinatorConfig{
		EventBus:      setup.Bus,
		Pipeline:      validationPipeline,
		StoreProvider: storeProvider,
		Logger:        logger,
	})

	// ProposalValidator: admission webhook + HTTP-store content promotion.
	proposalValidatorComponent := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          setup.Bus,
		Pipeline:          validationPipeline,
		BaseStoreProvider: storeProvider,
		Logger:            logger,
	})

	// One constructor, wired inside the deployer package: the connections
	// between these three used to be optional setters a caller could forget.
	deployStack := deployer.NewDeployStack(setup.Bus, cfg, logger, setup.MetricsComponent.Metrics())
	deployerComponent := deployStack.Deployer
	deploymentSchedulerComponent := deployStack.Scheduler
	driftMonitorComponent := deployStack.DriftMonitor

	// Create Discovery component and set pod store
	// This detects the local HAProxy version (fatal if fails - controller cannot start
	// without knowing its local version for compatibility checking)
	discoveryComponent, err := discovery.New(setup.Bus, logger)
	if err != nil {
		return nil, fmt.Errorf("creating discovery component: %w", err)
	}
	podStore := resourceWatcher.GetStore(names.HAProxyPodsResourceType)
	if podStore == nil {
		return nil, fmt.Errorf("%s store not found (should be auto-injected)", names.HAProxyPodsResourceType)
	}
	discoveryComponent.SetPodStore(podStore)

	// Create Config Publisher (pure publisher + event adapter)
	// Publishes runtime config resources after successful validation
	crdClientset, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return nil, fmt.Errorf("creating CRD clientset: %w", err)
	}

	// Create publisher with informer-backed listers for cached reads
	purePublisher, err := createConfigPublisher(setup.IterCtx.Done(), crdClientset, k8sClient, logger)
	if err != nil {
		return nil, err
	}
	configPublisherComponent := ctrlconfigpublisher.New(purePublisher, setup.Bus, logger,
		ctrlconfigpublisher.WithPublishInterval(cfg.Dataplane.GetConfigPublishInterval()),
	)

	// Create Status Updater (updates HAProxyTemplateConfig CRD status with validation results)
	// This allows users to see validation errors via `kubectl describe haproxytemplateconfig`
	statusUpdaterComponent := configchange.NewStatusUpdater(crdClientset, k8sClient.Clientset(), setup.Bus, logger)

	// Build a RESTMapper from the cluster's discovery so the status/resource
	// appliers resolve apiVersion+kind → GroupVersionResource from authoritative
	// cluster data, never a hardcoded or guessed plural (RULE #1). Shared by both
	// appliers; the deferred mapper fetches discovery lazily on first use.
	gvrMapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(k8sClient.Clientset().Discovery()),
	)

	// Create StatusApplier (applies template-driven status patches to Kubernetes resources via SSA)
	// All-replica: subscribes in constructor to cache patches from renders; only the leader applies.
	statusApplierComponent := statusapplier.New(&statusapplier.Config{
		EventBus:      setup.Bus,
		DynamicClient: k8sClient.DynamicClient(),
		GVRResolver:   statusapplier.NewRestMapperResolver(gvrMapper),
		Logger:        logger,
	})

	resourceApplierComponent := newResourceApplier(crd, k8sClient, gvrMapper, setup.Bus, logger)

	// EventEmitter forwards template-recorded Kubernetes Events (recordEvent, e.g.
	// a RouteConflict Warning on an Ingress) to the API server. All-replica:
	// subscribes on every replica but only the leader emits (the source
	// ReconciliationCompletedEvent is leader-only, and an internal leader flag
	// double-gates it).
	eventEmitterComponent := eventemitter.New(&eventemitter.Config{
		EventBus:   setup.Bus,
		KubeClient: k8sClient.Clientset(),
		Logger:     logger,
	})

	// Register components with the lifecycle registry.
	// Coordinator is leader-only because it performs rendering (state changes).
	// DriftMonitor is leader-only to avoid multi-replica race conditions.
	// StatusUpdater is leader-only to avoid API conflicts from concurrent updates.
	// ProposalValidator is all-replica because HTTPStore depends on it for HTTP content validation.
	registerLifecycleComponents(setup.Registry,
		[]lifecycle.Component{
			reconcilerComponent,
			discoveryComponent,
			httpStoreComponent,
			proposalValidatorComponent,
			statusApplierComponent,
			resourceApplierComponent,
			eventEmitterComponent,
		},
		[]lifecycle.Component{
			coordinatorComponent,
			driftMonitorComponent,
			deployerComponent,
			deploymentSchedulerComponent,
			configPublisherComponent,
			statusUpdaterComponent,
		},
	)

	return &reconciliationWiring{
		deployer:            deployerComponent,
		deploymentScheduler: deploymentSchedulerComponent,
		configPublisher:     configPublisherComponent,
		httpStore:           httpStoreComponent,
		capabilities:        capabilities,
		engineWiring:        wiring,
		gvrMapper:           gvrMapper,
	}, nil
}

// registerLifecycleComponents registers the all-replica components first, then
// the leader-only ones (started later, after leadership is acquired).
func registerLifecycleComponents(reg *lifecycle.Registry, allReplica, leaderOnly []lifecycle.Component) {
	for _, c := range allReplica {
		reg.Register(c, false)
	}
	for _, c := range leaderOnly {
		reg.Register(c, true)
	}
}

// buildValidationPipeline creates the shared full-validation pipeline used by
// reconciliation, admission, and HTTP content promotion. DNS lookup remains
// flaky and recovers at runtime (HAProxy starts the server DOWN and brings
// it up when the next health check resolves).
func buildValidationPipeline(
	cfg *coreconfig.Config,
	localVersion *dataplane.Version,
	renderService *renderer.RenderService,
	outputValidator pipeline.RenderedOutputValidator,
	logger *slog.Logger,
) *pipeline.Pipeline {
	dirConfig := extractValidationDirConfig(&cfg.Dataplane)
	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger.With("validation", "full"),
		Version:           localVersion,
		SkipDNSValidation: true,
		BaseDir:           dirConfig.BaseDir,
		MapsDir:           dirConfig.MapsDir,
		SSLCertsDir:       dirConfig.SSLCertsDir,
		GeneralDir:        dirConfig.GeneralDir,
	})
	return pipeline.New(&pipeline.PipelineConfig{
		Renderer:        renderService,
		Validator:       validationService,
		OutputValidator: outputValidator,
		Logger:          logger,
	})
}

// newResourceApplier builds the all-replica/leader-only ResourceApplier
// component. Extracted from createReconciliationComponents so the parent
// stays under the function-length lint cap; the inputs (CR identity for
// ownerRef, namespace for restriction default) and outputs are otherwise
// independent of the rest of the wiring.
//
// All-replica subscriber, leader-only applier — same shape as StatusApplier.
// Resource-agnostic: templates declare resources under spec.k8sResources and
// the applier reconciles whatever the renderer parsed out of them, with
// checksum dedup so unchanged resources don't hammer kube-api. Cross-
// namespace SSA is allowed at the controller boundary; the security gate is
// the chart's RBAC (a misbehaving template still gets Forbidden when the
// granted Role/ClusterRole doesn't cover the target namespace).
func newResourceApplier(crd *v1alpha1.HAProxyTemplateConfig, k8sClient *client.Client, gvrMapper meta.RESTMapper, bus *busevents.EventBus, logger *slog.Logger) *resourceapplier.Component {
	ownNamespace := os.Getenv("POD_NAMESPACE")
	if ownNamespace == "" {
		ownNamespace = k8sClient.Namespace()
	}
	// OwnerReference identity from the live HAProxyTemplateConfig CR. The
	// applier injects this into every full-ownership SSA payload so
	// Kubernetes garbage collection cascade-deletes the rendered resources
	// when the CR is removed (e.g. `helm uninstall`).
	ownerRef := resourceapplier.OwnerReference{
		APIVersion: v1alpha1.SchemeGroupVersion.String(),
		Kind:       configKind,
		Name:       crd.GetName(),
		UID:        string(crd.GetUID()),
	}
	return resourceapplier.New(&resourceapplier.Config{
		EventBus:               bus,
		DynamicClient:          k8sClient.DynamicClient(),
		DiscoveryClient:        k8sClient.Clientset().Discovery(),
		GVRResolver:            statusapplier.NewRestMapperResolver(gvrMapper),
		Logger:                 logger,
		OwnNamespace:           ownNamespace,
		RestrictToOwnNamespace: false,
		OwnerRef:               ownerRef,
	})
}

// createConfigPublisher creates a config publisher with informer-backed listers for cached reads.
// This significantly reduces API calls by checking cached state before doing status updates.
// stopCh must be the iteration's Done channel: the controller rebuilds its
// components per iteration, so informers tied to anything longer-lived leak a
// reflector per informer per iteration.
func createConfigPublisher(stopCh <-chan struct{}, crdClientset versioned.Interface, k8sClient *client.Client, logger *slog.Logger) (*configpublisher.Publisher, error) {
	// Create shared informer factory for HAProxy CRDs
	// The informers provide cached reads for status updates, significantly reducing API calls.
	// We use a 30-second resync period to keep the cache reasonably fresh while minimizing overhead.
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		crdClientset,
		timeouts.InformerResyncPeriod,
		informers.WithNamespace(k8sClient.Namespace()),
	)

	// Initialize informers by calling Lister() - this creates the underlying SharedIndexInformer
	// The informers won't start watching until Start() is called below
	haproxyInformers := informerFactory.HaproxyTemplateIC().V1alpha1()
	listers := &configpublisher.Listers{
		MapFiles:     haproxyInformers.HAProxyMapFiles().Lister(),
		GeneralFiles: haproxyInformers.HAProxyGeneralFiles().Lister(),
		CRTListFiles: haproxyInformers.HAProxyCRTListFiles().Lister(),
		HAProxyCfgs:  haproxyInformers.HAProxyCfgs().Lister(),
	}

	// Start informers in background - they'll begin watching and populating the cache
	// The factory tracks all created informers and starts them together
	informerFactory.Start(stopCh)

	// Wait for cache to sync before creating publisher
	// This ensures listers have initial data before first use
	logger.Debug("Waiting for HAProxy CRD informer caches to sync")
	syncResult := informerFactory.WaitForCacheSync(stopCh)
	for informerType, synced := range syncResult {
		if !synced {
			return nil, fmt.Errorf("informer cache sync failed for %v", informerType)
		}
	}
	logger.Debug("HAProxy CRD informer caches synced")

	// Create publisher with listers for cached reads
	return configpublisher.NewWithListers(k8sClient.Clientset(), crdClientset, listers, logger), nil
}

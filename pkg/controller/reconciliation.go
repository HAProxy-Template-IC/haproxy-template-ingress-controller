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
	"os"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	ctrlconfigpublisher "gitlab.com/haproxy-haptic/haptic/pkg/controller/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/discovery"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/eventemitter"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	leaderelectionctrl "gitlab.com/haproxy-haptic/haptic/pkg/controller/leaderelection"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/reconciler"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendergate"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceapplier"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/warmer"
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
//   - httpStore/capabilities/engine/typedResourceTypes/gvrMapper/publishedCurrentFiles:
//     read by createDryRunValidator when the webhook validators are wired up.
type reconciliationWiring struct {
	// renderService is the reconciliation render service. Admission renders
	// on it too: its graph is warm on every replica (leader renders, follower
	// warmer), so a dry-run rebases its overlay on the current root instead
	// of rebuilding the graph per request, and it sees the fleet capabilities
	// the deploy side sources into it.
	renderService         *renderer.RenderService
	publishedCurrentFiles *publishedAuxFiles
	gvrMapper             meta.RESTMapper
}

// renderInputs routes the deploy side's two feedback channels: the plan the
// fleet ACKed belongs to the reconciliation render alone, while the fleet's
// capabilities go to every render that feeds a gate.
type renderInputs struct {
	deployer.AckedPlanSink
	deployer.FleetCapabilitiesSink
}

// leadershipFence builds the epoch every apply is fenced by and hands it to
// the leader-election component through setup, so both halves share one
// counter. Nil without leader election: a single writer needs no fence.
//
// Standing down cancels the current election attempt, so the Lease is
// released (ReleaseOnCancel) and superviseElection re-enters election in
// place: nothing short of a fresh acquisition claims a fresh epoch, and the
// iteration — its stores and admission validators — keeps running.
func leadershipFence(setup *componentSetup, cfg *coreconfig.Config, k8sClient *client.Client, logger *slog.Logger) deployer.LeadershipFence {
	if !cfg.Controller.LeaderElection.Enabled {
		return nil
	}
	podName, podNamespace := leaderIdentity(k8sClient, logger)
	epoch := leaderelectionctrl.NewLeaseEpoch(k8sClient.Clientset(),
		podNamespace, cfg.Controller.LeaderElection.LeaseName, podName, logger)
	setup.ElectionRestart = make(chan struct{}, 1)
	setup.LeaderEpoch = leaderelectionctrl.NewTerm(epoch, func(reason string) {
		logger.Error("Giving up leadership, re-electing", "reason", reason, "identity", podName)
		select {
		case setup.ElectionRestart <- struct{}{}:
		default:
		}
	})
	return setup.LeaderEpoch
}

// detectLocalCapabilities seeds the template `capabilities` input from the
// controller image's own HAProxy binary. Discovery replaces it with the fleet's
// lowest reported version once the pods answer; the chart pins the same
// haproxyVersion for both images, so the seed is right on a healthy fleet and
// only an in-flight upgrade moves it.
func detectLocalCapabilities(ctx context.Context, logger *slog.Logger) (
	*renderer.CapabilitiesFanout, error,
) {
	localVersion, err := dataplane.DetectLocalVersionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting local HAProxy version: %w", err)
	}
	capabilities := renderer.NewCapabilitiesFanout(dataplane.CapabilitiesFromVersion(localVersion))

	local := capabilities.Capabilities()
	logger.Info("Detected local HAProxy version",
		"version", localVersion.Full,
		"supports_crt_list", local.SupportsCrtList,
		"supports_map_storage", local.SupportsMapStorage,
		"supports_general_storage", local.SupportsGeneralStorage)
	return capabilities, nil
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
	currentFiles *currentFilesAuthority,
	storeProvider stores.StoreProvider,
	outputValidator pipeline.RenderedOutputValidator,
	logger *slog.Logger,
) (*reconciliationWiring, error) {
	// Create Reconciler. It fires immediately on every resource/HTTP event;
	// there is no reconciler-level refractory. Batching is per-watcher
	// (debounceInterval), reload throttling is the deployer's
	// minDeploymentInterval (which the runtime-eligible fast path bypasses).
	reconcilerComponent := reconciler.New(setup.Bus, logger)

	// The controller image's own HAProxy binary seeds the template
	capabilities, err := detectLocalCapabilities(setup.IterCtx, logger)
	if err != nil {
		return nil, err
	}

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
		Engine:             engine,
		Config:             cfg,
		Logger:             logger,
		Capabilities:       capabilities.Capabilities(),
		HAProxyPodStore:    haproxyPodStore,
		HTTPStoreComponent: httpStoreComponent,
		TypedResourceTypes: wiring.TypedResourceTypes,
	})
	setup.AddCleanup(func() {
		if err := renderService.RetireIncrementalCache(); err != nil {
			logger.Error("Retiring incremental render cache failed", "error", err)
		}
	})

	// Two pipeline instances, because the two callers answer to different
	// clocks. The reconcile instance renders and hands the bytes to the fleet;
	// HAProxy's verdict on them arrives from the render gate (ADR-0022). The
	// proposal instance answers an admission request, which must carry the
	// verdict in its own reply, so it keeps the full synchronous check.
	proposalValidation := newProposalValidator(cfg, logger)
	reconcilePipeline := pipeline.New(&pipeline.PipelineConfig{
		Renderer:        renderService,
		OutputValidator: outputValidator,
		CommitValidator: proposalValidation,
		Logger:          logger,
	})
	proposalPipeline := pipeline.New(&pipeline.PipelineConfig{
		Renderer:        renderService,
		Validator:       proposalValidation,
		OutputValidator: outputValidator,
		Logger:          logger,
	})

	// Coordinator: leader-side render + deploy.
	coordinatorComponent := reconciler.NewCoordinator(&reconciler.CoordinatorConfig{
		EventBus:      setup.Bus,
		Pipeline:      reconcilePipeline,
		StoreProvider: storeProvider,
		CurrentFiles:  currentFiles,
		Metrics:       setup.MetricsComponent.Metrics(),
		Logger:        logger,
	})

	// Warmer: a follower's render, committed for the graph and then dropped.
	// Its own pipeline leaves out the pluggable output validators, which would
	// otherwise run on every replica for a render nothing deploys.
	warmerComponent := warmer.New(&warmer.Config{
		EventBus: setup.Bus,
		Pipeline: pipeline.New(&pipeline.PipelineConfig{
			Renderer:        renderService,
			CommitValidator: proposalValidation,
			Logger:          logger,
		}),
		StoreProvider: storeProvider,
		CurrentFiles:  currentFiles.publishedSnapshot,
		Metrics:       setup.MetricsComponent.Metrics(),
		Logger:        logger,
	})

	// ProposalValidator: admission webhook + HTTP-store content promotion.
	proposalValidatorComponent := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:             setup.Bus,
		Pipeline:             proposalPipeline,
		BaseStoreProvider:    storeProvider,
		CurrentFilesProvider: currentFiles.publishedSnapshot,
		Logger:               logger,
	})

	// RenderGate: the reconcile path's `haproxy -c`, off the wall clock, on its
	// own semaphore slot so admission never queues behind a fleet-sized check.
	renderGateComponent := rendergate.New(&rendergate.Config{
		EventBus: setup.Bus,
		Logger:   logger,
		Checker:  rendergate.ServiceChecker{Service: newRenderGateValidator(cfg, logger)},
		Metrics:  setup.MetricsComponent.Metrics(),
	})

	// One constructor, wired inside the deployer package: the connections
	// between these three used to be optional setters a caller could forget.
	capabilities.Add(renderService)
	deployStack := deployer.NewDeployStack(setup.Bus, cfg, logger,
		setup.MetricsComponent.Metrics(),
		renderInputs{AckedPlanSink: renderService, FleetCapabilitiesSink: capabilities},
		leadershipFence(setup, cfg, k8sClient, logger))
	deployerComponent := deployStack.Deployer
	deploymentSchedulerComponent := deployStack.Scheduler
	driftMonitorComponent := deployStack.DriftMonitor

	// Create Discovery component and set pod store
	discoveryComponent := discovery.New(setup.Bus, logger)
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
	purePublisher, stopPublisherInformers, err := createConfigPublisher(setup.IterCtx, crdClientset, k8sClient, logger)
	if err != nil {
		return nil, err
	}
	setup.AddCleanup(stopPublisherInformers)
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
		SelfWrites:    setup.SelfWrites,
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
			warmerComponent,
			discoveryComponent,
			httpStoreComponent,
			proposalValidatorComponent,
			statusApplierComponent,
			resourceApplierComponent,
			eventEmitterComponent,
		},
		[]lifecycle.Component{
			coordinatorComponent,
			renderGateComponent,
			driftMonitorComponent,
			deployerComponent,
			deploymentSchedulerComponent,
			configPublisherComponent,
			statusUpdaterComponent,
		},
	)

	return &reconciliationWiring{
		renderService:         renderService,
		publishedCurrentFiles: currentFiles.published,
		gvrMapper:             gvrMapper,
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

// newRenderGateValidator builds the render gate's validation service: the
// `haproxy -c -dr` run, on a gate of its own.
//
// `-dr` matches the shipped pod and today's reconcile path — a DNS blip must
// never revert a fleet. The gate's own CheckGate keeps the webhook's 9 s
// failurePolicy: Fail budget clear of it, and its duty-cycle interval bounds
// the CPU a render storm can take from admission.
func newRenderGateValidator(cfg *coreconfig.Config, logger *slog.Logger) *validation.ValidationService {
	dirConfig := extractValidationDirConfig(&cfg.Dataplane)
	return validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger.With("validation", "rendergate"),
		SkipDNSValidation: true,
		CheckGate:         dataplane.NewCheckGate(cfg.Controller.GetRenderGateInterval()),
		BaseDir:           dirConfig.BaseDir,
		MapsDir:           dirConfig.MapsDir,
		SSLCertsDir:       dirConfig.SSLCertsDir,
		GeneralDir:        dirConfig.GeneralDir,
	})
}

// newProposalValidator creates the full-validation service that answers for
// operator input: admission proposals, HTTP content promotion, and the commit
// of external content a reconcile render accepted for the first time. DNS
// lookup remains flaky and recovers at runtime (HAProxy starts the server DOWN
// and brings it up when the next health check resolves).
func newProposalValidator(
	cfg *coreconfig.Config,
	logger *slog.Logger,
) *validation.ValidationService {
	dirConfig := extractValidationDirConfig(&cfg.Dataplane)
	return validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger.With("validation", "full"),
		SkipDNSValidation: true,
		BaseDir:           dirConfig.BaseDir,
		MapsDir:           dirConfig.MapsDir,
		SSLCertsDir:       dirConfig.SSLCertsDir,
		GeneralDir:        dirConfig.GeneralDir,
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
func createConfigPublisher(ctx context.Context, crdClientset versioned.Interface, k8sClient *client.Client, logger *slog.Logger) (*configpublisher.Publisher, func(), error) {
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

	informerCtx, cancelInformers := context.WithCancel(ctx)
	stopInformers := sync.OnceFunc(func() {
		cancelInformers()
		informerFactory.Shutdown()
	})
	informerFactory.StartWithContext(informerCtx)

	// Wait for cache to sync before creating publisher
	// This ensures listers have initial data before first use
	logger.Debug("Waiting for HAProxy CRD informer caches to sync")
	syncResult := informerFactory.WaitForCacheSyncWithContext(informerCtx)
	for informerType, synced := range syncResult.Synced {
		if !synced {
			stopInformers()
			return nil, nil, fmt.Errorf("informer cache sync failed for %v: %w", informerType, syncResult.Err)
		}
	}
	logger.Debug("HAProxy CRD informer caches synced")

	// Create publisher with listers for cached reads
	return configpublisher.NewWithListers(k8sClient.Clientset(), crdClientset, listers, logger), stopInformers, nil
}

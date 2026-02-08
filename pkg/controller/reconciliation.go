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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	ctrlconfigpublisher "gitlab.com/haproxy-haptic/haptic/pkg/controller/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/deployer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/discovery"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/reconciler"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	informers "gitlab.com/haproxy-haptic/haptic/pkg/generated/informers/externalversions"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

// reconciliationComponents holds all reconciliation-related components.
type reconciliationComponents struct {
	reconciler          *reconciler.Reconciler
	coordinator         *reconciler.Coordinator // Orchestrates render-validate pipeline
	discovery           *discovery.Component
	deployer            *deployer.Component
	deploymentScheduler *deployer.DeploymentScheduler
	driftMonitor        *deployer.DriftPreventionMonitor
	configPublisher     *ctrlconfigpublisher.Component
	statusUpdater       *configchange.StatusUpdater  // Updates CRD status with validation results
	httpStore           *httpstore.Component         // HTTP resource fetcher for dynamic content
	proposalValidator   *proposalvalidator.Component // Validates HTTP content and webhook proposals
	capabilities        dataplane.Capabilities       // HAProxy/DataPlane API capabilities
}

// createReconciliationComponents creates all reconciliation components and registers them with the lifecycle registry.
func createReconciliationComponents(
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	currentConfigStore *currentconfigstore.Store,
	storeManager *resourcestore.Manager,
	bus *busevents.EventBus,
	registry *lifecycle.Registry,
	logger *slog.Logger,
) (*reconciliationComponents, error) {
	// Create Reconciler with default configuration
	reconcilerComponent := reconciler.New(bus, logger, nil)

	// Detect local HAProxy version and compute capabilities
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to detect local HAProxy version: %w", err)
	}
	capabilities := dataplane.CapabilitiesFromVersion(localVersion)

	logger.Info("detected local HAProxy version",
		"version", localVersion.Full,
		"supports_crt_list", capabilities.SupportsCrtList,
		"supports_map_storage", capabilities.SupportsMapStorage,
		"supports_general_storage", capabilities.SupportsGeneralStorage)

	// Get haproxy-pods store for pod-maxconn calculations in templates
	haproxyPodStore := resourceWatcher.GetStore(names.HAProxyPodsResourceType)
	if haproxyPodStore == nil {
		return nil, fmt.Errorf(names.HAProxyPodsResourceType + " store not found (should be auto-injected)")
	}

	// Create HTTPStore component for dynamic HTTP content fetching
	// This component manages periodic refreshes and content validation coordination
	// Eviction maxAge is 2x drift prevention interval to catch stale URLs
	driftInterval := cfg.Dataplane.GetDriftPreventionInterval()
	httpStoreEvictionMaxAge := 2 * driftInterval
	httpStoreComponent := httpstore.New(bus, logger, httpStoreEvictionMaxAge)

	// Create template engine for the Coordinator's Pipeline
	// currentConfig is needed for slot preservation during renders
	additionalDeclarations := map[string]any{
		"currentConfig": (*parserconfig.StructuredConfig)(nil),
	}
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, additionalDeclarations, helpers.EngineOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create template engine for reconciliation: %w", err)
	}

	// Create RenderService with full dependencies for production rendering
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:             engine,
		Config:             cfg,
		Logger:             logger,
		Capabilities:       capabilities,
		HAProxyPodStore:    haproxyPodStore,
		HTTPStoreComponent: httpStoreComponent,
		CurrentConfigStore: currentConfigStore,
	})

	// Create ValidationService with permissive DNS validation for runtime
	// (servers with unresolvable hostnames start DOWN instead of failing)
	dirConfig := extractValidationDirConfig(&cfg.Dataplane)
	validationService := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            logger,
		Version:           localVersion,
		SkipDNSValidation: true, // Permissive for runtime reconciliation
		BaseDir:           dirConfig.BaseDir,
		MapsDir:           dirConfig.MapsDir,
		SSLCertsDir:       dirConfig.SSLCertsDir,
		GeneralDir:        dirConfig.GeneralDir,
	})

	// Create Pipeline (composes render + validate)
	pipelineInstance := pipeline.New(&pipeline.PipelineConfig{
		Renderer:  renderService,
		Validator: validationService,
		Logger:    logger,
	})

	// Create StoreProvider from storeManager for the Coordinator
	baseStoreProvider := newStoreProviderFromManager(storeManager)

	// Create Coordinator (replaces Executor + Renderer event handling + HAProxyValidator)
	// The Coordinator calls Pipeline.Execute() directly and publishes events for downstream components
	coordinatorComponent := reconciler.NewCoordinator(&reconciler.CoordinatorConfig{
		EventBus:      bus,
		Pipeline:      pipelineInstance,
		StoreProvider: baseStoreProvider,
		Logger:        logger,
	})

	// Create ProposalValidator (handles validation of HTTP content and webhook proposals)
	// This is an all-replica component because HTTPStore (all-replica) depends on it to
	// validate HTTP content before promotion. Webhook validation also uses this component.
	proposalValidatorComponent := proposalvalidator.New(&proposalvalidator.ComponentConfig{
		EventBus:          bus,
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStoreProvider,
		Logger:            logger,
	})

	// Create Deployer with maxParallel and rawPushThreshold from config
	deployerComponent := deployer.New(bus, logger, cfg.Dataplane.MaxParallel, cfg.Dataplane.RawPushThreshold)

	// Create DeploymentScheduler with rate limiting and timeout
	minDeploymentInterval := cfg.Dataplane.GetMinDeploymentInterval()
	deploymentTimeout := cfg.Dataplane.GetDeploymentTimeout()
	deploymentSchedulerComponent := deployer.NewDeploymentScheduler(bus, logger, minDeploymentInterval, deploymentTimeout)

	// Create DriftPreventionMonitor
	driftPreventionInterval := cfg.Dataplane.GetDriftPreventionInterval()
	driftMonitorComponent := deployer.NewDriftPreventionMonitor(bus, logger, driftPreventionInterval)

	// Create Discovery component and set pod store
	// This detects the local HAProxy version (fatal if fails - controller cannot start
	// without knowing its local version for compatibility checking)
	discoveryComponent, err := discovery.New(bus, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery component: %w", err)
	}
	podStore := resourceWatcher.GetStore(names.HAProxyPodsResourceType)
	if podStore == nil {
		return nil, fmt.Errorf(names.HAProxyPodsResourceType + " store not found (should be auto-injected)")
	}
	discoveryComponent.SetPodStore(podStore)

	// Create Config Publisher (pure publisher + event adapter)
	// Publishes runtime config resources after successful validation
	crdClientset, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create CRD clientset: %w", err)
	}

	// Create publisher with informer-backed listers for cached reads
	purePublisher, err := createConfigPublisher(crdClientset, k8sClient, logger)
	if err != nil {
		return nil, err
	}
	configPublisherComponent := ctrlconfigpublisher.New(purePublisher, bus, logger)

	// Create Status Updater (updates HAProxyTemplateConfig CRD status with validation results)
	// This allows users to see validation errors via `kubectl describe haproxytemplateconfig`
	statusUpdaterComponent := configchange.NewStatusUpdater(crdClientset, bus, logger)

	// Register components with the lifecycle registry using builder pattern
	// Coordinator is leader-only because it performs rendering (state changes).
	// DriftMonitor is leader-only to avoid multi-replica race conditions.
	// StatusUpdater is leader-only to avoid API conflicts from concurrent updates.
	// ProposalValidator is all-replica because HTTPStore depends on it for HTTP content validation.
	registry.Build().
		AllReplica(
			reconcilerComponent,
			discoveryComponent,
			httpStoreComponent,
			proposalValidatorComponent,
		).
		LeaderOnly(
			coordinatorComponent,
			driftMonitorComponent,
			deployerComponent,
			deploymentSchedulerComponent,
			configPublisherComponent,
			statusUpdaterComponent,
		).
		Done()

	return &reconciliationComponents{
		reconciler:          reconcilerComponent,
		coordinator:         coordinatorComponent,
		discovery:           discoveryComponent,
		deployer:            deployerComponent,
		deploymentScheduler: deploymentSchedulerComponent,
		driftMonitor:        driftMonitorComponent,
		configPublisher:     configPublisherComponent,
		statusUpdater:       statusUpdaterComponent,
		httpStore:           httpStoreComponent,
		proposalValidator:   proposalValidatorComponent,
		capabilities:        capabilities,
	}, nil
}

// createConfigPublisher creates a config publisher with informer-backed listers for cached reads.
// This significantly reduces API calls by checking cached state before doing status updates.
func createConfigPublisher(crdClientset versioned.Interface, k8sClient *client.Client, logger *slog.Logger) (*configpublisher.Publisher, error) {
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
	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)

	// Wait for cache to sync before creating publisher
	// This ensures listers have initial data before first use
	logger.Debug("waiting for HAProxy CRD informer caches to sync")
	syncResult := informerFactory.WaitForCacheSync(stopCh)
	for informerType, synced := range syncResult {
		if !synced {
			close(stopCh)
			return nil, fmt.Errorf("informer cache sync failed for %v", informerType)
		}
	}
	logger.Debug("HAProxy CRD informer caches synced")

	// Create publisher with listers for cached reads
	return configpublisher.NewWithListers(k8sClient.Clientset(), crdClientset, listers, logger), nil
}

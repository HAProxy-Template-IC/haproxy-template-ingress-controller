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

// Package controller provides the main controller orchestration for the HAProxy template ingress controller.
//
// The controller follows an event-driven architecture with a reinitialization loop:
// 1. Fetch and validate initial configuration
// 2. Create EventBus and components
// 3. Start components and watchers
// 4. Wait for configuration changes
// 5. Reinitialize on valid config changes
package controller

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"

	"haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/pkg/controller/commentator"
	"haproxy-template-ic/pkg/controller/configchange"
	"haproxy-template-ic/pkg/controller/configloader"
	ctrlconfigpublisher "haproxy-template-ic/pkg/controller/configpublisher"
	"haproxy-template-ic/pkg/controller/conversion"
	"haproxy-template-ic/pkg/controller/credentialsloader"
	"haproxy-template-ic/pkg/controller/debug"
	"haproxy-template-ic/pkg/controller/deployer"
	"haproxy-template-ic/pkg/controller/discovery"
	dryrunvalidator "haproxy-template-ic/pkg/controller/dryrunvalidator"
	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/executor"
	"haproxy-template-ic/pkg/controller/helpers"
	"haproxy-template-ic/pkg/controller/httpstore"
	"haproxy-template-ic/pkg/controller/indextracker"
	leaderelectionctrl "haproxy-template-ic/pkg/controller/leaderelection"
	"haproxy-template-ic/pkg/controller/metrics"
	"haproxy-template-ic/pkg/controller/reconciler"
	"haproxy-template-ic/pkg/controller/renderer"
	"haproxy-template-ic/pkg/controller/resourcestore"
	"haproxy-template-ic/pkg/controller/resourcewatcher"
	"haproxy-template-ic/pkg/controller/validator"
	"haproxy-template-ic/pkg/controller/webhook"
	coreconfig "haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/generated/clientset/versioned"
	"haproxy-template-ic/pkg/introspection"
	"haproxy-template-ic/pkg/k8s/client"
	"haproxy-template-ic/pkg/k8s/configpublisher"
	k8sleaderelection "haproxy-template-ic/pkg/k8s/leaderelection"
	"haproxy-template-ic/pkg/k8s/types"
	"haproxy-template-ic/pkg/k8s/watcher"
	"haproxy-template-ic/pkg/lifecycle"
	pkgmetrics "haproxy-template-ic/pkg/metrics"
)

const (
	// RetryDelay is the duration to wait before retrying after an iteration failure.
	RetryDelay = 5 * time.Second
	// ConfigPollInterval is the interval for polling HAProxyTemplateConfig availability.
	ConfigPollInterval = 5 * time.Second
	// DebugEventBufferSize is the size of the event buffer for debug/introspection.
	DebugEventBufferSize = 1000
)

// errNoWebhookRules indicates that no webhook rules are configured.
// This is used to signal that DryRunValidator should not be created.
var errNoWebhookRules = errors.New("no webhook rules configured")

// configState tracks initialization state for health checks.
// It allows the health endpoint to report unhealthy status until
// the HAProxyTemplateConfig is successfully loaded.
type configState struct {
	mu           sync.RWMutex
	configLoaded bool
	message      string
}

// SetLoaded marks the config as successfully loaded.
func (s *configState) SetLoaded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configLoaded = true
	s.message = ""
}

// SetWaiting marks the config as not yet loaded with a status message.
func (s *configState) SetWaiting(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configLoaded = false
	s.message = msg
}

// IsLoaded returns true if the config has been successfully loaded.
func (s *configState) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configLoaded
}

// Message returns the current status message (empty if config is loaded).
func (s *configState) Message() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.message
}

// Run is the main entry point for the controller.
//
// It performs initial configuration fetching and validation, then enters a reinitialization
// loop where it responds to configuration changes by restarting with the new configuration.
//
// The controller uses an event-driven architecture:
//   - EventBus coordinates all components
//   - SingleWatcher monitors HAProxyTemplateConfig CRD and Secret
//   - Components react to events and publish results
//   - ConfigChangeHandler detects validated config changes and signals reinitialization
//
// Parameters:
//   - ctx: Context for cancellation (SIGTERM, SIGINT, etc.)
//   - k8sClient: Kubernetes client for API access
//   - crdName: Name of the HAProxyTemplateConfig CRD
//   - secretName: Name of the Secret containing HAProxy Dataplane API credentials
//   - webhookCertSecretName: Name of the Secret containing webhook TLS certificates
//   - debugPort: Port for debug HTTP server (0 to disable)
//
// Returns:
//   - Error if the controller cannot start or encounters a fatal error
//   - nil if the context is cancelled (graceful shutdown)
func Run(ctx context.Context, k8sClient *client.Client, crdName, secretName, webhookCertSecretName string, debugPort int) error {
	logger := slog.Default()

	logger.Info("HAProxy Template Ingress Controller starting",
		"crd_name", crdName,
		"secret", secretName,
		"webhook_cert_secret", webhookCertSecretName,
		"namespace", k8sClient.Namespace())

	// Main reinitialization loop
	for {
		select {
		case <-ctx.Done():
			logger.Info("Controller shutting down", "reason", ctx.Err())
			return nil
		default:
			// Run one iteration
			err := runIteration(ctx, k8sClient, crdName, secretName, webhookCertSecretName, debugPort, logger)
			if err != nil {
				// Check if error is context cancellation (graceful shutdown)
				if ctx.Err() != nil {
					logger.Info("Controller shutting down during iteration", "reason", ctx.Err())
					return nil //nolint:nilerr // Graceful shutdown is not an error
				}

				// Log error and retry after delay
				logger.Error("Controller iteration failed, retrying",
					"error", err,
					"retry_delay", RetryDelay)
				time.Sleep(RetryDelay)
			}
			// If err == nil, config change occurred and we reinitialize immediately
		}
	}
}

// fetchAndValidateInitialConfig fetches, parses, and validates the initial ConfigMap and Secret.
//
// Returns the validated configuration and credentials, or an error if any step fails.
// WebhookCertificates holds the TLS certificate and private key for the webhook server.
type WebhookCertificates struct {
	CertPEM []byte
	KeyPEM  []byte
	Version string
}

func fetchAndValidateInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	webhookCertSecretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*coreconfig.Config, *v1alpha1.HAProxyTemplateConfig, *coreconfig.Credentials, *WebhookCertificates, error) {
	logger.Info("Fetching initial CRD, credentials, and webhook certificates",
		"crd_name", crdName)

	var crdResource *unstructured.Unstructured
	var secretResource *unstructured.Unstructured
	var webhookCertSecretResource *unstructured.Unstructured

	g, gCtx := errgroup.WithContext(ctx)

	// Fetch HAProxyTemplateConfig CRD
	g.Go(func() error {
		var err error
		crdResource, err = k8sClient.GetResource(gCtx, crdGVR, crdName)
		if err != nil {
			return fmt.Errorf("failed to fetch HAProxyTemplateConfig %q: %w", crdName, err)
		}
		return nil
	})

	// Fetch Secret (credentials)
	g.Go(func() error {
		var err error
		secretResource, err = k8sClient.GetResource(gCtx, secretGVR, secretName)
		if err != nil {
			return fmt.Errorf("failed to fetch Secret %q: %w", secretName, err)
		}
		return nil
	})

	// Fetch Secret (webhook certificates)
	g.Go(func() error {
		var err error
		webhookCertSecretResource, err = k8sClient.GetResource(gCtx, secretGVR, webhookCertSecretName)
		if err != nil {
			return fmt.Errorf("failed to fetch webhook certificate Secret %q: %w", webhookCertSecretName, err)
		}
		return nil
	})

	// Wait for all fetches to complete
	if err := g.Wait(); err != nil {
		return nil, nil, nil, nil, err
	}

	// Parse initial configuration
	logger.Info("Parsing initial configuration, credentials, and webhook certificates")

	cfg, crd, err := parseCRD(crdResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse initial HAProxyTemplateConfig: %w", err)
	}

	creds, err := parseSecret(secretResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse initial Secret: %w", err)
	}

	webhookCerts, err := parseWebhookCertSecret(webhookCertSecretResource)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse webhook certificate Secret: %w", err)
	}

	// Validate initial configuration
	logger.Info("Validating initial configuration and credentials")

	if err := coreconfig.ValidateStructure(cfg); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initial configuration validation failed: %w", err)
	}

	if err := coreconfig.ValidateCredentials(creds); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("initial credentials validation failed: %w", err)
	}

	logger.Info("Initial configuration validated successfully",
		"crd_version", crdResource.GetResourceVersion(),
		"secret_version", secretResource.GetResourceVersion(),
		"webhook_cert_version", webhookCertSecretResource.GetResourceVersion())

	return cfg, crd, creds, webhookCerts, nil
}

// waitForInitialConfig polls for the HAProxyTemplateConfig until it exists.
// This handles the race condition during fresh installs where the controller pod
// may start before the HAProxyTemplateConfig CR is fully available in the API server.
//
// Returns nil when config is found, or ctx.Err() if context is cancelled.
func waitForInitialConfig(
	ctx context.Context,
	k8sClient *client.Client,
	crdName string,
	crdGVR schema.GroupVersionResource,
	state *configState,
	logger *slog.Logger,
) error {
	state.SetWaiting("waiting for HAProxyTemplateConfig")

	// Try immediately first
	exists, _ := checkConfigExists(ctx, k8sClient, crdGVR, crdName)
	if exists {
		logger.Info("HAProxyTemplateConfig found", "name", crdName)
		return nil
	}

	logger.Info("Waiting for HAProxyTemplateConfig to become available",
		"name", crdName,
		"poll_interval", ConfigPollInterval)

	ticker := time.NewTicker(ConfigPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			exists, err := checkConfigExists(ctx, k8sClient, crdGVR, crdName)
			if err != nil {
				// Log at debug level - transient errors during polling are expected
				logger.Debug("Error checking for HAProxyTemplateConfig", "error", err)
				continue
			}
			if exists {
				logger.Info("HAProxyTemplateConfig found", "name", crdName)
				return nil
			}
			logger.Debug("HAProxyTemplateConfig not yet available, continuing to wait",
				"name", crdName)
		}
	}
}

// checkConfigExists checks if the HAProxyTemplateConfig resource exists.
// Returns (true, nil) if exists, (false, nil) if not found, or (false, err) on other errors.
func checkConfigExists(ctx context.Context, k8sClient *client.Client, gvr schema.GroupVersionResource, name string) (bool, error) {
	_, err := k8sClient.GetResource(ctx, gvr, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// finalizeConfigLoad marks config as loaded for health checks and sets the initial config
// version to prevent the infinite reinitialization loop. CRDWatcher.onAdd will trigger
// ConfigValidatedEvent with this version - without tracking it, that event would trigger
// reinitialization creating an infinite loop.
func finalizeConfigLoad(state *configState, setup *componentSetup, resourceVersion string) {
	state.SetLoaded()
	setup.ConfigChangeHandler.SetInitialConfigVersion(resourceVersion)
}

// componentSetup contains all resources created during component initialization.
type componentSetup struct {
	Bus                   *busevents.EventBus
	Registry              *lifecycle.Registry // Component lifecycle registry
	MetricsComponent      *metrics.Component
	MetricsRegistry       *prometheus.Registry
	IntrospectionRegistry *introspection.Registry
	IntrospectionServer   *introspection.Server // Server reference for custom handler registration
	StoreManager          *resourcestore.Manager
	ConfigChangeHandler   *configchange.ConfigChangeHandler // For setting initial config version
	IterCtx               context.Context
	Cancel                context.CancelFunc
	ConfigChangeCh        chan *coreconfig.Config
	ErrGroup              *errgroup.Group // Tracks all background goroutines for graceful shutdown
}

// setupComponents creates and starts all event-driven components.
func setupComponents(
	ctx context.Context,
	logger *slog.Logger,
) *componentSetup {
	// Create EventBus with buffer for pre-start events
	bus := busevents.NewEventBus(100)

	// Create Prometheus registry for this iteration (instance-based, not global)
	registry := prometheus.NewRegistry()

	// Create metrics collector
	domainMetrics := metrics.NewMetrics(registry)
	metricsComponent := metrics.New(domainMetrics, bus)

	// Create ResourceStoreManager for webhook validation
	storeManager := resourcestore.NewManager()

	// Create components
	eventCommentator := commentator.NewEventCommentator(bus, logger, 1000)
	configLoaderComponent := configloader.NewConfigLoaderComponent(bus, logger)
	credentialsLoaderComponent := credentialsloader.NewCredentialsLoaderComponent(bus, logger)

	// Create config validators (for ConfigMap validation)
	basicValidator := validator.NewBasicValidator(bus, logger)
	templateValidator := validator.NewTemplateValidator(bus, logger)
	jsonpathValidator := validator.NewJSONPathValidator(bus, logger)

	// Create webhook validators (for admission validation)
	basicWebhookValidator := webhook.NewBasicValidatorComponent(bus, logger)

	// Create config change channel for reinitialization signaling
	configChangeCh := make(chan *coreconfig.Config, 1)

	// Register validators for scatter-gather validation
	validators := validator.AllValidatorNames()

	configChangeHandlerComponent := configchange.NewConfigChangeHandler(
		bus,
		logger,
		configChangeCh,
		validators,
		0, // Use default debounce interval (500ms)
	)

	// Start components in goroutines with iteration-specific context
	iterCtx, cancel := context.WithCancel(ctx)

	// Create errgroup to track all background goroutines for graceful shutdown
	g, gCtx := errgroup.WithContext(iterCtx)

	// Start components in errgroup (these return nil on graceful shutdown)
	g.Go(func() error {
		if err := eventCommentator.Start(gCtx); err != nil {
			logger.Error("event commentator failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := configLoaderComponent.Start(gCtx); err != nil {
			logger.Error("config loader failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := credentialsLoaderComponent.Start(gCtx); err != nil {
			logger.Error("credentials loader failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := basicValidator.Start(gCtx); err != nil {
			logger.Error("basic validator failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := templateValidator.Start(gCtx); err != nil {
			logger.Error("template validator failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := jsonpathValidator.Start(gCtx); err != nil {
			logger.Error("jsonpath validator failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := basicWebhookValidator.Start(gCtx); err != nil {
			logger.Error("basic webhook validator failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := configChangeHandlerComponent.Start(gCtx); err != nil {
			logger.Error("config change handler failed", "error", err)
			cancel()
			return err
		}
		return nil
	})

	logger.Debug("All components started")

	// Create introspection registry for debug variables
	// This registry will be shared between early server startup and later variable registration
	introspectionRegistry := introspection.NewRegistry()

	// Create lifecycle registry for managing reconciliation components
	lifecycleRegistry := lifecycle.NewRegistry().WithLogger(logger)

	return &componentSetup{
		Bus:                   bus,
		Registry:              lifecycleRegistry,
		MetricsComponent:      metricsComponent,
		MetricsRegistry:       registry,
		IntrospectionRegistry: introspectionRegistry,
		StoreManager:          storeManager,
		ConfigChangeHandler:   configChangeHandlerComponent,
		IterCtx:               gCtx, // Use errgroup context so cancellation propagates
		Cancel:                cancel,
		ConfigChangeCh:        configChangeCh,
		ErrGroup:              g,
	}
}

// startEarlyInfrastructureServers starts debug and metrics HTTP servers early in startup.
// This function is called BEFORE fetching the initial configuration, so servers are available
// for debugging even if the controller fails to fetch config (e.g., RBAC issues).
//
// Unlike setupInfrastructureServers, this uses default/environment-based metrics port
// since the config hasn't been loaded yet.
//
// The introspection server uses two-phase initialization (Setup + Serve):
// 1. Register custom handlers (including /debug/events) before Setup()
// 2. Call Setup() to finalize routes
// 3. Call Serve() to start serving HTTP requests
//
// This pattern allows the /debug/events endpoint to be available while still starting
// health checks early for Kubernetes probes during the config waiting phase.
func startEarlyInfrastructureServers(
	ctx context.Context,
	debugPort int,
	setup *componentSetup,
	state *configState,
	eventBuffer *debug.EventBuffer,
	logger *slog.Logger,
) {
	logger.Info("Starting infrastructure servers (early initialization)")

	// Create introspection HTTP server (always enabled for health checks)
	// Provides /healthz endpoint for Kubernetes probes and /debug/* endpoints for debugging
	setup.IntrospectionServer = introspection.NewServer(fmt.Sprintf(":%d", debugPort), setup.IntrospectionRegistry)

	// Register /debug/events handler BEFORE Setup()
	// EventBuffer was created before this function to ensure proper subscription ordering
	debug.RegisterEventsHandler(setup.IntrospectionServer, eventBuffer)

	// Set initial health checker that reports unhealthy until config is loaded
	// This will be replaced by the full lifecycle-based checker in setupInfrastructureServers
	setup.IntrospectionServer.SetHealthChecker(func() map[string]introspection.ComponentHealth {
		if !state.IsLoaded() {
			return map[string]introspection.ComponentHealth{
				"config": {
					Healthy: false,
					Error:   state.Message(),
				},
			}
		}
		// Config loaded - return healthy (will be replaced by full checker later)
		return map[string]introspection.ComponentHealth{
			"config": {Healthy: true},
		}
	})

	// Setup routes (including custom handlers) - must be called before Serve()
	setup.IntrospectionServer.Setup()

	// Start serving HTTP requests
	go func() {
		if err := setup.IntrospectionServer.Serve(ctx); err != nil {
			logger.Error("introspection server failed", "error", err, "port", debugPort)
		}
	}()

	logger.Info("Introspection HTTP server started (early startup)",
		"port", debugPort,
		"bind_address", fmt.Sprintf("0.0.0.0:%d", debugPort),
		"endpoints", "/healthz, /debug/vars, /debug/pprof, /debug/events")

	// Start metrics HTTP server with default port
	// We use a default because config hasn't been loaded yet
	defaultMetricsPort := 9090
	if envPort := os.Getenv("METRICS_PORT"); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil {
			defaultMetricsPort = port
		}
	}

	if defaultMetricsPort > 0 {
		metricsServer := pkgmetrics.NewServer(fmt.Sprintf(":%d", defaultMetricsPort), setup.MetricsRegistry)
		go func() {
			if err := metricsServer.Start(ctx); err != nil {
				logger.Error("metrics server failed", "error", err, "port", defaultMetricsPort)
			}
		}()
		logger.Info("Metrics HTTP server scheduled (early startup)",
			"port", defaultMetricsPort,
			"bind_address", fmt.Sprintf("0.0.0.0:%d", defaultMetricsPort),
			"endpoint", "/metrics")
	} else {
		logger.Debug("Metrics HTTP server disabled (port=0)")
	}
}

// setupInfrastructureServers registers debug variables after config is loaded.
// The introspection server is already started by startEarlyInfrastructureServers, so this
// function registers debug variables and updates the health checker to use the lifecycle registry.
//
// Note: /debug/events is already registered in startEarlyInfrastructureServers via two-phase
// initialization (Setup/Serve pattern), so it's available even during early startup.
func setupInfrastructureServers(
	ctx context.Context,
	cfg *coreconfig.Config,
	setup *componentSetup,
	stateCache *StateCache,
	eventBuffer *debug.EventBuffer, // Pre-created buffer (created before EventBus.Start())
	logger *slog.Logger,
) {
	logger.Info("Stage 6: Registering debug variables and updating health checker")

	// Start event buffer (created before EventBus.Start() to ensure proper subscription)
	go func() {
		if err := eventBuffer.Start(ctx); err != nil {
			logger.Error("event buffer failed", "error", err)
		}
	}()

	// Register debug variables with the shared introspection registry
	debug.RegisterVariables(setup.IntrospectionRegistry, stateCache, eventBuffer)

	// Update health checker to use the full lifecycle registry
	// This replaces the initial simple health checker set in startEarlyInfrastructureServers
	setup.IntrospectionServer.SetHealthChecker(func() map[string]introspection.ComponentHealth {
		status := setup.Registry.Status()
		result := make(map[string]introspection.ComponentHealth, len(status))
		for name, info := range status {
			// StatusStandby is healthy - component is intentionally not active
			// (e.g., leader-only components on non-leader pods)
			healthy := info.Status == lifecycle.StatusRunning || info.Status == lifecycle.StatusStandby
			if info.Healthy != nil {
				healthy = *info.Healthy
			}
			result[name] = introspection.ComponentHealth{
				Healthy: healthy,
				Error:   info.Error,
			}
		}
		return result
	})

	logger.Info("Debug variables registered and health checker updated",
		"metrics_port", cfg.Controller.MetricsPort,
		"endpoints", "/debug/vars, /debug/pprof, /healthz")
}

// setupResourceWatchers creates and starts resource watchers and index tracker, then waits for sync.
//
// Returns the ResourceWatcherComponent and an error if watcher creation or synchronization fails.
func setupResourceWatchers(
	iterCtx context.Context,
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	bus *busevents.EventBus,
	logger *slog.Logger,
	cancel context.CancelFunc,
) (*resourcewatcher.ResourceWatcherComponent, error) {
	// Extract resource type names for IndexSynchronizationTracker
	// Include haproxy-pods which is auto-injected by ResourceWatcherComponent
	resourceNames := make([]string, 0, len(cfg.WatchedResources)+1)
	for name := range cfg.WatchedResources {
		resourceNames = append(resourceNames, name)
	}
	// Add haproxy-pods (auto-injected)
	resourceNames = append(resourceNames, "haproxy-pods")

	// Create ResourceWatcherComponent
	resourceWatcher, err := resourcewatcher.New(cfg, k8sClient, bus, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource watcher: %w", err)
	}

	// Create IndexSynchronizationTracker
	indexTracker := indextracker.New(bus, logger, resourceNames)

	// Start resource watcher and index tracker
	go func() {
		if err := resourceWatcher.Start(iterCtx); err != nil {
			logger.Error("resource watcher failed", "error", err)
			cancel()
		}
	}()

	go func() {
		if err := indexTracker.Start(iterCtx); err != nil {
			logger.Error("index tracker failed", "error", err)
			cancel()
		}
	}()

	// Wait for all resource indices to sync
	logger.Debug("Waiting for resource indices to sync")
	if err := resourceWatcher.WaitForAllSync(iterCtx); err != nil {
		return nil, fmt.Errorf("resource watcher sync failed: %w", err)
	}
	logger.Info("All resource indices synced successfully")

	return resourceWatcher, nil
}

// setupConfigWatchers creates and starts HAProxyTemplateConfig CRD and Secret watchers, then waits for sync.
//
// Returns an error if watcher creation or synchronization fails.
func setupConfigWatchers(
	iterCtx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	bus *busevents.EventBus,
	logger *slog.Logger,
	cancel context.CancelFunc,
) error {
	// Create watcher for HAProxyTemplateConfig CRD
	crdWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       crdGVR,
		Namespace: k8sClient.Namespace(),
		Name:      crdName,
		OnChange: func(obj interface{}) error {
			bus.Publish(events.NewConfigResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return fmt.Errorf("failed to create HAProxyTemplateConfig watcher: %w", err)
	}

	secretWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       secretGVR,
		Namespace: k8sClient.Namespace(),
		Name:      secretName,
		OnChange: func(obj interface{}) error {
			bus.Publish(events.NewSecretResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return fmt.Errorf("failed to create Secret watcher: %w", err)
	}

	// Start watchers in goroutines
	go func() {
		if err := crdWatcher.Start(iterCtx); err != nil {
			logger.Error("HAProxyTemplateConfig watcher failed", "error", err)
			cancel()
		}
	}()

	go func() {
		if err := secretWatcher.Start(iterCtx); err != nil {
			logger.Error("Secret watcher failed", "error", err)
			cancel()
		}
	}()

	logger.Debug("Watchers started, waiting for initial sync")

	// Wait for watchers to complete initial sync in parallel
	watcherGroup, watcherCtx := errgroup.WithContext(iterCtx)

	watcherGroup.Go(func() error {
		if err := crdWatcher.WaitForSync(watcherCtx); err != nil {
			return fmt.Errorf("HAProxyTemplateConfig watcher sync failed: %w", err)
		}
		return nil
	})

	watcherGroup.Go(func() error {
		if err := secretWatcher.WaitForSync(watcherCtx); err != nil {
			return fmt.Errorf("secret watcher sync failed: %w", err)
		}
		return nil
	})

	// Wait for both watchers to sync
	if err := watcherGroup.Wait(); err != nil {
		return err
	}

	logger.Info("Watchers synced successfully")

	// Initial config already passed via bootstrap event. Watchers handle subsequent changes only.

	return nil
}

// reconciliationComponents holds all reconciliation-related components.
type reconciliationComponents struct {
	reconciler          *reconciler.Reconciler
	renderer            *renderer.Component
	haproxyValidator    *validator.HAProxyValidatorComponent
	executor            *executor.Executor
	discovery           *discovery.Component
	deployer            *deployer.Component
	deploymentScheduler *deployer.DeploymentScheduler
	driftMonitor        *deployer.DriftPreventionMonitor
	configPublisher     *ctrlconfigpublisher.Component
	httpStore           *httpstore.Component   // HTTP resource fetcher for dynamic content
	capabilities        dataplane.Capabilities // HAProxy/DataPlane API capabilities
}

// leaderOnlyComponents holds components that only the leader should run.
type leaderOnlyComponents struct {
	deployer            *deployer.Component
	deploymentScheduler *deployer.DeploymentScheduler
	configPublisher     *ctrlconfigpublisher.Component
	ctx                 context.Context
	cancel              context.CancelFunc
}

// createReconciliationComponents creates all reconciliation components and registers them with the lifecycle registry.
func createReconciliationComponents(
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
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

	// Create Renderer with stores from ResourceWatcher
	stores := resourceWatcher.GetAllStores()

	// Get haproxy-pods store for pod-maxconn calculations in templates
	haproxyPodStore := resourceWatcher.GetStore("haproxy-pods")
	if haproxyPodStore == nil {
		return nil, fmt.Errorf("haproxy-pods store not found (should be auto-injected)")
	}

	rendererComponent, err := renderer.New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create renderer: %w", err)
	}

	// Create HTTPStore component for dynamic HTTP content fetching
	// This component manages periodic refreshes and content validation coordination
	// Eviction maxAge is 2x drift prevention interval to catch stale URLs
	driftInterval := cfg.Dataplane.GetDriftPreventionInterval()
	httpStoreEvictionMaxAge := 2 * driftInterval
	httpStoreComponent := httpstore.New(bus, logger, httpStoreEvictionMaxAge)
	rendererComponent.SetHTTPStoreComponent(httpStoreComponent)

	// Create HAProxy Validator
	// Validation paths are now created per-render by the Renderer component for parallel validation support
	haproxyValidatorComponent := validator.NewHAProxyValidator(bus, logger)

	// Create Executor
	executorComponent := executor.New(bus, logger)

	// Create Deployer
	deployerComponent := deployer.New(bus, logger)

	// Create DeploymentScheduler with rate limiting
	minDeploymentInterval := cfg.Dataplane.GetMinDeploymentInterval()
	deploymentSchedulerComponent := deployer.NewDeploymentScheduler(bus, logger, minDeploymentInterval)

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
	podStore := resourceWatcher.GetStore("haproxy-pods")
	if podStore == nil {
		return nil, fmt.Errorf("haproxy-pods store not found (should be auto-injected)")
	}
	discoveryComponent.SetPodStore(podStore)

	// Create Config Publisher (pure publisher + event adapter)
	// Publishes runtime config resources after successful validation
	crdClientset, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create CRD clientset: %w", err)
	}
	purePublisher := configpublisher.New(k8sClient.Clientset(), crdClientset, logger)
	configPublisherComponent := ctrlconfigpublisher.New(purePublisher, bus, logger)

	// Register components with the lifecycle registry using builder pattern
	// DriftMonitor runs on all replicas to keep HTTP Store caches warm.
	// Only leader's DeploymentScheduler will actually deploy.
	registry.Build().
		AllReplica(
			reconcilerComponent,
			rendererComponent,
			haproxyValidatorComponent,
			executorComponent,
			discoveryComponent,
			httpStoreComponent,
			driftMonitorComponent,
		).
		LeaderOnly(
			deployerComponent,
			deploymentSchedulerComponent,
			configPublisherComponent,
		).
		Done()

	return &reconciliationComponents{
		reconciler:          reconcilerComponent,
		renderer:            rendererComponent,
		haproxyValidator:    haproxyValidatorComponent,
		executor:            executorComponent,
		discovery:           discoveryComponent,
		deployer:            deployerComponent,
		deploymentScheduler: deploymentSchedulerComponent,
		driftMonitor:        driftMonitorComponent,
		configPublisher:     configPublisherComponent,
		httpStore:           httpStoreComponent,
		capabilities:        capabilities,
	}, nil
}

// startReconciliationComponents starts all-replica reconciliation components using the lifecycle registry.
// Leader-only components (Deployer, DeploymentScheduler, ConfigPublisher) are NOT started here.
func startReconciliationComponents(
	iterCtx context.Context,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	cancel context.CancelFunc,
) {
	// Start all-replica components using the registry (non-blocking)
	// The registry handles concurrent startup and error propagation
	go func() {
		if err := registry.StartAll(iterCtx, false); err != nil {
			logger.Error("reconciliation component failed", "error", err)
			cancel()
		}
	}()

	logger.Info("Reconciliation components started via lifecycle registry (all replicas)",
		"component_count", registry.Count())
}

// startLeaderOnlyComponents starts components that only the leader should run using the lifecycle registry.
// Returns a leaderOnlyComponents struct with cancellation control.
func startLeaderOnlyComponents(
	parentCtx context.Context,
	components *reconciliationComponents,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	parentCancel context.CancelFunc,
) *leaderOnlyComponents {
	// Create separate context for leader-only components
	leaderCtx, leaderCancel := context.WithCancel(parentCtx)

	// Start leader-only components using the registry (non-blocking)
	// The registry handles concurrent startup and error propagation
	go func() {
		if err := registry.StartLeaderOnlyComponents(leaderCtx); err != nil && leaderCtx.Err() == nil {
			logger.Error("leader-only component failed", "error", err)
			parentCancel()
		}
	}()

	logger.Info("Leader-only components started via lifecycle registry",
		"components", "Deployer, DeploymentScheduler, ConfigPublisher")

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
			deps.logger.Info("Became leader, starting deployment components")
			state.mu.Lock()
			defer state.mu.Unlock()
			state.components = startLeaderOnlyComponents(
				deps.iterCtx,
				deps.reconComponents,
				deps.registry,
				deps.logger,
				deps.cancel,
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
			deps.logger.Info("New leader observed",
				"leader_identity", identity,
				"is_self", identity == deps.podName,
			)
		},
	}

	return callbacks, state
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
	iterCtx context.Context,
	cfg *coreconfig.Config,
	webhookCerts *WebhookCertificates,
	k8sClient *client.Client,
	bus *busevents.EventBus,
	dryrunValidator *dryrunvalidator.Component, // Pre-created validator (may be nil)
	logger *slog.Logger,
	metricsRecorder webhook.MetricsRecorder,
	cancel context.CancelFunc,
) {
	// Extract webhook rules from config
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 {
		logger.Debug("No webhook rules extracted (webhook enabled but no matching resources)")
		return
	}

	logger.Info("Webhook validation enabled",
		"rule_count", len(rules))

	// Start DryRunValidator if provided (created before EventBus.Start())
	if dryrunValidator != nil {
		go func() {
			if err := dryrunValidator.Start(iterCtx); err != nil {
				logger.Error("dry-run validator failed", "error", err)
				cancel()
			}
		}()
		logger.Info("Dry-run validator started")
	}

	// Create RESTMapper for resolving resource kinds from GVR
	// This uses the Kubernetes API discovery to get authoritative mappings
	logger.Debug("Creating RESTMapper for resource kind resolution")
	discoveryClient := k8sClient.Clientset().Discovery()
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(discoveryClient),
	)

	// Create webhook component with certificate data from Kubernetes API
	// Certificates are fetched from Secret via Kubernetes API and passed directly to component
	webhookComponent := webhook.New(
		bus,
		logger,
		&webhook.Config{
			Port:    9443, // Default webhook port
			Path:    "/validate",
			Rules:   rules,
			CertPEM: webhookCerts.CertPEM,
			KeyPEM:  webhookCerts.KeyPEM,
		},
		mapper,
		metricsRecorder,
	)

	// Start webhook component in background
	go func() {
		if err := webhookComponent.Start(iterCtx); err != nil {
			logger.Error("webhook component failed", "error", err)
			cancel()
		}
	}()

	logger.Info("Webhook component started")
}

// createDryRunValidator creates a DryRunValidator component for webhook validation.
//
// This function is called BEFORE EventBus.Start() to ensure the validator subscribes
// to events before any buffered events are released.
//
// Returns (nil, nil) if webhook rules are empty (no resources to validate).
// Returns (nil, error) if engine creation fails.
func createDryRunValidator(
	cfg *coreconfig.Config,
	bus *busevents.EventBus,
	storeManager *resourcestore.Manager,
	capabilities dataplane.Capabilities,
	logger *slog.Logger,
) (*dryrunvalidator.Component, error) {
	// Check if there are any webhook rules - if not, no validator needed
	rules := webhook.ExtractWebhookRules(cfg)
	if len(rules) == 0 {
		logger.Debug("No webhook rules extracted, skipping DryRunValidator creation")
		return nil, errNoWebhookRules
	}

	logger.Debug("Creating DryRunValidator for webhook validation")

	// Create template engine using helper (handles template extraction, filters, engine type parsing)
	engine, err := helpers.NewEngineFromConfig(cfg, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create template engine for dry-run validation: %w", err)
	}

	// Create validation paths
	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           cfg.Dataplane.MapsDir,
		SSLCertsDir:       cfg.Dataplane.SSLCertsDir,
		GeneralStorageDir: cfg.Dataplane.GeneralStorageDir,
		ConfigFile:        cfg.Dataplane.ConfigFile,
	}

	// Create DryRunValidator (subscribes in constructor)
	return dryrunvalidator.New(bus, storeManager, cfg, engine, validationPaths, capabilities, logger), nil
}

// setupReconciliation creates and starts the reconciliation components (Stage 5).
//
// The Reconciler debounces resource changes and triggers reconciliation events.
// The Renderer subscribes to reconciliation events and renders HAProxy configuration.
// The HAProxyValidator validates rendered configurations using syntax and semantic checks.
// The Executor subscribes to reconciliation events and orchestrates pure components
// (Renderer, Validator, Deployer) to perform the reconciliation workflow.
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
	bus *busevents.EventBus,
	registry *lifecycle.Registry,
	logger *slog.Logger,
	cancel context.CancelFunc,
) (*reconciliationComponents, error) {
	// Create all components
	components, err := createReconciliationComponents(cfg, k8sClient, resourceWatcher, bus, registry, logger)
	if err != nil {
		return nil, err
	}

	// Start all-replica components in background
	// Leader-only components (Deployer, DeploymentScheduler, ConfigPublisher) are NOT started here
	// Note: Components already subscribed during construction, so they're ready to receive events
	startReconciliationComponents(iterCtx, registry, logger, cancel)

	// Publish initial config and credentials events
	// These events are buffered by EventBus until Start() is called in the main controller loop
	// This ensures reconciliation components (especially Discovery) receive the initial state
	// even though they were created after the initial ConfigMap/Secret watcher events
	// Note: We pass the actual CRD (not nil) so ConfigPublisher can cache it for creating HAProxyCfg resources
	bus.Publish(events.NewConfigValidatedEvent(cfg, crd, "initial", "initial"))
	logger.Debug("Published initial ConfigValidatedEvent (buffered until EventBus.Start())")

	bus.Publish(events.NewCredentialsUpdatedEvent(creds, "initial"))
	logger.Debug("Published initial CredentialsUpdatedEvent (buffered until EventBus.Start())")

	// Trigger initial reconciliation to bootstrap the pipeline
	// This ensures at least one reconciliation cycle runs even with 0 resources
	// A new correlation ID is generated to trace this initial reconciliation cycle
	initialReconciliation := events.NewReconciliationTriggeredEvent("initial_sync_complete", events.WithNewCorrelation())
	bus.Publish(initialReconciliation)
	logger.Debug("Published initial reconciliation trigger (buffered until EventBus.Start())",
		"correlation_id", initialReconciliation.CorrelationID())

	return components, nil
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
		components: startLeaderOnlyComponents(iterCtx, reconComponents, registry, logger, cancel),
	}
	return state
}

// runIteration runs a single controller iteration.
//
// This function orchestrates the initialization sequence:
//  1. Fetches and validates initial ConfigMap and Secret
//  2. Creates and starts all event-driven components
//  3. Creates and starts resource watchers, waits for sync
//  4. Creates and starts ConfigMap/Secret watchers, waits for sync
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
	logger *slog.Logger,
) error {
	logger.Info("Starting controller iteration")

	// Define GVRs for HAProxyTemplateConfig CRD and Secret
	crdGVR := schema.GroupVersionResource{
		Group:    "haproxy-template-ic.gitlab.io",
		Version:  "v1alpha1",
		Resource: "haproxytemplateconfigs",
	}

	secretGVR := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	// Create config state tracker for health checks
	// This allows the health endpoint to report unhealthy status until config is loaded
	state := &configState{}

	// 0. Setup components BEFORE fetching config so we can start servers early
	setup := setupComponents(ctx, logger)
	defer setup.Cancel()

	// 0.25. Create EventBuffer early (subscribes in constructor)
	// Must be created before startEarlyInfrastructureServers() to register /debug/events handler
	// and before EventBus.Start() to ensure proper subscription ordering
	eventBuffer := debug.NewEventBuffer(DebugEventBufferSize, setup.Bus)

	// 0.5. Start infrastructure servers EARLY (before config fetch)
	// This allows debugging startup issues and makes health endpoint available immediately
	// The health checker will report unhealthy until config is loaded
	// Uses two-phase initialization (Setup/Serve) to register /debug/events before serving
	startEarlyInfrastructureServers(setup.IterCtx, debugPort, setup, state, eventBuffer, logger)

	// 1. Wait for HAProxyTemplateConfig to exist (polls every 5s)
	// This handles the race condition during fresh installs where the controller pod
	// may start before the HAProxyTemplateConfig CR is fully available
	if err := waitForInitialConfig(ctx, k8sClient, crdName, crdGVR, state, logger); err != nil {
		return err
	}

	// 2. Fetch and validate initial configuration (now guaranteed to exist)
	cfg, crd, creds, webhookCerts, err := fetchAndValidateInitialConfig(
		ctx, k8sClient, crdName, secretName, webhookCertSecretName,
		crdGVR, secretGVR, logger,
	)
	if err != nil {
		return err
	}

	// Mark config as loaded and set initial version for bootstrap loop prevention
	finalizeConfigLoad(state, setup, crd.GetResourceVersion())

	// 3. Setup resource watchers
	resourceWatcher, err := setupResourceWatchers(setup.IterCtx, cfg, k8sClient, setup.Bus, logger, setup.Cancel)
	if err != nil {
		return err
	}

	// Register stores with ResourceStoreManager for webhook validation
	registerResourceStores(resourceWatcher, setup.StoreManager, logger)

	// 4. Setup config watchers
	if err := setupConfigWatchers(
		setup.IterCtx, k8sClient, crdName, secretName,
		crdGVR, secretGVR, setup.Bus, logger, setup.Cancel,
	); err != nil {
		return err
	}

	// 5. Initialize StateCache and start background components
	stateCache := NewStateCache(setup.Bus, resourceWatcher, logger)
	startBackgroundComponents(setup.IterCtx, stateCache, setup.MetricsComponent, logger)

	// 6. Create reconciliation components (Stage 5)
	// Components subscribe during construction, before EventBus.Start()
	logger.Info("Stage 5: Creating reconciliation components")
	reconComponents, err := setupReconciliation(setup.IterCtx, cfg, crd, creds, k8sClient, resourceWatcher, setup.Bus, setup.Registry, logger, setup.Cancel)
	if err != nil {
		return err
	}

	// 6.1. EventBuffer was already created early (step 0.25) for /debug/events handler
	// It subscribes in constructor before EventBus.Start() for proper subscription ordering

	// 6.2. Create DryRunValidator for webhook validation (subscribes in constructor)
	// Must be created before EventBus.Start() to ensure proper subscription
	var dryrunValidator *dryrunvalidator.Component
	if webhook.HasWebhookEnabled(cfg) {
		var err error
		dryrunValidator, err = createDryRunValidator(cfg, setup.Bus, setup.StoreManager, reconComponents.capabilities, logger)
		if err != nil && !errors.Is(err, errNoWebhookRules) {
			return fmt.Errorf("failed to create dry-run validator: %w", err)
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
	if webhook.HasWebhookEnabled(cfg) {
		logger.Info("Stage 7: Setting up webhook validation")
		setupWebhook(setup.IterCtx, cfg, webhookCerts, k8sClient, setup.Bus, dryrunValidator, logger, setup.MetricsComponent.Metrics(), setup.Cancel)
	}

	// 9. Setup debug and metrics infrastructure (start pre-created EventBuffer)
	// Note: The introspection server is already started by startEarlyInfrastructureServers
	// This call registers debug variables and updates the health checker
	setupInfrastructureServers(setup.IterCtx, cfg, setup, stateCache, eventBuffer, logger)

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

	logger.Info("Reinitialization triggered - starting new iteration")
}

// registerResourceStores registers all resource stores from the watcher with the store manager.
func registerResourceStores(
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	storeManager *resourcestore.Manager,
	logger *slog.Logger,
) {
	logger.Debug("Registering resource stores with ResourceStoreManager")
	stores := resourceWatcher.GetAllStores()
	for resourceType, store := range stores {
		storeManager.RegisterStore(resourceType, store)
		logger.Debug("Registered store", "resource_type", resourceType)
	}
}

// startBackgroundComponents starts the StateCache and metrics component in background goroutines.
// These components subscribe immediately and wait for events. Errors are logged but non-fatal.
func startBackgroundComponents(
	ctx context.Context,
	stateCache *StateCache,
	metricsComponent *metrics.Component,
	logger *slog.Logger,
) {
	go func() {
		if err := stateCache.Start(ctx); err != nil {
			logger.Error("state cache failed", "error", err)
		}
	}()

	go func() {
		if err := metricsComponent.Start(ctx); err != nil {
			logger.Error("metrics component failed", "error", err)
		}
	}()
}

// waitForGoroutinesToFinish waits for all goroutines in errgroup to finish with a timeout.
// This is CRITICAL for lease release - elector needs time to call ReleaseOnCancel.
func waitForGoroutinesToFinish(errGroup *errgroup.Group, logger *slog.Logger, prefix string) {
	logger.Info(fmt.Sprintf("Waiting for goroutines to finish %s...", strings.ToLower(prefix)))

	done := make(chan error, 1)
	go func() {
		done <- errGroup.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			logger.Warn(fmt.Sprintf("Goroutines finished with error during %s", strings.ToLower(prefix)), "error", err)
		} else {
			logger.Info("All goroutines finished gracefully")
		}
	case <-time.After(30 * time.Second):
		logger.Warn(fmt.Sprintf("%s timeout exceeded (30s) - some goroutines may not have finished", prefix))
	}
}

// parseCRD extracts and converts configuration from a HAProxyTemplateConfig CRD resource.
func parseCRD(resource *unstructured.Unstructured) (*coreconfig.Config, *v1alpha1.HAProxyTemplateConfig, error) {
	return conversion.ParseCRD(resource)
}

// parseSecret extracts and parses credentials from a Secret resource.
func parseSecret(resource *unstructured.Unstructured) (*coreconfig.Credentials, error) {
	// Extract Secret data field
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("failed to extract data field: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("secret has no data field")
	}

	// Convert map[string]interface{} to map[string][]byte
	// Kubernetes Secrets are base64-encoded, so we need to decode them
	data := make(map[string][]byte)
	for key, value := range dataRaw {
		if strValue, ok := value.(string); ok {
			// Decode base64-encoded value
			decoded, err := base64.StdEncoding.DecodeString(strValue)
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 for key %q: %w", key, err)
			}
			data[key] = decoded
		} else {
			return nil, fmt.Errorf("secret data key %q has invalid type: %T", key, value)
		}
	}

	// Load credentials
	creds, err := coreconfig.LoadCredentials(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}

	return creds, nil
}

// parseWebhookCertSecret extracts and decodes webhook TLS certificate data from a Secret.
func parseWebhookCertSecret(resource *unstructured.Unstructured) (*WebhookCertificates, error) {
	// Extract Secret data field
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("failed to extract data field: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("secret has no data field")
	}

	// Extract tls.crt (standard Kubernetes TLS Secret key)
	tlsCertBase64, ok := dataRaw["tls.crt"]
	if !ok {
		return nil, fmt.Errorf("secret data missing 'tls.crt' key")
	}

	// Extract tls.key (standard Kubernetes TLS Secret key)
	tlsKeyBase64, ok := dataRaw["tls.key"]
	if !ok {
		return nil, fmt.Errorf("secret data missing 'tls.key' key")
	}

	// Decode base64 certificate
	var certPEM []byte
	if strValue, ok := tlsCertBase64.(string); ok {
		certPEM, err = base64.StdEncoding.DecodeString(strValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 tls.crt: %w", err)
		}
	} else {
		return nil, fmt.Errorf("tls.crt has invalid type: %T", tlsCertBase64)
	}

	// Decode base64 private key
	var keyPEM []byte
	if strValue, ok := tlsKeyBase64.(string); ok {
		keyPEM, err = base64.StdEncoding.DecodeString(strValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 tls.key: %w", err)
		}
	} else {
		return nil, fmt.Errorf("tls.key has invalid type: %T", tlsKeyBase64)
	}

	// Validate we have non-empty data
	if len(certPEM) == 0 {
		return nil, fmt.Errorf("tls.crt is empty")
	}
	if len(keyPEM) == 0 {
		return nil, fmt.Errorf("tls.key is empty")
	}

	return &WebhookCertificates{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		Version: resource.GetResourceVersion(),
	}, nil
}

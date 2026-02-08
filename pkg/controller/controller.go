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
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/commentator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configloader"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/credentialsloader"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// RetryDelay is the duration to wait before retrying after an iteration failure.
	RetryDelay = 5 * time.Second
	// ConfigPollInterval is the interval for polling HAProxyTemplateConfig availability.
	ConfigPollInterval = 5 * time.Second
	// DebugEventBufferSize is the size of the event buffer for debug/introspection.
	DebugEventBufferSize = 1000
	// ShutdownTimeout is the maximum time to wait for goroutines to finish during shutdown.
	// Set to 25s to allow clean exit before Kubernetes' default 30s terminationGracePeriodSeconds.
	ShutdownTimeout = 25 * time.Second
	// ShutdownProgressInterval is how often to log progress during shutdown.
	ShutdownProgressInterval = 5 * time.Second
)

// errNoWebhookRules indicates that no webhook rules are configured.
// This is used to signal that DryRunValidator should not be created.
var errNoWebhookRules = errors.New("no webhook rules configured")

// GVRs for Kubernetes resources used by the controller.
var (
	// crdGVR is the GVR for HAProxyTemplateConfig custom resource.
	crdGVR = schema.GroupVersionResource{
		Group:    "haproxy-haptic.org",
		Version:  "v1alpha1",
		Resource: "haproxytemplateconfigs",
	}
	// secretGVR is the GVR for Kubernetes Secrets.
	secretGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}
	// haproxyCfgGVR is the GVR for HAProxyCfg custom resource.
	haproxyCfgGVR = schema.GroupVersionResource{
		Group:    "haproxy-haptic.org",
		Version:  "v1alpha1",
		Resource: "haproxycfgs",
	}
)

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

// persistentInfra holds infrastructure servers that persist across controller iterations.
// These servers are started once and reused to prevent port binding race conditions
// during rapid reinitializations.
type persistentInfra struct {
	IntrospectionRegistry *introspection.Registry
	IntrospectionServer   *introspection.Server
	serverStarted         bool // True after first iteration has started the server
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

	logger.Debug("HAProxy Template Ingress Controller starting",
		"crd_name", crdName,
		"secret", secretName,
		"webhook_cert_secret", webhookCertSecretName,
		"namespace", k8sClient.Namespace())

	// Create persistent infrastructure (lives across iterations)
	// This prevents port binding race conditions during rapid reinitializations
	infra := &persistentInfra{
		IntrospectionRegistry: introspection.NewRegistry(),
	}

	// Create and start the introspection server once, before the loop
	// The server will be reused across iterations with the registry cleared between them
	if debugPort > 0 {
		infra.IntrospectionServer = introspection.NewServer(fmt.Sprintf(":%d", debugPort), infra.IntrospectionRegistry)
		// Note: Setup() and Serve() will be called in startEarlyInfrastructureServers
		// on the first iteration only
	}

	// Main reinitialization loop
	for {
		select {
		case <-ctx.Done():
			logger.Info("Controller shutting down", "reason", ctx.Err())
			return nil
		default:
			// Run one iteration
			err := runIteration(ctx, k8sClient, crdName, secretName, webhookCertSecretName, debugPort, infra, logger)
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
// The introspectionRegistry is passed in from the persistent infrastructure
// to avoid recreating it on each iteration (which would require rebinding the port).
func setupComponents(
	ctx context.Context,
	introspectionRegistry *introspection.Registry,
	logger *slog.Logger,
) *componentSetup {
	// Create EventBus with buffer for pre-start events
	bus := busevents.NewEventBus(100)

	// Create Prometheus registry for this iteration (instance-based, not global)
	registry := prometheus.NewRegistry()

	// Create metrics collector
	domainMetrics := metrics.NewMetrics(registry)
	metricsComponent := metrics.New(domainMetrics, bus)

	// Register event drop callback for observability
	bus.SetDropCallback(func(info busevents.DropInfo) {
		logger.Warn("event dropped due to full subscriber buffer",
			"subscriber", info.SubscriberName,
			"event_type", info.EventType,
			"buffer_size", info.BufferSize,
			"subscribed_types", info.EventTypes,
		)
		domainMetrics.RecordEventDrop(info.SubscriberName, info.EventType)
	})

	// Create ResourceStoreManager for webhook validation
	storeManager := resourcestore.NewManager()

	// Create components
	eventCommentator := commentator.NewEventCommentator(bus, logger, 500)
	configLoaderComponent := configloader.NewConfigLoaderComponent(bus, logger)
	credentialsLoaderComponent := credentialsloader.NewCredentialsLoaderComponent(bus, logger)

	// Create config validators (for ConfigMap validation)
	basicValidator := validator.NewBasicValidator(bus, logger)
	templateValidator := validator.NewTemplateValidator(bus, logger)
	jsonpathValidator := validator.NewJSONPathValidator(bus, logger)

	// Create config change channel for reinitialization signaling
	configChangeCh := make(chan *coreconfig.Config, 1)

	// Register validators for scatter-gather validation
	validators := validator.AllValidatorNames()

	configChangeHandlerComponent := configchange.NewConfigChangeHandler(
		bus,
		logger,
		configChangeCh,
		validators,
		0, // Use default debounce interval (5s)
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
		if err := configChangeHandlerComponent.Start(gCtx); err != nil {
			logger.Error("config change handler failed", "error", err)
			cancel()
			return err
		}
		return nil
	})

	logger.Debug("All components started")

	// Note: introspection registry is passed in from persistent infrastructure
	// to avoid port rebinding issues during rapid reinitializations

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

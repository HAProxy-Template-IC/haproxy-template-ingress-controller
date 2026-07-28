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
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/commentator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configloader"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/credentialsloader"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	pkgmetrics "gitlab.com/haproxy-haptic/haptic/pkg/metrics"
)

const (
	// RetryDelay is the duration to wait before retrying after an iteration failure.
	RetryDelay = 5 * time.Second
	// ConfigPollInterval is the interval for polling HAProxyTemplateConfig availability.
	ConfigPollInterval = 5 * time.Second
	// DebugEventBufferSize is the size of the event buffer for debug/introspection.
	DebugEventBufferSize = busevents.DebugSubscriberBuffer
	// ShutdownTimeout is the maximum time to wait for goroutines to finish during shutdown.
	// Set to 25s to allow clean exit before Kubernetes' default 30s terminationGracePeriodSeconds.
	ShutdownTimeout = 25 * time.Second
	// ShutdownProgressInterval is how often to log progress during shutdown.
	ShutdownProgressInterval = 5 * time.Second
)

// buildVersionInfo holds build-time version information exposed via haptic_build_info metric.
// Set this before calling Run() using SetBuildInfo().
var buildVersionInfo struct {
	version        string
	haproxyVersion string
	goVersion      string
}

func init() {
	buildVersionInfo.version = "dev"
	buildVersionInfo.haproxyVersion = "unknown"
	buildVersionInfo.goVersion = runtime.Version()
}

// SetBuildInfo configures version information exposed via the haptic_build_info Prometheus metric.
// Must be called before Run() to take effect.
//
// Parameters:
//   - version: Controller version (e.g., "0.1.0-alpha.10")
//   - haproxyVersion: HAProxy version the controller was built for (e.g., "3.2")
func SetBuildInfo(version, haproxyVersion string) {
	buildVersionInfo.version = version
	buildVersionInfo.haproxyVersion = haproxyVersion
}

// GVRs for Kubernetes resources used by the controller.
const (
	// hapticAPIGroup and hapticAPIVersion identify HAPTIC's own CRDs.
	hapticAPIGroup   = "haproxy-haptic.org"
	hapticAPIVersion = "v1alpha1"
)

var (
	// crdGVR is the GVR for HAProxyTemplateConfig custom resource.
	crdGVR = schema.GroupVersionResource{
		Group:    hapticAPIGroup,
		Version:  hapticAPIVersion,
		Resource: "haproxytemplateconfigs",
	}
	// validationTestsGVR is the GVR for HAProxyValidationTests, HAPTIC's own
	// kind carrying the suite the load gate runs.
	validationTestsGVR = schema.GroupVersionResource{
		Group:    hapticAPIGroup,
		Version:  hapticAPIVersion,
		Resource: "haproxyvalidationtests",
	}
	// secretGVR is the GVR for Kubernetes Secrets.
	secretGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}
	// haproxyCfgGVR is the GVR for HAProxyCfg custom resource.
	haproxyCfgGVR = schema.GroupVersionResource{
		Group:    hapticAPIGroup,
		Version:  hapticAPIVersion,
		Resource: "haproxycfgs",
	}
	// haproxyMapFileGVR, haproxyGeneralFileGVR, and haproxyCRTListFileGVR are the
	// GVRs for HAPTIC's own published auxiliary-file CRDs, read back to prime
	// `currentFiles` on a cold iteration.
	haproxyMapFileGVR     = v1alpha1.SchemeGroupVersion.WithResource("haproxymapfiles")
	haproxyGeneralFileGVR = v1alpha1.SchemeGroupVersion.WithResource("haproxygeneralfiles")
	haproxyCRTListFileGVR = v1alpha1.SchemeGroupVersion.WithResource("haproxycrtlistfiles")
)

// configState tracks initialization state for health checks.
// It allows the health endpoint to report unhealthy status until
// the HAProxyTemplateConfig is successfully loaded AND the staged
// startup (resource watchers, event bus, reconciliation components,
// leader election, webhook, debug servers) has finished. /healthz
// returns 503 until both gates pass, then 200 — operators (and the
// e2e suite) get a single "is the controller ready to accept work"
// signal without polling internal pipeline state that's wiped on
// every reconciliation trigger.
type configState struct {
	mu           sync.RWMutex
	configLoaded bool
	initialized  bool
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

// SetInitialized marks the controller as having finished its staged
// startup. Called once at the end of runIteration, right before the
// event loop. After this flips true, /healthz can report 200 (subject
// to the rest of the lifecycle.Registry components being healthy too).
func (s *configState) SetInitialized() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = true
}

// IsLoaded returns true if the config has been successfully loaded.
func (s *configState) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configLoaded
}

// IsInitialized returns true if the controller has finished its
// staged startup. Reset implicitly when a new iteration starts because
// configState is constructed fresh per runIteration call.
func (s *configState) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
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
	MetricsServer         *pkgmetrics.Server
	serverStarted         bool // True after first iteration has started the server
	metricsServerStarted  bool // True after first iteration has started the metrics server

	// Reinitialization grace tracking. A VOLUNTARY iteration restart (config
	// change, CRD change) tears down and rebuilds every component; during
	// that window the per-iteration health state reports unhealthy even
	// though the process is doing exactly what it should. Without a grace
	// window, any reinit longer than the liveness budget (~30s — and the
	// config's embedded validationTests alone can take a large share of
	// that) gets the container killed mid-reinit. The grace applies ONLY
	// after the controller has been fully initialized at least once, so the
	// fail-closed startup contract is untouched: a fresh pod with a bad
	// config still crash-loops, and a reinit that can't complete within the
	// grace window goes unhealthy and gets restarted into that same
	// fail-closed startup path.
	graceMu            sync.Mutex
	everInitialized    bool
	iterationStartedAt time.Time
	settledThisIter    bool
}

// ReinitGraceWindow is how long after a voluntary iteration restart /healthz
// keeps reporting healthy while components re-initialize. Sized to cover a
// slow reinit (embedded validationTests + watcher re-sync + leader
// re-acquisition) while staying well below the fresh-pod startup budget the
// liveness restart would fall back to.
const ReinitGraceWindow = 90 * time.Second

// NoteIterationStart records the beginning of an iteration for reinit-grace
// accounting.
func (p *persistentInfra) NoteIterationStart() {
	p.graceMu.Lock()
	defer p.graceMu.Unlock()
	p.iterationStartedAt = time.Now()
	p.settledThisIter = false
}

// NoteInitialized records that an iteration completed its staged startup.
func (p *persistentInfra) NoteInitialized() {
	p.graceMu.Lock()
	defer p.graceMu.Unlock()
	p.everInitialized = true
}

// NoteSettled records that the current iteration has been observed fully
// healthy at least once. From that point on the grace no longer applies —
// an unhealthy entry AFTER settling is a genuine failure, not rebuild
// churn, and must surface immediately instead of being masked for the
// remainder of the window.
func (p *persistentInfra) NoteSettled() {
	p.graceMu.Lock()
	defer p.graceMu.Unlock()
	p.settledThisIter = true
}

// InReinitGrace reports whether unhealthy health entries should be tolerated
// because a voluntary reinitialization is (recently) in progress. The grace
// ends at the EARLIER of: the window expiring, or the iteration settling
// (fully healthy once — see NoteSettled).
func (p *persistentInfra) InReinitGrace() bool {
	p.graceMu.Lock()
	defer p.graceMu.Unlock()
	return p.everInitialized && !p.settledThisIter && time.Since(p.iterationStartedAt) < ReinitGraceWindow
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
//   - crdNames: Names of the HAProxyTemplateConfigs to merge, in merge order
//     (later wins); the last one is the primary, see primaryConfigName
//   - secretName: Name of the Secret containing HAProxy Dataplane API credentials
//   - webhookCertDir: Directory holding the webhook TLS cert (tls.crt/tls.key); empty disables the webhook
//   - webhookAdmissionTimeouts: Controller-side admission deadlines. Zero
//     values use the webhook component defaults.
//   - debugPort: Port for debug HTTP server (0 to disable)
//
// Returns:
//   - Error if the controller cannot start or encounters a fatal error
//   - nil if the context is cancelled (graceful shutdown)
func Run(
	ctx context.Context,
	k8sClient *client.Client,
	crdNames []string,
	secretName, webhookCertDir string,
	webhookAdmissionTimeouts WebhookAdmissionTimeouts,
	debugPort int,
) error {
	logger := slog.Default()

	logger.Debug("HAProxy Template Ingress Controller starting",
		"crd_names", crdNames,
		"secret", secretName,
		"webhook_cert_dir", webhookCertDir,
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

	// Create the metrics server once, before the loop.
	// The registry will be swapped via SetRegistry() on each iteration.
	metricsPort, err := listenerPortFromEnv("METRICS_PORT", 9090, true)
	if err != nil {
		return err
	}
	if metricsPort > 0 {
		infra.MetricsServer = pkgmetrics.NewServer(fmt.Sprintf(":%d", metricsPort), prometheus.NewRegistry())
	}
	webhookPort, err := listenerPortFromEnv("WEBHOOK_PORT", 9443, false)
	if err != nil {
		return err
	}

	// Main reinitialization loop
	for {
		select {
		case <-ctx.Done():
			logger.Info("Controller shutting down", "reason", ctx.Err())
			return nil
		default:
			// Run one iteration
			err := runIteration(ctx, k8sClient, crdNames, secretName, webhookCertDir, webhookAdmissionTimeouts, debugPort, webhookPort, infra, logger)
			if err != nil {
				// Check if error is context cancellation (graceful shutdown)
				if ctx.Err() != nil {
					logger.Info("Controller shutting down during iteration", "reason", ctx.Err())
					return nil // Graceful shutdown is not an error
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

func listenerPortFromEnv(envName string, defaultPort int, allowDisabled bool) (int, error) {
	raw := os.Getenv(envName)
	if raw == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing %s=%q as a TCP port: %w", envName, raw, err)
	}
	minimum := 1
	if allowDisabled {
		minimum = 0
	}
	if port < minimum || port > 65535 {
		return 0, fmt.Errorf("%s must be between %d and 65535", envName, minimum)
	}
	return port, nil
}

// componentSetup contains all resources created during component initialization.
type componentSetup struct {
	Bus                   *busevents.EventBus
	Registry              *lifecycle.Registry // Component lifecycle registry
	MetricsComponent      *metrics.Component
	MetricsRegistry       *prometheus.Registry
	IntrospectionRegistry *introspection.Registry
	IntrospectionServer   *introspection.Server             // Server reference for custom handler registration
	ConfigChangeHandler   *configchange.ConfigChangeHandler // For setting initial config version
	IterCtx               context.Context
	Cancel                context.CancelFunc
	ConfigChangeCh        chan *coreconfig.Config
	ErrGroup              *errgroup.Group // Tracks all background goroutines for graceful shutdown

	// cleanups holds tear-down callbacks registered by helpers
	// during setup. RunCleanups invokes them in reverse-registration
	// order on iteration exit (mirrors `defer` semantics) so e.g. a
	// pluggable-validator manager's connection pools get drained
	// before the iteration context goes away. Mutex-guarded because
	// registration may happen from multiple stages of setup.
	cleanupsMu sync.Mutex
	cleanups   []func()
}

// AddCleanup registers a callback to run on iteration teardown.
// Callbacks fire in reverse-registration order (LIFO), mirroring
// `defer` semantics. Safe for concurrent registration.
func (s *componentSetup) AddCleanup(fn func()) {
	if fn == nil {
		return
	}
	s.cleanupsMu.Lock()
	s.cleanups = append(s.cleanups, fn)
	s.cleanupsMu.Unlock()
}

// RunCleanups invokes registered cleanup callbacks in reverse order.
// Idempotent — calling a second time is a no-op (cleanups have
// already drained).
func (s *componentSetup) RunCleanups() {
	s.cleanupsMu.Lock()
	cleanups := s.cleanups
	s.cleanups = nil
	s.cleanupsMu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

// setupComponents creates and starts all event-driven components.
// The introspectionRegistry is passed in from the persistent infrastructure
// to avoid recreating it on each iteration (which would require rebinding the port).
//
// typeBootstrapper is the closure the Stage-1 TemplateValidator
// uses to resolve real reflect.Types for the request's
// watchedResources during config validation. Production passes a
// closure around runTypeBootstrap with the iteration's K8s
// client; the closure must be non-nil. See
// pkg/controller/validator.TypeBootstrapper for the contract.
func setupComponents(
	ctx context.Context,
	introspectionRegistry *introspection.Registry,
	typeBootstrapper validator.TypeBootstrapper,
	crdNames []string,
	resolveTests configloader.ValidationTestResolver,
	logger *slog.Logger,
) *componentSetup {
	logger.Info("Stage 1: Creating config management components")

	// Create EventBus with buffer for pre-start events
	bus := busevents.NewEventBus(100)

	// Create Prometheus registry for this iteration (instance-based, not global)
	registry := prometheus.NewRegistry()

	// Create metrics collector
	domainMetrics := metrics.NewMetrics(registry)
	domainMetrics.SetBuildInfo(buildVersionInfo.version, buildVersionInfo.haproxyVersion, buildVersionInfo.goVersion)
	metricsComponent := metrics.New(domainMetrics, bus)

	// Register event drop callback for observability
	bus.SetDropCallback(func(info busevents.DropInfo) {
		logger.Warn("Event dropped due to full subscriber buffer",
			"subscriber", info.SubscriberName,
			"event_type", info.EventType,
			"buffer_size", info.BufferSize,
			"subscribed_types", info.EventTypes,
		)
		domainMetrics.RecordEventDrop(info.SubscriberName, info.EventType)
	})

	// Create components
	eventCommentator := commentator.NewEventCommentator(bus, logger, 500)
	configLoaderComponent := configloader.NewConfigLoaderComponent(bus, crdNames, resolveTests, logger)
	credentialsLoaderComponent := credentialsloader.NewCredentialsLoaderComponent(bus, logger)

	// Create config validators (scatter-gather responders for HAProxyTemplateConfig CRD validation)
	basicValidator := validator.NewBasicValidator(bus, logger)
	templateValidator := validator.NewTemplateValidator(bus, logger, typeBootstrapper)
	jsonpathValidator := validator.NewJSONPathValidator(bus, logger)
	// Runs the config's embedded validationTests before the config is accepted,
	// so the daemon never loads (at startup or on a live change) a config whose
	// tests fail — matching the guarantee of the `controller validate` CLI.
	validationTestsValidator := validator.NewValidationTestsValidator(bus, logger, typeBootstrapper)

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
	startInErrGroup(g, gCtx, logger, cancel, "event commentator", eventCommentator.Start)
	startInErrGroup(g, gCtx, logger, cancel, "config loader", configLoaderComponent.Start)
	startInErrGroup(g, gCtx, logger, cancel, "credentials loader", credentialsLoaderComponent.Start)
	startInErrGroup(g, gCtx, logger, cancel, "basic validator", basicValidator.Start)
	startInErrGroup(g, gCtx, logger, cancel, "template validator", templateValidator.Start)
	startInErrGroup(g, gCtx, logger, cancel, "jsonpath validator", jsonpathValidator.Start)
	startInErrGroup(g, gCtx, logger, cancel, "validationtests validator", validationTestsValidator.Start)
	startInErrGroup(g, gCtx, logger, cancel, "config change handler", configChangeHandlerComponent.Start)

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
		ConfigChangeHandler:   configChangeHandlerComponent,
		IterCtx:               gCtx, // Use errgroup context so cancellation propagates
		Cancel:                cancel,
		ConfigChangeCh:        configChangeCh,
		ErrGroup:              g,
	}
}

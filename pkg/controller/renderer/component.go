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

// Package renderer implements the Renderer component that renders HAProxy
// configuration and auxiliary files from templates.
//
// The Renderer is a Stage 5 component that subscribes to reconciliation
// trigger events, builds rendering context from resource stores, and publishes
// rendered output events for the next phase (validation/deployment).
package renderer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "renderer"
)

// singleRenderResult holds the output of a single render (production or validation).
type singleRenderResult struct {
	haproxyConfig  string
	auxiliaryFiles *dataplane.AuxiliaryFiles
	durationMs     int64
}

// Component implements the renderer component.
//
// It subscribes to ReconciliationTriggeredEvent, renders all templates using
// the template engine and resource stores, and publishes the results via
// TemplateRenderedEvent or TemplateRenderFailedEvent.
//
// The component renders configurations twice per reconciliation:
// 1. Production version with absolute paths for HAProxy pods (/etc/haproxy/*)
// 2. Validation version with temp directory paths for controller validation
//
// This is a leader-only component that starts when leadership is acquired.
// The Reconciler triggers a fresh reconciliation on BecameLeaderEvent to provide current state.
//
// CRT-list Fallback:
// The component determines CRT-list storage capability from the local HAProxy version
// (passed at construction time). When CRT-list storage is not supported (HAProxy < 3.2),
// CRT-list file paths are resolved to the general files directory instead of the SSL
// directory, ensuring the generated configuration matches where files are actually stored.
type Component struct {
	eventBus           *busevents.EventBus
	eventChan          <-chan busevents.Event // Subscribed in Start() for leader-only pattern
	engine             templating.Engine
	config             *config.Config
	stores             map[string]types.Store
	haproxyPodStore    types.Store               // HAProxy controller pods store for pod-maxconn calculations
	httpStoreComponent *httpstore.Component      // HTTP resource store for dynamic HTTP content fetching
	currentConfigStore *currentconfigstore.Store // Current deployed HAProxy config for slot preservation
	logger             *slog.Logger
	ctx                context.Context // Context from Start() for HTTP requests

	// capabilities defines which features are available for the local HAProxy version.
	// Determined from local HAProxy version at construction time via CapabilitiesFromVersion().
	// When capabilities.SupportsCrtList is false, CRT-list paths resolve to general files directory.
	capabilities dataplane.Capabilities

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}
}

// New creates a new Renderer component.
//
// The component pre-compiles all templates during initialization for optimal
// runtime performance.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - config: Controller configuration containing templates
//   - stores: Map of resource type names to their stores (e.g., "ingresses" -> Store)
//   - haproxyPodStore: Store containing HAProxy controller pods for pod-maxconn calculations
//   - currentConfigStore: Store containing the current deployed HAProxy config (for slot preservation)
//   - capabilities: HAProxy capabilities determined from local version
//   - logger: Structured logger for component logging
//
// Returns:
//   - A new Component instance ready to be started
//   - Error if template compilation fails
func New(
	eventBus *busevents.EventBus,
	cfg *config.Config,
	stores map[string]types.Store,
	haproxyPodStore types.Store,
	currentConfigStore *currentconfigstore.Store,
	capabilities dataplane.Capabilities,
	logger *slog.Logger,
) (*Component, error) {
	// Log stores received during initialization
	logger.Debug("Creating renderer component",
		"store_count", len(stores))
	for resourceTypeName := range stores {
		logger.Debug("Renderer received store",
			"resource_type", resourceTypeName)
	}

	// Create template engine using helper (handles template extraction, filters, engine type parsing)
	// Note: The fail() function is auto-registered by the Scriggo engine.
	// Post-processor configs are automatically extracted from cfg when nil is passed.
	//
	// Register domain-specific types for Scriggo templates:
	// - currentConfig: The parsed HAProxy config from HAProxyCfg CRD for slot-aware server assignment
	additionalDeclarations := map[string]any{
		"currentConfig": (*parserconfig.StructuredConfig)(nil),
	}
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, additionalDeclarations, helpers.EngineOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create template engine: %w", err)
	}

	// Note: Subscription happens in Start() using SubscribeTypesLeaderOnly() because
	// this is a leader-only component that starts after leadership is acquired.

	logger.Debug("Renderer initialized with capabilities",
		"supports_crt_list", capabilities.SupportsCrtList,
		"supports_map_storage", capabilities.SupportsMapStorage,
		"supports_general_storage", capabilities.SupportsGeneralStorage)

	return &Component{
		eventBus:           eventBus,
		engine:             engine,
		config:             cfg,
		stores:             stores,
		haproxyPodStore:    haproxyPodStore,
		currentConfigStore: currentConfigStore,
		logger:             logger.With("component", ComponentName),
		capabilities:       capabilities,
		healthTracker:      lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		subscriptionReady:  make(chan struct{}),
	}, nil
}

// SetHTTPStoreComponent sets the HTTP store component for dynamic HTTP resource fetching.
// This must be called before Start() to enable http.Fetch() in templates.
func (c *Component) SetHTTPStoreComponent(httpStoreComponent *httpstore.Component) {
	c.httpStoreComponent = httpStoreComponent
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
//
// For leader-only components like the Renderer, subscription happens in Start()
// rather than in the constructor. This method allows the lifecycle registry to
// wait for subscription before signaling that the component is ready.
func (c *Component) SubscriptionReady() <-chan struct{} {
	return c.subscriptionReady
}

// Start begins the renderer's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// As a leader-only component, it subscribes to events when started (after leadership is acquired).
//
// The component runs until the context is cancelled, at which point it
// performs cleanup and returns.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (c *Component) Start(ctx context.Context) error {
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// Use Critical buffer: standard processing with event coalescing
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(buffers.Critical(),
		events.EventTypeReconciliationTriggered,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	// This allows the registry to know we're ready to receive events before
	// EventBus.Start() replays buffered events.
	close(c.subscriptionReady)

	c.logger.Debug("renderer starting")

	// Store context for HTTP requests during rendering
	c.ctx = ctx

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("Renderer shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(event busevents.Event) {
	if ev, ok := event.(*events.ReconciliationTriggeredEvent); ok {
		c.handleReconciliationTriggered(ev)
	}
}

// validationEnvironment holds temporary paths for validation rendering.
type validationEnvironment struct {
	tmpDir     string
	mapsDir    string
	sslDir     string
	generalDir string
	configFile string
}

// setupValidationEnvironment creates temporary validation directories.
func (c *Component) setupValidationEnvironment() (*validationEnvironment, func(), error) {
	tmpDir, err := os.MkdirTemp("", "haproxy-validate-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	env := &validationEnvironment{
		tmpDir:     tmpDir,
		mapsDir:    filepath.Join(tmpDir, "maps"),
		sslDir:     filepath.Join(tmpDir, "certs"),
		generalDir: filepath.Join(tmpDir, "general"),
		configFile: filepath.Join(tmpDir, "haproxy.cfg"),
	}

	for _, dir := range []string{env.mapsDir, env.sslDir, env.generalDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, nil, fmt.Errorf("failed to create validation directory %s: %w", dir, err)
		}
	}

	cleanup := func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			c.logger.Warn("Failed to clean up validation temp directory",
				"path", tmpDir, "error", err)
		}
	}

	return env, cleanup, nil
}

// toPathResolver converts dataplane.ResolvedPaths to templating.PathResolver.
// This conversion is done in the controller layer to maintain architectural separation
// between pkg/dataplane and pkg/templating.
func toPathResolver(r *dataplane.ResolvedPaths) *templating.PathResolver {
	return &templating.PathResolver{
		MapsDir:    r.MapsDir,
		SSLDir:     r.SSLDir,
		CRTListDir: r.CRTListDir,
		GeneralDir: r.GeneralDir,
	}
}

// createPathResolvers creates production and validation path resolvers.
// Uses centralized path resolution to ensure CRT-list fallback is handled consistently.
func (c *Component) createPathResolvers(env *validationEnvironment) (production, validation *templating.PathResolver, validationPaths *dataplane.ValidationPaths) {
	// Production paths from config
	productionBase := dataplane.PathConfig{
		MapsDir:    c.config.Dataplane.MapsDir,
		SSLDir:     c.config.Dataplane.SSLCertsDir,
		GeneralDir: c.config.Dataplane.GeneralStorageDir,
	}
	productionResolved := dataplane.ResolvePaths(productionBase, c.capabilities)
	production = toPathResolver(productionResolved)

	// Validation paths from temp environment
	validationBase := dataplane.PathConfig{
		MapsDir:    env.mapsDir,
		SSLDir:     env.sslDir,
		GeneralDir: env.generalDir,
		ConfigFile: env.configFile,
	}
	validationResolved := dataplane.ResolvePaths(validationBase, c.capabilities)
	validation = toPathResolver(validationResolved)
	validationPaths = validationResolved.ToValidationPaths()

	// Set TempDir so validator can clean up the validation temp directory.
	// Cleanup is done by the validator after validation completes, not here,
	// to prevent race conditions where cleanup runs before async validation.
	validationPaths.TempDir = env.tmpDir

	return production, validation, validationPaths
}

// handleReconciliationTriggered implements "latest wins" coalescing for reconciliation events.
//
// When multiple ReconciliationTriggeredEvents arrive while rendering is in progress,
// intermediate events are superseded - only the latest pending trigger is processed.
// This prevents queue backlog where measured reconciliation times grow progressively
// when events arrive faster than they can be processed.
//
// Since the event loop is single-threaded, events buffer in eventChan during rendering.
// After each render completes, we drain the channel to find the latest trigger and
// process only that one, skipping intermediate triggers.
//
// Pattern:
//
//	t=0:     Trigger #1 → Render starts (takes ~3s)
//	t=500:   Trigger #2 → Buffers in eventChan
//	t=1000:  Trigger #3 → Buffers in eventChan
//	t=3000:  #1 done → Drain channel, find #3 (latest), skip #2
//	t=6000:  #3 done
//
// This follows the same pattern as DeploymentScheduler.
func (c *Component) handleReconciliationTriggered(event *events.ReconciliationTriggeredEvent) {
	// Process current event
	c.performRender(event)

	// After rendering completes, drain the event channel for any pending reconciliation triggers.
	// Since the event loop is single-threaded, events buffer in eventChan while performRender executes.
	// We process only the latest trigger (coalescing), handling other event types normally.
	for {
		latest := c.drainReconciliationTriggers()
		if latest == nil {
			return
		}

		c.logger.Debug("Processing coalesced reconciliation trigger",
			"correlation_id", latest.CorrelationID(),
			"reason", latest.Reason)
		c.performRender(latest)
	}
}

// drainReconciliationTriggers non-blocking drains the event channel for ReconciliationTriggeredEvents,
// returning only the latest one. Other event types (e.g., BecameLeaderEvent) are handled inline.
func (c *Component) drainReconciliationTriggers() *events.ReconciliationTriggeredEvent {
	var latest *events.ReconciliationTriggeredEvent
	supersededCount := 0

	for {
		select {
		case event := <-c.eventChan:
			switch ev := event.(type) {
			case *events.ReconciliationTriggeredEvent:
				if latest != nil {
					supersededCount++
				}
				latest = ev
			default:
				// Handle non-reconciliation events normally
				c.handleEvent(event)
			}
		default:
			// No more events in channel
			if supersededCount > 0 {
				c.logger.Debug("Coalesced reconciliation triggers",
					"superseded_count", supersededCount,
					"processing", latest.CorrelationID())
			}
			return latest
		}
	}
}

// performRender renders all templates for a reconciliation event.
// Renders configuration twice in parallel: once for production deployment, once for validation.
// Propagates correlation ID from the triggering event to the rendered event.
// This method is called by handleReconciliationTriggered after coalescing logic.
func (c *Component) performRender(event *events.ReconciliationTriggeredEvent) {
	// Track processing for health check stall detection
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	startTime := time.Now()
	correlationID := event.CorrelationID()
	c.logger.Debug("Template rendering triggered",
		"reason", event.Reason,
		"correlation_id", correlationID)

	// Setup validation environment (sequential - needed by both renders)
	// Note: We don't call cleanup() here. The validator is responsible for cleaning up
	// the temp directory after validation completes (via validationPaths.TempDir).
	// This prevents race conditions where cleanup runs before async validation.
	setupStart := time.Now()
	validationEnv, _, err := c.setupValidationEnvironment()
	if err != nil {
		c.publishRenderFailure("validation-setup", err)
		return
	}

	// Create path resolvers (sequential - needed by both renders)
	productionPathResolver, validationPathResolver, validationPaths := c.createPathResolvers(validationEnv)
	setupMs := time.Since(setupStart).Milliseconds()

	// Run production and validation renders in parallel
	var productionResult, validationResult *singleRenderResult
	var renderErr error

	g, _ := errgroup.WithContext(c.ctx)

	// Production render goroutine
	g.Go(func() error {
		result, err := c.renderSingle(productionPathResolver, false)
		if err != nil {
			return err
		}
		productionResult = result
		return nil
	})

	// Validation render goroutine
	g.Go(func() error {
		result, err := c.renderSingle(validationPathResolver, true)
		if err != nil {
			return err
		}
		validationResult = result
		return nil
	})

	// Wait for both renders to complete
	if renderErr = g.Wait(); renderErr != nil {
		// Error already published by renderSingle
		return
	}

	// Calculate metrics and log timing breakdown
	durationMs := time.Since(startTime).Milliseconds()
	auxFileCount := len(productionResult.auxiliaryFiles.MapFiles) +
		len(productionResult.auxiliaryFiles.GeneralFiles) +
		len(productionResult.auxiliaryFiles.SSLCertificates)

	c.logger.Debug("Template rendering completed (parallel)",
		"total_ms", durationMs,
		"setup_ms", setupMs,
		"prod_render_ms", productionResult.durationMs,
		"val_render_ms", validationResult.durationMs,
		"production_config_bytes", len(productionResult.haproxyConfig),
		"validation_config_bytes", len(validationResult.haproxyConfig),
		"auxiliary_files", auxFileCount,
	)

	// Publish success event with both rendered configs, propagating correlation
	c.eventBus.Publish(events.NewTemplateRenderedEvent(
		productionResult.haproxyConfig,
		validationResult.haproxyConfig,
		validationPaths,
		productionResult.auxiliaryFiles,
		validationResult.auxiliaryFiles,
		auxFileCount,
		durationMs,
		event.Reason,
		events.PropagateCorrelation(event),
	))
}

// renderSingle performs a single render (production or validation) and returns the result.
// This is called concurrently for production and validation renders.
func (c *Component) renderSingle(pathResolver *templating.PathResolver, isValidation bool) (*singleRenderResult, error) {
	renderStart := time.Now()
	label := "production"
	if isValidation {
		label = "validation"
	}

	// Build rendering context
	contextStart := time.Now()
	renderContext, fileRegistry := c.buildRenderingContext(c.ctx, pathResolver, isValidation)
	contextMs := time.Since(contextStart).Milliseconds()

	// Render main HAProxy config
	mainStart := time.Now()
	haproxyConfig, err := c.engine.Render("haproxy.cfg", renderContext)
	mainMs := time.Since(mainStart).Milliseconds()
	if err != nil {
		templateName := "haproxy.cfg"
		if isValidation {
			templateName = "haproxy.cfg-validation"
		}
		c.publishRenderFailure(templateName, err)
		return nil, err
	}

	// Render auxiliary files
	auxStart := time.Now()
	staticFiles, err := c.renderAuxiliaryFiles(renderContext)
	auxMs := time.Since(auxStart).Milliseconds()
	if err != nil {
		// Error already published by renderAuxiliaryFiles
		return nil, err
	}

	dynamicFiles := fileRegistry.GetFiles()
	auxiliaryFiles := MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	totalMs := time.Since(renderStart).Milliseconds()

	c.logger.Debug("Render breakdown",
		"path", label,
		"context_ms", contextMs,
		"main_template_ms", mainMs,
		"aux_files_ms", auxMs,
		"total_ms", totalMs,
	)

	return &singleRenderResult{
		haproxyConfig:  haproxyConfig,
		auxiliaryFiles: auxiliaryFiles,
		durationMs:     totalMs,
	}, nil
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) in parallel.
func (c *Component) renderAuxiliaryFiles(renderCtx map[string]interface{}) (*dataplane.AuxiliaryFiles, error) {
	totalFiles := len(c.config.Maps) + len(c.config.Files) + len(c.config.SSLCertificates)
	if totalFiles == 0 {
		return &dataplane.AuxiliaryFiles{}, nil
	}

	// Use mutex-protected slices for concurrent appends
	var mu sync.Mutex
	auxFiles := &dataplane.AuxiliaryFiles{}

	g, _ := errgroup.WithContext(context.Background())

	// Render map files in parallel
	for name := range c.config.Maps {
		g.Go(func() error {
			rendered, err := c.engine.Render(name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render general files in parallel
	for name := range c.config.Files {
		g.Go(func() error {
			rendered, err := c.engine.Render(name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
				Filename: name,
				Path:     filepath.Join(c.config.Dataplane.GeneralStorageDir, name),
				Content:  rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render SSL certificates in parallel
	for name := range c.config.SSLCertificates {
		g.Go(func() error {
			rendered, err := c.engine.Render(name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return auxFiles, nil
}

// publishRenderFailure publishes a template render failure event.
func (c *Component) publishRenderFailure(templateName string, err error) {
	// Get template content for context in error message
	templateContent, _ := c.engine.GetRawTemplate(templateName)

	// Format error for human readability
	formattedError := templating.FormatRenderError(err, templateName, templateContent)

	// Log formatted error (multi-line for readability)
	c.logger.Error("Template rendering failed\n"+formattedError,
		"template", templateName,
		"error_raw", err.Error()) // Keep raw error for programmatic access

	// Publish event with formatted error
	c.eventBus.Publish(events.NewTemplateRenderFailedEvent(
		templateName,
		formattedError,
		"", // Stack trace could be added here if needed
	))
}

// mergeAuxiliaryFiles merges static (pre-declared) and dynamic (registered during rendering) auxiliary files.
//
// The function combines both sets of files into a single AuxiliaryFiles structure.
// Both static and dynamic files are included in the merged result.
//
// Parameters:
//   - static: Pre-declared auxiliary files from config (templates in config.Maps, config.Files, config.SSLCertificates)
//   - dynamic: Dynamically registered files from FileRegistry during template rendering
//
// Returns:
//   - Merged AuxiliaryFiles containing all files from both sources
//
// MergeAuxiliaryFiles merges static (pre-declared) and dynamic (FileRegistry-registered) auxiliary files.
// Exported for use by test runner.
func MergeAuxiliaryFiles(static, dynamic *dataplane.AuxiliaryFiles) *dataplane.AuxiliaryFiles {
	merged := &dataplane.AuxiliaryFiles{}

	// Merge map files
	merged.MapFiles = append(merged.MapFiles, static.MapFiles...)
	merged.MapFiles = append(merged.MapFiles, dynamic.MapFiles...)

	// Merge general files
	merged.GeneralFiles = append(merged.GeneralFiles, static.GeneralFiles...)
	merged.GeneralFiles = append(merged.GeneralFiles, dynamic.GeneralFiles...)

	// Merge SSL certificates
	merged.SSLCertificates = append(merged.SSLCertificates, static.SSLCertificates...)
	merged.SSLCertificates = append(merged.SSLCertificates, dynamic.SSLCertificates...)

	// Merge CRT-list files
	merged.CRTListFiles = append(merged.CRTListFiles, static.CRTListFiles...)
	merged.CRTListFiles = append(merged.CRTListFiles, dynamic.CRTListFiles...)

	return merged
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

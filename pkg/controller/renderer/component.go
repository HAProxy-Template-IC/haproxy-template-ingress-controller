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
//
// Files in this package:
//   - component.go — Component lifecycle, event subscription, dispatch.
//   - render.go    — Template rendering (performRender / renderSingle / renderAuxiliaryFiles).
//   - context.go   — Building the rendering context map passed to templates.
//   - service.go   — Pure RenderService used by other components (pipeline, proposalvalidator).
package renderer

import (
	"context"
	"fmt"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "renderer"
)

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
	stores             map[string]stores.Store
	haproxyPodStore    stores.Store              // HAProxy controller pods store for pod-maxconn calculations
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

	// lastRenderedChecksum tracks the checksum of the last successfully rendered config.
	// Used to skip redundant TemplateRenderedEvents when content hasn't changed.
	// Cleared on leadership loss to ensure fresh render on leadership regain.
	// Not mutex-protected because it's only accessed from the single-threaded event loop.
	lastRenderedChecksum string
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
	storeMap map[string]stores.Store,
	haproxyPodStore stores.Store,
	currentConfigStore *currentconfigstore.Store,
	capabilities dataplane.Capabilities,
	logger *slog.Logger,
) (*Component, error) {
	// Log stores received during initialization
	logger.Debug("Creating renderer component",
		"store_count", len(storeMap))
	for resourceTypeName := range storeMap {
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
		return nil, fmt.Errorf("creating template engine: %w", err)
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
		stores:             storeMap,
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
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, buffers.Critical(),
		events.EventTypeReconciliationTriggered,
		events.EventTypeLostLeadership,
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
	switch ev := event.(type) {
	case *events.ReconciliationTriggeredEvent:
		c.handleReconciliationTriggered(ev)
	case *events.LostLeadershipEvent:
		c.handleLostLeadership(ev)
	}
}

// handleLostLeadership clears cached state when leadership is lost.
// This ensures a fresh render on leadership regain, preventing stale checksums
// from causing missed events.
func (c *Component) handleLostLeadership(_ *events.LostLeadershipEvent) {
	if c.lastRenderedChecksum != "" {
		c.logger.Debug("cleared render checksum cache on leadership loss")
		c.lastRenderedChecksum = ""
	}
}

// handleReconciliationTriggered implements "latest wins" coalescing for reconciliation events.
//
// When multiple coalescible ReconciliationTriggeredEvents arrive while rendering is in progress,
// intermediate events are superseded - only the latest pending trigger is processed.
// This prevents queue backlog where measured reconciliation times grow progressively
// when events arrive faster than they can be processed.
//
// Non-coalescible events (e.g., initial_sync, drift_prevention) are always processed and
// never skipped, even if newer events are available.
//
// Since the event loop is single-threaded, events buffer in eventChan during rendering.
// After each render completes, we drain the channel to find the latest coalescible trigger
// and process only that one, skipping intermediate coalescible triggers.
//
// Pattern:
//
//	t=0:     Trigger #1 (coalescible) → Render starts (takes ~3s)
//	t=500:   Trigger #2 (coalescible) → Buffers in eventChan
//	t=1000:  Trigger #3 (coalescible) → Buffers in eventChan
//	t=3000:  #1 done → Drain channel, find #3 (latest), skip #2
//	t=6000:  #3 done
//
// Uses the centralized coalesce.DrainLatest utility for consistent behavior across components.
func (c *Component) handleReconciliationTriggered(event *events.ReconciliationTriggeredEvent) {
	// Process current event
	c.performRender(event)

	// After rendering completes, drain the event channel for any pending coalescible triggers.
	// Since the event loop is single-threaded, events buffer in eventChan while performRender executes.
	// We process only the latest coalescible trigger, handling other event types normally.
	for {
		latest, supersededCount := coalesce.DrainLatest[*events.ReconciliationTriggeredEvent](
			c.eventChan,
			c.handleEvent, // Handle non-coalescible and other event types
		)
		if latest == nil {
			return
		}

		if supersededCount > 0 {
			c.logger.Debug("Coalesced reconciliation triggers",
				"superseded_count", supersededCount,
				"processing", latest.CorrelationID())
		}
		c.performRender(latest)
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

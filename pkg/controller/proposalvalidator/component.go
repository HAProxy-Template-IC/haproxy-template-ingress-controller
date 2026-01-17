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

// Package proposalvalidator provides validation of hypothetical configuration changes.
package proposalvalidator

import (
	"context"
	"log/slog"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "proposalvalidator"

	// EventBufferSize is the buffer size for event channel.
	EventBufferSize = 50
)

// Component validates hypothetical configuration changes without deploying.
//
// It supports two modes:
//  1. Async (event-driven): Subscribes to ProposalValidationRequestedEvent and publishes
//     ProposalValidationCompletedEvent.
//  2. Sync (direct call): ValidateSync() for synchronous callers like webhooks.
//
// The component uses RenderValidatePipeline with CompositeStoreProvider to render
// and validate configurations with proposed changes overlaid on actual stores.
type Component struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	pipeline  *pipeline.Pipeline
	baseStore stores.StoreProvider
	logger    *slog.Logger
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// ComponentConfig contains configuration for creating a ProposalValidator.
type ComponentConfig struct {
	// EventBus is the event bus for async validation requests.
	// Required for async mode, optional for sync-only mode.
	EventBus *busevents.EventBus

	// Pipeline is the render-validate pipeline.
	Pipeline *pipeline.Pipeline

	// BaseStoreProvider is the provider for actual (non-overlaid) stores.
	BaseStoreProvider stores.StoreProvider

	// Logger is the structured logger for logging.
	Logger *slog.Logger

	// SyncOnly, when true, creates a validator that only supports ValidateSync().
	// It does not subscribe to EventBus events and Start() should not be called.
	// Use this mode when only synchronous validation is needed (e.g., webhook).
	SyncOnly bool
}

// New creates a new ProposalValidator component.
//
// For async mode (SyncOnly=false): The component subscribes to events during construction
// (before EventBus.Start()) to ensure proper startup synchronization. Call Start() to
// begin processing events.
//
// For sync-only mode (SyncOnly=true): No event subscription occurs. Only ValidateSync()
// can be used. Do not call Start().
func New(cfg *ComponentConfig) *Component {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var eventChan <-chan busevents.Event
	if !cfg.SyncOnly && cfg.EventBus != nil {
		// Subscribe only to ProposalValidationRequestedEvent during construction per event bus contract.
		// Using SubscribeTypes prevents buffer overflow from unrelated events.
		eventChan = cfg.EventBus.SubscribeTypes("proposalvalidator", EventBufferSize,
			events.EventTypeProposalValidationRequested)
	}

	return &Component{
		eventBus:  cfg.EventBus,
		eventChan: eventChan,
		pipeline:  cfg.Pipeline,
		baseStore: cfg.BaseStoreProvider,
		logger:    logger.With("component", "proposalvalidator"),
	}
}

// Start runs the component's event loop.
//
// It listens for ProposalValidationRequestedEvent and processes validation requests.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Info("proposal validator started")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("proposal validator shutting down")
			return ctx.Err()
		}
	}
}

// handleEvent processes incoming events.
func (c *Component) handleEvent(event busevents.Event) {
	if e, ok := event.(*events.ProposalValidationRequestedEvent); ok {
		c.handleValidationRequest(e)
	}
}

// handleValidationRequest processes a proposal validation request.
func (c *Component) handleValidationRequest(req *events.ProposalValidationRequestedEvent) {
	hasHTTPOverlay := req.HTTPOverlay != nil && !req.HTTPOverlay.IsEmpty()
	c.logger.Debug("processing proposal validation request",
		"request_id", req.ID,
		"source", req.Source,
		"context", req.SourceContext,
		"k8s_overlay_count", len(req.Overlays),
		"has_http_overlay", hasHTTPOverlay,
	)

	startTime := time.Now()

	// Build ValidationContext from K8s overlays and HTTP overlay
	validationCtx := stores.NewValidationContext(req.Overlays)
	if req.HTTPOverlay != nil {
		validationCtx = validationCtx.WithHTTPOverlay(req.HTTPOverlay)
	}

	// Create OverlayStoreProvider that applies K8s overlays and exposes HTTP overlay
	overlayProvider := stores.NewOverlayStoreProvider(c.baseStore, validationCtx)

	// Validate overlays reference valid stores
	if err := overlayProvider.Validate(); err != nil {
		c.logger.Warn("proposal validation failed: invalid overlays",
			"request_id", req.ID,
			"error", err,
		)
		c.eventBus.Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			"setup",
			err,
			time.Since(startTime).Milliseconds(),
		))
		return
	}

	// Execute render-validate pipeline with timeout context
	// Event handlers don't have a parent context, so we create one with a timeout
	// to prevent validation from hanging indefinitely.
	// The OverlayStoreProvider automatically enables validation mode
	// (RenderService detects it and extracts HTTP overlay if present)
	ctx, cancel := context.WithTimeout(context.Background(), validation.DefaultValidationTimeout)
	defer cancel()
	_, validationResult, err := c.pipeline.ExecuteWithResult(ctx, overlayProvider)
	if err != nil {
		// Render failed
		c.logger.Warn("proposal validation failed: render error",
			"request_id", req.ID,
			"error", err,
		)
		c.eventBus.Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			"render",
			err,
			time.Since(startTime).Milliseconds(),
		))
		return
	}

	// Check validation result
	if !validationResult.Valid {
		c.logger.Info("proposal validation failed",
			"request_id", req.ID,
			"phase", validationResult.Phase,
			"error", validationResult.Error,
			"duration_ms", time.Since(startTime).Milliseconds(),
		)
		c.eventBus.Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			validationResult.Phase,
			validationResult.Error,
			time.Since(startTime).Milliseconds(),
		))
		return
	}

	// Validation succeeded
	c.logger.Info("proposal validation succeeded",
		"request_id", req.ID,
		"source", req.Source,
		"duration_ms", time.Since(startTime).Milliseconds(),
	)
	c.eventBus.Publish(events.NewProposalValidationCompletedEvent(
		req.ID,
		time.Since(startTime).Milliseconds(),
	))
}

// ValidateSync performs synchronous validation of proposed changes.
//
// This is the preferred method for webhook admission where the caller needs
// an immediate response. Unlike event-driven validation, this blocks until
// validation completes.
//
// Parameters:
//   - ctx: Context for cancellation
//   - overlays: Map of store name to proposed changes
//
// Returns:
//   - ValidationResult with valid/invalid status and error details.
func (c *Component) ValidateSync(ctx context.Context, overlays map[string]*stores.StoreOverlay) *validation.ValidationResult {
	startTime := time.Now()

	// Build ValidationContext from K8s overlays
	validationCtx := stores.NewValidationContext(overlays)

	// Create OverlayStoreProvider that applies K8s overlays
	overlayProvider := stores.NewOverlayStoreProvider(c.baseStore, validationCtx)

	// Validate overlays reference valid stores
	if err := overlayProvider.Validate(); err != nil {
		return &validation.ValidationResult{
			Valid:      false,
			Phase:      "setup",
			Error:      err,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	// Execute render-validate pipeline
	// The OverlayStoreProvider automatically enables validation mode
	_, validationResult, err := c.pipeline.ExecuteWithResult(ctx, overlayProvider)
	if err != nil {
		return &validation.ValidationResult{
			Valid:      false,
			Phase:      "render",
			Error:      err,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	// Check validation result
	if !validationResult.Valid {
		return &validation.ValidationResult{
			Valid:      false,
			Phase:      validationResult.Phase,
			Error:      validationResult.Error,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	return &validation.ValidationResult{
		Valid:      true,
		DurationMs: time.Since(startTime).Milliseconds(),
	}
}

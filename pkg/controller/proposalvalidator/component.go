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
	"fmt"
	"log/slog"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "proposalvalidator"

	// EventBufferSize is the buffer size for event channel.
	EventBufferSize = busevents.StandardSubscriberBuffer
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
	// Base is the embedded event-loop scaffold for async mode. It is nil in
	// sync-only mode (SyncOnly=true), where no subscription occurs and
	// Start() must not be called.
	*component.Base

	pipeline  *pipeline.Pipeline
	baseStore stores.StoreProvider

	// logger is kept alongside Base's (it carries the same component
	// annotation) because the sync-only path (ValidateSync from the
	// webhook) has no Base to provide one.
	logger *slog.Logger
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface. Kept as an override (rather
// than relying on the promoted Base.Name) because sync-only components have
// no Base.
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

	c := &Component{
		pipeline:  cfg.Pipeline,
		baseStore: cfg.BaseStoreProvider,
		logger:    logger.With("component", ComponentName),
	}
	if !cfg.SyncOnly && cfg.EventBus != nil {
		// The Base subscribes only to ProposalValidationRequestedEvent during
		// construction per the event bus contract; the typed subscription
		// prevents buffer overflow from unrelated events. Sync-only
		// components skip the subscription entirely (Base stays nil).
		c.Base = component.New(&component.Config{
			EventBus:   cfg.EventBus,
			Logger:     logger,
			Name:       ComponentName,
			BufferSize: EventBufferSize,
			Handler:    c,
			EventTypes: []string{events.EventTypeProposalValidationRequested},
		})
	}
	return c
}

// Start runs the embedded component.Base event loop until the context is
// cancelled. It listens for ProposalValidationRequestedEvent and processes
// validation requests. Must not be called in sync-only mode (Base is nil).
func (c *Component) Start(ctx context.Context) error {
	if c.Base == nil {
		return fmt.Errorf("%s: Start called on a sync-only instance (no event subscription)", ComponentName)
	}
	return c.Base.Start(ctx)
}

// HandleEvent implements component.EventHandler: it processes incoming events.
func (c *Component) HandleEvent(event busevents.Event) {
	if e, ok := event.(*events.ProposalValidationRequestedEvent); ok {
		c.handleValidationRequest(e)
	}
}

// handleValidationRequest processes a proposal validation request.
//
// This path is used by HTTPStore to ask "is the HAProxy config valid if I
// promote this newly-fetched HTTP content?" — a different semantic from
// ValidateSync's "is this admission request OK?" path. Admission can
// usefully relax to "admit when baseline already fails" because denying
// every unrelated admission on an existing broken resource is a real
// production reliability bug. Pending-content promotion CANNOT relax the
// same way: if baseline is already broken, promoting new (possibly bad)
// HTTP content compounds the broken state instead of recovering from it,
// and downstream observers (HTTPStore.handleProposalValidationCompleted)
// branch on event.Valid to decide whether to promote or reject. So this
// path keeps the strict "deny on any failure" semantics.
func (c *Component) handleValidationRequest(req *events.ProposalValidationRequestedEvent) {
	hasHTTPOverlay := req.HTTPOverlay != nil && !req.HTTPOverlay.IsEmpty()
	c.logger.Debug("Processing proposal validation request",
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
		c.logger.Warn("Proposal validation failed: invalid overlays",
			"request_id", req.ID,
			"error", err,
		)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(
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
	_, validationResult, err := c.pipeline.ExecuteWithResult(ctx, overlayProvider, rendercontext.RenderModeAdmission)
	if err != nil {
		// Render failed
		c.logger.Warn("Proposal validation failed: render error",
			"request_id", req.ID,
			"error", err,
		)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			"render",
			err,
			time.Since(startTime).Milliseconds(),
		))
		return
	}

	// Check validation result
	if !validationResult.Valid {
		c.logger.Info("Proposal validation failed",
			"request_id", req.ID,
			"phase", validationResult.Phase,
			"error", validationResult.Error,
			"duration_ms", time.Since(startTime).Milliseconds(),
		)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			validationResult.Phase,
			validationResult.Error,
			time.Since(startTime).Milliseconds(),
		))
		return
	}

	// Validation succeeded
	c.logger.Debug("Proposal validation succeeded",
		"request_id", req.ID,
		"source", req.Source,
		"duration_ms", time.Since(startTime).Milliseconds(),
	)
	c.EventBus().Publish(events.NewProposalValidationCompletedEvent(
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
// On a proposed-config failure (render or validate phase), this method also
// runs a baseline check — the same render+validate pipeline against the live
// stores *without* the overlay. If the baseline already fails, the new
// resource isn't the cause of the failure and admission is allowed (with a
// warning log). This prevents a real production reliability issue: a single
// broken existing resource (e.g., an Ingress referencing a Secret the user
// deleted) would otherwise block admission of every unrelated resource until
// the broken one is fixed. The baseline check reuses the validation cache, so
// in steady state (baseline healthy) the extra cost only kicks in on failure.
//
// Parameters:
//   - ctx: Context for cancellation
//   - overlays: Map of store name to proposed changes
//
// Returns:
//   - PipelineResult with the rendered HAProxy config + auxiliary files,
//     populated only when ValidationResult.Valid is true. Callers wiring
//     pluggable validators after the standard render+validate phases need
//     these to feed Manager.ValidateAll. Nil on any failure.
//   - ValidationResult with valid/invalid status and error details.
func (c *Component) ValidateSync(ctx context.Context, overlays map[string]*stores.StoreOverlay) (*pipeline.PipelineResult, *validation.ValidationResult) {
	startTime := time.Now()

	// Build ValidationContext from K8s overlays
	validationCtx := stores.NewValidationContext(overlays)

	// Create OverlayStoreProvider that applies K8s overlays
	overlayProvider := stores.NewOverlayStoreProvider(c.baseStore, validationCtx)

	// Validate overlays reference valid stores
	if err := overlayProvider.Validate(); err != nil {
		return nil, &validation.ValidationResult{
			Valid:      false,
			Phase:      "setup",
			Error:      err,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	outcome := c.runWithBaselineCheck(ctx, overlayProvider)
	if outcome.Admit {
		return outcome.PipelineResult, &validation.ValidationResult{
			Valid:      true,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}
	return nil, &validation.ValidationResult{
		Valid:      false,
		Phase:      outcome.Phase,
		Error:      outcome.Error,
		DurationMs: time.Since(startTime).Milliseconds(),
	}
}

// validationOutcome is the decision the baseline-aware pipeline driver
// returns to its caller. Admit=true means "let this through" (either the
// proposed run succeeded, or it failed but baseline failed too); Admit=false
// means "deny" with the proposed run's failure metadata. AdmittedViaBaseline
// distinguishes the "proposed succeeded" case from the "baseline-also-fails"
// case for log-level decisions. PipelineResult carries the rendered config +
// auxiliary files for the success path; nil otherwise (including the
// admitted-via-baseline path, since baseline rendered against the live store
// not the proposed overlay and downstream consumers want the proposed-state
// files, not the live-state ones).
type validationOutcome struct {
	Admit               bool
	AdmittedViaBaseline bool
	Phase               string
	Error               error
	PipelineResult      *pipeline.PipelineResult
}

// runWithBaselineCheck runs the render-validate pipeline twice: first with
// the given overlayProvider (proposed state), and — only if the proposed run
// fails — once more against the live stores without overlays (baseline). If
// the baseline also fails, the new resource isn't the cause of the failure,
// so the outcome admits. The baseline check is the load-bearing fix for the
// production reliability bug where a single broken existing resource (e.g.
// an Ingress whose Secret was deleted) blocks admission of every unrelated
// resource until an operator intervenes; see the package's component
// docstring for the rationale and the e2e flake history that motivated it.
//
// The baseline run reuses the validation service's content-checksum cache,
// so in steady state (baseline healthy) the second pipeline execution is
// only invoked on failure paths.
func (c *Component) runWithBaselineCheck(ctx context.Context, overlayProvider *stores.OverlayStoreProvider) validationOutcome {
	pipelineResult, proposedResult, proposedErr := c.pipeline.ExecuteWithResult(ctx, overlayProvider, rendercontext.RenderModeAdmission)
	if proposedErr == nil && proposedResult.Valid {
		return validationOutcome{Admit: true, PipelineResult: pipelineResult}
	}

	baselineResult, baselineErr := c.runBaselineCheck(ctx)
	baselineFailed := baselineErr != nil || (baselineResult != nil && !baselineResult.Valid)
	if baselineFailed {
		c.logger.Warn("Admitting proposed change because baseline validation already fails — pre-existing broken state, not the new resource",
			"proposed_render_err", proposedErr,
			"proposed_validation_phase", validationPhaseOf(proposedResult),
			"proposed_validation_err", validationErrorOf(proposedResult),
			"baseline_render_err", baselineErr,
			"baseline_validation_phase", validationPhaseOf(baselineResult),
			"baseline_validation_err", validationErrorOf(baselineResult))
		return validationOutcome{Admit: true, AdmittedViaBaseline: true}
	}

	// Baseline is healthy → the new resource is the cause of the failure → deny.
	if proposedErr != nil {
		return validationOutcome{Phase: "render", Error: proposedErr}
	}
	return validationOutcome{Phase: proposedResult.Phase, Error: proposedResult.Error}
}

// runBaselineCheck runs the render-validate pipeline against the live stores
// without any overlays. Used by runWithBaselineCheck to determine whether a
// proposed-state failure is caused by the new resource or by pre-existing
// broken state. Returns (validationResult, renderErr): exactly one is
// non-nil on a failure path, both nil on internal-error paths the caller
// treats as "baseline failed" (conservative).
func (c *Component) runBaselineCheck(ctx context.Context) (*validation.ValidationResult, error) {
	// Wrap the base store in an OverlayStoreProvider with an empty
	// ValidationContext so the pipeline accepts a uniform StoreProvider type
	// in both the proposed and baseline paths. With no overlays the provider
	// behaves as a pass-through to the live stores.
	emptyCtx := stores.NewValidationContext(nil)
	baselineProvider := stores.NewOverlayStoreProvider(c.baseStore, emptyCtx)
	// Baseline runs in the same admission mode as the proposed render so a
	// conflict-style check that fail()s is symmetric: a pre-existing conflict
	// fails BOTH renders, which is exactly what tells runWithBaselineCheck the
	// failure is not caused by the proposed resource (→ admit, don't block an
	// unrelated change on a conflict that was already there).
	_, result, err := c.pipeline.ExecuteWithResult(ctx, baselineProvider, rendercontext.RenderModeAdmission)
	return result, err
}

// validationPhaseOf returns the Phase from a ValidationResult, or "" if r is nil.
// Used in the structured-log fields that compare proposed vs baseline outcomes.
func validationPhaseOf(r *validation.ValidationResult) string {
	if r == nil {
		return ""
	}
	return r.Phase
}

// validationErrorOf returns the Error from a ValidationResult, or nil if r is nil.
func validationErrorOf(r *validation.ValidationResult) error {
	if r == nil {
		return nil
	}
	return r.Error
}

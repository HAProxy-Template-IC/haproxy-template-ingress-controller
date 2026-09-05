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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "proposalvalidator"

	// EventBufferSize is the buffer size for event channel.
	EventBufferSize = busevents.StandardSubscriberBuffer
	renderPhase     = "render"
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

	pipeline             *pipeline.Pipeline
	baseStore            stores.StoreProvider
	currentFilesProvider func() (map[string]string, error)

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

	// CurrentFilesProvider returns the published auxiliary baseline. One
	// snapshot is shared by every render in a proposal-validation decision.
	CurrentFilesProvider func() (map[string]string, error)

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
		pipeline:             cfg.Pipeline,
		baseStore:            cfg.BaseStoreProvider,
		currentFilesProvider: cfg.CurrentFilesProvider,
		logger:               logger.With("component", ComponentName),
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

// HandlePanic implements component.PanicHandler. Every other exit from
// handleValidationRequest publishes a verdict; without this one a panic would
// publish nothing, and the HTTP store's entry — which only leaves
// StateValidating on a verdict — would stay pending until its stuck-validation
// deadline. The outer recover in component.Base keeps the event loop alive.
func (c *Component) HandlePanic(recovered any, event busevents.Event) {
	req, ok := event.(*events.ProposalValidationRequestedEvent)
	if !ok {
		return
	}
	c.EventBus().Publish(events.NewProposalValidationFailedEvent(
		req.ID,
		"panic",
		fmt.Errorf("proposal validator panicked: %v", recovered),
		0,
	))
}

// handleValidationRequest processes a proposal validation request.
//
// This path is used by HTTPStore to ask "is the HAProxy config valid if I
// promote this newly-fetched HTTP content?" — a different semantic from
// ValidateSync's "does this resource change the invalid output?" path.
// Pending HTTP content always changes the candidate input, so this path keeps
// strict deny-on-failure semantics.
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

	ctx, cancel := context.WithTimeout(c.LifecycleContext(), validation.DefaultValidationTimeout)
	defer cancel()
	opts, err := c.withCurrentFilesSnapshot(admissionSubjectOpts(req.Overlays))
	if err != nil {
		c.logger.Warn("Proposal validation failed: currentFiles unavailable", "request_id", req.ID, "error", err)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			renderPhase,
			err,
			time.Since(startTime).Milliseconds(),
		))
		return
	}
	_, validationResult, err := c.pipeline.ExecuteWithResult(ctx, overlayProvider, rendercontext.RenderModeAdmission, opts...)
	if err != nil {
		phase := pipelineFailurePhase(err)
		c.logger.Warn("Proposal validation failed: pipeline error",
			"request_id", req.ID,
			"phase", phase,
			"error", err,
		)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(
			req.ID,
			phase,
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
// Unlike event-driven validation, this blocks until validation completes.
//
// On a validation failure, this method also runs the pipeline against the live
// stores without the overlay. Admission remains possible only when both runs
// render the exact same HAProxy content, proving that the proposal does not
// change the already-invalid configuration. Render failures and changed
// invalid content are denied.
//
// Parameters:
//   - ctx: Context for cancellation
//   - overlays: Map of store name to proposed changes
//
// Returns:
//   - PipelineResult with the rendered output, populated on success and on the
//     unchanged-invalid exception. Nil when no proposed-state output exists.
//   - ValidationResult with valid/invalid status and error details.
//
// admissionSubjectOpts derives a subject for one-object, one-store proposals.
func admissionSubjectOpts(overlays map[string]*stores.StoreOverlay) []rendercontext.Option {
	var opts []rendercontext.Option
	count := 0
	for storeName, overlay := range overlays {
		if overlay == nil {
			continue
		}
		for _, obj := range overlay.Additions {
			if accessor, err := meta.Accessor(obj); err == nil {
				count++
				opts = []rendercontext.Option{rendercontext.WithAdmissionSubject(storeName, accessor.GetNamespace(), accessor.GetName())}
			}
		}
		for _, obj := range overlay.Modifications {
			if accessor, err := meta.Accessor(obj); err == nil {
				count++
				opts = []rendercontext.Option{rendercontext.WithAdmissionSubject(storeName, accessor.GetNamespace(), accessor.GetName())}
			}
		}
		for _, key := range overlay.Deletions {
			count++
			opts = []rendercontext.Option{rendercontext.WithAdmissionSubject(storeName, key.Namespace, key.Name)}
		}
	}
	if count != 1 {
		return nil
	}
	return opts
}

func (c *Component) ValidateSync(ctx context.Context, overlays map[string]*stores.StoreOverlay) (*pipeline.PipelineResult, *validation.ValidationResult) {
	return c.validateSync(ctx, overlays, admissionSubjectOpts(overlays)...)
}

// ValidateSyncWithAdmissionSubject validates one admitted object across every
// configured store alias affected by the request.
func (c *Component) ValidateSyncWithAdmissionSubject(ctx context.Context, overlays map[string]*stores.StoreOverlay, storeAliases []string, namespace, name string) (*pipeline.PipelineResult, *validation.ValidationResult) {
	var opts []rendercontext.Option
	if len(storeAliases) > 0 {
		opts = []rendercontext.Option{rendercontext.WithAdmissionSubjectStores(storeAliases, namespace, name)}
	}
	return c.validateSync(ctx, overlays, opts...)
}

func (c *Component) validateSync(ctx context.Context, overlays map[string]*stores.StoreOverlay, opts ...rendercontext.Option) (*pipeline.PipelineResult, *validation.ValidationResult) {
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

	opts, err := c.withCurrentFilesSnapshot(opts)
	if err != nil {
		return nil, &validation.ValidationResult{
			Valid:      false,
			Phase:      renderPhase,
			Error:      err,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}
	outcome := c.runWithBaselineCheck(ctx, overlayProvider, opts...)
	if outcome.Admit {
		return outcome.PipelineResult, &validation.ValidationResult{
			Valid:      true,
			DurationMs: time.Since(startTime).Milliseconds(),
			Warnings:   outcome.Warnings,
		}
	}
	return nil, &validation.ValidationResult{
		Valid:      false,
		Phase:      outcome.Phase,
		Error:      outcome.Error,
		DurationMs: time.Since(startTime).Milliseconds(),
		Warnings:   outcome.Warnings,
	}
}

func (c *Component) withCurrentFilesSnapshot(opts []rendercontext.Option) ([]rendercontext.Option, error) {
	if c.currentFilesProvider == nil {
		return opts, nil
	}
	currentFiles, err := c.currentFilesProvider()
	if err != nil {
		return nil, err
	}
	return append(opts, rendercontext.WithCurrentAuxFiles(currentFiles)), nil
}

// validationOutcome is the decision the baseline-aware pipeline driver returns
// to its caller. PipelineResult is the proposed-state output and is populated
// for every admitted outcome so downstream validators inspect the same files.
type validationOutcome struct {
	Admit          bool
	Phase          string
	Error          error
	PipelineResult *pipeline.PipelineResult
	Warnings       []string
}

// runWithBaselineCheck allows an invalid proposed state only when its rendered
// HAProxy content is byte-equivalent to the already-invalid live state.
func (c *Component) runWithBaselineCheck(ctx context.Context, overlayProvider *stores.OverlayStoreProvider, proposedOpts ...rendercontext.Option) validationOutcome {
	pipelineResult, proposedResult, proposedErr := c.pipeline.ExecuteWithResult(ctx, overlayProvider, rendercontext.RenderModeAdmission, proposedOpts...)
	if authorityErr := validationAuthorityFailure(ctx, proposedErr, proposedResult); authorityErr != nil {
		return validationOutcome{Phase: pipelineFailurePhase(authorityErr), Error: authorityErr}
	}
	if proposedErr != nil {
		return validationOutcome{Phase: pipelineFailurePhase(proposedErr), Error: proposedErr}
	}
	if proposedResult == nil {
		return validationOutcome{Phase: "validation", Error: fmt.Errorf("proposal validation returned no result")}
	}
	if proposedResult.Valid {
		return validationOutcome{Admit: true, PipelineResult: pipelineResult, Warnings: proposedResult.Warnings}
	}

	baselinePipelineResult, baselineResult, baselineErr := c.runBaselineCheck(ctx, proposedOpts...)
	if authorityErr := validationAuthorityFailure(ctx, baselineErr, baselineResult); authorityErr != nil {
		return validationOutcome{Phase: pipelineFailurePhase(authorityErr), Error: authorityErr}
	}
	if baselineErr == nil && baselineResult != nil && !baselineResult.Valid &&
		sameRenderedContent(pipelineResult, baselinePipelineResult) {
		c.logger.Warn("Admitting resource because it does not change the already-invalid rendered configuration",
			"validation_phase", proposedResult.Phase,
			"validation_error", proposedResult.Error,
			"content_checksum", pipelineResult.ContentChecksum)
		// Authority may expire concurrently after the first check; recheck at admission.
		if authorityErr := validationAuthorityFailure(ctx, baselineErr, baselineResult); authorityErr != nil {
			return validationOutcome{Phase: pipelineFailurePhase(authorityErr), Error: authorityErr}
		}
		return validationOutcome{Admit: true, PipelineResult: pipelineResult, Warnings: proposedResult.Warnings}
	}

	return validationOutcome{Phase: proposedResult.Phase, Error: proposedResult.Error, Warnings: proposedResult.Warnings}
}

func sameRenderedContent(left, right *pipeline.PipelineResult) bool {
	leftContent, err := authenticatedRenderedContent(left)
	if err != nil {
		return false
	}
	rightContent, err := authenticatedRenderedContent(right)
	if err != nil || leftContent.config != rightContent.config {
		return false
	}
	same, err := leftContent.artifacts.SameRoot(rightContent.artifacts)
	if err != nil || same {
		return err == nil && same
	}
	equal, err := leftContent.artifacts.ExactEqual(rightContent.artifacts)
	return err == nil && equal
}

type renderedContent struct {
	config    string
	artifacts *renderartifact.Snapshot
}

func authenticatedRenderedContent(result *pipeline.PipelineResult) (*renderedContent, error) {
	if result == nil || result.CycleSnapshot == nil {
		return nil, errors.New("pipeline result has no authenticated render cycle")
	}
	output, err := result.CycleSnapshot.OutputSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading render cycle output: %w", err)
	}
	config, err := output.Config()
	if err != nil {
		return nil, fmt.Errorf("reading render config: %w", err)
	}
	artifacts, err := output.ArtifactSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading render artifacts: %w", err)
	}
	return &renderedContent{config: config, artifacts: artifacts}, nil
}

func validationAuthorityFailure(ctx context.Context, runErr error, runResult *validation.ValidationResult) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	if runErr != nil && errors.Is(runErr, cause) {
		return runErr
	}

	phase := pipeline.PhaseValidation
	validationPhase := ""
	if pipelineErr, ok := errors.AsType[*pipeline.PipelineError](runErr); ok {
		phase = pipelineErr.Phase
		validationPhase = pipelineErr.ValidationPhase
	} else if runResult != nil {
		validationPhase = runResult.Phase
	}
	authorityErr := fmt.Errorf("proposal validation did not finish: %w; retry the request", cause)
	if runErr != nil {
		authorityErr = errors.Join(runErr, authorityErr)
	}
	return &pipeline.PipelineError{
		Phase:           phase,
		ValidationPhase: validationPhase,
		Cause:           authorityErr,
	}
}

func pipelineFailurePhase(err error) string {
	pipelineErr, ok := errors.AsType[*pipeline.PipelineError](err)
	if !ok {
		return string(pipeline.PhaseRender)
	}
	if pipelineErr.Phase == pipeline.PhaseValidation && pipelineErr.ValidationPhase != "" {
		return pipelineErr.ValidationPhase
	}
	if pipelineErr.Phase == "" {
		return string(pipeline.PhaseRender)
	}
	return string(pipelineErr.Phase)
}

// runBaselineCheck runs the render-validate pipeline against the live stores
// without any overlays. Used by runWithBaselineCheck to determine whether a
// proposed validation failure came from unchanged rendered content.
func (c *Component) runBaselineCheck(ctx context.Context, opts ...rendercontext.Option) (*pipeline.PipelineResult, *validation.ValidationResult, error) {
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
	return c.pipeline.ExecuteWithResult(ctx, baselineProvider, rendercontext.RenderModeAdmission, opts...)
}

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

// Package pipeline provides the render-validate pipeline for HAProxy configuration.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// PipelinePhase identifies which phase of the pipeline failed.
type PipelinePhase string

const (
	// PhaseRender indicates the render phase.
	PhaseRender PipelinePhase = "render"
	// PhaseValidation indicates the validation phase.
	PhaseValidation PipelinePhase = "validation"
)

// PipelineError is a structured error that identifies which pipeline phase failed.
// Callers can use errors.AsType[*PipelineError] (or errors.As with a typed
// pointer target on Go < 1.26) to extract phase information instead of string
// parsing. The Coordinator does this in handlePipelineFailure to set the
// reconciliation-failed event's phase field.
type PipelineError struct {
	// Phase identifies which pipeline phase failed.
	Phase PipelinePhase

	// ValidationPhase is set when Phase is PhaseValidation.
	// It contains the specific validation sub-phase (syntax, schema, semantic).
	ValidationPhase string

	// Cause is the underlying error.
	Cause error
}

// Error implements the error interface.
func (e *PipelineError) Error() string {
	if e.Phase == PhaseValidation && e.ValidationPhase != "" {
		return fmt.Sprintf("%s failed in %s phase: %v", e.Phase, e.ValidationPhase, e.Cause)
	}
	return fmt.Sprintf("%s failed: %v", e.Phase, e.Cause)
}

// Unwrap returns the underlying error for errors.Is/As compatibility.
func (e *PipelineError) Unwrap() error {
	return e.Cause
}

// PipelineResult contains the output of a render-validate pipeline execution.
type PipelineResult struct {
	// HAProxyConfig is the rendered HAProxy configuration.
	HAProxyConfig string

	// AuxiliaryFiles contains all rendered auxiliary files.
	AuxiliaryFiles *dataplane.AuxiliaryFiles

	// Plan is the structure this render declared: its sections, backends, map
	// entries and file set. Nil when the renderer produced none.
	Plan *renderplan.Plan

	// PlanID is the digest identifying Plan.
	PlanID string

	// StatusPatches contains status patches registered by templates during rendering.
	// Each patch targets a Kubernetes resource and contains outcome-keyed variants.
	StatusPatches []templating.StatusPatch

	// Events contains Kubernetes Events templates asked to emit via recordEvent()
	// (e.g. a RouteConflict Warning on an Ingress). Forwarded to the EventEmitter.
	Events []templating.RenderedEvent

	// RenderedResources contains full Kubernetes resources the templates declared
	// the controller should own and reconcile (e.g. an auxiliary Service or other
	// object a template emits alongside the HAProxy config).
	RenderedResources []templating.RenderedResource

	// AuxFileCount is the total number of auxiliary files.
	AuxFileCount int

	// ContentChecksum is the pre-computed content checksum covering config + aux files.
	// Computed once in the pipeline and propagated through events to downstream consumers,
	// eliminating redundant hashing across validation, publishing, and deployment.
	ContentChecksum string

	// RenderDurationMs is the rendering duration in milliseconds.
	RenderDurationMs int64

	// ValidateDurationMs is the validation duration in milliseconds.
	ValidateDurationMs int64

	// TotalDurationMs is the total pipeline duration in milliseconds.
	TotalDurationMs int64

	// ValidationPhase indicates which validation phase completed last.
	// Empty string means all phases passed.
	ValidationPhase string

	// ParsedConfig is the pre-parsed desired configuration from syntax validation.
	// May be nil when validation fails or the validation service discards parsed results.
	// When non-nil, can be passed to downstream sync operations to avoid re-parsing.
	ParsedConfig *parser.StructuredConfig

	// ValidationWarnings contains non-fatal diagnostics produced after render.
	ValidationWarnings []string
}

// RenderedOutputValidator validates the complete rendered file set.
type RenderedOutputValidator interface {
	ValidateRenderedOutput(ctx context.Context, result *PipelineResult) (warnings []string, err error)
}

// Pipeline composes render and validate services into a single workflow.
//
// The pipeline:
// 1. Renders HAProxy configuration from stores
// 2. Validates the rendered configuration
// 3. Returns combined result
//
// This is a pure service with no event dependencies. It can be used by:
// - ReconciliationCoordinator for normal reconciliation flow
// - ProposalValidator for validation-only requests.
type Pipeline struct {
	renderer        *renderer.RenderService
	validator       *validation.ValidationService
	outputValidator RenderedOutputValidator
	commitValidator *validation.ValidationService
	logger          *slog.Logger
}

// PipelineConfig contains configuration for creating a Pipeline.
type PipelineConfig struct {
	// Renderer is the render service for generating configuration.
	Renderer *renderer.RenderService

	// Validator is the validation service for checking configuration. Nil
	// makes the pipeline render-only: the reconcile instance leaves HAProxy's
	// verdict to the asynchronous render gate (ADR-0022), while the proposal
	// instance keeps the synchronous check admission answers with.
	Validator *validation.ValidationService

	// OutputValidator optionally validates rendered auxiliary formats.
	OutputValidator RenderedOutputValidator

	// CommitValidator gates accepting external content no previous render
	// used. The reconcile pipeline has no Validator, but a render that pulls in
	// new HTTP-store content is the moment that content becomes the store's
	// accepted version — so it takes the full synchronous check before the
	// commit, not the gate's later verdict. Renders that accept nothing new
	// never reach it, which is every render in a steady state.
	CommitValidator *validation.ValidationService

	// Logger is the structured logger for logging.
	Logger *slog.Logger
}

// New creates a new render-validate pipeline.
//
// Panics if Renderer is nil. This is intentional: it is a required dependency,
// and failing at construction time is clearer than returning errors at
// execution time.
func New(cfg *PipelineConfig) *Pipeline {
	if cfg.Renderer == nil {
		panic("pipeline: Renderer is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Pipeline{
		renderer:        cfg.Renderer,
		validator:       cfg.Validator,
		outputValidator: cfg.OutputValidator,
		commitValidator: cfg.CommitValidator,
		logger:          logger,
	}
}

// Execute runs the render-validate pipeline.
//
// The render mode (production vs validation) is determined automatically:
// - If provider is *OverlayStoreProvider with overlays: validation mode
// - Otherwise: production mode
//
// Parameters:
//   - ctx: Context for cancellation
//   - provider: StoreProvider for accessing resource stores
//
// Returns:
//   - PipelineResult containing rendered config and validation status
//   - Error if rendering or validation fails
func (p *Pipeline) Execute(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*PipelineResult, error) {
	result, validationResult, err := p.execute(ctx, provider, mode, extraOpts...)
	if err != nil {
		return nil, err
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, validationResult.Phase, validationResult.Error); err != nil {
		return nil, err
	}
	if !validationResult.Valid {
		return nil, &PipelineError{
			Phase:           PhaseValidation,
			ValidationPhase: validationResult.Phase,
			Cause:           validationResult.Error,
		}
	}
	return result, nil
}

// ExecuteWithResult runs the pipeline and returns validation result even on failure.
// This is useful when you need details about why validation failed.
//
// The render mode (production vs validation) is determined automatically:
// - If provider is *OverlayStoreProvider with overlays: validation mode
// - Otherwise: production mode
//
// Parameters:
//   - ctx: Context for cancellation
//   - provider: StoreProvider for accessing resource stores
//
// Returns:
//   - PipelineResult with config and timing (nil if render or authority failed)
//   - ValidationResult with validation details (nil if render or authority failed)
//   - Error if rendering fails or context authority expires; ordinary validation
//     failures return a non-nil ValidationResult
func (p *Pipeline) ExecuteWithResult(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*PipelineResult, *validation.ValidationResult, error) {
	result, validationResult, err := p.execute(ctx, provider, mode, extraOpts...)
	if err != nil {
		return nil, nil, err
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, validationResult.Phase, validationResult.Error); err != nil {
		return nil, nil, err
	}
	return result, validationResult, nil
}

// execute is the shared render-validate body behind Execute and
// ExecuteWithResult. It always renders, then validates, and returns the
// assembled result alongside the raw validation result so callers can decide
// how to treat a validation failure (Execute turns it into a PipelineError;
// ExecuteWithResult hands the details back). A render failure short-circuits
// with a PipelineError and nil results.
func (p *Pipeline) execute(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*PipelineResult, *validation.ValidationResult, error) {
	startTime := time.Now()
	if err := pipelineCancellationError(ctx, PhaseRender, "", nil); err != nil {
		return nil, nil, err
	}

	// Phase 1: Render configuration
	renderResult, err := p.renderer.Render(ctx, provider, mode, extraOpts...)
	if renderResult != nil && renderResult.InputTransaction != nil {
		defer renderResult.InputTransaction.Abort()
	}
	if contextErr := pipelineCancellationError(ctx, PhaseRender, "", err); contextErr != nil {
		return nil, nil, contextErr
	}
	if err != nil {
		return nil, nil, &PipelineError{
			Phase: PhaseRender,
			Cause: err,
		}
	}
	// Compute content checksum once — propagated to all downstream consumers
	contentChecksum := dataplane.ComputeContentChecksum(renderResult.HAProxyConfig, renderResult.AuxiliaryFiles)

	// Phase 2: Validate configuration (pass pre-computed checksum to avoid rehashing)
	validationResult := &validation.ValidationResult{Valid: true}
	if p.validator != nil {
		validationResult = p.validator.ValidateWithChecksum(ctx, renderResult.HAProxyConfig, renderResult.AuxiliaryFiles, contentChecksum)
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, validationResult.Phase, validationResult.Error); err != nil {
		return nil, nil, err
	}

	result := &PipelineResult{
		HAProxyConfig:      renderResult.HAProxyConfig,
		AuxiliaryFiles:     renderResult.AuxiliaryFiles,
		Plan:               renderResult.Plan,
		PlanID:             renderResult.PlanID,
		StatusPatches:      renderResult.StatusPatches,
		Events:             renderResult.Events,
		RenderedResources:  renderResult.RenderedResources,
		AuxFileCount:       renderResult.AuxFileCount,
		ContentChecksum:    contentChecksum,
		RenderDurationMs:   renderResult.DurationMs,
		ValidateDurationMs: validationResult.DurationMs,
		TotalDurationMs:    time.Since(startTime).Milliseconds(),
		ValidationPhase:    validationResult.Phase,
		ParsedConfig:       validationResult.ParsedConfig,
	}

	if validationResult.Valid && p.outputValidator != nil {
		if err := pipelineCancellationError(ctx, PhaseValidation, "external", nil); err != nil {
			return nil, nil, err
		}
		validationStart := time.Now()
		warnings, outputErr := p.outputValidator.ValidateRenderedOutput(ctx, result)
		result.ValidationWarnings = warnings
		result.ValidateDurationMs += time.Since(validationStart).Milliseconds()
		result.TotalDurationMs = time.Since(startTime).Milliseconds()

		combined := *validationResult
		combined.Warnings = warnings
		combined.DurationMs = result.ValidateDurationMs
		if outputErr != nil {
			combined.Valid = false
			combined.Phase = "external"
			combined.Error = outputErr
		}
		validationResult = &combined
		result.ValidationPhase = validationResult.Phase
		if err := pipelineCancellationError(ctx, PhaseValidation, "external", outputErr); err != nil {
			return nil, nil, err
		}
	}

	if err := pipelineCancellationError(ctx, PhaseValidation, result.ValidationPhase, validationResult.Error); err != nil {
		return nil, nil, err
	}
	if validationResult.Valid && renderResult.InputTransaction != nil {
		if err := p.commitInputs(ctx, renderResult.InputTransaction, result); err != nil {
			return nil, nil, err
		}
		result.TotalDurationMs = time.Since(startTime).Milliseconds()
	}
	return result, validationResult, nil
}

// commitInputs accepts the external content this render used, after the check
// that acceptance requires.
func (p *Pipeline) commitInputs(
	ctx context.Context, transaction renderer.RenderInputTransaction, result *PipelineResult,
) *PipelineError {
	if err := p.checkBeforeCommit(ctx, transaction, result); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return &PipelineError{
			Phase: PhaseRender,
			Cause: fmt.Errorf("committing validated render inputs: %w", err),
		}
	}
	return nil
}

// checkBeforeCommit runs the full synchronous check on a render that is about
// to make external content the store's accepted version.
//
// Accepting content is not undoable by the render gate: its later verdict
// reverts the fleet's files, not the store's idea of what a URL returned. So
// the acceptance takes HAProxy's verdict up front — on the rare render that
// fetches something new, never on the steady-state ones.
func (p *Pipeline) checkBeforeCommit(
	ctx context.Context, transaction renderer.RenderInputTransaction, result *PipelineResult,
) *PipelineError {
	if p.commitValidator == nil || !transaction.HasCandidates() {
		return nil
	}
	verdict := p.commitValidator.ValidateWithChecksum(
		ctx, result.HAProxyConfig, result.AuxiliaryFiles, result.ContentChecksum)
	if err := pipelineCancellationError(ctx, PhaseValidation, verdict.Phase, verdict.Error); err != nil {
		return err
	}
	if verdict.Valid {
		return nil
	}
	return &PipelineError{
		Phase:           PhaseValidation,
		ValidationPhase: verdict.Phase,
		Cause:           fmt.Errorf("refusing to accept new external content: %w", verdict.Error),
	}
}

func pipelineCancellationError(
	ctx context.Context,
	phase PipelinePhase,
	validationPhase string,
	phaseErr error,
) *PipelineError {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	cancellationErr := fmt.Errorf("pipeline operation canceled: %w", cause)
	if phaseErr != nil && !errors.Is(phaseErr, cause) {
		cancellationErr = errors.Join(phaseErr, cancellationErr)
	}
	return &PipelineError{
		Phase:           phase,
		ValidationPhase: validationPhase,
		Cause:           cancellationErr,
	}
}

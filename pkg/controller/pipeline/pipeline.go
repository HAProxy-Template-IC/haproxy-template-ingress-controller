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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
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
// Callers can use errors.AsType[*PipelineError] to extract phase information
// instead of string parsing. The Coordinator does this in handlePipelineFailure
// to set the reconciliation-failed event's phase field.
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
	// CycleSnapshot binds the output and every effect from this render.
	CycleSnapshot *rendercycle.Snapshot

	// OutputSnapshot binds the exact config, plan, and auxiliary artifacts.
	OutputSnapshot *renderoutput.Snapshot

	// HAProxyConfig is the rendered HAProxy configuration.
	HAProxyConfig string

	// AuxiliaryFiles contains all rendered auxiliary files.
	// Production results leave it nil and use AuxiliaryFileSnapshot.
	AuxiliaryFiles *dataplane.AuxiliaryFiles

	// AuxiliaryFileSnapshot is the authenticated immutable production representation.
	AuxiliaryFileSnapshot *renderartifact.Snapshot

	// Plan is the structure this render declared: its sections, backends, map
	// entries and file set. Nil when the renderer produced none.
	Plan *renderplan.Plan

	// PlanID is the digest identifying Plan.
	PlanID string

	// StatusPatches is the detached compatibility representation.
	// Production renders leave it nil and use StatusPatchSnapshot.
	StatusPatches []templating.StatusPatch

	// StatusPatchSnapshot is the authenticated immutable production representation.
	StatusPatchSnapshot *templating.StatusPatchSnapshot

	// Events contains Kubernetes Events templates asked to emit via recordEvent()
	// (e.g. a RouteConflict Warning on an Ingress). Forwarded to the EventEmitter.
	// Production renders leave it nil and use EventSnapshot.
	Events []templating.RenderedEvent

	// EventSnapshot is the authenticated immutable production representation.
	EventSnapshot *templating.RenderedEventSnapshot

	// RenderedResources contains full Kubernetes resources the templates declared
	// the controller should own and reconcile (e.g. an auxiliary Service or other
	// object a template emits alongside the HAProxy config).
	// Production renders leave it nil and use RenderedResourceSnapshot.
	RenderedResources []templating.RenderedResource

	// RenderedResourceSnapshot is the authenticated immutable production representation.
	RenderedResourceSnapshot *templating.RenderedResourceSnapshot

	// AuxFileCount is the total number of auxiliary files.
	AuxFileCount int

	// ContentChecksum is the pre-computed content checksum covering config + aux files.
	// Computed once in the pipeline and propagated through events to downstream consumers,
	// eliminating redundant hashing across validation, publishing, and deployment.
	ContentChecksum string

	// RenderDurationMs is the rendering duration in milliseconds.
	RenderDurationMs int64

	// CacheState is "warm" when the render had a graph to build on, "cold" when it
	// re-evaluated everything, and "replay" when it reused the previous output.
	CacheState string

	// CacheBuildMs is what the most recent completed cache build cost, 0 while none
	// has completed.
	CacheBuildMs int64

	// ValidateDurationMs is the validation duration in milliseconds.
	ValidateDurationMs int64

	// TotalDurationMs is the total pipeline duration in milliseconds.
	TotalDurationMs int64

	// ValidationPhase indicates which validation phase completed last.
	// Empty string means all phases passed.
	ValidationPhase string

	// ValidationWarnings contains non-fatal diagnostics produced after render.
	ValidationWarnings []string
}

// MaterializeAuxiliaryFiles returns a caller-isolated compatibility view.
func (r *PipelineResult) MaterializeAuxiliaryFiles() (*dataplane.AuxiliaryFiles, error) {
	cycle, err := r.authenticatedCycle()
	if err != nil {
		return nil, err
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading pipeline cycle output: %w", err)
	}
	snapshot, err := output.ArtifactSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading pipeline output artifacts: %w", err)
	}
	return dataplane.MaterializeAuxiliaryFileSnapshot(snapshot)
}

// MaterializeStatusPatches returns a caller-isolated compatibility view.
func (r *PipelineResult) MaterializeStatusPatches() ([]templating.StatusPatch, error) {
	cycle, err := r.authenticatedCycle()
	if err != nil {
		return nil, err
	}
	statusSnapshot, err := cycle.StatusPatchSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading pipeline cycle status patches: %w", err)
	}
	return statusSnapshot.Patches()
}

// MaterializeEvents returns a caller-isolated compatibility view.
func (r *PipelineResult) MaterializeEvents() ([]templating.RenderedEvent, error) {
	cycle, err := r.authenticatedCycle()
	if err != nil {
		return nil, err
	}
	eventSnapshot, err := cycle.RenderedEventSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading pipeline cycle events: %w", err)
	}
	return eventSnapshot.Events()
}

// MaterializeRenderedResources returns a caller-isolated compatibility view.
func (r *PipelineResult) MaterializeRenderedResources() ([]templating.RenderedResource, error) {
	cycle, err := r.authenticatedCycle()
	if err != nil {
		return nil, err
	}
	resourceSnapshot, err := cycle.RenderedResourceSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading pipeline cycle resources: %w", err)
	}
	return resourceSnapshot.Resources()
}

func (r *PipelineResult) authenticatedCycle() (*rendercycle.Snapshot, error) {
	if r == nil || r.CycleSnapshot == nil {
		return nil, errors.New("pipeline result has no authenticated render cycle")
	}
	if err := r.CycleSnapshot.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("authenticating pipeline render cycle: %w", err)
	}
	return r.CycleSnapshot, nil
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
	result, validationResult, err := p.executeSettlingInputConflicts(ctx, provider, mode, extraOpts...)
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
	result, validationResult, err := p.executeSettlingInputConflicts(ctx, provider, mode, extraOpts...)
	if err != nil {
		return nil, nil, err
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, validationResult.Phase, validationResult.Error); err != nil {
		return nil, nil, err
	}
	return result, validationResult, nil
}

type authenticatedRenderOutput struct {
	cycle     *rendercycle.Snapshot
	snapshot  *renderoutput.Snapshot
	config    string
	artifacts *renderartifact.Snapshot
	status    *templating.StatusPatchSnapshot
	events    *templating.RenderedEventSnapshot
	resources *templating.RenderedResourceSnapshot
	planID    string
	checksum  string
	auxCount  int
}

func authenticateRenderOutput(result *renderer.RenderResult) (*authenticatedRenderOutput, error) {
	if result == nil {
		return nil, errors.New("renderer returned no result")
	}
	cycle := result.CycleSnapshot
	if cycle == nil {
		return nil, errors.New("renderer returned no authenticated render cycle")
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return nil, fmt.Errorf("authenticating render cycle: %w", err)
	}
	status, err := cycle.StatusPatchSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading render cycle status patches: %w", err)
	}
	renderedEvents, err := cycle.RenderedEventSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading render cycle events: %w", err)
	}
	resources, err := cycle.RenderedResourceSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading render cycle resources: %w", err)
	}
	if err := output.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("authenticating render output: %w", err)
	}
	config, err := output.Config()
	if err != nil {
		return nil, fmt.Errorf("reading rendered config: %w", err)
	}
	artifacts, err := output.ArtifactSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading rendered artifacts: %w", err)
	}
	planID, err := output.PlanID()
	if err != nil {
		return nil, fmt.Errorf("reading rendered plan ID: %w", err)
	}
	counts, err := output.Counts()
	if err != nil {
		return nil, fmt.Errorf("reading rendered output counts: %w", err)
	}
	checksum, err := output.ContentChecksum()
	if err != nil {
		return nil, fmt.Errorf("reading rendered output checksum: %w", err)
	}
	return &authenticatedRenderOutput{
		cycle: cycle, snapshot: output, config: config, artifacts: artifacts,
		status: status, events: renderedEvents, resources: resources,
		planID: planID, checksum: checksum, auxCount: counts.Artifacts,
	}, nil
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
	authenticatedOutput, err := authenticateRenderOutput(renderResult)
	if err != nil {
		return nil, nil, &PipelineError{Phase: PhaseRender, Cause: err}
	}

	// Phase 2: Validate configuration (pass pre-computed checksum to avoid rehashing)
	validationResult := &validation.ValidationResult{Valid: true}
	if p.validator != nil {
		validationResult = p.validator.ValidateOutputSnapshotWithChecksum(
			ctx, authenticatedOutput.snapshot, authenticatedOutput.checksum,
		)
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, validationResult.Phase, validationResult.Error); err != nil {
		return nil, nil, err
	}

	result := &PipelineResult{
		CycleSnapshot:            authenticatedOutput.cycle,
		OutputSnapshot:           authenticatedOutput.snapshot,
		HAProxyConfig:            authenticatedOutput.config,
		AuxiliaryFileSnapshot:    authenticatedOutput.artifacts,
		PlanID:                   authenticatedOutput.planID,
		StatusPatchSnapshot:      authenticatedOutput.status,
		EventSnapshot:            authenticatedOutput.events,
		RenderedResourceSnapshot: authenticatedOutput.resources,
		AuxFileCount:             authenticatedOutput.auxCount,
		ContentChecksum:          authenticatedOutput.checksum,
		RenderDurationMs:         renderResult.DurationMs,
		CacheState:               renderResult.CacheState,
		CacheBuildMs:             renderResult.CacheBuildMs,
		ValidateDurationMs:       validationResult.DurationMs,
		TotalDurationMs:          time.Since(startTime).Milliseconds(),
		ValidationPhase:          validationResult.Phase,
	}

	validationResult, err = p.validateExternalOutput(ctx, result, validationResult, startTime)
	if err != nil {
		return nil, nil, err
	}
	if err := restoreAuthenticatedCycleResult(result, authenticatedOutput); err != nil {
		return nil, nil, &PipelineError{Phase: PhaseValidation, ValidationPhase: "external", Cause: err}
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

func (p *Pipeline) validateExternalOutput(
	ctx context.Context,
	result *PipelineResult,
	validationResult *validation.ValidationResult,
	startTime time.Time,
) (*validation.ValidationResult, error) {
	if !validationResult.Valid || p.outputValidator == nil {
		return validationResult, nil
	}
	if err := pipelineCancellationError(ctx, PhaseValidation, "external", nil); err != nil {
		return nil, err
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
	result.ValidationPhase = combined.Phase
	if err := pipelineCancellationError(ctx, PhaseValidation, "external", outputErr); err != nil {
		return nil, err
	}
	return &combined, nil
}

func restoreAuthenticatedCycleResult(
	result *PipelineResult,
	authenticated *authenticatedRenderOutput,
) error {
	if result == nil || authenticated == nil || authenticated.cycle == nil {
		return errors.New("authenticated render cycle is unavailable")
	}
	if result.CycleSnapshot != authenticated.cycle {
		return errors.New("rendered output validator replaced the authenticated render cycle")
	}
	if err := authenticated.cycle.ValidateAuthentication(); err != nil {
		return fmt.Errorf("rendered output validator invalidated the render cycle: %w", err)
	}
	result.OutputSnapshot = authenticated.snapshot
	result.HAProxyConfig = authenticated.config
	result.AuxiliaryFiles = nil
	result.AuxiliaryFileSnapshot = authenticated.artifacts
	result.Plan = nil
	result.PlanID = authenticated.planID
	result.StatusPatches = nil
	result.StatusPatchSnapshot = authenticated.status
	result.Events = nil
	result.EventSnapshot = authenticated.events
	result.RenderedResources = nil
	result.RenderedResourceSnapshot = authenticated.resources
	result.AuxFileCount = authenticated.auxCount
	result.ContentChecksum = authenticated.checksum
	return nil
}

// renderInputConflictAttempts bounds the re-renders spent losing the commit race.
//
// A conflict is not a bad configuration: it means a watched input moved while
// this render was reading it, so the render describes a cluster state that no
// longer exists. The next attempt reads the newer revision, which settles the
// ordinary case in one retry; the bound stops a continuously-changing cluster
// from spinning here instead of making progress.
const renderInputConflictAttempts = 3

// inputsMovedUnderTheRender reports whether a render failed because what it was
// reading changed while it read it, which the next attempt sees settled.
//
// A revision conflict is the transaction noticing at commit. A changed snapshot
// is a store noticing mid-read: the informer generation moved before the API
// read that would have confirmed it. Both say the same thing — this render was
// composed against inputs that no longer hold — and both are answered by
// rendering again, not by refusing.
//
// Refusing is what made this matter: an admission render that hit a snapshot
// change DENIED the object under review, so one namespace's Secret rotating
// could reject an unrelated Ingress in another. The user's object was never the
// problem.
func inputsMovedUnderTheRender(err error, mode rendercontext.RenderMode) bool {
	if errors.Is(err, incremental.ErrRevisionConflict) {
		return true
	}
	// A moved snapshot is only worth re-reading inline for admission, which has
	// to answer THIS request and must not deny the operator's object for a race
	// inside the controller. A reconcile is re-triggered by the very change that
	// beat it, so retrying here buys nothing and costs up to
	// renderInputConflictAttempts slow renders in front of the deploy — measured
	// as a 2.4s gap in endpoint propagation on a contended node, long enough for
	// a rolling restart to lose its last server.
	return mode == rendercontext.RenderModeAdmission && errors.Is(err, stores.ErrSnapshotChanged)
}

// admissionInputConflictBackoff paces an admission re-render.
//
// Counting attempts is the wrong bound for the webhook: three of them fire
// inside 25ms, which is shorter than the commit they keep losing to, so they
// are one attempt spelled three times and the operator's update is denied for a
// race inside the controller. Pausing lets that commit land before the next
// read. (Measured on an e2e run: two denials, attempts 1-3 spanning 24ms and
// 86ms.)
const admissionInputConflictBackoff = 20 * time.Millisecond

// admissionInputConflictBudget caps the pacing above so a cluster that never
// stops changing still answers, rather than holding the request open.
const admissionInputConflictBudget = 750 * time.Millisecond

// admissionInputConflictReserve keeps enough of the webhook's own timeout for
// the attempt that follows the last wait.
const admissionInputConflictReserve = 500 * time.Millisecond

// executeSettlingInputConflicts re-runs a render whose commit lost the race with
// a concurrent input change.
//
// Without this the conflict reaches the caller as a failure, and the two callers
// fail very differently: a reconcile logs an error and recovers on the next
// trigger, while the admission webhook denies the operator's create or update —
// for a race inside the controller rather than anything wrong with their object.
// A conflicting attempt publishes nothing (the commit is the publishing step and
// it is what failed), so re-rendering has no effect to undo.
func (p *Pipeline) executeSettlingInputConflicts(
	ctx context.Context,
	provider stores.StoreProvider,
	mode rendercontext.RenderMode,
	extraOpts ...rendercontext.Option,
) (*PipelineResult, *validation.ValidationResult, error) {
	return settleInputConflicts(ctx, p.logger, mode, func() (*PipelineResult, *validation.ValidationResult, error) {
		return p.execute(ctx, provider, mode, extraOpts...)
	})
}

// settleInputConflicts holds the retry policy on its own so it can be exercised
// without a cluster racing the render.
func settleInputConflicts(
	ctx context.Context,
	logger *slog.Logger,
	mode rendercontext.RenderMode,
	render func() (*PipelineResult, *validation.ValidationResult, error),
) (*PipelineResult, *validation.ValidationResult, error) {
	var (
		result           *PipelineResult
		validationResult *validation.ValidationResult
		err              error
	)
	retryUntil := admissionInputConflictDeadline(ctx)
	for attempt := 1; ; attempt++ {
		result, validationResult, err = render()
		if err == nil || !inputsMovedUnderTheRender(err, mode) {
			return result, validationResult, err
		}
		if context.Cause(ctx) != nil {
			return nil, nil, err
		}
		if logger != nil {
			logger.Debug("render inputs changed mid-render, re-rendering",
				"attempt", attempt, "mode", mode)
		}
		if mode != rendercontext.RenderModeAdmission {
			if attempt >= renderInputConflictAttempts {
				return result, validationResult, err
			}
			// Pace this one too. A re-render is not cheap at scale, and the burst
			// that produced the conflict is exactly when the cluster can least
			// afford three of them back-to-back.
			if !pauseBeforeInputConflictRetry(ctx, admissionInputConflictBackoff) {
				return result, validationResult, err
			}
			continue
		}
		if !waitBeforeInputConflictRetry(ctx, retryUntil, attempt) {
			return result, validationResult, err
		}
	}
}

// admissionInputConflictDeadline is when re-reading a moving graph stops being
// worth the operator's wait, never later than the request's own deadline.
func admissionInputConflictDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(admissionInputConflictBudget)
	if requestDeadline, ok := ctx.Deadline(); ok {
		if reserved := requestDeadline.Add(-admissionInputConflictReserve); reserved.Before(deadline) {
			return reserved
		}
	}
	return deadline
}

// waitBeforeInputConflictRetry reports whether another re-render is worth
// starting, pausing first so the commit that won this race can finish.
func waitBeforeInputConflictRetry(ctx context.Context, retryUntil time.Time, attempt int) bool {
	backoff := admissionInputConflictBackoff << min(attempt-1, 3)
	if time.Now().Add(backoff).After(retryUntil) {
		return false
	}
	return pauseBeforeInputConflictRetry(ctx, backoff)
}

// pauseBeforeInputConflictRetry waits out one backoff, reporting whether the
// wait completed rather than the render being cancelled under it.
func pauseBeforeInputConflictRetry(ctx context.Context, backoff time.Duration) bool {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
		if commitConflictLeavesOutputUsable(err, transaction) {
			if p.logger != nil {
				p.logger.Debug("render inputs moved before the cache could be published; " +
					"keeping this render and leaving the cache where it was")
			}
			return nil
		}
		return &PipelineError{
			Phase: PhaseRender,
			Cause: fmt.Errorf("committing validated render inputs: %w", err),
		}
	}
	return nil
}

// commitConflictLeavesOutputUsable reports whether a failed commit cost only
// the incremental cache, leaving this render's output fit to deploy.
//
// A revision conflict says the inputs moved while the render was reading them,
// so the render describes a snapshot that has already passed. For a render that
// accepted no external content that is the ordinary state of any controller:
// the output is what the cluster looked like a moment ago, the watch event for
// the change is already queued, and the next reconcile supersedes it. The only
// casualty is the cache, which stays where it was and costs the next render its
// incremental start.
//
// Failing instead starves the fleet. Under a burst — a conformance suite, or a
// GitOps apply of many objects — conflicts arrive faster than renders finish,
// and every reconcile fails: measured at 21 in a row and 176 seconds without a
// single successful render, while the cluster waited for routes that had been
// created minutes earlier.
//
// A render that IS accepting external content keeps failing. There the commit
// is not bookkeeping: it decides the store's accepted version of something
// fetched over the network, the render gate cannot undo that acceptance later,
// and a conflict means the check that authorised it was against inputs that
// have since moved.
func commitConflictLeavesOutputUsable(err error, transaction renderer.RenderInputTransaction) bool {
	if !errors.Is(err, incremental.ErrRevisionConflict) {
		return false
	}
	if transaction.HasCandidates() {
		return false
	}
	// Lease accounting counts too, though it accepts nothing. The commit tells
	// the HTTP store how many renders hold each source; drop it and the store
	// keeps counting references this render has already released, until a later
	// render's removals exceed the count and it rejects them as inconsistent.
	// Measured: 60 such rejections in one e2e run when this condition asked
	// only about candidates.
	carrier, ok := transaction.(interface{ CarriesHTTPState() bool })
	return !ok || !carrier.CarriesHTTPState()
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
	output, checksum, err := authenticatedPipelineResultOutput(result)
	if err != nil {
		return &PipelineError{Phase: PhaseValidation, ValidationPhase: "setup", Cause: err}
	}
	verdict := p.commitValidator.ValidateOutputSnapshotWithChecksum(ctx, output, checksum)
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

func authenticatedPipelineResultOutput(
	result *PipelineResult,
) (*renderoutput.Snapshot, string, error) {
	if result == nil || result.CycleSnapshot == nil {
		return nil, "", errors.New("pipeline result has no authenticated render cycle")
	}
	output, err := result.CycleSnapshot.OutputSnapshot()
	if err != nil {
		return nil, "", fmt.Errorf("reading pipeline cycle output: %w", err)
	}
	checksum, err := result.CycleSnapshot.ContentChecksum()
	if err != nil {
		return nil, "", fmt.Errorf("reading pipeline cycle checksum: %w", err)
	}
	return output, checksum, nil
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

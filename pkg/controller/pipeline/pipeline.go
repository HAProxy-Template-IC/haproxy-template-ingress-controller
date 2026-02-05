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
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
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
// Callers can use errors.As() to extract phase information instead of string parsing.
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
	// May be nil if validation cache was used.
	// When non-nil, can be passed to downstream sync operations to avoid re-parsing.
	ParsedConfig *parser.StructuredConfig
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
	renderer  *renderer.RenderService
	validator *validation.ValidationService
	logger    *slog.Logger
}

// PipelineConfig contains configuration for creating a Pipeline.
type PipelineConfig struct {
	// Renderer is the render service for generating configuration.
	Renderer *renderer.RenderService

	// Validator is the validation service for checking configuration.
	Validator *validation.ValidationService

	// Logger is the structured logger for logging.
	Logger *slog.Logger
}

// New creates a new render-validate pipeline.
//
// Panics if Renderer or Validator is nil. This is intentional: these are
// required dependencies, and failing at construction time is clearer than
// returning errors at execution time.
func New(cfg *PipelineConfig) *Pipeline {
	if cfg.Renderer == nil {
		panic("pipeline: Renderer is required")
	}
	if cfg.Validator == nil {
		panic("pipeline: Validator is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Pipeline{
		renderer:  cfg.Renderer,
		validator: cfg.Validator,
		logger:    logger,
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
func (p *Pipeline) Execute(ctx context.Context, provider stores.StoreProvider) (*PipelineResult, error) {
	startTime := time.Now()

	// Phase 1: Render configuration
	renderResult, err := p.renderer.Render(ctx, provider)
	if err != nil {
		return nil, &PipelineError{
			Phase: PhaseRender,
			Cause: err,
		}
	}

	// Compute content checksum once — propagated to all downstream consumers
	contentChecksum := dataplane.ComputeContentChecksum(renderResult.HAProxyConfig, renderResult.AuxiliaryFiles)

	// Phase 2: Validate configuration (pass pre-computed checksum to avoid rehashing)
	validationResult := p.validator.ValidateWithChecksum(ctx, renderResult.HAProxyConfig, renderResult.AuxiliaryFiles, contentChecksum)
	if !validationResult.Valid {
		return nil, &PipelineError{
			Phase:           PhaseValidation,
			ValidationPhase: validationResult.Phase,
			Cause:           validationResult.Error,
		}
	}

	return &PipelineResult{
		HAProxyConfig:      renderResult.HAProxyConfig,
		AuxiliaryFiles:     renderResult.AuxiliaryFiles,
		AuxFileCount:       renderResult.AuxFileCount,
		ContentChecksum:    contentChecksum,
		RenderDurationMs:   renderResult.DurationMs,
		ValidateDurationMs: validationResult.DurationMs,
		TotalDurationMs:    time.Since(startTime).Milliseconds(),
		ParsedConfig:       validationResult.ParsedConfig,
	}, nil
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
//   - PipelineResult with config and timing (nil if render failed)
//   - ValidationResult with validation details (nil if render failed)
//   - Error if rendering fails (validation failures return non-nil ValidationResult)
func (p *Pipeline) ExecuteWithResult(ctx context.Context, provider stores.StoreProvider) (*PipelineResult, *validation.ValidationResult, error) {
	startTime := time.Now()

	// Phase 1: Render configuration
	renderResult, err := p.renderer.Render(ctx, provider)
	if err != nil {
		return nil, nil, &PipelineError{
			Phase: PhaseRender,
			Cause: err,
		}
	}

	// Compute content checksum once
	contentChecksum := dataplane.ComputeContentChecksum(renderResult.HAProxyConfig, renderResult.AuxiliaryFiles)

	// Phase 2: Validate configuration (pass pre-computed checksum)
	validationResult := p.validator.ValidateWithChecksum(ctx, renderResult.HAProxyConfig, renderResult.AuxiliaryFiles, contentChecksum)

	result := &PipelineResult{
		HAProxyConfig:      renderResult.HAProxyConfig,
		AuxiliaryFiles:     renderResult.AuxiliaryFiles,
		AuxFileCount:       renderResult.AuxFileCount,
		ContentChecksum:    contentChecksum,
		RenderDurationMs:   renderResult.DurationMs,
		ValidateDurationMs: validationResult.DurationMs,
		TotalDurationMs:    time.Since(startTime).Milliseconds(),
		ValidationPhase:    validationResult.Phase,
		ParsedConfig:       validationResult.ParsedConfig,
	}

	return result, validationResult, nil
}

// RenderOnly renders configuration without validation.
// Use this when you need to inspect the rendered output without validation.
//
// Parameters:
//   - ctx: Context for cancellation
//   - provider: StoreProvider for accessing resource stores
//
// Returns:
//   - RenderResult from the render service
//   - Error if rendering fails
func (p *Pipeline) RenderOnly(ctx context.Context, provider stores.StoreProvider) (*renderer.RenderResult, error) {
	return p.renderer.Render(ctx, provider)
}

// ValidateConfig validates a pre-rendered configuration.
// Use this when you already have rendered config and just need validation.
//
// Parameters:
//   - ctx: Context for cancellation
//   - config: The HAProxy configuration content
//   - auxFiles: Auxiliary files for the configuration
//
// Returns:
//   - ValidationResult with validation details
func (p *Pipeline) ValidateConfig(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles) *validation.ValidationResult {
	return p.validator.Validate(ctx, config, auxFiles)
}

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package pipeline

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// pipeline.New has two intentional panics: nil Renderer and nil Validator.
// The doc comment explicitly says "Panics if Renderer or Validator is nil.
// This is intentional: these are required dependencies, and failing at
// construction time is clearer than returning errors at execution time."
//
// The existing TestNew only exercises the happy path. These tests pin both
// panic branches so a future refactor that silently softened them (e.g.,
// `if cfg.Renderer == nil { return nil }`) can't sneak through — that
// regression would shift failure to runtime, blowing up inside the
// reconciler's render/validate hot path with a nil deref instead of at
// startup.
//
// We also pin the Logger=nil → slog.Default() fallback. That's the third
// branch in New(), and it's load-bearing: a regression that panicked on
// nil logger (or worse, dereferenced it at render time) would force every
// caller to construct a logger they don't actually want.

func TestPipelineNew_PanicsOnNilRenderer(t *testing.T) {
	validator := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger: slog.Default(),
	})

	assert.PanicsWithValue(t, "pipeline: Renderer is required",
		func() {
			New(&PipelineConfig{
				Renderer:  nil, // <-- the regression we're guarding against
				Validator: validator,
				Logger:    slog.Default(),
			})
		},
		"pipeline.New MUST panic with the documented exact message when "+
			"Renderer is nil — this is the documented contract that "+
			"surfaces missing wiring at startup instead of inside the "+
			"reconciler hot path. A regression that returned a non-panicking "+
			"value would defer the failure to a nil deref at render time.")
}

func TestPipelineNew_PanicsOnNilValidator(t *testing.T) {
	renderSvc := makeRenderService(t)

	assert.PanicsWithValue(t, "pipeline: Validator is required",
		func() {
			New(&PipelineConfig{
				Renderer:  renderSvc,
				Validator: nil, // <-- the regression we're guarding against
				Logger:    slog.Default(),
			})
		},
		"pipeline.New MUST panic with the documented exact message when "+
			"Validator is nil — same reasoning as the Renderer panic")
}

func TestPipelineNew_NilLoggerFallsBackToDefault(t *testing.T) {
	// Nil logger is NOT a panic — the constructor falls back to
	// slog.Default(). Pin that fallback so a regression that flipped
	// this to a panic doesn't surprise callers who legitimately don't
	// want to inject a logger.
	renderSvc := makeRenderService(t)
	validator := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger: slog.Default(),
	})

	var p *Pipeline
	assert.NotPanics(t, func() {
		p = New(&PipelineConfig{
			Renderer:  renderSvc,
			Validator: validator,
			Logger:    nil, // intentional: must fall back to slog.Default()
		})
	}, "pipeline.New MUST NOT panic on nil Logger — it falls back to "+
		"slog.Default(); flipping this to a panic would force callers to "+
		"construct a logger they don't actually want")
	require.NotNil(t, p)
	assert.NotNil(t, p.logger,
		"after fallback the pipeline's logger must be non-nil so "+
			"render-path log calls don't nil-deref")
}

// Pipeline.Execute and Pipeline.ExecuteWithResult both compute a
// ContentChecksum and propagate it through the result. Drift detection
// downstream uses this checksum to decide whether a config change is
// material. If the two methods accidentally hashed differently (e.g., one
// hashed before and the other after some normalization), drift detection
// would fire on every reload depending on which method last ran.
//
// Pin the invariant that BOTH methods produce IDENTICAL ContentChecksum
// for the same input — this catches a future bug where a maintainer
// optimizes one path but not the other.
func TestPipeline_ContentChecksumInvariantAcrossMethods(t *testing.T) {
	template := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	pipeline := makePipeline(t, template)

	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}
	ctx := context.Background()

	resExec, err := pipeline.Execute(ctx, provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err, "Execute must succeed for the contract test")
	require.NotNil(t, resExec)
	require.NotEmpty(t, resExec.ContentChecksum,
		"Execute MUST always populate ContentChecksum so downstream consumers "+
			"can use it for drift detection without an extra hashing step")

	resWith, _, err := pipeline.ExecuteWithResult(ctx, provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, resWith)
	require.NotEmpty(t, resWith.ContentChecksum,
		"ExecuteWithResult MUST also populate ContentChecksum — the same "+
			"contract as Execute")

	// CRITICAL: same input → same checksum across both methods.
	assert.Equal(t, resExec.ContentChecksum, resWith.ContentChecksum,
		"Execute and ExecuteWithResult MUST produce IDENTICAL "+
			"ContentChecksum for identical input — drift detection downstream "+
			"compares the most recent checksum against the cached one; a "+
			"divergence here would silently fire false-positive drift events "+
			"on every reload that switched code paths")

	// Also pin: HAProxyConfig and AuxiliaryFiles must match too. If
	// these diverge, the checksum check above could pass coincidentally
	// (e.g., both empty) while the actual rendered output differs.
	assert.Equal(t, resExec.HAProxyConfig, resWith.HAProxyConfig,
		"both methods must render the same HAProxyConfig — they share "+
			"the renderer, but pinning this catches a regression that "+
			"diverged the call path")
	assert.Equal(t, resExec.AuxFileCount, resWith.AuxFileCount,
		"both methods must produce the same AuxFileCount")
}

// Multiple invocations of Execute against the same provider/template
// MUST produce the same ContentChecksum. Drift detection compares the
// checksum across reconciliations — if Execute were nondeterministic
// (e.g., depended on map iteration order), drift detection would
// trigger spurious deployments on every reconcile.
func TestPipeline_ContentChecksumIsDeterministicAcrossInvocations(t *testing.T) {
	// Template needs at least one listener so HAProxy semantic
	// validation passes ("no listener" is treated as an error by the
	// haproxy -c binary).
	template := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`
	pipeline := makePipeline(t, template)
	provider := &mockStoreProvider{storeMap: map[string]stores.Store{}}
	ctx := context.Background()

	first, err := pipeline.Execute(ctx, provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	second, err := pipeline.Execute(ctx, provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	third, err := pipeline.Execute(ctx, provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)

	assert.Equal(t, first.ContentChecksum, second.ContentChecksum,
		"ContentChecksum MUST be deterministic across Execute() calls "+
			"with the same input — drift detection compares the new "+
			"checksum against the cached one; nondeterminism would trigger "+
			"false-positive deployments on every reconcile")
	assert.Equal(t, second.ContentChecksum, third.ContentChecksum,
		"ContentChecksum determinism must hold across at least N=3 "+
			"invocations; some nondeterminism only surfaces after multiple "+
			"runs (e.g., Go map iteration order can be the same twice in "+
			"a row by chance)")
}

// makeRenderService builds a minimal RenderService for constructor tests.
// Kept separate from the existing createTestPipeline helper because we
// don't always want to bundle a full pipeline.
func makeRenderService(t *testing.T) *renderer.RenderService {
	t.Helper()
	const template = "global\n    daemon\n"
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: template},
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)
	return renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})
}

// makePipeline mirrors createTestPipeline (existing helper in pipeline_test.go)
// but lives here so this test file is self-contained — it doesn't import
// from the sibling test file's helper namespace.
func makePipeline(t *testing.T, template string) *Pipeline {
	t.Helper()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: template},
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)
	renderSvc := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})
	validationSvc := validation.NewValidationService(&validation.ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})
	return New(&PipelineConfig{
		Renderer:  renderSvc,
		Validator: validationSvc,
		Logger:    slog.Default(),
	})
}

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package testrunner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// assertDeterministic catches non-deterministic templates (the
// classic culprit being map-iteration order) by rendering twice and
// comparing the outputs. Two of its load-bearing failure modes were
// uncovered:
//
//  1. Main HAProxy config differs between renders → assertion MUST
//     fail with a "differs between renders" message that includes a
//     unified diff. Without this branch, real non-deterministic
//     templates would silently pass validation tests and produce
//     flaky deployments where every reconciliation pushes a slightly
//     different HAProxy config (provoking spurious reloads, hash
//     mismatches in deployer, etc).
//
//  2. Auxiliary files differ between renders → assertion MUST fail.
//     Same reasoning as above but for map files / SSL certs /
//     general files.
//
// Existing tests cover the happy path and the "no first render"
// early return. We test the divergence-detection contract by
// passing a hand-crafted firstConfig/firstAuxFiles that doesn't
// match what the template will produce on the second render — the
// comparison logic is what's load-bearing, not the source of the
// non-determinism.

// determinismTestRunner builds a Runner with the minimal fields the
// assertion actually touches: config + logger.
func determinismTestRunner(t *testing.T) *Runner {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return &Runner{
		config: &config.Config{},
		logger: logger,
	}
}

// determinismRenderDeps builds a RenderDependencies with a working
// engine that produces a known-deterministic output. Tests then pass
// a deliberately-different firstConfig/firstAuxFiles to exercise
// the divergence path — the second render will produce the engine's
// real output, which won't match.
func determinismRenderDeps(t *testing.T, template string) *RenderDependencies {
	t.Helper()

	tmpDir := t.TempDir()
	validationPaths := &dataplane.ValidationPaths{
		SSLCertsDir:       filepath.Join(tmpDir, "ssl"),
		CRTListDir:        filepath.Join(tmpDir, "ssl"),
		MapsDir:           filepath.Join(tmpDir, "maps"),
		GeneralStorageDir: filepath.Join(tmpDir, "files"),
		ConfigFile:        filepath.Join(tmpDir, "haproxy.cfg"),
	}

	engine, err := templating.New(map[string]string{"haproxy.cfg": template}, nil)
	require.NoError(t, err)

	return &RenderDependencies{
		Engine:          engine,
		Stores:          make(map[string]stores.Store),
		ValidationPaths: validationPaths,
	}
}

func TestAssertDeterministic_DetectsConfigDivergence(t *testing.T) {
	// Template renders deterministically. We pass a firstConfig that
	// differs from what the engine will produce on the second render
	// — the assertion MUST detect the divergence and fail.
	r := determinismTestRunner(t)
	deps := determinismRenderDeps(t, "# real-output\nfrontend test\n  bind *:80")

	assertion := &config.ValidationAssertion{
		Type:        "deterministic",
		Description: "MUST catch divergence",
	}

	// Deliberately wrong firstConfig — this simulates the case where
	// the FIRST render produced different output than the SECOND.
	// In production a non-deterministic template (e.g. one that
	// iterates map keys without sorting) would produce exactly this
	// shape of divergence between renders.
	const wrongFirstConfig = "# stale-render-output\nfrontend test\n  bind *:8080"
	emptyAux := &dataplane.AuxiliaryFiles{}

	result := r.assertDeterministic(assertion, wrongFirstConfig, emptyAux, deps)

	assert.False(t, result.Passed,
		"assertDeterministic MUST fail when firstConfig differs from "+
			"the second render — without this branch, non-deterministic "+
			"templates would silently pass validation and produce flaky "+
			"deployments where every reconciliation pushes a slightly "+
			"different HAProxy config (spurious reloads, hash mismatches "+
			"in the deployer, etc)")
	assert.Equal(t, "deterministic", result.Type)
	assert.Contains(t, result.Error, "differs between renders",
		"the failure message MUST mention divergence so operators can "+
			"diagnose 'why is the assertion failing?' without re-reading "+
			"the test source")
	// The error must include enough context to locate the divergence
	// (a unified diff, per the implementation). Without it, operators
	// would see "differs" with no signal where to look.
	assert.True(t,
		strings.Contains(result.Error, "stale-render-output") ||
			strings.Contains(result.Error, "real-output"),
		"failure message MUST include a diff or the divergent text "+
			"so operators can pinpoint the non-determinism — error was: %s",
		result.Error)
}

func TestAssertDeterministic_DetectsAuxiliaryFileDivergence(t *testing.T) {
	// Same divergence-detection contract for aux files. Pass a
	// firstAuxFiles that contains a map file the second render
	// won't produce — the comparison branch MUST fail the assertion.
	r := determinismTestRunner(t)
	// Template that produces NO aux files on the second render.
	deps := determinismRenderDeps(t, "# config without aux\nfrontend test\n  bind *:80")

	assertion := &config.ValidationAssertion{
		Type:        "deterministic",
		Description: "MUST catch aux divergence",
	}

	// Engine will produce this exact main config on the second
	// render — match it so the FIRST comparison (main config)
	// passes and we exercise the SECOND comparison (aux files).
	const matchingFirstConfig = "# config without aux\nfrontend test\n  bind *:80\n"

	// Hand-crafted aux file that the (aux-less) template will NOT
	// reproduce on the second render.
	firstAuxFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "phantom.map", Content: "phantom-content-not-in-template\n"},
		},
	}

	result := r.assertDeterministic(assertion, matchingFirstConfig, firstAuxFiles, deps)

	assert.False(t, result.Passed,
		"assertDeterministic MUST fail when first/second aux files diverge — "+
			"map file order, SSL cert content, or general file content that "+
			"differs between renders is just as harmful as main-config "+
			"divergence (causes spurious aux file syncs to HAProxy)")
	assert.NotEmpty(t, result.Error,
		"the failure message MUST be populated so operators can diagnose "+
			"WHICH aux file diverged — empty error string would force them "+
			"back to source code")
}

// TestDeterminismCheckIsAutomatic pins that the runner appends the check to
// every rendering test, not only the ones whose author asked for it. It was
// opt-in and 6 of 722 chart tests opted in, which let two host-map builders
// ship ranging a map[string]bool unsorted — every declared assertion passed,
// because assertions match entries and the defect is in their order.
func TestDeterminismCheckIsAutomatic(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: "global\n  daemon\n"},
		ValidationTests: map[string]config.ValidationTest{
			"no-assertions-declared": {
				Description: "declares nothing; the runner must still check determinism",
			},
		},
	}

	engine, err := templating.New(map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template}, nil)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	paths := &dataplane.ValidationPaths{
		TempDir:           tmpDir,
		SSLCertsDir:       filepath.Join(tmpDir, "ssl"),
		CRTListDir:        filepath.Join(tmpDir, "ssl"),
		MapsDir:           filepath.Join(tmpDir, "maps"),
		GeneralStorageDir: filepath.Join(tmpDir, "files"),
		ConfigFile:        filepath.Join(tmpDir, "haproxy.cfg"),
	}
	runner := New(cfg, engine, paths, &Options{Logger: slog.Default(), Workers: 1})
	results, err := runner.RunTests(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, results.TestResults, 1)

	var found bool
	for _, a := range results.TestResults[0].Assertions {
		if a.Type == "deterministic" {
			found = true
			assert.True(t, a.Passed, a.Error)
		}
	}
	assert.True(t, found, "a test declaring no assertions must still be checked for determinism")
}

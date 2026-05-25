// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Build-tag-gated to !ingress_conformance so these tests run as part
// of the standard `make test` (no tag) but stay OUT of the in-cluster
// conformance binary built with `-tags=ingress_conformance`. Otherwise
// they'd run inside the container alongside TestIngressConformance,
// and TestShardEnvValidation uses t.Setenv on SHARD_ID/SHARD_COUNT —
// a race against TestIngressConformance's env read at startup.

//go:build !ingress_conformance

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrepareShardedFeaturesCoverage is the single load-bearing invariant
// of the sharding split: aggregating every shard's output for a given
// shardCount must reproduce the unsharded scenario set exactly — no
// dropped scenarios, no duplicates. A regression here would silently
// hide scenarios from CI while still showing every shard green.
//
// Runs against a synthesized features tree so the test stays a pure
// unit test — no KUBECONFIG, no kind cluster, fast feedback while
// iterating on the splitter.
func TestPrepareShardedFeaturesCoverage(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "features"), 0o755))

	pathRules := buildFeature("Path rules", 16, true)
	hostRules := buildFeature("Host rules", 6, true)
	defaultBackend := buildFeature("Default backend", 1, true)
	ingressClass := buildFeature("Ingress class", 1, false)
	loadBalancing := buildFeature("Load balancing", 1, true)
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(src, "features", name), []byte(content), 0o644))
	}
	write("path_rules.feature", pathRules)
	write("host_rules.feature", hostRules)
	write("default_backend.feature", defaultBackend)
	write("ingress_class.feature", ingressClass)
	write("load_balancing.feature", loadBalancing)

	baseline := scenarioNames(t, filepath.Join(src, "features"))
	require.Len(t, baseline, 25, "baseline scenario count")

	for _, shardCount := range []int{1, 2, 3, 4, 5, 8} {
		t.Run(fmt.Sprintf("shardCount=%d", shardCount), func(t *testing.T) {
			var aggregated []string
			for shardID := 1; shardID <= shardCount; shardID++ {
				dest := t.TempDir()
				require.NoError(t,
					prepareShardedFeatures(src, dest, shardID, shardCount),
					"shard %d/%d", shardID, shardCount)
				aggregated = append(aggregated,
					scenarioNames(t, filepath.Join(dest, "features"))...)
			}
			sort.Strings(aggregated)
			// Flat sorted comparison preserves multiplicity: if any
			// scenario got dropped, duplicated, or assigned to multiple
			// shards, the slices will differ in length or contents.
			// Compares title-only because synthesized scenarios above
			// have unique titles; the upstream-fixture test below
			// covers the real-world duplicate-title shape.
			assert.Equal(t, baseline, aggregated,
				"aggregated shard scenarios must match the unsharded set")
		})
	}
}

// TestPrepareShardedFeaturesAgainstUpstreamFixture runs the splitter
// against an actual clone of the upstream conformance repo if one
// exists at $INGRESS_CONFORMANCE_UPSTREAM_DIR (path to a checked-out
// kubernetes-sigs/ingress-controller-conformance tree at our pinned
// SHA). Skipped otherwise — keeps CI fast while letting a developer
// validate against real fixtures via:
//
//	cd /tmp && git clone https://github.com/kubernetes-sigs/ingress-controller-conformance.git icc
//	git -C /tmp/icc checkout d920ed36a0076e169a9a329a850844ab3a695ae8
//	INGRESS_CONFORMANCE_UPSTREAM_DIR=/tmp/icc make test
//
// Same no-drop / no-duplicate invariant as the synthesized test, but
// over the real path_rules.feature (16 scenarios, real Background:
// block, real Gherkin shape).
func TestPrepareShardedFeaturesAgainstUpstreamFixture(t *testing.T) {
	upstream := os.Getenv("INGRESS_CONFORMANCE_UPSTREAM_DIR")
	if upstream == "" {
		t.Skip("set INGRESS_CONFORMANCE_UPSTREAM_DIR to a checked-out upstream repo to run this")
	}
	if _, err := os.Stat(filepath.Join(upstream, "features", "path_rules.feature")); err != nil {
		t.Skipf("upstream features dir not found under %s: %v", upstream, err)
	}

	baseline := scenarioNames(t, filepath.Join(upstream, "features"))
	require.NotEmpty(t, baseline, "real upstream must have scenarios to test against")

	const shardCount = 4
	var aggregated []string
	for shardID := 1; shardID <= shardCount; shardID++ {
		dest := t.TempDir()
		require.NoError(t,
			prepareShardedFeatures(upstream, dest, shardID, shardCount),
			"shard %d/%d", shardID, shardCount)
		aggregated = append(aggregated,
			scenarioNames(t, filepath.Join(dest, "features"))...)
	}
	sort.Strings(aggregated)
	// Real upstream path_rules.feature contains two scenarios with
	// identical titles at different positions (lines 181 and 188 at
	// the pinned SHA — both "An Ingress with a trailing slashes in
	// a prefix path rule should ignore the trailing slash …"). A
	// flat sorted comparison preserves that multiplicity; a set
	// comparison would mask a real dropped-scenario regression.
	assert.Equal(t, baseline, aggregated,
		"sharding the real upstream features must reproduce the same scenario set (with multiplicity)")
}

// TestShardedFeatureFilesAreStandaloneValid asserts every shard's output
// is a syntactically-valid godog feature file — Feature: line is present,
// the Background: block (when there is one upstream) is preserved.
// godog refuses to load a feature file missing either; we have to keep
// both regardless of which scenarios got assigned to this shard.
func TestShardedFeatureFilesAreStandaloneValid(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "features"), 0o755))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(src, "features", "path_rules.feature"),
			[]byte(buildFeature("Path rules", 12, true)),
			0o644,
		),
	)

	const shardCount = 4
	for shardID := 1; shardID <= shardCount; shardID++ {
		dest := t.TempDir()
		require.NoError(t, prepareShardedFeatures(src, dest, shardID, shardCount))

		raw, err := os.ReadFile(filepath.Join(dest, "features", "path_rules.feature"))
		require.NoErrorf(t, err, "shard %d", shardID)
		body := string(raw)
		assert.Containsf(t, body, "Feature: Path rules", "shard %d missing Feature: line", shardID)
		assert.Containsf(t, body, "Background:", "shard %d missing Background: block", shardID)
		assert.GreaterOrEqualf(t,
			strings.Count(body, "Scenario:"), 1,
			"shard %d has no scenarios — empty shards should be skipped, not written",
			shardID)
	}
}

// TestScenarioNamesPicksUpOutlines locks in parity between the test
// helper (scenarioNames) and the production splitter
// (shardPathRulesScenarios) on `Scenario Outline:` lines. The
// splitter treats both `Scenario:` and `Scenario Outline:` as
// scenario boundaries; if the helper ever drops the Outline branch
// the coverage-invariant tests would silently pass on a future
// upstream pin bump that adds outlines (both baseline and aggregated
// would miss the outlines equally, comparison stays equal). Guard
// the parity explicitly.
func TestScenarioNamesPicksUpOutlines(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mixed.feature"), []byte(`Feature: mixed
  Scenario: plain scenario one
    Given a thing

  Scenario Outline: parameterised scenario
    Given a <thing>

    Examples:
      | thing |
      | foo   |

  Scenario: plain scenario two
    Then done
`), 0o600))

	got := scenarioNames(t, dir)
	assert.Equal(t, []string{
		"parameterised scenario",
		"plain scenario one",
		"plain scenario two",
	}, got, "helper must surface both Scenario: and Scenario Outline: titles")
}

// TestShardEnvValidation covers the malformed-env guard rails. The
// production path surfaces these as a test failure via require.NoError;
// here we just check shardEnv's return contract directly.
func TestShardEnvValidation(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		count     string
		expectOK  bool
		expectErr bool
	}{
		{name: "unset", id: "", count: "", expectOK: false},
		{name: "valid", id: "2", count: "4", expectOK: true},
		{name: "id without count", id: "1", count: "", expectErr: true},
		{name: "count without id", id: "", count: "4", expectErr: true},
		{name: "id non-numeric", id: "x", count: "4", expectErr: true},
		{name: "count non-numeric", id: "1", count: "y", expectErr: true},
		{name: "id zero", id: "0", count: "4", expectErr: true},
		{name: "id greater than count", id: "5", count: "4", expectErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHARD_ID", tc.id)
			t.Setenv("SHARD_COUNT", tc.count)
			id, total, ok, err := shardEnv()
			if tc.expectErr {
				assert.Error(t, err)
				assert.False(t, ok)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectOK, ok)
			if tc.expectOK {
				assert.Equal(t, 2, id)
				assert.Equal(t, 4, total)
			}
		})
	}
}

// buildFeature synthesizes a Gherkin feature file shaped like the
// upstream ones: a feature-level @tag line, Feature: header, optional
// Background:, then N numbered scenarios with one Given step each.
func buildFeature(name string, scenarioCount int, withBackground bool) string {
	var sb strings.Builder
	sb.WriteString("@synthetic @test\n")
	sb.WriteString("Feature: " + name + "\n")
	sb.WriteString("  Synthesized feature for shard-splitter tests.\n\n")
	if withBackground {
		sb.WriteString("  Background:\n")
		sb.WriteString("    Given a baseline resource exists\n\n")
	}
	for i := 1; i <= scenarioCount; i++ {
		fmt.Fprintf(&sb, "  Scenario: %s scenario %d\n", name, i)
		sb.WriteString("    Given a precondition\n")
		sb.WriteString("    When something happens\n")
		sb.WriteString("    Then an outcome is observed\n\n")
	}
	return sb.String()
}

// scenarioNames returns the alphabetically-sorted set of `Scenario:` titles
// across every .feature file in dir. Used to compare baseline vs.
// aggregated shard coverage.
// scenarioNames must recognise the same scenario-boundary shapes as
// the splitter (shardPathRulesScenarios) — both `Scenario:` and
// `Scenario Outline:`. Without the Outline branch a future upstream
// pin bump that adds outlines would silently pass the coverage
// invariant: the helper would miss the outlines in both baseline and
// aggregated slices, comparison stays equal, real sharding bugs hide.
// TestScenarioNamesPicksUpOutlines guards the parity.
func scenarioNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".feature") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "Scenario Outline:"):
				out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "Scenario Outline:")))
			case strings.HasPrefix(trimmed, "Scenario:"):
				out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "Scenario:")))
			}
		}
	}
	sort.Strings(out)
	return out
}

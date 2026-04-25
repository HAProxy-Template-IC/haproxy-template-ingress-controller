// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// shouldSkipTest decides whether a validation test gets executed based on
// the test's declared MinHAProxyVersion vs. the runner's detected HAProxy
// version. The function has FIVE distinct branches and zero direct test
// coverage. Each branch maps to a distinct user-visible behaviour:
//
//   1. MinHAProxyVersion empty           → run (no version constraint)
//   2. haproxyVersion unknown (nil)      → SKIP with "version is unknown"
//   3. MinHAProxyVersion unparseable     → run anyway, log warning
//   4. local version < min version       → SKIP with version-mismatch
//   5. local version >= min version      → run
//
// Three branches are particularly load-bearing:
//
// (b) The "unknown version → SKIP" rule prevents tests that genuinely need
//     a feature from running against an unknown version (which might lack
//     the feature) and producing misleading PASS/FAIL results. A regression
//     that flipped this to "run anyway" would let v3.3-only tests
//     accidentally execute on a v3.0-or-unknown host and surface as test
//     failures attributed to the user's config rather than the test
//     environment.
//
// (c) The "unparseable → run anyway" rule is the operator-friendliness
//     branch: a typo in MinHAProxyVersion ("3.3.beta", "v3.3", etc.)
//     mustn't silently skip the test, because skipping a test on a typo
//     would let bugs slip through. Logging a warning + running is the
//     safe default. A regression that returned a skip reason here would
//     silently disable tests on every misformatted version string.
//
// (e) The version-meets-requirement happy path must produce empty string
//     (== "do not skip"). A regression that returned non-empty would skip
//     every test even when the version constraint is satisfied.

// minimalRunner returns a Runner with just the fields shouldSkipTest
// touches: logger and haproxyVersion. The other Runner fields aren't
// read on this code path.
func minimalRunner(version *dataplane.Version) *Runner {
	return &Runner{
		logger:         testutil.NewTestLogger(),
		haproxyVersion: version,
	}
}

func ver(major, minor int, full string) *dataplane.Version {
	return &dataplane.Version{Major: major, Minor: minor, Full: full}
}

func TestShouldSkipTest(t *testing.T) {
	tests := []struct {
		name          string
		runnerVersion *dataplane.Version
		minVersion    string
		wantSkip      bool
		wantSubstr    string // expected fragment of the skip reason (if skipping)
		why           string
	}{
		{
			name:          "no version constraint runs unconditionally",
			runnerVersion: ver(3, 0, "v3.0.0"),
			minVersion:    "",
			wantSkip:      false,
			why:           "MinHAProxyVersion empty means the test author didn't gate by version",
		},
		{
			name:          "no version constraint runs even when local version is unknown",
			runnerVersion: nil,
			minVersion:    "",
			wantSkip:      false,
			why: "without a constraint we don't care about the local version — a " +
				"regression that flipped this would skip every test whenever HAProxy " +
				"version detection failed",
		},
		{
			name:          "unknown local version SKIPS constrained tests with explicit reason",
			runnerVersion: nil,
			minVersion:    "3.3",
			wantSkip:      true,
			wantSubstr:    "version is unknown",
			why: "unknown version must SKIP rather than 'run anyway' because the " +
				"feature might be missing — a regression that ran the test would " +
				"produce misleading PASS/FAIL results attributed to user config " +
				"rather than the test environment",
		},
		{
			name:          "unparseable min-version runs ANYWAY (operator-typo safety)",
			runnerVersion: ver(3, 0, "v3.0.0"),
			minVersion:    "v3.3.beta", // unparseable: ParseVersionString rejects 'v' prefix etc.
			wantSkip:      false,
			why: "a typo in MinHAProxyVersion mustn't silently skip the test — " +
				"skipping on typos would let bugs slip through. Logging a warning + " +
				"running is the safe default",
		},
		{
			name:          "older local version SKIPS with version-mismatch detail",
			runnerVersion: ver(3, 0, "v3.0.0"),
			minVersion:    "3.3",
			wantSkip:      true,
			wantSubstr:    "v3.0.0", // the detected version must appear in the skip reason
			why: "skip reason must name BOTH the required min and the detected " +
				"version so an operator triaging a CI skip immediately sees why",
		},
		{
			name:          "older minor version still SKIPS",
			runnerVersion: ver(3, 1, "v3.1.0"),
			minVersion:    "3.3",
			wantSkip:      true,
			wantSubstr:    "3.3",
		},
		{
			name:          "exactly matching version RUNS (boundary)",
			runnerVersion: ver(3, 3, "v3.3.0"),
			minVersion:    "3.3",
			wantSkip:      false,
			why: "the comparison must be >= NOT >, so a test that requires 3.3 and " +
				"finds exactly 3.3 must run — a regression to strict > would skip " +
				"tests on the exact version they were written for",
		},
		{
			name:          "newer minor version RUNS",
			runnerVersion: ver(3, 5, "v3.5.0"),
			minVersion:    "3.3",
			wantSkip:      false,
		},
		{
			name:          "newer major version RUNS",
			runnerVersion: ver(4, 0, "v4.0.0"),
			minVersion:    "3.3",
			wantSkip:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := minimalRunner(tt.runnerVersion)
			vt := &config.ValidationTest{
				MinHAProxyVersion: tt.minVersion,
			}

			reason := r.shouldSkipTest(vt)

			if tt.wantSkip {
				assert.NotEmpty(t, reason,
					"expected a skip reason; %s", tt.why)
				if tt.wantSubstr != "" {
					assert.Contains(t, reason, tt.wantSubstr,
						"skip reason must contain %q so the operator sees relevant context",
						tt.wantSubstr)
				}
			} else {
				assert.Empty(t, reason,
					"expected the test to run (empty skip reason); %s", tt.why)
			}
		})
	}
}

// TestShouldSkipTest_SkipReasonAlwaysReferencesMinVersion pins that the
// skip reason ALWAYS includes the requested min version when skipping
// (regardless of which branch produced the skip). Without this an
// operator would have to dig through the test definition to figure out
// what the test wanted.
func TestShouldSkipTest_SkipReasonAlwaysReferencesMinVersion(t *testing.T) {
	const minVersion = "3.3"

	t.Run("unknown version branch references min", func(t *testing.T) {
		r := minimalRunner(nil)
		reason := r.shouldSkipTest(&config.ValidationTest{MinHAProxyVersion: minVersion})
		assert.Contains(t, reason, minVersion,
			"the unknown-version skip reason must reference the required min "+
				"so the operator knows what the test wanted")
	})

	t.Run("version-too-old branch references min", func(t *testing.T) {
		r := minimalRunner(ver(3, 0, "v3.0.0"))
		reason := r.shouldSkipTest(&config.ValidationTest{MinHAProxyVersion: minVersion})
		assert.Contains(t, reason, minVersion,
			"the version-too-old skip reason must reference the required min")
	})
}

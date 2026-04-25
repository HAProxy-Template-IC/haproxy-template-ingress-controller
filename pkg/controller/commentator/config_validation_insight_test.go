// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// configInsight and validationInsight produce operator-facing log messages
// for config-validation and HAProxy-validation events. Both have multiple
// load-bearing CONDITIONAL fragments / formatting contracts that govern
// what operators see during a validation cycle:
//
//  configInsight:
//   * ConfigValidationResponse "✓ OK" vs "✗ FAILED" — boolean Valid flips
//     both the symbol and the text. A regression flipping these would
//     silently invert dashboard pass/fail signals.
//   * ConfigValidationResponse ", N errors" — appended only when Valid=false.
//     Empty errors on a failing validator must still produce the trailing
//     count so operators see the failure shape.
//   * ConfigInvalidEvent first-error PREVIEW — the per-validator breakdown
//     truncates long error messages to maxErrorPreviewLength (80) with a
//     "..." suffix so the summary line stays scannable in log viewers.
//     Short errors must NOT be truncated. A regression off-by-one in this
//     boundary either chops legible errors or floods log lines.
//   * ConfigInvalidEvent ": <breakdown>" — appended only when at least one
//     validator has errors. An empty ValidationErrors map (or all-empty
//     slices) must produce no trailing colon, otherwise the summary reads
//     as "...0 validators:" which confuses operators.
//
//  validationInsight:
//   * ValidationCompletedEvent " with N warnings" — appended only when
//     Warnings is non-empty. Operators count warnings to detect drift.
//   * ValidationCompletedEvent " (trigger: ...)" — appended only when
//     TriggerReason is non-empty; same on-call routing semantics as
//     other trigger fragments (template_deployment_insight).
//   * ValidationFailedEvent " (trigger: ...)" — same conditional rule
//     as ValidationCompletedEvent. Failed validations without a trigger
//     reason came from older callers and must NOT show "(trigger: )".

func TestConfigInsight_ValidationResponse_StatusFlipAndErrorCount(t *testing.T) {
	tests := []struct {
		name         string
		valid        bool
		errors       []string
		wantSymbol   string // expected status symbol "✓" or "✗"
		wantStatus   string // "OK" or "FAILED"
		wantContains string // additional fragment that must appear
		notContains  string // fragment that must be ABSENT
	}{
		{
			name:        "valid → ✓ OK, no error count",
			valid:       true,
			errors:      nil,
			wantSymbol:  "✓",
			wantStatus:  "OK",
			notContains: ", 0 errors", // count must NOT appear when valid
		},
		{
			name:        "valid with leftover empty errors slice → still ✓ OK",
			valid:       true,
			errors:      []string{}, // empty but non-nil
			wantSymbol:  "✓",
			wantStatus:  "OK",
			notContains: "errors", // never the count fragment when valid
		},
		{
			name:         "invalid with one error → ✗ FAILED, ', 1 errors'",
			valid:        false,
			errors:       []string{"some error"},
			wantSymbol:   "✗",
			wantStatus:   "FAILED",
			wantContains: ", 1 errors",
		},
		{
			name:         "invalid with empty errors → ✗ FAILED, ', 0 errors'",
			valid:        false,
			errors:       nil,
			wantSymbol:   "✗",
			wantStatus:   "FAILED",
			wantContains: ", 0 errors", // count must surface even when slice is empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := cvECommentator()
			evt := ctlevents.NewConfigValidationResponse(
				"req-1", "basic", tt.valid, tt.errors)

			insight, attrs := ec.configInsight(evt, nil)

			require.NotEmpty(t, insight,
				"every ConfigValidationResponse must produce an operator log message")
			assert.Contains(t, insight, "Validator 'basic':",
				"the validator name must always surface so operators can identify "+
					"which validator emitted this status")
			assert.Contains(t, insight, tt.wantSymbol,
				"valid=%v MUST produce %q symbol — a regression flipping ✓/✗ would "+
					"silently invert dashboard pass/fail signals", tt.valid, tt.wantSymbol)
			assert.Contains(t, insight, tt.wantStatus,
				"valid=%v MUST produce %q status text — symbol+text must match",
				tt.valid, tt.wantStatus)
			if tt.wantContains != "" {
				assert.Contains(t, insight, tt.wantContains,
					"failing validators MUST surface the error count as ', N errors' "+
						"so operators see the failure shape even when the slice is empty")
			}
			if tt.notContains != "" {
				assert.NotContains(t, insight, tt.notContains,
					"valid responses MUST NOT carry the error-count fragment")
			}
			assert.Equal(t, tt.valid, cvAttr(attrs, "valid"),
				"the structured valid attr must match the insight symbol — "+
					"a text/attr drift would break log↔metrics correlation")
			assert.Equal(t, len(tt.errors), cvAttr(attrs, "error_count"),
				"the structured error_count attr must match the message count")
		})
	}
}

func TestConfigInsight_InvalidEvent_FirstErrorTruncationBoundary(t *testing.T) {
	// The per-validator breakdown shows the FIRST error as a sample.
	// Long errors are truncated to maxErrorPreviewLength (80) with the
	// last 3 chars replaced by "...". This test pins the boundary so a
	// regression off-by-one either chops legible errors or floods lines.
	tests := []struct {
		name             string
		firstError       string
		wantTruncated    bool   // does the message contain "..."?
		wantMaxFragLen   int    // max length of the quoted-error fragment in the summary
		wantContainsPart string // a recognizable substring that MUST survive truncation
	}{
		{
			name:             "short error (< 80) preserved verbatim",
			firstError:       "short error message",
			wantTruncated:    false,
			wantContainsPart: "short error message",
		},
		{
			name: "exactly 80-char error preserved verbatim (boundary: <=80 not truncated)",
			// Exactly 80 chars; len("...") == 3 so the truncation branch only
			// fires when len > 80.
			firstError:       strings.Repeat("a", 80),
			wantTruncated:    false,
			wantContainsPart: strings.Repeat("a", 80),
		},
		{
			name:             "81-char error triggers truncation to 77 chars + '...'",
			firstError:       strings.Repeat("b", 81),
			wantTruncated:    true,
			wantContainsPart: strings.Repeat("b", 77), // first 77 b's must survive
			wantMaxFragLen:   80,                      // 77 chars + "..." = 80
		},
		{
			name:             "very long error (200 chars) truncated to 77 + '...'",
			firstError:       "RECOGNIZABLE_PREFIX_" + strings.Repeat("c", 200),
			wantTruncated:    true,
			wantContainsPart: "RECOGNIZABLE_PREFIX_", // prefix must survive
			wantMaxFragLen:   80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := cvECommentator()
			evt := ctlevents.NewConfigInvalidEvent("v1", nil, map[string][]string{
				"basic": {tt.firstError},
			})

			insight, attrs := ec.configInsight(evt, nil)

			require.NotEmpty(t, insight)
			assert.Contains(t, insight, "Configuration validation failed",
				"summary must lead with the canonical failure header for log scrapers")
			assert.Contains(t, insight, "basic: 1 errors",
				"per-validator breakdown must surface name and count")
			assert.Contains(t, insight, tt.wantContainsPart,
				"the recognizable part of the first error must survive truncation — "+
					"a regression that chopped this would prevent operators from "+
					"identifying the error class")

			if tt.wantTruncated {
				assert.Contains(t, insight, "...",
					"errors longer than maxErrorPreviewLength MUST be truncated with "+
						"a '...' suffix so log lines stay scannable")
				// Verify no fragment exceeds the truncation budget — find the
				// quoted error in the message and check its length.
				start := strings.Index(insight, "(e.g., \"")
				require.GreaterOrEqual(t, start, 0,
					"breakdown must include the (e.g., \"...\") fragment for context")
				end := strings.LastIndex(insight, "\")")
				require.Greater(t, end, start)
				quoted := insight[start+len("(e.g., \"") : end]
				assert.LessOrEqual(t, len(quoted), tt.wantMaxFragLen,
					"truncated error fragment %q must not exceed %d chars; "+
						"a regression here floods log lines", quoted, tt.wantMaxFragLen)
			} else {
				// Short / boundary errors must NOT have the "..." suffix added
				// (the original error itself might contain "..." but the test
				// inputs here don't).
				assert.NotContains(t, insight, "...",
					"errors at or below maxErrorPreviewLength MUST NOT be truncated; "+
						"a regression here chops legible errors")
			}

			// Full untruncated errors must still be available as a structured
			// attribute for debugging dashboards.
			validationErrs, ok := cvAttr(attrs, "validation_errors").(map[string][]string)
			require.True(t, ok,
				"validation_errors attr must be present as map[string][]string for dashboards")
			assert.Equal(t, []string{tt.firstError}, validationErrs["basic"],
				"the structured attr must carry the FULL untruncated error so "+
					"debugging dashboards can show the original message")
		})
	}
}

func TestConfigInsight_InvalidEvent_BreakdownOnlyWhenErrorsPresent(t *testing.T) {
	// The ": <breakdown>" suffix is appended only when at least one
	// validator has errors. Empty maps or all-empty-slice maps must
	// produce a clean header with no trailing colon.
	tests := []struct {
		name             string
		validationErrors map[string][]string
		wantContains     string // a fragment that must appear
		wantTrailingNo   string // a fragment that must NOT appear (the colon hint)
	}{
		{
			name:             "non-empty errors → breakdown appended",
			validationErrors: map[string][]string{"basic": {"oops"}},
			wantContains:     ": basic: 1 errors",
		},
		{
			name:             "empty validator map → no breakdown, no trailing colon",
			validationErrors: map[string][]string{},
			wantTrailingNo:   "0 validators:", // would indicate stale colon
		},
		{
			name: "validator with empty error slice → no breakdown for that entry",
			validationErrors: map[string][]string{
				"basic": {}, // empty slice for a validator
			},
			// breakdown loop only appends when len(errs) > 0, so no ": basic:"
			wantTrailingNo: ": basic:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := cvECommentator()
			evt := ctlevents.NewConfigInvalidEvent("v1", nil, tt.validationErrors)

			insight, _ := ec.configInsight(evt, nil)

			require.NotEmpty(t, insight)
			assert.Contains(t, insight, "Configuration validation failed",
				"failure header must always appear for log-scraper grep")

			if tt.wantContains != "" {
				assert.Contains(t, insight, tt.wantContains,
					"non-empty errors must produce the per-validator breakdown")
			}
			if tt.wantTrailingNo != "" {
				assert.NotContains(t, insight, tt.wantTrailingNo,
					"empty/no-error validator data MUST NOT produce a stale "+
						"trailing-colon fragment that confuses operators")
			}
		})
	}
}

func TestValidationInsight_CompletedAndFailed_ConditionalFragments(t *testing.T) {
	// ValidationCompletedEvent and ValidationFailedEvent both have
	// optional " (trigger: ...)" suffixes governed by TriggerReason.
	// ValidationCompletedEvent additionally appends " with N warnings"
	// only when Warnings is non-empty. These conditional fragments are
	// what tells operators why a validation ran and whether HAProxy
	// flagged anything that doesn't fail the build.
	type tcCompleted struct {
		name          string
		warnings      []string
		trigger       string
		wantContains  []string // fragments that MUST appear
		wantNotInsigh []string // fragments that must be ABSENT
	}
	completedCases := []tcCompleted{
		{
			name:          "no warnings, no trigger → both fragments absent",
			warnings:      nil,
			trigger:       "",
			wantContains:  []string{"HAProxy configuration validation succeeded"},
			wantNotInsigh: []string{"warnings", "(trigger:"},
		},
		{
			name:          "warnings only → 'with N warnings' appended, no trigger",
			warnings:      []string{"deprecated directive"},
			trigger:       "",
			wantContains:  []string{"with 1 warnings"},
			wantNotInsigh: []string{"(trigger:"},
		},
		{
			name:          "trigger only → '(trigger: ...)' appended, no warnings",
			warnings:      nil,
			trigger:       "config_change",
			wantContains:  []string{"(trigger: config_change)"},
			wantNotInsigh: []string{"warnings"},
		},
		{
			name:         "warnings + trigger → both fragments appended",
			warnings:     []string{"a", "b", "c"},
			trigger:      "debounce_timer",
			wantContains: []string{"with 3 warnings", "(trigger: debounce_timer)"},
		},
	}

	for _, tt := range completedCases {
		t.Run("completed/"+tt.name, func(t *testing.T) {
			ec := cvECommentator()
			evt := ctlevents.NewValidationCompletedEvent(tt.warnings, 42, tt.trigger, nil, false)

			insight, attrs := ec.validationInsight(evt, nil)

			require.NotEmpty(t, insight)
			for _, want := range tt.wantContains {
				assert.Contains(t, insight, want,
					"completed-validation insight MUST contain %q so operators "+
						"see the warning count / trigger context", want)
			}
			for _, notWant := range tt.wantNotInsigh {
				assert.NotContains(t, insight, notWant,
					"completed-validation insight MUST NOT contain %q when the "+
						"corresponding field is empty — stale fragments confuse operators",
					notWant)
			}
			// duration_ms is always present so latency dashboards can group
			// regardless of the conditional branches.
			assert.Equal(t, int64(42), cvAttr(attrs, "duration_ms"),
				"duration_ms attr must always be present for dashboards")
			assert.Equal(t, tt.trigger, cvAttr(attrs, "trigger_reason"),
				"trigger_reason attr must always be present (even empty) so "+
					"dashboards group consistently regardless of branch")
		})
	}

	type tcFailed struct {
		name         string
		errors       []string
		trigger      string
		wantContains []string
		notContains  []string
	}
	failedCases := []tcFailed{
		{
			name:         "no trigger → '(trigger:' fragment absent",
			errors:       []string{"bad config"},
			trigger:      "",
			wantContains: []string{"failed with 1 errors"},
			notContains:  []string{"(trigger:"},
		},
		{
			name:         "with trigger → '(trigger: ...)' appended",
			errors:       []string{"bad config", "another"},
			trigger:      "drift_prevention",
			wantContains: []string{"failed with 2 errors", "(trigger: drift_prevention)"},
		},
	}

	for _, tt := range failedCases {
		t.Run("failed/"+tt.name, func(t *testing.T) {
			ec := cvECommentator()
			evt := ctlevents.NewValidationFailedEvent(tt.errors, 100, tt.trigger)

			insight, attrs := ec.validationInsight(evt, nil)

			require.NotEmpty(t, insight)
			for _, want := range tt.wantContains {
				assert.Contains(t, insight, want,
					"failed-validation insight MUST contain %q", want)
			}
			for _, notWant := range tt.notContains {
				assert.NotContains(t, insight, notWant,
					"failed-validation insight MUST NOT contain %q when "+
						"TriggerReason is empty", notWant)
			}
			assert.Equal(t, len(tt.errors), cvAttr(attrs, "error_count"),
				"error_count attr must match the message count")
		})
	}
}

func TestConfigInsight_UnknownEventReturnsEmpty(t *testing.T) {
	// Default arm: events not owned by this insight must produce empty
	// output so the dispatcher can fall through to the next handler.
	ec := cvECommentator()
	other := ctlevents.NewBecameLeaderEvent("pod") // owned by leaderInsight

	insight, attrs := ec.configInsight(other, []any{"keep", "me"})

	assert.Empty(t, insight,
		"unhandled events must produce empty insight (dispatcher skip signal)")
	assert.Equal(t, []any{"keep", "me"}, attrs,
		"attrs must pass through UNCHANGED on the default arm so the next "+
			"handler in the dispatch chain sees the original slice")
}

func TestValidationInsight_UnknownEventReturnsEmpty(t *testing.T) {
	ec := cvECommentator()
	other := ctlevents.NewBecameLeaderEvent("pod")

	insight, attrs := ec.validationInsight(other, []any{"a", 1})

	assert.Empty(t, insight,
		"unhandled events must produce empty insight (dispatcher skip signal)")
	assert.Equal(t, []any{"a", 1}, attrs,
		"attrs must pass through UNCHANGED on the default arm")
}

// cvECommentator returns a minimal EventCommentator with just the fields
// configInsight / validationInsight touch (the ring buffer, used for the
// optional ConfigValidatedEvent correlation). The cv-prefix avoids
// collision with sibling test files in this package.
func cvECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(8)}
}

// cvAttr walks slog-style key/value attrs for the value of the named key.
// Returns nil if not found.
func cvAttr(attrs []any, key string) any {
	for i := 0; i+1 < len(attrs); i += 2 {
		k, ok := attrs[i].(string)
		if !ok {
			continue
		}
		if k == key {
			return attrs[i+1]
		}
	}
	return nil
}

// Compile-time guard that the test file uses the busevents alias (and so
// keeps tracking the right package across renames).
var _ busevents.Event = (*ctlevents.ConfigValidationResponse)(nil)

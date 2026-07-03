// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// reconciliationInsight has TWO correlation branches that are critical
// to operator observability:
//
//  ReconciliationTriggeredEvent:
//   * `(previous reconciliation was X ago)` — appended ONLY when the
//     ring buffer holds a prior ReconciliationCompletedEvent within
//     reconciliationLookbackWindow (5 min). This is what tells
//     operators "this is reconciling every 2s — something's
//     thrashing." A regression that always or never appended would
//     either flood empty parens or silently mask reconciliation
//     storms (the kind that page on-call at 03:00).
//   * Reason MUST always surface (no correlation needed) so log
//     scrapers can grep for trigger types.
//
//  ReconciliationCompletedEvent:
//   * Has TWO mutually-exclusive completion-line variants:
//       - WITH a prior ReconciliationStartedEvent in the buffer:
//         `(total cycle: 42ms, reconciliation: 30ms)` — surfaces the
//         queue overhead between Started and Completed (= total wall
//         clock minus pure reconciliation time). Operators rely on
//         this to detect pipeline backpressure.
//       - WITHOUT a prior Started event: `(30ms)` only — fallback
//         when correlation is impossible (e.g., commentator restarted
//         mid-cycle). Operators MUST NOT see ", reconciliation:" in
//         this branch — it would imply correlation that didn't happen.
//
//  ReconciliationFailedEvent:
//   * Has no conditional fragment, but the message must always
//     surface BOTH `phase` and `error` because on-call operators use
//     the phase to decide which subsystem to investigate.
//
// These tests pin the correlation contracts so a regression that
// drops or flips the conditional logic would fail.

func TestReconciliationInsight_TriggeredEvent_PriorReconciliationCorrelation(t *testing.T) {
	tests := []struct {
		name        string
		seedBuffer  func(*RingBuffer) // setup for ring buffer state
		reason      string
		wantContain string // additional fragment that MUST appear
		notContain  string // fragment that must be ABSENT
	}{
		{
			name:        "no prior reconciliation in buffer → no correlation suffix",
			seedBuffer:  func(_ *RingBuffer) {}, // empty buffer
			reason:      "config_change",
			notContain:  "previous reconciliation",
			wantContain: "Reconciliation triggered: config_change",
		},
		{
			name: "prior ReconciliationCompletedEvent in buffer → '(previous reconciliation was X ago)' appended",
			seedBuffer: func(rb *RingBuffer) {
				// A recent completion in the buffer triggers the
				// correlation suffix. Time math uses the events'
				// own timestamps so a freshly-constructed event
				// will always satisfy the window check.
				rb.Add(ctlevents.NewReconciliationCompletedEvent(50, nil, nil))
			},
			reason:      "debounce_timer",
			wantContain: "(previous reconciliation was",
		},
		{
			name: "prior unrelated event (Started, not Completed) → no correlation suffix",
			seedBuffer: func(rb *RingBuffer) {
				// reconciliationInsight specifically looks for
				// ReconciliationCompletedEvent — Started doesn't
				// count toward the "previous reconciliation"
				// correlation.
				rb.Add(ctlevents.NewReconciliationStartedEvent("test"))
			},
			reason:      "drift_prevention",
			wantContain: "Reconciliation triggered: drift_prevention",
			notContain:  "previous reconciliation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := rcECommentator()
			tt.seedBuffer(ec.ringBuffer)

			evt := ctlevents.NewReconciliationTriggeredEvent(tt.reason, true)
			insight, attrs := ec.reconciliationInsight(evt, nil)

			require.NotEmpty(t, insight,
				"every ReconciliationTriggeredEvent must produce an operator log")
			assert.Contains(t, insight, tt.wantContain,
				"trigger reason %q MUST always surface so log scrapers can "+
					"grep for trigger types regardless of correlation state",
				tt.reason)
			if tt.notContain != "" {
				assert.NotContains(t, insight, tt.notContain,
					"absent prior completion MUST NOT produce '%s' fragment — "+
						"that would imply correlation that didn't happen and "+
						"confuse operators triaging reconciliation frequency",
					tt.notContain)
			}
			// reason attr is always present so dashboards can group/filter
			// by trigger consistently across both correlation branches.
			assert.Equal(t, tt.reason, rcAttr(attrs, "reason"),
				"reason attr must be set regardless of correlation state")
		})
	}
}

func TestReconciliationInsight_CompletedEvent_StartedCorrelationFork(t *testing.T) {
	// The completion line has TWO mutually-exclusive variants
	// determined by whether a ReconciliationStartedEvent is in the
	// ring buffer. Pin both so a regression in the correlation branch
	// either way is caught.
	tests := []struct {
		name        string
		seedBuffer  func(*RingBuffer)
		durationMs  int64
		wantContain []string // fragments that MUST appear
		notContain  []string // fragments that MUST NOT appear
	}{
		{
			name:       "no prior Started in buffer → fallback '(Xms)' only",
			seedBuffer: func(_ *RingBuffer) {},
			durationMs: 250,
			wantContain: []string{
				"Reconciliation completed successfully",
				"(250ms)", // fallback format
			},
			notContain: []string{
				"total cycle",     // correlation suffix must be ABSENT
				"reconciliation:", // correlation suffix must be ABSENT
			},
		},
		{
			name: "prior Started in buffer → '(total cycle: X, reconciliation: Yms)' appended",
			seedBuffer: func(rb *RingBuffer) {
				// Add a Started event so the correlation branch fires.
				rb.Add(ctlevents.NewReconciliationStartedEvent("config_change"))
			},
			durationMs: 75,
			wantContain: []string{
				"Reconciliation completed successfully",
				"total cycle:",
				"reconciliation: 75ms",
			},
		},
		{
			name: "prior Triggered (not Started) in buffer → still fallback",
			seedBuffer: func(rb *RingBuffer) {
				// Triggered ≠ Started for this correlation; the
				// completion line should fall back to plain (Xms).
				rb.Add(ctlevents.NewReconciliationTriggeredEvent("test", true))
			},
			durationMs: 99,
			wantContain: []string{
				"(99ms)",
			},
			notContain: []string{
				"total cycle",
				"reconciliation:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := rcECommentator()
			tt.seedBuffer(ec.ringBuffer)

			evt := ctlevents.NewReconciliationCompletedEvent(tt.durationMs, nil, nil)
			insight, attrs := ec.reconciliationInsight(evt, nil)

			require.NotEmpty(t, insight)
			for _, want := range tt.wantContain {
				assert.Contains(t, insight, want,
					"completion line MUST contain %q in this branch — "+
						"a regression flipping the correlation fork would "+
						"either hide pipeline backpressure metrics or "+
						"surface them when they don't apply", want)
			}
			for _, notWant := range tt.notContain {
				assert.NotContains(t, insight, notWant,
					"completion line MUST NOT contain %q in this branch — "+
						"would imply correlation that didn't happen", notWant)
			}
			// duration_ms attr is always present so dashboards aggregate
			// regardless of which branch produced the message.
			assert.Equal(t, tt.durationMs, rcAttr(attrs, "duration_ms"),
				"duration_ms attr must always match the event's DurationMs "+
					"for log↔metrics correlation")
		})
	}
}

func TestReconciliationInsight_FailedEvent_PhaseAndErrorAlwaysSurface(t *testing.T) {
	// ReconciliationFailedEvent has no conditional fragments, but the
	// message must surface BOTH phase and error verbatim because
	// operators use the phase to decide which subsystem to
	// investigate (template vs validation vs deploy).
	tests := []struct {
		name      string
		errString string
		phase     string
	}{
		{
			name:      "template phase failure",
			errString: "template syntax error at line 5",
			phase:     "template",
		},
		{
			name:      "validation phase failure",
			errString: "haproxy -c rejected the config",
			phase:     "validation",
		},
		{
			name:      "deploy phase failure",
			errString: "endpoint 10.0.0.1:5555 unreachable",
			phase:     "deploy",
		},
		{
			name:      "empty error string still surfaces phase",
			errString: "",
			phase:     "template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := rcECommentator()
			evt := ctlevents.NewReconciliationFailedEvent(tt.errString, tt.phase, nil)
			insight, attrs := ec.reconciliationInsight(evt, nil)

			require.NotEmpty(t, insight)
			assert.Contains(t, insight, "Reconciliation failed",
				"failure message must lead with the canonical prefix for "+
					"log scrapers and on-call alerts")
			assert.Contains(t, insight, tt.phase+" phase",
				"phase MUST always surface in canonical 'X phase' form so "+
					"operators know which subsystem to investigate")
			if tt.errString != "" {
				assert.Contains(t, insight, tt.errString,
					"the inner error string must surface verbatim so "+
						"operators see the root cause without having to "+
						"open the structured attrs")
			}
			assert.Equal(t, tt.phase, rcAttr(attrs, "phase"),
				"phase attr must match the message — text/attr drift "+
					"would break log↔metrics correlation")
			assert.Equal(t, tt.errString, rcAttr(attrs, "error"),
				"error attr must always be present (even empty string) "+
					"so dashboards group consistently regardless of branch")
		})
	}
}

func TestReconciliationInsight_StartedEvent_PinsTriggerInBothMessageAndAttr(t *testing.T) {
	// Started events have no conditional fragment but must surface
	// the trigger in BOTH the message body AND the structured attr
	// (the latter so the commentator can be queried via debug
	// endpoints without parsing the message string).
	ec := rcECommentator()
	evt := ctlevents.NewReconciliationStartedEvent("index_synchronized")
	insight, attrs := ec.reconciliationInsight(evt, nil)

	assert.Equal(t, "Reconciliation started: index_synchronized", insight,
		"started insight must follow exact 'Reconciliation started: <trigger>' "+
			"format for log-scraper grep contracts")
	assert.Equal(t, "index_synchronized", rcAttr(attrs, "trigger"),
		"trigger attr must match the message — text/attr drift breaks log↔metrics")
}

func TestReconciliationInsight_UnknownEventReturnsEmpty(t *testing.T) {
	// Default arm: events not owned by this insight must produce
	// empty output so the dispatcher can fall through cleanly.
	ec := rcECommentator()
	other := ctlevents.NewBecameLeaderEvent("pod") // owned by leaderInsight

	insight, attrs := ec.reconciliationInsight(other, []any{"keep", 1})

	assert.Empty(t, insight,
		"unhandled events must produce empty insight (dispatcher skip signal)")
	assert.Equal(t, []any{"keep", 1}, attrs,
		"attrs must pass through UNCHANGED on the default arm so the next "+
			"handler in the dispatch chain sees the original slice")
}

// rcECommentator returns a minimal EventCommentator with just the ring
// buffer that reconciliationInsight uses for correlation. The rc-prefix
// avoids collision with sibling insight test files (the ri prefix is
// taken by resource_insight_test.go).
func rcECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(16)}
}

// rcAttr walks slog-style key/value attrs for the value of the named key.
// Returns nil if not found.
func rcAttr(attrs []any, key string) any {
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

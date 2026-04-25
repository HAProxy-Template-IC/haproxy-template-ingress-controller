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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// leaderInsight and statusInsight produce operator-facing log messages
// for leadership-transition and status-patch events. Both have
// load-bearing CONDITIONAL fragments that determine whether operators
// see critical context:
//
//  leaderInsight:
//   * LostLeadershipEvent " (reason: ...)" — appended only when
//     Reason is non-empty. The reason explains WHY leadership was
//     lost (graceful shutdown vs lease expiration vs panic), which
//     determines on-call response. A regression that always or never
//     appended would either clutter logs with empty parens or strip
//     critical incident context.
//   * NewLeaderObservedEvent "this replica" vs "another replica" —
//     boolean IsSelf flips the message. This is what tells operators
//     whether THIS pod is the new leader (re-routes alerting) vs
//     just observing one. A regression that flipped these labels
//     would silently mis-route every leader-related alert.
//
//  statusInsight:
//   * StatusUpdateFailedEvent " (retriable)" — appended only when
//     Retriable is true. Same on-call response semantics as the
//     deployment-failure (retryable) tag in template_deployment_insight.

func TestLeaderInsight_LostLeadership_ReasonFragmentConditional(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		wantContains string // expected fragment of the formatted insight
		notContains  string // fragment that must be ABSENT
	}{
		{
			name:         "non-empty reason → '(reason: ...)' fragment appended",
			reason:       "lease_expired",
			wantContains: "(reason: lease_expired)",
		},
		{
			name:        "empty reason → fragment must be ABSENT (no stale parens)",
			reason:      "",
			notContains: "(reason:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := lsECommentator()
			evt := ctlevents.NewLostLeadershipEvent("pod-abc", tt.reason)

			insight, attrs := ec.leaderInsight(evt, nil)

			require.NotEmpty(t, insight,
				"every LostLeadershipEvent must produce an operator log message")
			assert.Contains(t, insight, "Lost leadership: pod-abc",
				"identity must always appear so operators can identify which pod lost leadership")

			if tt.wantContains != "" {
				assert.Contains(t, insight, tt.wantContains,
					"non-empty Reason MUST surface as '(reason: %s)' so operators "+
						"can distinguish graceful shutdown from incident causes — "+
						"a regression dropping this would strip critical context",
					tt.reason)
			}
			if tt.notContains != "" {
				assert.NotContains(t, insight, tt.notContains,
					"empty Reason MUST NOT produce a stale '(reason:' fragment — "+
						"a regression that emitted '(reason: )' would clutter logs")
			}

			// reason attr is always present (even when empty) so dashboards
			// can group/filter consistently.
			assert.Equal(t, tt.reason, lsAttr(attrs, "reason"),
				"the structured reason attr must always be present (even empty) "+
					"so dashboards can group consistently regardless of branch")
			assert.Equal(t, "pod-abc", lsAttr(attrs, "identity"))
		})
	}
}

func TestLeaderInsight_NewLeaderObserved_IsSelfFlipsLabel(t *testing.T) {
	tests := []struct {
		name      string
		isSelf    bool
		wantLabel string
		notLabel  string
	}{
		{
			name:      "isSelf=true → 'this replica' label",
			isSelf:    true,
			wantLabel: "(this replica)",
			notLabel:  "(another replica)",
		},
		{
			name:      "isSelf=false → 'another replica' label",
			isSelf:    false,
			wantLabel: "(another replica)",
			notLabel:  "(this replica)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := lsECommentator()
			evt := ctlevents.NewNewLeaderObservedEvent("pod-xyz", tt.isSelf)

			insight, attrs := ec.leaderInsight(evt, nil)

			assert.Contains(t, insight, "pod-xyz",
				"the new leader's identity must always appear")
			assert.Contains(t, insight, tt.wantLabel,
				"isSelf=%v MUST produce %q label so operators can tell "+
					"WHETHER THIS POD is the new leader (re-routes alerting) — "+
					"a regression flipping these would silently mis-route every "+
					"leader-related alert",
				tt.isSelf, tt.wantLabel)
			assert.NotContains(t, insight, tt.notLabel,
				"the OTHER label must NOT appear — strict separation is what "+
					"lets operators reliably filter by leader role")
			assert.Equal(t, tt.isSelf, lsAttr(attrs, "is_self"),
				"the structured is_self attr must match the label — "+
					"a text/attr drift would break log↔metrics correlation")
		})
	}
}

func TestLeaderInsight_BasicEventTypes(t *testing.T) {
	// Per-event-type sanity tests for the non-conditional events.
	// These pin the message prefix (log-scraper grep) and identifier
	// presence, which a regression in formatting would break.
	tests := []struct {
		name    string
		event   busevents.Event
		prefix  string
		idAttr  string
		idValue string
	}{
		{
			name: "LeaderElectionStartedEvent identity+lease",
			event: ctlevents.NewLeaderElectionStartedEvent(
				"pod-abc", "haptic-leader", "haptic"),
			prefix:  "Leader election started:",
			idAttr:  "identity",
			idValue: "pod-abc",
		},
		{
			name:    "BecameLeaderEvent identity surfaces",
			event:   ctlevents.NewBecameLeaderEvent("pod-abc"),
			prefix:  "Became leader: pod-abc",
			idAttr:  "identity",
			idValue: "pod-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := lsECommentator()
			insight, attrs := ec.leaderInsight(tt.event, nil)
			assert.Contains(t, insight, tt.prefix,
				"insight must start with the documented prefix for log-scraper grep")
			assert.Equal(t, tt.idValue, lsAttr(attrs, tt.idAttr),
				"identity attr must match the message")
		})
	}
}

func TestStatusInsight_FailedEvent_RetriableFragmentConditional(t *testing.T) {
	tests := []struct {
		name      string
		retriable bool
		want      string
		notWant   string
	}{
		{
			name:      "retriable=true → '(retriable)' fragment appended",
			retriable: true,
			want:      "(retriable)",
		},
		{
			name:      "retriable=false → fragment must be ABSENT",
			retriable: false,
			notWant:   "(retriable)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := lsECommentator()
			evt := ctlevents.NewStatusUpdateFailedEvent(
				"default", "my-ingress", "networking.k8s.io/v1/ingresses",
				"conflict on ResourceVersion", tt.retriable)

			insight, attrs := ec.statusInsight(evt, nil)

			assert.Contains(t, insight, "Status patch failed for default/my-ingress",
				"the ns/name target must surface so operators identify the "+
					"affected resource")
			assert.Contains(t, insight, "conflict on ResourceVersion",
				"the inner error must always be visible to operators")

			if tt.want != "" {
				assert.Contains(t, insight, tt.want,
					"retriable failures MUST be tagged so operators ignore them "+
						"in incident dashboards (auto-recovers); a regression "+
						"dropping this tag would page on-call for transient failures")
			}
			if tt.notWant != "" {
				assert.NotContains(t, insight, tt.notWant,
					"non-retriable failures MUST NOT carry the (retriable) tag — "+
						"operators wait for a retry that won't come, missing the "+
						"manual-intervention requirement")
			}

			assert.Equal(t, tt.retriable, lsAttr(attrs, "retriable"),
				"the structured retriable attr must match the tag presence")
		})
	}
}

func TestStatusInsight_CompletedEvent_FormatsCounts(t *testing.T) {
	// StatusUpdateCompletedEvent has no conditional fragments — just
	// pin the format so a regression that swapped applied/skipped or
	// dropped duration is caught.
	ec := lsECommentator()
	evt := ctlevents.NewStatusUpdateCompletedEvent(
		"render", 7, 3, 42)

	insight, attrs := ec.statusInsight(evt, nil)

	assert.Contains(t, insight, "Status patches applied (render phase)",
		"phase must appear in canonical 'X phase' form for log scrapers")
	assert.Contains(t, insight, "7 applied",
		"applied count must surface — operators verify expected patches landed")
	assert.Contains(t, insight, "3 skipped",
		"skipped count must surface — operators verify checksum dedup is working")
	assert.Contains(t, insight, "42ms",
		"duration must always appear")
	assert.Equal(t, 7, lsAttr(attrs, "applied"))
	assert.Equal(t, 3, lsAttr(attrs, "skipped"))
}

func TestStatusInsight_UnknownEventReturnsEmpty(t *testing.T) {
	// Default arm: events not owned by this insight must produce
	// empty output so the dispatcher skips cleanly.
	ec := lsECommentator()
	other := ctlevents.NewBecameLeaderEvent("pod") // owned by leaderInsight

	insight, attrs := ec.statusInsight(other, []any{"keep", "me"})

	assert.Empty(t, insight,
		"unhandled events must produce empty insight (dispatcher skip signal)")
	assert.Equal(t, []any{"keep", "me"}, attrs,
		"attrs must pass through UNCHANGED on the default arm")
}

// lsECommentator returns a minimal EventCommentator with just the
// fields leaderInsight / statusInsight touch (none — they're pure
// formatters). The ls-prefix avoids collision with sibling test files.
func lsECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(8)}
}

// lsAttr walks slog-style key/value attrs for the value of the named
// key. Returns nil if not found.
func lsAttr(attrs []any, key string) any {
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

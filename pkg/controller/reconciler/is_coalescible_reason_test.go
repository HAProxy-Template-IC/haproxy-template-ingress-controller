// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// isCoalescibleReason maps a trigger reason string to a boolean
// coalescibility flag that gets stamped on every published
// ReconciliationTriggeredEvent. The flag is consumed downstream by
// the EventBus's coalescing logic (newer coalescible events of the
// same type can override older ones still in queue).
//
// The function is small but EVERY reason mapping is load-bearing:
//
//	STATE UPDATES (coalescible=true): only the latest matters
//	  - "resource_change"       → individual resource change
//	  - "http_resource_change"  → HTTP content changed
//
//	COMMANDS (coalescible=false): MUST be processed individually
//	  - "index_synchronized"    → initial sync complete; if coalesced
//	                              the controller would never produce
//	                              the first reconciliation
//	  - "drift_prevention"      → periodic drift enforcement; if
//	                              coalesced under load, drift would
//	                              silently accumulate and never get
//	                              corrected
//	  - "became_leader"         → leadership acquired; if coalesced
//	                              the new leader's initial state
//	                              reconciliation would be skipped
//	                              leaving HAProxy on stale config
//	  - "http_resource_accepted" → validated HTTP content promoted;
//	                               if coalesced, the deploy of the
//	                               new content would never fire
//
// A regression that flipped the boolean for ANY of these reasons
// would change runtime behavior in a way that wouldn't surface in
// integration tests until the specific scenario hit (e.g. a leader
// transition under load coalescing the became_leader event with
// concurrent resource changes). The table below pins each reason
// explicitly so a typo in the switch statement fails immediately.

func TestIsCoalescibleReason(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   bool
		why    string
	}{
		// State updates → coalescible (only latest matters)
		{
			name:   "resource_change is coalescible (state update)",
			reason: "resource_change",
			want:   true,
			why: "individual resource changes converge to the same render output " +
				"under steady-state — coalescing reduces reconciliation pressure " +
				"during cluster churn",
		},
		{
			name:   "http_resource_change is coalescible (state update)",
			reason: "http_resource_change",
			want:   true,
			why: "HTTP content updates are state — only the latest version needs " +
				"to be reconciled",
		},
		// Commands → NOT coalescible (must be processed individually)
		{
			name:   "index_synchronized is NOT coalescible (initial sync command)",
			reason: "index_synchronized",
			want:   false,
			why: "if this got coalesced the controller's FIRST reconciliation " +
				"after startup could be silently skipped, leaving HAProxy without " +
				"any rendered config until the next unrelated trigger",
		},
		{
			name:   "drift_prevention is NOT coalescible (periodic enforcement)",
			reason: "drift_prevention",
			want:   false,
			why: "drift prevention is the safety net against config drift — if " +
				"coalesced under load it would silently fail to enforce HAProxy " +
				"state, allowing drift to accumulate indefinitely without alerting",
		},
		{
			name:   "became_leader is NOT coalescible (leadership transition command)",
			reason: "became_leader",
			want:   false,
			why: "the new leader's initial reconciliation MUST run to populate " +
				"its state; a coalesced became_leader would leave the new " +
				"leader operating on stale or empty state",
		},
		{
			name:   "http_resource_accepted is NOT coalescible (validation promotion command)",
			reason: "http_resource_accepted",
			want:   false,
			why: "validated HTTP content has been formally promoted from pending " +
				"to accepted — the deployment of that promotion must run; if " +
				"coalesced, the new content would never reach HAProxy pods",
		},
		// Defensive: unknown reasons default to non-coalescible (the
		// safe default per the switch's `default` arm)
		{
			name:   "unknown reason defaults to NON-coalescible (safe default)",
			reason: "future_reason_not_yet_classified",
			want:   false,
			why: "the default arm of the switch returns false (non-coalescible) " +
				"because dropping events is more dangerous than over-processing — " +
				"a future reason added without an explicit case still gets the " +
				"safe must-process default",
		},
		{
			name:   "empty reason defaults to NON-coalescible",
			reason: "",
			want:   false,
			why:    "empty string isn't in any case arm — must hit the safe default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCoalescibleReason(tt.reason)
			assert.Equal(t, tt.want, got, tt.why)
		})
	}
}

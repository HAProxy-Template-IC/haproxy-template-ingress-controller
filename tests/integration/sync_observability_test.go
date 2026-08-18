//go:build integration

package integration

import (
	"testing"
)

// TestSyncObservability tests synchronization of observability sections that
// later HAProxy releases introduced; a case is skipped below the release that
// can parse its directives.
func TestSyncObservability(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== LOG PROFILE OPERATIONS (HAProxy 3.1+) ====================
		{
			name:              "log-profile-add",
			initialConfigFile: "log-profiles/base.cfg",
			desiredConfigFile: "log-profiles/with-profile.cfg",
			minHAProxy:        "3.1",
		},
		{
			name:              "log-profile-remove",
			initialConfigFile: "log-profiles/with-profile.cfg",
			desiredConfigFile: "log-profiles/base.cfg",
			minHAProxy:        "3.1",
		},

		// ==================== TRACES OPERATIONS (HAProxy 3.1+) ====================
		{
			name:              "traces-add",
			initialConfigFile: "traces/base.cfg",
			desiredConfigFile: "traces/with-traces.cfg",
			minHAProxy:        "3.1",
		},

		// ==================== QUIC INITIAL RULES (HAProxy 3.1+) ====================
		{
			name:              "quic-initial-rule-add",
			initialConfigFile: "quic-rules/frontend-base.cfg",
			desiredConfigFile: "quic-rules/frontend-with-quic-rules.cfg",
			minHAProxy:        "3.1",
		},
		{
			name:              "quic-initial-rule-remove",
			initialConfigFile: "quic-rules/frontend-with-quic-rules.cfg",
			desiredConfigFile: "quic-rules/frontend-base.cfg",
			minHAProxy:        "3.1",
		},

		// ==================== ACME PROVIDERS (HAProxy 3.2+) ====================
		{
			name:              "acme-provider-add",
			initialConfigFile: "acme/base.cfg",
			desiredConfigFile: "acme/with-letsencrypt.cfg",
			minHAProxy:        "3.2",
		},
		{
			name:              "acme-provider-remove",
			initialConfigFile: "acme/with-letsencrypt.cfg",
			desiredConfigFile: "acme/base.cfg",
			minHAProxy:        "3.2",
		},
	}

	for _, tt := range testCases {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runSyncTest(t, tt)
		})
	}
}

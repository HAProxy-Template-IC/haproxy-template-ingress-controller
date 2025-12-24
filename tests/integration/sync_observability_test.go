//go:build integration

package integration

import (
	"testing"
)

// TestSyncObservability tests synchronization of observability and v3.1+/v3.2+ features.
// These tests are skipped on HAProxy versions that don't support the features.
func TestSyncObservability(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== LOG PROFILE OPERATIONS (v3.1+) ====================
		{
			name:              "log-profile-add",
			initialConfigFile: "log-profiles/base.cfg",
			desiredConfigFile: "log-profiles/with-profile.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create log-profile 'request-log'",
			},
			expectedReload: true,
			skipFunc:       skipIfLogProfilesNotSupported,
		},
		{
			name:              "log-profile-remove",
			initialConfigFile: "log-profiles/with-profile.cfg",
			desiredConfigFile: "log-profiles/base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete log-profile 'request-log'",
			},
			expectedReload: true,
			skipFunc:       skipIfLogProfilesNotSupported,
		},

		// ==================== TRACES OPERATIONS (v3.1+) ====================
		// Note: No traces-remove test because traces is a singleton section that cannot
		// be deleted through the API. When desired config has no traces section, the
		// comparator intentionally generates no operations.
		{
			name:              "traces-add",
			initialConfigFile: "traces/base.cfg",
			desiredConfigFile: "traces/with-traces.cfg",
			expectedCreates:   1,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create ring 'buf1'",
				"Update traces section",
			},
			expectedReload: true,
			skipFunc:       skipIfTracesNotSupported,
		},

		// ==================== QUIC INITIAL RULES (v3.1+) ====================
		{
			name:              "quic-initial-rule-add",
			initialConfigFile: "quic-rules/frontend-base.cfg",
			desiredConfigFile: "quic-rules/frontend-with-quic-rules.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create quic-initial-rule at index 0 in frontend 'quic-in'",
			},
			expectedReload: true,
			skipFunc:       skipIfQUICInitialRulesNotSupported,
		},
		{
			name:              "quic-initial-rule-remove",
			initialConfigFile: "quic-rules/frontend-with-quic-rules.cfg",
			desiredConfigFile: "quic-rules/frontend-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete quic-initial-rule at index 0 from frontend 'quic-in'",
			},
			expectedReload: true,
			skipFunc:       skipIfQUICInitialRulesNotSupported,
		},

		// ==================== ACME PROVIDERS (v3.2+) ====================
		{
			name:              "acme-provider-add",
			initialConfigFile: "acme/base.cfg",
			desiredConfigFile: "acme/with-letsencrypt.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create acme-provider 'letsencrypt'",
			},
			expectedReload: true,
			skipFunc:       skipIfAcmeProvidersNotSupported,
		},
		{
			name:              "acme-provider-remove",
			initialConfigFile: "acme/with-letsencrypt.cfg",
			desiredConfigFile: "acme/base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete acme-provider 'letsencrypt'",
			},
			expectedReload: true,
			skipFunc:       skipIfAcmeProvidersNotSupported,
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

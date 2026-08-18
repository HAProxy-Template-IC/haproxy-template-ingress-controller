//go:build integration

package integration

import (
	"testing"
)

// TestSyncBackends runs table-driven synchronization tests for backend operations
func TestSyncBackends(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== BASIC BACKEND OPERATIONS ====================
		{
			name:              "add-backend-with-server",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "basic/one-backend.cfg",
		},
		{
			name:              "remove-backend-with-server",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "basic/empty.cfg",
		},

		// ==================== MULTIPLE BACKEND OPERATIONS ====================
		{
			name:              "add-two-backends",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/two-backends.cfg",
		},
		{
			name:              "add-three-backends",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/three-backends.cfg",
		},
		{
			name:              "remove-two-backends",
			initialConfigFile: "backends/two-backends.cfg",
			desiredConfigFile: "basic/empty.cfg",
		},
		{
			name:              "add-backend-no-servers",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/empty-backend.cfg",
		},

		// ==================== COMPLEX MIXED OPERATIONS ====================
		{
			name:              "multi-backend-mixed",
			initialConfigFile: "backends/two-backends.cfg",
			desiredConfigFile: "complex/multiple-backends-mixed.cfg",
		},

		{
			name:              "backend-add-acl",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "acls/backend-with-acl.cfg",
		},
		{
			name:              "backend-add-http-request-rule",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "rules/http-request.cfg",
		},
		{
			name:              "backend-add-http-response-rule",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "rules/http-response.cfg",
		},
		{
			name:              "backend-change-balance-algorithm",
			initialConfigFile: "backend-attrs/balance-roundrobin.cfg",
			desiredConfigFile: "backends/balance-leastconn.cfg",
		},
		{
			name:              "backend-change-mode",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "backend-attrs/mode-tcp.cfg",
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "backend-add-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/backend-with-timeouts.cfg",
		},
		{
			name:              "backend-remove-timeouts",
			initialConfigFile: "timeouts/backend-with-timeouts.cfg",
			desiredConfigFile: "timeouts/defaults-base.cfg",
		},

		// ==================== COOKIE-BASED PERSISTENCE ====================
		{
			name:              "backend-add-server-cookies",
			initialConfigFile: "cookies/cookies-base.cfg",
			desiredConfigFile: "cookies/cookies-server-cookies.cfg",
		},
		{
			name:              "backend-add-cookie-prefix",
			initialConfigFile: "cookies/cookies-server-cookies.cfg",
			desiredConfigFile: "cookies/cookies-with-prefix.cfg",
		},
		{
			name:              "backend-remove-cookies",
			initialConfigFile: "cookies/cookies-server-cookies.cfg",
			desiredConfigFile: "cookies/cookies-base.cfg",
		},

		// ==================== TCP RULE OPERATIONS ====================
		{
			name:              "backend-add-tcp-request-rule",
			initialConfigFile: "tcp-rules/backend-base.cfg",
			desiredConfigFile: "tcp-rules/backend-with-tcp-request.cfg",
		},
		{
			name:              "backend-add-tcp-response-rule",
			initialConfigFile: "tcp-rules/backend-base.cfg",
			desiredConfigFile: "tcp-rules/backend-with-tcp-response.cfg",
		},

		// ==================== LOG TARGET OPERATIONS ====================
		{
			name:              "backend-add-log-target",
			initialConfigFile: "log-targets/backend-base.cfg",
			desiredConfigFile: "log-targets/backend-with-log.cfg",
		},

		// ==================== STICK RULE OPERATIONS ====================
		{
			name:              "backend-add-stick-on-rule",
			initialConfigFile: "stick-rules/backend-base.cfg",
			desiredConfigFile: "stick-rules/backend-with-stick-on.cfg",
		},
		{
			name:              "backend-add-stick-match-rule",
			initialConfigFile: "stick-rules/backend-base.cfg",
			desiredConfigFile: "stick-rules/backend-with-stick-match.cfg",
		},

		// ==================== HTTP AFTER RESPONSE RULE OPERATIONS ====================
		{
			name:              "backend-add-http-after-rule",
			initialConfigFile: "http-after-rules/backend-base.cfg",
			desiredConfigFile: "http-after-rules/backend-with-http-after.cfg",
		},

		// ==================== SWITCHING RULE OPERATIONS ====================
		{
			name:              "backend-add-server-switching-rule",
			initialConfigFile: "server-switching-rules/backend-base.cfg",
			desiredConfigFile: "server-switching-rules/backend-with-switching.cfg",
		},

		// ==================== FILTER OPERATIONS ====================
		{
			name:              "backend-add-filter",
			initialConfigFile: "filters/backend-base.cfg",
			desiredConfigFile: "filters/backend-with-filter.cfg",
		},

		// ==================== CHECK RULE OPERATIONS ====================
		{
			name:              "backend-add-http-check",
			initialConfigFile: "http-checks/backend-base.cfg",
			desiredConfigFile: "http-checks/backend-with-http-check.cfg",
		},
		{
			name:              "backend-add-tcp-check",
			initialConfigFile: "tcp-checks/backend-base.cfg",
			desiredConfigFile: "tcp-checks/backend-with-tcp-check.cfg",
		},

		// ==================== SERVER TEMPLATE OPERATIONS ====================
		{
			name:              "backend-add-server-template",
			initialConfigFile: "server-templates/backend-base.cfg",
			desiredConfigFile: "server-templates/backend-with-template.cfg",
		},
		{
			name:              "backend-remove-server-template",
			initialConfigFile: "server-templates/backend-with-template.cfg",
			desiredConfigFile: "server-templates/backend-base.cfg",
		},
		{
			name:              "backend-update-server-template",
			initialConfigFile: "server-templates/backend-with-template.cfg",
			desiredConfigFile: "server-templates/template-num-changed.cfg",
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

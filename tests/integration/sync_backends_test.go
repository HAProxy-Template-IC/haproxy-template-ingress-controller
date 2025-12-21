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
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'test-backend'",
				"Create server 'srv1' in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-backend-with-server",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "basic/empty.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete backend 'test-backend'",
			},
			expectedReload: true,
		},

		// ==================== MULTIPLE BACKEND OPERATIONS ====================
		{
			name:              "add-two-backends",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/two-backends.cfg",
			expectedCreates:   4,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'web'",
				"Create server 'srv1' in backend 'web'",
				"Create backend 'api'",
				"Create server 'srv1' in backend 'api'",
			},
			expectedReload: true,
		},
		{
			name:              "add-three-backends",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/three-backends.cfg",
			expectedCreates:   6,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'web'",
				"Create server 'srv1' in backend 'web'",
				"Create backend 'api'",
				"Create server 'srv1' in backend 'api'",
				"Create backend 'admin'",
				"Create server 'srv1' in backend 'admin'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-two-backends",
			initialConfigFile: "backends/two-backends.cfg",
			desiredConfigFile: "basic/empty.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   2,
			expectedOperations: []string{
				"Delete backend 'web'",
				"Delete backend 'api'",
			},
			expectedReload: true,
		},
		{
			name:              "add-backend-no-servers",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "backends/empty-backend.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== COMPLEX MIXED OPERATIONS ====================
		{
			name:              "multi-backend-mixed",
			initialConfigFile: "backends/two-backends.cfg",
			desiredConfigFile: "complex/multiple-backends-mixed.cfg",
			expectedCreates:   4,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create server 'srv3' in backend 'web'",
				"Create server 'srv2' in backend 'api'",
				"Create backend 'admin'",
				"Create server 'srv1' in backend 'admin'",
			},
			expectedReload: true,
		},

		{
			name:              "backend-add-acl",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "acls/backend-with-acl.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create ACL 'is_local' in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-http-request-rule",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "rules/http-request.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create HTTP request rule (set-header) in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-http-response-rule",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "rules/http-response.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create HTTP response rule (set-header) in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-change-balance-algorithm",
			initialConfigFile: "backend-attrs/balance-roundrobin.cfg",
			desiredConfigFile: "backends/balance-leastconn.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-change-mode",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "backend-attrs/mode-tcp.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'test-backend'",
			},
			expectedReload: true,
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "backend-add-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/backend-with-timeouts.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-remove-timeouts",
			initialConfigFile: "timeouts/backend-with-timeouts.cfg",
			desiredConfigFile: "timeouts/defaults-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== COOKIE-BASED PERSISTENCE ====================
		{
			name:              "backend-add-server-cookies",
			initialConfigFile: "cookies/cookies-base.cfg",
			desiredConfigFile: "cookies/cookies-server-cookies.cfg",
			expectedCreates:   0,
			expectedUpdates:   3,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
				"Update server 'srv1' in backend 'web'",
				"Update server 'srv2' in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-cookie-prefix",
			initialConfigFile: "cookies/cookies-server-cookies.cfg",
			desiredConfigFile: "cookies/cookies-with-prefix.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-remove-cookies",
			initialConfigFile: "cookies/cookies-server-cookies.cfg",
			desiredConfigFile: "cookies/cookies-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   3,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update backend 'web'",
				"Update server 'srv1' in backend 'web'",
				"Update server 'srv2' in backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== TCP RULE OPERATIONS ====================
		{
			name:              "backend-add-tcp-request-rule",
			initialConfigFile: "tcp-rules/backend-base.cfg",
			desiredConfigFile: "tcp-rules/backend-with-tcp-request.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create TCP request rule (content) in backend 'mysql'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-tcp-response-rule",
			initialConfigFile: "tcp-rules/backend-base.cfg",
			desiredConfigFile: "tcp-rules/backend-with-tcp-response.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create TCP response rule (content) in backend 'mysql'",
			},
			expectedReload: true,
		},

		// ==================== LOG TARGET OPERATIONS ====================
		{
			name:              "backend-add-log-target",
			initialConfigFile: "log-targets/backend-base.cfg",
			desiredConfigFile: "log-targets/backend-with-log.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create log target (127.0.0.1:514) in backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== STICK RULE OPERATIONS ====================
		{
			name:              "backend-add-stick-on-rule",
			initialConfigFile: "stick-rules/backend-base.cfg",
			desiredConfigFile: "stick-rules/backend-with-stick-on.cfg",
			expectedCreates:   1,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create stick rule (on) in backend 'web'",
				"Update backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-stick-match-rule",
			initialConfigFile: "stick-rules/backend-base.cfg",
			desiredConfigFile: "stick-rules/backend-with-stick-match.cfg",
			expectedCreates:   1,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create stick rule (match) in backend 'web'",
				"Update backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== HTTP AFTER RESPONSE RULE OPERATIONS ====================
		{
			name:              "backend-add-http-after-rule",
			initialConfigFile: "http-after-rules/backend-base.cfg",
			desiredConfigFile: "http-after-rules/backend-with-http-after.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create HTTP after response rule (set-header) in backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== SWITCHING RULE OPERATIONS ====================
		{
			name:              "backend-add-server-switching-rule",
			initialConfigFile: "server-switching-rules/backend-base.cfg",
			desiredConfigFile: "server-switching-rules/backend-with-switching.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create server switching rule (srv2) in backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== FILTER OPERATIONS ====================
		{
			name:              "backend-add-filter",
			initialConfigFile: "filters/backend-base.cfg",
			desiredConfigFile: "filters/backend-with-filter.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create filter (compression) in backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== CHECK RULE OPERATIONS ====================
		{
			name:              "backend-add-http-check",
			initialConfigFile: "http-checks/backend-base.cfg",
			desiredConfigFile: "http-checks/backend-with-http-check.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create HTTP check (send) in backend 'web'",
				"Create HTTP check (expect) in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-add-tcp-check",
			initialConfigFile: "tcp-checks/backend-base.cfg",
			desiredConfigFile: "tcp-checks/backend-with-tcp-check.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create TCP check (connect) in backend 'mysql'",
				"Create TCP check (expect) in backend 'mysql'",
			},
			expectedReload: true,
		},

		// ==================== SERVER TEMPLATE OPERATIONS ====================
		{
			name:              "backend-add-server-template",
			initialConfigFile: "server-templates/backend-base.cfg",
			desiredConfigFile: "server-templates/backend-with-template.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create server template 'web-' in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-remove-server-template",
			initialConfigFile: "server-templates/backend-with-template.cfg",
			desiredConfigFile: "server-templates/backend-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete server template 'web-' from backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "backend-update-server-template",
			initialConfigFile: "server-templates/backend-with-template.cfg",
			desiredConfigFile: "server-templates/template-num-changed.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server template 'web-' in backend 'web'",
			},
			expectedReload: true,
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

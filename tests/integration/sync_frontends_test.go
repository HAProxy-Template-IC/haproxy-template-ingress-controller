//go:build integration

package integration

import (
	"testing"
)

// TestSyncFrontends runs table-driven synchronization tests for frontend operations
func TestSyncFrontends(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		{
			name:              "frontend-add",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "frontends/basic.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create frontend 'web'",
				"Create bind '*:80' in frontend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "frontend-with-acl",
			initialConfigFile: "frontends/basic.cfg",
			desiredConfigFile: "frontends/with-acl.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create ACL 'is_static' in frontend 'web'",
			},
			expectedReload: true,
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "frontend-add-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/frontend-with-timeouts.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create frontend 'http-in'",
				"Create bind '*:80' in frontend 'http-in'",
			},
			expectedReload: true,
		},

		// ==================== BIND OPERATIONS ====================
		{
			name:              "frontend-add-binds",
			initialConfigFile: "binds/frontend-with-bind.cfg",
			desiredConfigFile: "binds/frontend-multiple-binds.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create bind '*:8080' in frontend 'http-in'",
				"Create bind '*:8081' in frontend 'http-in'",
			},
			expectedReload: true,
		},
		{
			name:              "frontend-remove-binds",
			initialConfigFile: "binds/frontend-multiple-binds.cfg",
			desiredConfigFile: "binds/frontend-with-bind.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   2,
			expectedOperations: []string{
				"Delete bind '*:8080' from frontend 'http-in'",
				"Delete bind '*:8081' from frontend 'http-in'",
			},
			expectedReload: true,
		},

		// ==================== TCP RULE OPERATIONS ====================
		{
			name:              "frontend-add-tcp-request-rule",
			initialConfigFile: "tcp-rules/frontend-base.cfg",
			desiredConfigFile: "tcp-rules/frontend-with-tcp-request.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create TCP request rule (connection) in frontend 'tcp-in'",
			},
			expectedReload: true,
		},

		// ==================== LOG TARGET OPERATIONS ====================
		{
			name:              "frontend-add-log-target",
			initialConfigFile: "log-targets/frontend-base.cfg",
			desiredConfigFile: "log-targets/frontend-with-log.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create log target (127.0.0.1:514) in frontend 'http-in'",
			},
			expectedReload: true,
		},

		// ==================== SWITCHING RULE OPERATIONS ====================
		{
			name:              "frontend-add-backend-switching-rule",
			initialConfigFile: "backend-switching-rules/frontend-base.cfg",
			desiredConfigFile: "backend-switching-rules/frontend-with-switching.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend switching rule (api) in frontend 'http-in'",
			},
			expectedReload: true,
		},

		// ==================== FILTER OPERATIONS ====================
		{
			name:              "frontend-add-filter",
			initialConfigFile: "filters/frontend-base.cfg",
			desiredConfigFile: "filters/frontend-with-filter.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create filter (compression) in frontend 'http-in'",
			},
			expectedReload: true,
		},

		// ==================== CAPTURE OPERATIONS ====================
		{
			name:              "frontend-add-request-capture",
			initialConfigFile: "captures/frontend-base.cfg",
			desiredConfigFile: "captures/frontend-with-request-capture.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create capture (request) in frontend 'http-in'",
			},
			expectedReload: true,
		},
		{
			name:              "frontend-add-response-capture",
			initialConfigFile: "captures/frontend-base.cfg",
			desiredConfigFile: "captures/frontend-with-response-capture.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create capture (response) in frontend 'http-in'",
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

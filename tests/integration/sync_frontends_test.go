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
		},
		{
			name:              "frontend-maxconn-change",
			initialConfigFile: "frontends/maxconn-1000.cfg",
			desiredConfigFile: "frontends/maxconn-2000.cfg",
		},
		{
			name:              "frontend-with-acl",
			initialConfigFile: "frontends/basic.cfg",
			desiredConfigFile: "frontends/with-acl.cfg",
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "frontend-add-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/frontend-with-timeouts.cfg",
		},

		// ==================== BIND OPERATIONS ====================
		{
			name:              "frontend-add-binds",
			initialConfigFile: "binds/frontend-with-bind.cfg",
			desiredConfigFile: "binds/frontend-multiple-binds.cfg",
		},
		{
			name:              "frontend-remove-binds",
			initialConfigFile: "binds/frontend-multiple-binds.cfg",
			desiredConfigFile: "binds/frontend-with-bind.cfg",
		},

		// ==================== TCP RULE OPERATIONS ====================
		{
			name:              "frontend-add-tcp-request-rule",
			initialConfigFile: "tcp-rules/frontend-base.cfg",
			desiredConfigFile: "tcp-rules/frontend-with-tcp-request.cfg",
		},

		// ==================== LOG TARGET OPERATIONS ====================
		{
			name:              "frontend-add-log-target",
			initialConfigFile: "log-targets/frontend-base.cfg",
			desiredConfigFile: "log-targets/frontend-with-log.cfg",
		},

		// ==================== SWITCHING RULE OPERATIONS ====================
		{
			name:              "frontend-add-backend-switching-rule",
			initialConfigFile: "backend-switching-rules/frontend-base.cfg",
			desiredConfigFile: "backend-switching-rules/frontend-with-switching.cfg",
		},

		// ==================== FILTER OPERATIONS ====================
		{
			name:              "frontend-add-filter",
			initialConfigFile: "filters/frontend-base.cfg",
			desiredConfigFile: "filters/frontend-with-filter.cfg",
		},

		// ==================== CAPTURE OPERATIONS ====================
		{
			name:              "frontend-add-request-capture",
			initialConfigFile: "captures/frontend-base.cfg",
			desiredConfigFile: "captures/frontend-with-request-capture.cfg",
		},
		{
			name:              "frontend-add-response-capture",
			initialConfigFile: "captures/frontend-base.cfg",
			desiredConfigFile: "captures/frontend-with-response-capture.cfg",
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

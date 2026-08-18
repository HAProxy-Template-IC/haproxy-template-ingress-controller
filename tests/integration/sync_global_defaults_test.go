//go:build integration

package integration

import (
	"testing"
)

// TestSyncGlobalDefaults runs table-driven synchronization tests for global and defaults sections
func TestSyncGlobalDefaults(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		{
			name:              "global-change-maxconn",
			initialConfigFile: "global/maxconn-2000.cfg",
			desiredConfigFile: "global/maxconn-4000.cfg",
		},
		{
			name:              "defaults-change-mode",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "defaults/mode-tcp.cfg",
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "defaults-change-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/defaults-modified.cfg",
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

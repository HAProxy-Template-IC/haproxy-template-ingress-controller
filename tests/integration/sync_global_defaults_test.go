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
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update global section",
			},
			expectedReload: true,
		},
		{
			name:              "defaults-change-mode",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "defaults/mode-tcp.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update defaults section 'unnamed_defaults_1'",
			},
			expectedReload: true,
		},

		// ==================== TIMEOUT DIRECTIVE OPERATIONS ====================
		{
			name:              "defaults-change-timeouts",
			initialConfigFile: "timeouts/defaults-base.cfg",
			desiredConfigFile: "timeouts/defaults-modified.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update defaults section 'unnamed_defaults_1'",
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

//go:build integration

package integration

import (
	"testing"
)

// TestSyncServers runs table-driven synchronization tests for server operations
func TestSyncServers(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== BASIC SERVER OPERATIONS ====================
		{
			name:              "add-server-to-backend",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "basic/two-servers.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create server 'srv2' in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-server-from-backend",
			initialConfigFile: "basic/two-servers.cfg",
			desiredConfigFile: "basic/one-backend.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete server 'srv2' from backend 'test-backend'",
			},
			expectedReload: true,
		},

		// ==================== SERVER ATTRIBUTE MODIFICATIONS ====================
		{
			name:              "server-change-weight",
			initialConfigFile: "servers/weight-100.cfg",
			desiredConfigFile: "servers/weight-200.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'web'",
			},
			expectedReload: false,
		},
		{
			name:              "server-add-with-backup",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/with-backup.cfg",
			expectedCreates:   3,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete backend 'test-backend'",
				"Create backend 'web'",
				"Create server 'srv1' in backend 'web'",
				"Create server 'srv2' in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "server-with-maxconn",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "servers/with-maxconn.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'web'",
				"Create server 'srv1' in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "server-with-check-intervals",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "servers/with-check-inter.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create backend 'web'",
				"Create server 'srv1' in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "server-change-address",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/address-changed.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'test-backend'",
			},
			expectedReload: false,
		},
		{
			name:              "server-change-port",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/port-changed.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'test-backend'",
			},
			expectedReload: false,
		},

		// ==================== RUNTIME API OPERATIONS (NO RELOAD) ====================
		{
			name:              "srv-weight-no-reload",
			initialConfigFile: "servers/weight-100.cfg",
			desiredConfigFile: "servers/weight-200.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'web'",
			},
			expectedReload: false,
		},
		{
			name:              "srv-addr-no-reload",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/address-changed.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'test-backend'",
			},
			expectedReload: false,
		},
		{
			name:              "srv-port-no-reload",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/port-changed.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'test-backend'",
			},
			expectedReload: false,
		},
		{
			name:              "srv-enable-relocate",
			initialConfigFile: "servers/disabled-dummy.cfg",
			desiredConfigFile: "servers/enabled-real.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'web'",
			},
			expectedReload: false,
		},
		{
			name:              "srv-disable-relocate",
			initialConfigFile: "servers/enabled-real.cfg",
			desiredConfigFile: "servers/disabled-dummy.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'web'",
			},
			expectedReload: false,
		},
		{
			name:              "srv-maintenance",
			initialConfigFile: "servers/enabled-dummy.cfg",
			desiredConfigFile: "servers/disabled-dummy.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Update server 'srv1' in backend 'web'",
			},
			expectedReload: false,
		},

		// ==================== COMPLEX MIXED OPERATIONS ====================
		{
			name:              "mixed-add-remove-servers",
			initialConfigFile: "complex/three-servers.cfg",
			desiredConfigFile: "complex/srv2-srv3.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete server 'srv1' from backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "replace-all-servers",
			initialConfigFile: "basic/two-servers.cfg",
			desiredConfigFile: "complex/all-new-servers.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   2,
			expectedOperations: []string{
				"Delete server 'srv1' from backend 'test-backend'",
				"Delete server 'srv2' from backend 'test-backend'",
				"Create server 'srv4' in backend 'test-backend'",
				"Create server 'srv5' in backend 'test-backend'",
			},
			expectedReload: true,
		},
		{
			name:              "srv-weight-and-add",
			initialConfigFile: "servers/weight-100.cfg",
			desiredConfigFile: "servers/weight-100-plus-second.cfg",
			expectedCreates:   1,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create server 'srv2' in backend 'web'",
			},
			expectedReload: true,
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runSyncTest(t, tc)
		})
	}
}

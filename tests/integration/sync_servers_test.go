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
		},
		{
			name:              "remove-server-from-backend",
			initialConfigFile: "basic/two-servers.cfg",
			desiredConfigFile: "basic/one-backend.cfg",
		},

		// ==================== SERVER ATTRIBUTE MODIFICATIONS ====================
		{
			name:              "server-change-weight",
			initialConfigFile: "servers/weight-100.cfg",
			desiredConfigFile: "servers/weight-200.cfg",
		},
		{
			name:              "server-add-with-backup",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/with-backup.cfg",
		},
		{
			name:              "server-with-maxconn",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "servers/with-maxconn.cfg",
		},
		{
			name:              "server-with-check-intervals",
			initialConfigFile: "basic/empty.cfg",
			desiredConfigFile: "servers/with-check-inter.cfg",
		},
		{
			name:              "server-change-address",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/address-changed.cfg",
		},
		{
			name:              "server-change-port",
			initialConfigFile: "basic/one-backend.cfg",
			desiredConfigFile: "servers/port-changed.cfg",
		},

		// ==================== SLOT RELOCATION ====================
		{
			name:              "srv-enable-relocate",
			initialConfigFile: "servers/disabled-dummy.cfg",
			desiredConfigFile: "servers/enabled-real.cfg",
		},
		{
			name:              "srv-disable-relocate",
			initialConfigFile: "servers/enabled-real.cfg",
			desiredConfigFile: "servers/disabled-dummy.cfg",
		},
		{
			name:              "srv-maintenance",
			initialConfigFile: "servers/enabled-dummy.cfg",
			desiredConfigFile: "servers/disabled-dummy.cfg",
		},
		{
			// A reserved slot (disabled, no check on the line) becomes active
			// with `check` on the server line. HAProxy has no runtime setter for
			// it, so a render that moves `check` onto server lines gives up
			// reload-free server changes; the chart puts it on `default-server`.
			name:              "srv-enable-with-check-on-line",
			initialConfigFile: "servers/disabled-dummy.cfg",
			desiredConfigFile: "servers/enabled-with-check-on-line.cfg",
		},

		// ==================== COMPLEX MIXED OPERATIONS ====================
		{
			name:              "mixed-add-remove-servers",
			initialConfigFile: "complex/three-servers.cfg",
			desiredConfigFile: "complex/srv2-srv3.cfg",
		},
		{
			name:              "replace-all-servers",
			initialConfigFile: "basic/two-servers.cfg",
			desiredConfigFile: "complex/all-new-servers.cfg",
		},
		{
			name:              "srv-weight-and-add",
			initialConfigFile: "servers/weight-100.cfg",
			desiredConfigFile: "servers/weight-100-plus-second.cfg",
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

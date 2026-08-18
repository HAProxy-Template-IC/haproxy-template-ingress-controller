//go:build integration

package integration

import (
	"testing"
)

// TestSyncSections runs table-driven synchronization tests for section operations
// (resolvers, mailers, peers, cache, ring)
func TestSyncSections(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== RESOLVERS SECTION OPERATIONS ====================
		{
			name:              "resolvers-add-section",
			initialConfigFile: "resolvers/resolvers-base.cfg",
			desiredConfigFile: "resolvers/resolvers-with-dns.cfg",
		},
		{
			name:              "resolvers-remove-section",
			initialConfigFile: "resolvers/resolvers-with-dns.cfg",
			desiredConfigFile: "resolvers/resolvers-base.cfg",
		},

		// ==================== MAILERS SECTION OPERATIONS ====================
		{
			name:              "mailers-add-section",
			initialConfigFile: "mailers/mailers-base.cfg",
			desiredConfigFile: "mailers/mailers-with-alerts.cfg",
		},
		{
			name:              "mailers-remove-section",
			initialConfigFile: "mailers/mailers-with-alerts.cfg",
			desiredConfigFile: "mailers/mailers-base.cfg",
		},

		// ==================== PEERS SECTION OPERATIONS ====================
		{
			name:              "peers-add-section",
			initialConfigFile: "peers/peers-base.cfg",
			desiredConfigFile: "peers/peers-with-cluster.cfg",
		},
		{
			name:              "peers-remove-section",
			initialConfigFile: "peers/peers-with-cluster.cfg",
			desiredConfigFile: "peers/peers-base.cfg",
		},

		// ==================== CACHE SECTION OPERATIONS ====================
		{
			name:              "cache-add-section",
			initialConfigFile: "cache/cache-base.cfg",
			desiredConfigFile: "cache/cache-with-webcache.cfg",
		},
		{
			name:              "cache-remove-section",
			initialConfigFile: "cache/cache-with-webcache.cfg",
			desiredConfigFile: "cache/cache-base.cfg",
		},

		// ==================== RING SECTION OPERATIONS ====================
		{
			name:              "ring-add-section",
			initialConfigFile: "ring/ring-base.cfg",
			desiredConfigFile: "ring/ring-with-myring.cfg",
		},
		{
			name:              "ring-remove-section",
			initialConfigFile: "ring/ring-with-myring.cfg",
			desiredConfigFile: "ring/ring-base.cfg",
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

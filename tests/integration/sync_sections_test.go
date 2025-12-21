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
			expectedCreates:   3,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create resolver 'dns'",
				"Create nameserver 'dns1' in resolvers section 'dns'",
				"Create nameserver 'dns2' in resolvers section 'dns'",
			},
			expectedReload: true,
		},
		{
			name:              "resolvers-remove-section",
			initialConfigFile: "resolvers/resolvers-with-dns.cfg",
			desiredConfigFile: "resolvers/resolvers-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete resolver 'dns'",
			},
			expectedReload: true,
		},

		// ==================== MAILERS SECTION OPERATIONS ====================
		{
			name:              "mailers-add-section",
			initialConfigFile: "mailers/mailers-base.cfg",
			desiredConfigFile: "mailers/mailers-with-alerts.cfg",
			expectedCreates:   3,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create mailers 'alerts'",
				"Create mailer entry 'smtp1' in mailers section 'alerts'",
				"Create mailer entry 'smtp2' in mailers section 'alerts'",
			},
			expectedReload: true,
		},
		{
			name:              "mailers-remove-section",
			initialConfigFile: "mailers/mailers-with-alerts.cfg",
			desiredConfigFile: "mailers/mailers-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete mailers 'alerts'",
			},
			expectedReload: true,
		},

		// ==================== PEERS SECTION OPERATIONS ====================
		{
			name:              "peers-add-section",
			initialConfigFile: "peers/peers-base.cfg",
			desiredConfigFile: "peers/peers-with-cluster.cfg",
			expectedCreates:   4,
			expectedUpdates:   1,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create peer section 'mycluster'",
				"Create peer entry 'node1' in peer section 'mycluster'",
				"Create peer entry 'node2' in peer section 'mycluster'",
				"Create peer entry 'node3' in peer section 'mycluster'",
				"Update backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "peers-remove-section",
			initialConfigFile: "peers/peers-with-cluster.cfg",
			desiredConfigFile: "peers/peers-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   1,
			expectedDeletes:   1,
			expectedOperations: []string{
				"Delete peer section 'mycluster'",
				"Update backend 'web'",
			},
			expectedReload: true,
		},

		// ==================== CACHE SECTION OPERATIONS ====================
		{
			name:              "cache-add-section",
			initialConfigFile: "cache/cache-base.cfg",
			desiredConfigFile: "cache/cache-with-webcache.cfg",
			expectedCreates:   3,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create cache 'webcache'",
				"Create HTTP request rule (cache-use) in backend 'web'",
				"Create HTTP response rule (cache-store) in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "cache-remove-section",
			initialConfigFile: "cache/cache-with-webcache.cfg",
			desiredConfigFile: "cache/cache-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   3,
			expectedOperations: []string{
				"Delete HTTP request rule (cache-use) from backend 'web'",
				"Delete HTTP response rule (cache-store) from backend 'web'",
				"Delete cache 'webcache'",
			},
			expectedReload: true,
		},

		// ==================== RING SECTION OPERATIONS ====================
		{
			name:              "ring-add-section",
			initialConfigFile: "ring/ring-base.cfg",
			desiredConfigFile: "ring/ring-with-myring.cfg",
			expectedCreates:   2,
			expectedUpdates:   0,
			expectedDeletes:   0,
			expectedOperations: []string{
				"Create ring 'myring'",
				"Create log target (ring@myring) in backend 'web'",
			},
			expectedReload: true,
		},
		{
			name:              "ring-remove-section",
			initialConfigFile: "ring/ring-with-myring.cfg",
			desiredConfigFile: "ring/ring-base.cfg",
			expectedCreates:   0,
			expectedUpdates:   0,
			expectedDeletes:   2,
			expectedOperations: []string{
				"Delete log target (ring@myring) from backend 'web'",
				"Delete ring 'myring'",
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

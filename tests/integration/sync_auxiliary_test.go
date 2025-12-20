//go:build integration

package integration

import (
	"testing"
)

// TestSyncAuxiliary runs table-driven synchronization tests for auxiliary file operations
// (http-errors sections, SSL certificates, map files)
func TestSyncAuxiliary(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== HTTP ERRORS SECTION OPERATIONS ====================
		{
			name:              "add-http-errors-section",
			initialConfigFile: "http-errors/base.cfg",
			desiredConfigFile: "http-errors/with-errors.cfg",
			generalFiles: map[string]string{
				"/etc/haproxy/general/400.http": "error-files/400.http",
				"/etc/haproxy/general/403.http": "error-files/403.http",
				"/etc/haproxy/general/500.http": "error-files/500.http",
			},
			expectedCreates: 1,
			expectedUpdates: 0,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Create http-errors section 'myerrors'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-http-errors-section",
			initialConfigFile: "http-errors/with-errors.cfg",
			desiredConfigFile: "http-errors/base.cfg",
			// Initial config needs these files
			initialGeneralFiles: map[string]string{
				"/etc/haproxy/general/400.http": "error-files/400.http",
				"/etc/haproxy/general/403.http": "error-files/403.http",
				"/etc/haproxy/general/500.http": "error-files/500.http",
			},
			// Desired config has no error files (they should be deleted)
			generalFiles:    map[string]string{},
			expectedCreates: 0,
			expectedUpdates: 0,
			expectedDeletes: 1,
			expectedOperations: []string{
				"Delete http-errors section 'myerrors'",
			},
			expectedReload: true,
		},
		{
			name:              "update-http-errors-section",
			initialConfigFile: "http-errors/with-errors.cfg",
			desiredConfigFile: "http-errors/modified-errors.cfg",
			// Initial config needs these files
			initialGeneralFiles: map[string]string{
				"/etc/haproxy/general/400.http": "error-files/400.http",
				"/etc/haproxy/general/403.http": "error-files/403.http",
				"/etc/haproxy/general/500.http": "error-files/500.http",
			},
			// Desired config needs different files
			generalFiles: map[string]string{
				"/etc/haproxy/general/custom400.http": "error-files/custom400.http",
				"/etc/haproxy/general/404.http":       "error-files/404.http",
				"/etc/haproxy/general/503.http":       "error-files/503.http",
			},
			expectedCreates: 0,
			expectedUpdates: 1,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Update http-errors section 'myerrors'",
			},
			expectedReload: true,
		},

		// ==================== SSL FRONTEND OPERATIONS ====================
		{
			name:              "add-ssl-frontend",
			initialConfigFile: "ssl-frontend/base.cfg",
			desiredConfigFile: "ssl-frontend/with-ssl.cfg",
			sslCertificates: map[string]string{
				"example.com.pem": "ssl-certs/example.com.pem",
			},
			expectedCreates: 2,
			expectedUpdates: 0,
			expectedDeletes: 1,
			expectedOperations: []string{
				"Delete frontend 'http'",
				"Create frontend 'https'",
				"Create bind '*:443 ssl crt /etc/haproxy/ssl/example_com.pem' in frontend 'https'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-ssl-frontend",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/base.cfg",
			// Initial config needs SSL cert
			initialSSLCertificates: map[string]string{
				"example.com.pem": "ssl-certs/example.com.pem",
			},
			// Desired config has no SSL (cert should be deleted)
			sslCertificates: map[string]string{},
			expectedCreates: 2,
			expectedUpdates: 0,
			expectedDeletes: 1,
			expectedOperations: []string{
				"Delete frontend 'https'",
				"Create frontend 'http'",
				"Create bind '*:80' in frontend 'http'",
			},
			expectedReload: true,
		},
		{
			name:              "update-ssl-frontend-cert",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/modified-ssl.cfg",
			// Initial config needs this cert
			initialSSLCertificates: map[string]string{
				"example.com.pem": "ssl-certs/example.com.pem",
			},
			// Desired config needs different cert
			sslCertificates: map[string]string{
				"updated.com.pem": "ssl-certs/updated.com.pem",
			},
			expectedCreates: 0,
			expectedUpdates: 1,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Update bind '*:443 ssl crt /etc/haproxy/ssl/updated_com.pem' in frontend 'https'",
			},
			expectedReload: true,
		},

		// ==================== MAP FILE OPERATIONS ====================
		{
			name:              "add-map-frontend",
			initialConfigFile: "map-frontend/base.cfg",
			desiredConfigFile: "map-frontend/with-map.cfg",
			mapFiles: map[string]string{
				"domains.map": "map-files/domains.map",
			},
			expectedCreates: 1,
			expectedUpdates: 1,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Create backend switching rule (%[req.hdr(host),lower,map(/etc/haproxy/maps/domains.map,web)]) in frontend 'http'",
				"Update frontend 'http'",
			},
			expectedReload: true,
		},
		{
			name:              "remove-map-frontend",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/base.cfg",
			// Initial config needs map file
			initialMapFiles: map[string]string{
				"domains.map": "map-files/domains.map",
			},
			// Desired config has no map file (should be deleted)
			mapFiles:        map[string]string{},
			expectedCreates: 0,
			expectedUpdates: 1,
			expectedDeletes: 1,
			expectedOperations: []string{
				"Delete backend switching rule (%[req.hdr(host),lower,map(/etc/haproxy/maps/domains.map,web)]) from frontend 'http'",
				"Update frontend 'http'",
			},
			expectedReload: true,
		},
		{
			name:              "update-map-frontend",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/modified-map.cfg",
			// Initial config needs this map
			initialMapFiles: map[string]string{
				"domains.map": "map-files/domains.map",
			},
			// Desired config needs different map (config also references this new filename)
			mapFiles: map[string]string{
				"updated-domains.map": "map-files/updated-domains.map",
			},
			expectedCreates: 4,
			expectedUpdates: 1,
			expectedDeletes: 2,
			expectedOperations: []string{
				"Delete backend 'admin'",
				"Delete backend 'api'",
				"Create backend 'api-v2'",
				"Create backend 'mobile'",
				"Create server 'srv1' in backend 'api-v2'",
				"Create server 'srv1' in backend 'mobile'",
				"Update backend switching rule (%[req.hdr(host),lower,map(/etc/haproxy/maps/updated-domains.map,web)]) in frontend 'http'",
			},
			expectedReload: true,
		},
		{
			name:              "update-map-only-no-config-change",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/with-map.cfg", // SAME config - no changes
			// Initial config needs this map
			initialMapFiles: map[string]string{
				"domains.map": "map-files/domains.map",
			},
			// Desired config needs different map content
			mapFiles: map[string]string{
				"domains.map": "map-files/domains-updated.map", // Same name, different content
			},
			expectedCreates:    0,
			expectedUpdates:    0,
			expectedDeletes:    0,
			expectedOperations: []string{
				// No HAProxy config operations expected - config is identical
			},
			expectedReload: false, // No config changes, but map should still update
			// Verify the map file was actually updated
			verifyMapFiles: map[string]string{
				"domains.map": "map-files/domains-updated.map",
			},
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

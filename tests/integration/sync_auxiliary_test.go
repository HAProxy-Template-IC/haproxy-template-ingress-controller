//go:build integration

package integration

import (
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
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
				"Created general file 400.http",
				"Created general file 403.http",
				"Created general file 500.http",
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
			// Note: aux file deletions aren't tracked as operations when desired is empty
			// (orchestrator short-circuits comparison when desired files list is empty)
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
				"Created general file 404.http",
				"Created general file 503.http",
				"Created general file custom400.http",
				"Deleted general file 400.http",
				"Deleted general file 403.http",
				"Deleted general file 500.http",
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
				"Created SSL certificate example.com.pem",
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
			// Note: aux file deletions aren't tracked as operations when desired is empty
			// (orchestrator short-circuits comparison when desired files list is empty)
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
				"Created SSL certificate updated.com.pem",
				// Delete uses HAProxy's sanitized name (dots → underscores)
				"Deleted SSL certificate example_com.pem",
			},
			expectedReload: true,
		},
		{
			// Same cert filename, new PEM bytes, identical config: applied to the
			// live worker via the runtime API (set ssl cert + commit, v3.2+) with
			// no reload. SyncMode=runtime confirms the runtime path fired — a wrong
			// cert identifier would error and fall back to reload, failing this.
			name:              "update-ssl-cert-content-no-config-change",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/with-ssl.cfg", // SAME config
			initialSSLCertificates: map[string]string{
				"example.com.pem": "ssl-certs/example.com.pem",
			},
			sslCertificates: map[string]string{
				"example.com.pem": "ssl-certs/updated.com.pem", // same name, different PEM
			},
			// v3.2+: runtime `set ssl cert`, no reload. Older HAProxy reloads
			// (the runner flips the expectation via runtimeRequiresSSLCertCap).
			expectedReload:            false,
			expectedSyncMode:          dataplane.SyncModeRuntime,
			runtimeRequiresSSLCertCap: true,
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
				"Created map file domains.map",
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
			// Note: aux file deletions aren't tracked as operations when desired is empty
			// (orchestrator short-circuits comparison when desired files list is empty)
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
				"Created map file updated-domains.map",
				"Deleted map file domains.map",
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
			expectedCreates: 0,
			expectedUpdates: 0,
			expectedDeletes: 0,
			expectedOperations: []string{
				// No HAProxy config operations expected - config is identical
				// But map file update is tracked
				"Updated map file domains.map",
			},
			// Map content changed while the config body is identical: the
			// orchestrator applies the new map to the live worker via the
			// runtime API (ReplaceRuntimeMap, no reload) instead of
			// force-reloading. ReloadTriggered is false and the sync runs in
			// the runtime mode. The on-disk map file is still updated (verified
			// below) so any later unrelated reload converges.
			//
			// The updated map exercises all three runtime-delta op kinds
			// against real HAProxy: api.example.com's value changes
			// (atomic `set map`, no transient gap — the gitar-flagged case),
			// admin.example.com is removed (`del map`), and blog/shop are
			// added (`add map`).
			expectedReload:   false,
			expectedSyncMode: dataplane.SyncModeRuntime,
			verifyMapFiles: map[string]string{
				"domains.map": "map-files/domains-updated.map",
			},
			verifyRuntimeMap: true,
		},
		{
			// Capstone for the chart-side "Strategy 1" relocation: a per-route
			// policy value (here a body-size limit) that used to live in the
			// backend section now lives in body-size.map, applied by a static,
			// resource-agnostic frontend rule. Changing the value is therefore a
			// map-content-only change against an identical config body — so it
			// applies via the runtime API with NO reload. This pins the payoff
			// of the relocation end-to-end: the exact rule form the chart emits
			// (frontend-filters-250-request-body-size) is runtime-map-applicable.
			name:              "update-map-only-bodysize-no-reload",
			initialConfigFile: "map-frontend/with-body-size-map.cfg",
			desiredConfigFile: "map-frontend/with-body-size-map.cfg", // SAME config
			initialMapFiles: map[string]string{
				"body-size.map": "map-files/body-size.map", // web 1048576
			},
			mapFiles: map[string]string{
				"body-size.map": "map-files/body-size-updated.map", // web 8388608 (same name, new value)
			},
			expectedCreates: 0,
			expectedUpdates: 0,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Updated map file body-size.map",
			},
			expectedReload:   false,
			expectedSyncMode: dataplane.SyncModeRuntime,
			verifyMapFiles: map[string]string{
				"body-size.map": "map-files/body-size-updated.map",
			},
			verifyRuntimeMap: true,
		},
		{
			// The common Strategy-1 transition: an operator ADDS a per-route
			// policy (a body-size limit) to an ingress that didn't have one. The
			// backend's body-size.map entry appears where there was none, against
			// an identical config body. The live runtime map starts empty, so
			// this exercises the empty -> first-entry delta (a single `add map`)
			// — still no reload.
			name:              "add-map-entry-bodysize-no-reload",
			initialConfigFile: "map-frontend/with-body-size-map.cfg",
			desiredConfigFile: "map-frontend/with-body-size-map.cfg", // SAME config
			initialMapFiles: map[string]string{
				"body-size.map": "map-files/body-size-empty.map", // no entries
			},
			mapFiles: map[string]string{
				"body-size.map": "map-files/body-size.map", // web 1048576 (first entry)
			},
			expectedCreates: 0,
			expectedUpdates: 0,
			expectedDeletes: 0,
			expectedOperations: []string{
				"Updated map file body-size.map",
			},
			expectedReload:   false,
			expectedSyncMode: dataplane.SyncModeRuntime,
			verifyMapFiles: map[string]string{
				"body-size.map": "map-files/body-size.map",
			},
			verifyRuntimeMap: true,
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

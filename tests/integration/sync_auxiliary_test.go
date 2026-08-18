//go:build integration

package integration

import (
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
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
				"general/400.http": "error-files/400.http",
				"general/403.http": "error-files/403.http",
				"general/500.http": "error-files/500.http",
			},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "remove-http-errors-section",
			initialConfigFile: "http-errors/with-errors.cfg",
			desiredConfigFile: "http-errors/base.cfg",
			// Initial config needs these files
			initialGeneralFiles: map[string]string{
				"general/400.http": "error-files/400.http",
				"general/403.http": "error-files/403.http",
				"general/500.http": "error-files/500.http",
			},
			// The desired manifest omits them, which is what deletes them.
			generalFiles:    map[string]string{},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "update-http-errors-section",
			initialConfigFile: "http-errors/with-errors.cfg",
			desiredConfigFile: "http-errors/modified-errors.cfg",
			// Initial config needs these files
			initialGeneralFiles: map[string]string{
				"general/400.http": "error-files/400.http",
				"general/403.http": "error-files/403.http",
				"general/500.http": "error-files/500.http",
			},
			// Desired config needs different files
			generalFiles: map[string]string{
				"general/custom400.http": "error-files/custom400.http",
				"general/404.http":       "error-files/404.http",
				"general/503.http":       "error-files/503.http",
			},
			expectedVerdict: deployplan.VerdictReload,
		},

		// ==================== SSL FRONTEND OPERATIONS ====================
		{
			name:              "add-ssl-frontend",
			initialConfigFile: "ssl-frontend/base.cfg",
			desiredConfigFile: "ssl-frontend/with-ssl.cfg",
			sslCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/example.com.pem",
			},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "remove-ssl-frontend",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/base.cfg",
			// Initial config needs SSL cert
			initialSSLCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/example.com.pem",
			},
			// The desired manifest omits it, which is what deletes it.
			sslCertificates: map[string]string{},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "update-ssl-frontend-cert",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/modified-ssl.cfg",
			// Initial config needs this cert
			initialSSLCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/example.com.pem",
			},
			// Desired config needs different cert
			sslCertificates: map[string]string{
				"ssl/updated_com.pem": "ssl-certs/updated.com.pem",
			},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			// Same certificate path, new PEM bytes, identical configuration:
			// the change reaches the running worker through `set ssl cert` +
			// `commit ssl cert` with no reload. A wrong runtime identifier
			// would make HAProxy reject the command, which reloads instead and
			// fails this case.
			name:              "update-ssl-cert-content-no-config-change",
			initialConfigFile: "ssl-frontend/with-ssl.cfg",
			desiredConfigFile: "ssl-frontend/with-ssl.cfg", // SAME config
			initialSSLCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/example.com.pem",
			},
			sslCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/updated.com.pem", // same path, different PEM
			},
			expectedVerdict: deployplan.VerdictRuntime,
			verifySSLCertificates: map[string]string{
				"ssl/example_com.pem": "ssl-certs/updated.com.pem",
			},
		},

		// ==================== MAP FILE OPERATIONS ====================
		{
			name:              "add-map-frontend",
			initialConfigFile: "map-frontend/base.cfg",
			desiredConfigFile: "map-frontend/with-map.cfg",
			mapFiles: map[string]string{
				"maps/domains.map": "map-files/domains.map",
			},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "remove-map-frontend",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/base.cfg",
			// Initial config needs map file
			initialMapFiles: map[string]string{
				"maps/domains.map": "map-files/domains.map",
			},
			// The desired manifest omits it, which is what deletes it.
			mapFiles:        map[string]string{},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			name:              "update-map-frontend",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/modified-map.cfg",
			// Initial config needs this map
			initialMapFiles: map[string]string{
				"maps/domains.map": "map-files/domains.map",
			},
			// Desired config needs different map (config also references this new filename)
			mapFiles: map[string]string{
				"maps/updated-domains.map": "map-files/updated-domains.map",
			},
			expectedVerdict: deployplan.VerdictReload,
		},
		{
			// Map content changed against an identical configuration: the entry
			// delta runs on the live worker and nothing reloads. The updated map
			// exercises all three entry ops against real HAProxy —
			// api.example.com's value changes (`set map`, no transient gap),
			// admin.example.com is removed (`del map`), blog and shop are added
			// (`add map`).
			name:              "update-map-only-no-config-change",
			initialConfigFile: "map-frontend/with-map.cfg",
			desiredConfigFile: "map-frontend/with-map.cfg", // SAME config - no changes
			// Initial config needs this map
			initialMapFiles: map[string]string{
				"maps/domains.map": "map-files/domains.map",
			},
			// Desired config needs different map content
			mapFiles: map[string]string{
				"maps/domains.map": "map-files/domains-updated.map", // Same name, different content
			},
			expectedVerdict: deployplan.VerdictRuntime,
			verifyMapFiles: map[string]string{
				"maps/domains.map": "map-files/domains-updated.map",
			},
			verifyRuntimeMap: true,
		},
		{
			// Capstone for the chart-side "Strategy 1" relocation: a per-route
			// policy value (here a body-size limit) that used to live in the
			// backend section now lives in body-size.map, applied by a static,
			// resource-agnostic frontend rule. Changing the value is therefore a
			// map-content-only change against an identical config body, so it
			// runs on the live worker with NO reload. This pins the payoff of the
			// relocation end-to-end: the exact rule form the chart emits
			// (frontend-filters-250-request-body-size) is runtime-map-applicable.
			name:              "update-map-only-bodysize-no-reload",
			initialConfigFile: "map-frontend/with-body-size-map.cfg",
			desiredConfigFile: "map-frontend/with-body-size-map.cfg", // SAME config
			initialMapFiles: map[string]string{
				"maps/body-size.map": "map-files/body-size.map", // web 1048576
			},
			mapFiles: map[string]string{
				"maps/body-size.map": "map-files/body-size-updated.map", // web 8388608 (same name, new value)
			},
			expectedVerdict: deployplan.VerdictRuntime,
			verifyMapFiles: map[string]string{
				"maps/body-size.map": "map-files/body-size-updated.map",
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
				"maps/body-size.map": "map-files/body-size-empty.map", // no entries
			},
			mapFiles: map[string]string{
				"maps/body-size.map": "map-files/body-size.map", // web 1048576 (first entry)
			},
			expectedVerdict: deployplan.VerdictRuntime,
			verifyMapFiles: map[string]string{
				"maps/body-size.map": "map-files/body-size.map",
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

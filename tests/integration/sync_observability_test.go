//go:build integration

package integration

import (
	"testing"
)

// quicCertificate is what the QUIC frontend's bind needs on disk. The path is
// the manifest path; the configuration names it by its bare filename, which
// HAProxy resolves against `crt-base`.
var quicCertificate = map[string]string{"ssl/example_com.pem": "ssl-certs/example.com.pem"}

// TestSyncObservability tests synchronization of observability sections that
// later HAProxy releases introduced; a case is skipped below the release that
// can parse its directives.
func TestSyncObservability(t *testing.T) {
	t.Parallel()
	testCases := []syncTestCase{
		// ==================== LOG PROFILE OPERATIONS (HAProxy 3.1+) ====================
		{
			name:              "log-profile-add",
			initialConfigFile: "log-profiles/base.cfg",
			desiredConfigFile: "log-profiles/with-profile.cfg",
			minHAProxy:        "3.1",
		},
		{
			name:              "log-profile-remove",
			initialConfigFile: "log-profiles/with-profile.cfg",
			desiredConfigFile: "log-profiles/base.cfg",
			minHAProxy:        "3.1",
		},

		// ==================== TRACES OPERATIONS (HAProxy 3.1+) ====================
		{
			name:              "traces-add",
			initialConfigFile: "traces/base.cfg",
			desiredConfigFile: "traces/with-traces.cfg",
			minHAProxy:        "3.1",
		},

		// ==================== QUIC INITIAL RULES (HAProxy 3.1+) ====================
		{
			name:              "quic-initial-rule-add",
			initialConfigFile: "quic-rules/frontend-base.cfg",
			desiredConfigFile: "quic-rules/frontend-with-quic-rules.cfg",
			minHAProxy:        "3.1",
			// A QUIC bind is a TLS bind: without a certificate HAProxy
			// refuses to load the configuration at all.
			initialSSLCertificates: quicCertificate,
			sslCertificates:        quicCertificate,
		},
		{
			name:                   "quic-initial-rule-remove",
			initialConfigFile:      "quic-rules/frontend-with-quic-rules.cfg",
			desiredConfigFile:      "quic-rules/frontend-base.cfg",
			minHAProxy:             "3.1",
			initialSSLCertificates: quicCertificate,
			sslCertificates:        quicCertificate,
		},

		// ==================== ACME PROVIDERS (HAProxy 3.2+) ====================
		{
			name:              "acme-provider-add",
			initialConfigFile: "acme/base.cfg",
			desiredConfigFile: "acme/with-letsencrypt.cfg",
			minHAProxy:        "3.2",
		},
		{
			name:              "acme-provider-remove",
			initialConfigFile: "acme/with-letsencrypt.cfg",
			desiredConfigFile: "acme/base.cfg",
			minHAProxy:        "3.2",
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

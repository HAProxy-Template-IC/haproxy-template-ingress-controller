// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

// Capabilities defines which features are available for a given DataPlane API version.
// Version thresholds verified against OpenAPI specs for v3.0, v3.1, v3.2, v3.3.
type Capabilities struct {
	// Storage capabilities
	SupportsCrtList        bool // /v3/storage/ssl_crt_lists (v3.2+)
	SupportsMapStorage     bool // /v3/storage/maps (v3.0+)
	SupportsGeneralStorage bool // /v3/storage/general (v3.0+)
	SupportsSslCaFiles     bool // /v3/runtime/ssl_ca_files (v3.2+)
	SupportsSslCrlFiles    bool // /v3/runtime/ssl_crl_files (v3.2+)

	// Configuration capabilities
	SupportsHTTP2            bool // HTTP/2 configuration (v3.0+)
	SupportsQUIC             bool // QUIC/HTTP3 configuration (v3.0+)
	SupportsQUICInitialRules bool // QUIC initial rules endpoints (v3.1+)

	// Observability capabilities
	SupportsLogProfiles bool // /v3/services/haproxy/configuration/log_profiles (v3.1+)
	SupportsTraces      bool // /v3/services/haproxy/configuration/traces (v3.1+)

	// Certificate automation capabilities
	SupportsAcmeProviders bool // /v3/services/haproxy/configuration/acmes (v3.2+)

	// Model metadata capabilities
	SupportsConfigMetadata bool // Metadata field on config models like ACL, Server, etc. (v3.2+)

	// Runtime capabilities
	SupportsRuntimeMaps    bool // Runtime map operations (v3.0+)
	SupportsRuntimeServers bool // Runtime server operations (v3.0+)

	// Enterprise-only capabilities (all false for Community edition)
	// These are available in all Enterprise API versions (v3.0, v3.1, v3.2)

	// SupportsWAF indicates WAF management endpoints are available.
	// Includes: waf_body_rules (frontend/backend), waf/rulesets
	// Note: waf_global and waf_profiles require v3.2+ (see SupportsWAFGlobal, SupportsWAFProfiles)
	SupportsWAF bool

	// SupportsWAFGlobal indicates WAF global configuration endpoint is available.
	// Only available in HAProxy Enterprise v3.2+ (waf_global endpoint)
	SupportsWAFGlobal bool

	// SupportsWAFProfiles indicates WAF profile management endpoints are available.
	// Only available in HAProxy Enterprise v3.2+ (waf_profiles endpoint)
	SupportsWAFProfiles bool

	// SupportsUDPLBACLs indicates UDP load balancer ACL endpoints are available.
	// Only available in HAProxy Enterprise v3.2+ (udp_lbs/{name}/acls endpoint)
	SupportsUDPLBACLs bool

	// SupportsUDPLBServerSwitchingRules indicates UDP load balancer server switching rule endpoints are available.
	// Only available in HAProxy Enterprise v3.2+ (udp_lbs/{name}/server_switching_rules endpoint)
	SupportsUDPLBServerSwitchingRules bool

	// SupportsKeepalived indicates Keepalived/VRRP management endpoints are available.
	// Includes: vrrp_instances, vrrp_sync_groups, vrrp_track_scripts, keepalived transactions
	SupportsKeepalived bool

	// SupportsUDPLoadBalancing indicates UDP load balancer management endpoints are available.
	// Includes: udp_lbs with ACLs, dgram_binds, log_targets, server_switching_rules
	SupportsUDPLoadBalancing bool

	// SupportsBotManagement indicates bot management endpoints are available.
	// Includes: botmgmt_profiles, captchas
	SupportsBotManagement bool

	// SupportsGitIntegration indicates Git integration endpoints are available.
	// Includes: git/settings, git/actions
	SupportsGitIntegration bool

	// SupportsDynamicUpdate indicates dynamic update endpoints are available.
	// Includes: dynamic_update_rules, dynamic_update_section
	SupportsDynamicUpdate bool

	// SupportsALOHA indicates ALOHA feature endpoints are available.
	// Includes: aloha, aloha/actions
	SupportsALOHA bool

	// SupportsAdvancedLogging indicates advanced logging endpoints are available.
	// Includes: logs/config, logs/inputs, logs/outputs
	SupportsAdvancedLogging bool

	// SupportsPing indicates the ping endpoint is available.
	// Only available in HAProxy Enterprise v3.2+ (/v3/ping endpoint)
	SupportsPing bool
}

// buildCapabilities constructs a capability map based on version and edition.
// Thresholds verified against OpenAPI specs for v3.0, v3.1, v3.2, v3.3 (both Community and Enterprise).
func buildCapabilities(_, minor int, isEnterprise bool) Capabilities {
	// Baseline: all v3.0+ features (verified against OpenAPI specs)
	caps := Capabilities{
		SupportsGeneralStorage: true,
		SupportsMapStorage:     true, // All v3.x have /storage/maps
		SupportsHTTP2:          true,
		SupportsQUIC:           true, // All v3.x have QUIC options
		SupportsRuntimeMaps:    true,
		SupportsRuntimeServers: true,
	}

	// v3.1+ features (community)
	if minor >= 1 {
		caps.SupportsLogProfiles = true // log_profiles configuration added in v3.1
		caps.SupportsTraces = true      // traces configuration added in v3.1
	}

	// v3.2+ features (community)
	if minor >= 2 {
		caps.SupportsCrtList = true        // Only v3.2+ has /storage/ssl_crt_lists
		caps.SupportsSslCaFiles = true     // Only v3.2+ has /runtime/ssl_ca_files
		caps.SupportsSslCrlFiles = true    // Only v3.2+ has /runtime/ssl_crl_files
		caps.SupportsConfigMetadata = true // Metadata field on ACL, Server, etc. models (v3.2+)
	}

	// Enterprise-only features (available in all enterprise versions)
	if isEnterprise {
		caps.SupportsWAF = true // waf_body_rules, waf/rulesets available in all EE versions
		caps.SupportsKeepalived = true
		caps.SupportsUDPLoadBalancing = true
		caps.SupportsBotManagement = true
		caps.SupportsGitIntegration = true
		caps.SupportsDynamicUpdate = true
		caps.SupportsALOHA = true
		caps.SupportsAdvancedLogging = true

		// v3.2+ enterprise-only features
		if minor >= 2 {
			caps.SupportsWAFGlobal = true                 // waf_global only in v3.2ee
			caps.SupportsWAFProfiles = true               // waf_profiles only in v3.2ee
			caps.SupportsUDPLBACLs = true                 // udp_lbs/{name}/acls only in v3.2ee
			caps.SupportsUDPLBServerSwitchingRules = true // udp_lbs/{name}/server_switching_rules only in v3.2ee
			caps.SupportsPing = true                      // /v3/ping only in v3.2ee
		}
	}

	return caps
}

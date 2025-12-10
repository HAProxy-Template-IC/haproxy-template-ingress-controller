// Package client provides a multi-version wrapper for HAProxy Dataplane API clients.
//
// This package implements the Kubernetes-style clientset pattern to support multiple
// HAProxy DataPlane API versions (3.0, 3.1, 3.2) with:
// - Runtime version detection using /v3/info endpoint
// - Capability-based routing for graceful degradation
// - Version-specific client accessors
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	v30 "haproxy-template-ic/pkg/generated/dataplaneapi/v30"
	v30ee "haproxy-template-ic/pkg/generated/dataplaneapi/v30ee"
	v31 "haproxy-template-ic/pkg/generated/dataplaneapi/v31"
	v31ee "haproxy-template-ic/pkg/generated/dataplaneapi/v31ee"
	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// Clientset provides access to clients for all supported HAProxy DataPlane API versions.
// This follows the Kubernetes clientset pattern, allowing version-specific operations
// while maintaining compatibility across HAProxy versions.
type Clientset struct {
	// Community version-specific clients
	v30Client *v30.Client
	v31Client *v31.Client
	v32Client *v32.Client

	// Enterprise version-specific clients
	v30eeClient *v30ee.Client
	v31eeClient *v31ee.Client
	v32eeClient *v32ee.Client

	// Detected server version information
	detectedVersion string       // Full version string (e.g., "v3.2.6 87ad0bcf" or "v3.0r1")
	majorVersion    int          // Major version (3)
	minorVersion    int          // Minor version (0, 1, or 2)
	isEnterprise    bool         // True if HAProxy Enterprise edition
	capabilities    Capabilities // Feature availability map

	// Configuration
	endpoint Endpoint
	logger   *slog.Logger
}

// Capabilities defines which features are available for a given DataPlane API version.
// Version thresholds verified against OpenAPI specs for v3.0, v3.1, v3.2.
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

// VersionInfo contains detected version information from /v3/info endpoint.
type VersionInfo struct {
	API struct {
		Version string `json:"version"` // e.g., "v3.2.6 87ad0bcf"
	} `json:"api"`
}

// NewClientset creates a new multi-version clientset for the given endpoint.
// It detects the server's DataPlane API version and creates appropriate clients.
//
// Example:
//
//	clientset, err := client.NewClientset(ctx, client.Endpoint{
//	    URL:      "http://haproxy:5555",
//	    Username: "admin",
//	    Password: "password",
//	}, logger)
//	if err != nil {
//	    return err
//	}
//
//	// Use version-specific client
//	if clientset.Capabilities().SupportsCrtList {
//	    client := clientset.V32()
//	    // Use v3.2-specific features
//	} else {
//	    client := clientset.V30()
//	    // Fallback to v3.0-compatible operations
//	}
func NewClientset(ctx context.Context, endpoint *Endpoint, logger *slog.Logger) (*Clientset, error) {
	if logger == nil {
		logger = slog.Default()
	}

	var major, minor int
	var detectedVersion string
	var isEnterprise bool

	// Use cached version if available (avoids redundant /v3/info call)
	if endpoint.HasCachedVersion() {
		major = endpoint.CachedMajorVersion
		minor = endpoint.CachedMinorVersion
		detectedVersion = endpoint.CachedFullVersion
		isEnterprise = endpoint.CachedIsEnterprise
		logger.Debug("using cached version from discovery",
			"version", detectedVersion,
			"major", major,
			"minor", minor,
			"enterprise", isEnterprise,
		)
	} else {
		// Detect server version
		versionInfo, err := DetectVersion(ctx, endpoint, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to detect DataPlane API version: %w", err)
		}

		// Parse version string (e.g., "v3.2.6 87ad0bcf" -> major=3, minor=2)
		major, minor, err = ParseVersion(versionInfo.API.Version)
		if err != nil {
			logger.Warn("failed to parse version, assuming v3.0",
				"version", versionInfo.API.Version,
				"error", err,
			)
			major, minor = 3, 0
		}
		detectedVersion = versionInfo.API.Version

		// Detect enterprise edition from version string
		isEnterprise = IsEnterpriseVersion(detectedVersion)

		logger.Debug("detected DataPlane API version",
			"version", detectedVersion,
			"major", major,
			"minor", minor,
			"enterprise", isEnterprise,
		)
	}

	// Validate we support this major version
	if major != 3 {
		return nil, fmt.Errorf("unsupported DataPlane API major version: %d (only v3.x is supported)", major)
	}

	// Build capabilities map based on detected version and edition
	capabilities := buildCapabilities(major, minor, isEnterprise)

	// Create request editor for basic auth
	authEditor := func(ctx context.Context, req *http.Request) error {
		req.SetBasicAuth(endpoint.Username, endpoint.Password)
		return nil
	}

	// Create community clients for all supported versions
	// Note: We create all clients regardless of detected version for maximum flexibility
	v30Client, err := v30.NewClient(endpoint.URL, v30.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.0 client: %w", err)
	}

	v31Client, err := v31.NewClient(endpoint.URL, v31.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.1 client: %w", err)
	}

	v32Client, err := v32.NewClient(endpoint.URL, v32.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.2 client: %w", err)
	}

	// Create enterprise clients for all supported versions
	v30eeClient, err := v30ee.NewClient(endpoint.URL, v30ee.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.0 enterprise client: %w", err)
	}

	v31eeClient, err := v31ee.NewClient(endpoint.URL, v31ee.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.1 enterprise client: %w", err)
	}

	v32eeClient, err := v32ee.NewClient(endpoint.URL, v32ee.WithRequestEditorFn(authEditor))
	if err != nil {
		return nil, fmt.Errorf("failed to create v3.2 enterprise client: %w", err)
	}

	return &Clientset{
		v30Client:       v30Client,
		v31Client:       v31Client,
		v32Client:       v32Client,
		v30eeClient:     v30eeClient,
		v31eeClient:     v31eeClient,
		v32eeClient:     v32eeClient,
		detectedVersion: detectedVersion,
		majorVersion:    major,
		minorVersion:    minor,
		isEnterprise:    isEnterprise,
		capabilities:    capabilities,
		endpoint:        *endpoint,
		logger:          logger,
	}, nil
}

// V30 returns the DataPlane API v3.0 client.
// This client is compatible with HAProxy 2.4 and later.
func (c *Clientset) V30() *v30.Client {
	return c.v30Client
}

// V31 returns the DataPlane API v3.1 client.
// This client is compatible with HAProxy 2.6 and later.
func (c *Clientset) V31() *v31.Client {
	return c.v31Client
}

// V32 returns the DataPlane API v3.2 client.
// This client is compatible with HAProxy 2.8 and later.
func (c *Clientset) V32() *v32.Client {
	return c.v32Client
}

// V30EE returns the HAProxy Enterprise DataPlane API v3.0 client.
func (c *Clientset) V30EE() *v30ee.Client {
	return c.v30eeClient
}

// V31EE returns the HAProxy Enterprise DataPlane API v3.1 client.
func (c *Clientset) V31EE() *v31ee.Client {
	return c.v31eeClient
}

// V32EE returns the HAProxy Enterprise DataPlane API v3.2 client.
func (c *Clientset) V32EE() *v32ee.Client {
	return c.v32eeClient
}

// DetectedVersion returns the full version string detected from the server.
// Example: "v3.2.6 87ad0bcf" for community or "v3.0r1" for enterprise.
func (c *Clientset) DetectedVersion() string {
	return c.detectedVersion
}

// MajorVersion returns the major version number (e.g., 3 for v3.x).
func (c *Clientset) MajorVersion() int {
	return c.majorVersion
}

// MinorVersion returns the minor version number (e.g., 0, 1, or 2 for v3.0, v3.1, v3.2).
func (c *Clientset) MinorVersion() int {
	return c.minorVersion
}

// Capabilities returns the feature availability map for the detected version.
func (c *Clientset) Capabilities() Capabilities {
	return c.capabilities
}

// IsEnterprise returns true if the detected HAProxy is an Enterprise edition.
func (c *Clientset) IsEnterprise() bool {
	return c.isEnterprise
}

// PreferredClient returns the most appropriate client based on detected version and edition.
// This is useful for code that wants to use the best available API without
// explicitly checking capabilities.
//
// Returns:
//   - Enterprise clients (v32ee, v31ee, v30ee) if HAProxy Enterprise is detected
//   - Community clients (v32, v31, v30) for HAProxy Community
//
// Version selection:
//   - v3.2+ client if server is v3.2+
//   - v3.1 client if server is v3.1
//   - v3.0 client if server is v3.0 or unknown
func (c *Clientset) PreferredClient() interface{} {
	if c.isEnterprise {
		switch c.minorVersion {
		case 2:
			return c.v32eeClient
		case 1:
			return c.v31eeClient
		default:
			return c.v30eeClient
		}
	}

	switch c.minorVersion {
	case 2:
		return c.v32Client
	case 1:
		return c.v31Client
	default:
		return c.v30Client
	}
}

// DetectVersion queries the DataPlane API /v3/info endpoint to determine the server version.
// This function is exported for use by the discovery component to check remote pod versions
// before admitting them for deployment.
func DetectVersion(ctx context.Context, endpoint *Endpoint, _ *slog.Logger) (*VersionInfo, error) {
	// Construct /v3/info URL (strip any version suffix from base URL)
	baseURL := strings.TrimSuffix(endpoint.URL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v2")
	baseURL = strings.TrimSuffix(baseURL, "/v3")
	infoURL := baseURL + "/v3/info"

	req, err := http.NewRequestWithContext(ctx, "GET", infoURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(endpoint.Username, endpoint.Password)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version endpoint returned status %d", resp.StatusCode)
	}

	var versionInfo VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil {
		return nil, fmt.Errorf("failed to decode version response: %w", err)
	}

	if versionInfo.API.Version == "" {
		return nil, fmt.Errorf("version string is empty in response")
	}

	return &versionInfo, nil
}

// ParseVersion extracts major and minor version numbers from version string.
// Example: "v3.2.6 87ad0bcf" -> (3, 2, nil).
// This function is exported for use by the version compatibility checking logic.
func ParseVersion(version string) (major, minor int, err error) {
	// Split on whitespace to get version part (e.g., "v3.2.6")
	parts := strings.Fields(version)
	if len(parts) == 0 {
		return 0, 0, fmt.Errorf("empty version string")
	}

	versionPart := parts[0]

	// Strip 'v' prefix if present
	versionPart = strings.TrimPrefix(versionPart, "v")

	// Split on dots (e.g., "3.2.6" -> ["3", "2", "6"])
	segments := strings.Split(versionPart, ".")
	if len(segments) < 2 {
		return 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	// Parse major version
	if _, err := fmt.Sscanf(segments[0], "%d", &major); err != nil {
		return 0, 0, fmt.Errorf("failed to parse major version: %w", err)
	}

	// Parse minor version
	if _, err := fmt.Sscanf(segments[1], "%d", &minor); err != nil {
		return 0, 0, fmt.Errorf("failed to parse minor version: %w", err)
	}

	return major, minor, nil
}

// buildCapabilities constructs a capability map based on version and edition.
// Thresholds verified against OpenAPI specs for v3.0, v3.1, v3.2 (both Community and Enterprise).
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
		caps.SupportsCrtList = true     // Only v3.2+ has /storage/ssl_crt_lists
		caps.SupportsSslCaFiles = true  // Only v3.2+ has /runtime/ssl_ca_files
		caps.SupportsSslCrlFiles = true // Only v3.2+ has /runtime/ssl_crl_files
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

// IsEnterpriseVersion detects if a version string indicates HAProxy Enterprise edition.
// Enterprise versions typically contain "r" followed by a number (e.g., "3.0r1", "v3.1r1")
// or contain "Enterprise" in the version string.
//
// Examples:
//   - "v3.0r1" -> true (enterprise version format)
//   - "3.1r1" -> true (enterprise version format)
//   - "v3.2.6 87ad0bcf" -> false (community version format)
//   - "HAProxy Enterprise 3.0r1" -> true (contains "Enterprise")
//
// enterpriseHAProxyVersionPattern matches enterprise HAProxy version format: X.YrZ (e.g., 3.0r1, v3.1r1).
// This is used for detecting enterprise from HAProxy binary version strings.
var enterpriseHAProxyVersionPattern = regexp.MustCompile(`^v?\d+\.\d+r\d+`)

// enterpriseDataPlaneAPIPattern matches enterprise DataPlane API version format: vX.Y.Z-eeN (e.g., v3.0.15-ee1).
// This is used for detecting enterprise from DataPlane API version strings.
var enterpriseDataPlaneAPIPattern = regexp.MustCompile(`-ee\d+`)

func IsEnterpriseVersion(version string) bool {
	// Check for "Enterprise" keyword (case-insensitive)
	if strings.Contains(strings.ToLower(version), "enterprise") {
		return true
	}

	// Check for DataPlane API enterprise suffix: -eeN (e.g., v3.0.15-ee1)
	// This is the most reliable indicator from the DataPlane API /v3/info endpoint
	if enterpriseDataPlaneAPIPattern.MatchString(version) {
		return true
	}

	// Check for HAProxy enterprise version format: X.YrZ (e.g., 3.0r1, 3.1r1)
	// This pattern matches versions like "v3.0r1", "3.1r1", "v3.2r1"
	// Used for HAProxy binary version strings
	versionPart := strings.Fields(version)
	if len(versionPart) == 0 {
		return false
	}

	return enterpriseHAProxyVersionPattern.MatchString(versionPart[0])
}

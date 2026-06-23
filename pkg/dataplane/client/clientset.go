// Package client provides a multi-version wrapper for HAProxy Dataplane API clients.
//
// This package implements the Kubernetes-style clientset pattern to support multiple
// HAProxy DataPlane API versions (3.0, 3.1, 3.2, 3.3) with:
// - Runtime version detection using /v3/info endpoint
// - Capability-based routing for graceful degradation
// - Version-specific client accessors
package client

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

const (
	// pooledMaxIdleConnsPerHost sizes the idle connection pool per dataplane API
	// endpoint above Go's default (http.DefaultMaxIdleConnsPerHost = 2), so a
	// sync's parallel in-transaction operations to one endpoint reuse keep-alive
	// connections rather than re-establishing the excess each call.
	pooledMaxIdleConnsPerHost = 16
	// pooledMaxIdleConns bounds idle connections across all watched endpoints.
	pooledMaxIdleConns = 256
)

// sharedHTTPClient is reused by every version client across all endpoints so
// connections to each dataplane API server are pooled and kept alive between
// calls. Pooling is orthogonal to parallelism: the transport still dials a
// separate connection per concurrent request (HTTP/1.1 cannot multiplex), so
// reuse does NOT serialize a parallel fan-out — it only avoids re-establishing
// connections that the default 2-idle-per-host pool would otherwise drop.
var sharedHTTPClient = newPooledHTTPClient()

func newPooledHTTPClient() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// stdlib always uses *http.Transport; fall back to the default client.
		return &http.Client{}
	}
	tr := base.Clone()
	tr.MaxIdleConnsPerHost = pooledMaxIdleConnsPerHost
	tr.MaxIdleConns = pooledMaxIdleConns
	return &http.Client{Transport: tr}
}

// Clientset provides access to clients for all supported HAProxy DataPlane API versions.
// This follows the Kubernetes clientset pattern, allowing version-specific operations
// while maintaining compatibility across HAProxy versions.
type Clientset struct {
	// Community version-specific clients
	v30Client *v30.Client
	v31Client *v31.Client
	v32Client *v32.Client
	v33Client *v33.Client

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

// NewClientset creates a new multi-version clientset for the given endpoint.
// It detects the server's DataPlane API version and creates appropriate clients.
//
// Example:
//
//	// endpoint is taken by pointer so &client.Endpoint{...} is required.
//	clientset, err := client.NewClientset(ctx, &client.Endpoint{
//	    URL:      "http://haproxy:5555",
//	    Username: "admin",
//	    Password: "password",
//	}, logger)
//	if err != nil {
//	    return err
//	}
//
//	// Use version-specific client. Naming the variable `versioned`
//	// avoids shadowing the imported `client` package.
//	if clientset.Capabilities().SupportsCrtList {
//	    versioned := clientset.V32()
//	    // Use v3.2-specific features
//	    _ = versioned
//	} else {
//	    versioned := clientset.V30()
//	    // Fallback to v3.0-compatible operations
//	    _ = versioned
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
		logger.Debug("Using cached version from discovery",
			"version", detectedVersion,
			"major", major,
			"minor", minor,
			"enterprise", isEnterprise,
		)
	} else {
		// Detect server version
		versionInfo, err := DetectVersion(ctx, endpoint, logger)
		if err != nil {
			return nil, fmt.Errorf("detecting DataPlane API version: %w", err)
		}

		// Parse version string (e.g., "v3.2.6 87ad0bcf" -> major=3, minor=2)
		major, minor = 3, 0
		if v, perr := ParseVersion(versionInfo.API.Version); perr == nil {
			major, minor = v.Major, v.Minor
		} else {
			logger.Warn("Failed to parse version, assuming v3.0",
				"version", versionInfo.API.Version,
				"error", perr,
			)
		}
		detectedVersion = versionInfo.API.Version

		// Detect enterprise edition from version string
		isEnterprise = IsEnterpriseVersion(detectedVersion)

		logger.Debug("Detected DataPlane API version",
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

	// Build capabilities map based on detected version and edition.
	capabilities := buildCapabilities(major, minor, isEnterprise)

	// Create request editor for basic auth
	authEditor := func(ctx context.Context, req *http.Request) error {
		req.SetBasicAuth(endpoint.Username, endpoint.Password)
		return nil
	}

	// Create community clients for all supported versions
	// Note: We create all clients regardless of detected version for maximum flexibility.
	// All share sharedHTTPClient so connections to this endpoint are pooled and kept
	// alive across calls (see sharedHTTPClient): connection reuse, NOT request
	// serialization — the pool still hands out one connection per concurrent request.
	v30Client, err := v30.NewClient(endpoint.URL, v30.WithRequestEditorFn(authEditor), v30.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.0 client: %w", err)
	}

	v31Client, err := v31.NewClient(endpoint.URL, v31.WithRequestEditorFn(authEditor), v31.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.1 client: %w", err)
	}

	v32Client, err := v32.NewClient(endpoint.URL, v32.WithRequestEditorFn(authEditor), v32.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.2 client: %w", err)
	}

	v33Client, err := v33.NewClient(endpoint.URL, v33.WithRequestEditorFn(authEditor), v33.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.3 client: %w", err)
	}

	// Create enterprise clients for all supported versions
	v30eeClient, err := v30ee.NewClient(endpoint.URL, v30ee.WithRequestEditorFn(authEditor), v30ee.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.0 enterprise client: %w", err)
	}

	v31eeClient, err := v31ee.NewClient(endpoint.URL, v31ee.WithRequestEditorFn(authEditor), v31ee.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.1 enterprise client: %w", err)
	}

	v32eeClient, err := v32ee.NewClient(endpoint.URL, v32ee.WithRequestEditorFn(authEditor), v32ee.WithHTTPClient(sharedHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("creating v3.2 enterprise client: %w", err)
	}

	return &Clientset{
		v30Client:       v30Client,
		v31Client:       v31Client,
		v32Client:       v32Client,
		v33Client:       v33Client,
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

// V33 returns the DataPlane API v3.3 client.
func (c *Clientset) V33() *v33.Client {
	return c.v33Client
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
// Version selection (newer minors clamp down to the newest bundled client):
//   - v3.3 client if server is v3.3 or newer
//   - v3.2 client if server is v3.2
//   - v3.1 client if server is v3.1
//   - v3.0 client if server is v3.0 or unknown
func (c *Clientset) PreferredClient() any {
	if c.isEnterprise {
		// No v33ee client yet (newest bundled enterprise client is v3.2ee).
		// v3.3+ enterprise minors clamp DOWN to it (same API endpoints) rather
		// than falling through to the oldest v3.0ee client.
		switch {
		case c.minorVersion >= 2:
			return c.v32eeClient
		case c.minorVersion == 1:
			return c.v31eeClient
		default:
			return c.v30eeClient
		}
	}

	// v3.3 is the newest bundled community client. Minor versions newer than
	// that clamp DOWN to it: the DataPlane API minor cadence is independent of
	// these generated clients, so a v3.4+ server reuses the v3.3 client (newest
	// API surface) instead of falling through to the oldest, most-restrictive
	// v3.0 client. Mirrors getCachedValidatorForVersion in validate_syntax.go.
	switch {
	case c.minorVersion >= 3:
		return c.v33Client
	case c.minorVersion == 2:
		return c.v32Client
	case c.minorVersion == 1:
		return c.v31Client
	default:
		return c.v30Client
	}
}

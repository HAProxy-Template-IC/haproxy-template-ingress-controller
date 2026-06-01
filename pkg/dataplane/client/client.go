// Package client provides a high-level wrapper around the generated HAProxy Dataplane API client.
//
// This wrapper adds:
// - Multi-version support (v3.0, v3.1, v3.2, v3.3) and Enterprise variants
// - Runtime version detection
// - Capability-based feature detection
// - Raw config push (skip_reload + X-Runtime-Actions, or force_reload)
// - Auxiliary file storage operations (maps, SSL certs, general files, crt-lists)
// - Error handling and retry logic
package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Endpoint represents HAProxy Dataplane API connection information.
// This is a convenience type alias to avoid circular imports.
type Endpoint struct {
	URL      string
	Username string
	Password string
	PodName  string // Kubernetes pod name for observability

	// Cached version info (optional, avoids redundant /v3/info calls if set)
	CachedMajorVersion int
	CachedMinorVersion int
	CachedFullVersion  string
	CachedIsEnterprise bool // True if this is HAProxy Enterprise edition
}

// HasCachedVersion returns true if version info has been cached.
func (e *Endpoint) HasCachedVersion() bool {
	return e.CachedMajorVersion > 0
}

// DataplaneClient wraps the multi-version Clientset with additional functionality
// for HAProxy Dataplane API operations. It automatically uses the appropriate
// client version based on runtime detection.
type DataplaneClient struct {
	clientset *Clientset
	Endpoint  Endpoint // Embedded endpoint information
	logger    *slog.Logger
}

// Config contains configuration options for creating a DataplaneClient.
type Config struct {
	// BaseURL is the HAProxy Dataplane API endpoint (e.g., "http://localhost:5555/v3").
	// A trailing "/v2" or "/v3" is stripped before the v3 version-detection probe;
	// only v3.x operations are supported (no v2-only endpoints).
	BaseURL string

	// Username for basic authentication
	Username string

	// Password for basic authentication
	Password string

	// PodName is the Kubernetes pod name (for observability)
	PodName string

	// HTTPClient allows injecting a custom HTTP client (useful for testing)
	HTTPClient *http.Client

	// Logger for logging request/response details on errors (optional)
	// If nil, slog.Default() will be used
	Logger *slog.Logger
}

// New creates a new DataplaneClient with the provided configuration.
// It automatically detects the server's DataPlane API version and creates
// an appropriate multi-version clientset.
//
// Example:
//
//	// dpClient avoids shadowing the imported `client` package; cfg is
//	// taken by pointer so &client.Config{...} is required.
//	dpClient, err := client.New(ctx, &client.Config{
//	    BaseURL:  "http://haproxy-dataplane:5555",
//	    Username: "admin",
//	    Password: "password",
//	})
func New(ctx context.Context, cfg *Config) (*DataplaneClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("baseURL is required")
	}
	if cfg.Username == "" {
		return nil, errors.New("username is required")
	}
	if cfg.Password == "" {
		return nil, errors.New("password is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create endpoint
	endpoint := Endpoint{
		URL:      cfg.BaseURL,
		Username: cfg.Username,
		Password: cfg.Password,
		PodName:  cfg.PodName,
	}

	// Create multi-version clientset with automatic version detection
	clientset, err := NewClientset(ctx, &endpoint, logger)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}

	logger.Debug("Created DataPlane API client",
		"endpoint", endpoint.URL,
		"version", clientset.DetectedVersion(),
		"capabilities", clientset.Capabilities(),
	)

	return &DataplaneClient{
		clientset: clientset,
		Endpoint:  endpoint,
		logger:    logger,
	}, nil
}

// Clientset returns the underlying multi-version clientset for advanced operations.
// Use this when you need version-specific features or capability checking.
//
// Example:
//
//	if client.Clientset().Capabilities().SupportsCrtList {
//	    v32Client := client.Clientset().V32()
//	    // Use v3.2-specific crt-list operations
//	}
func (c *DataplaneClient) Clientset() *Clientset {
	return c.clientset
}

// PreferredClient returns the most appropriate versioned client based on
// the detected server version. Returns one of: *v30.Client, *v31.Client, *v32.Client.
//
// For most operations, you should use the wrapper methods instead of this.
// This is provided for operations that aren't wrapped yet.
func (c *DataplaneClient) PreferredClient() any {
	return c.clientset.PreferredClient()
}

// DetectedVersion returns the full version string of the DataPlane API server.
func (c *DataplaneClient) DetectedVersion() string {
	return c.clientset.DetectedVersion()
}

// Capabilities returns the feature availability for the detected version.
func (c *DataplaneClient) Capabilities() Capabilities {
	return c.clientset.Capabilities()
}

// BaseURL returns the configured base URL for the Dataplane API.
func (c *DataplaneClient) BaseURL() string {
	return c.Endpoint.URL
}

// NewFromEndpoint creates a new DataplaneClient from an Endpoint.
// This is a convenience function for creating a client with default options.
func NewFromEndpoint(ctx context.Context, endpoint *Endpoint, logger *slog.Logger) (*DataplaneClient, error) {
	return New(ctx, &Config{
		BaseURL:  endpoint.URL,
		Username: endpoint.Username,
		Password: endpoint.Password,
		PodName:  endpoint.PodName,
		Logger:   logger,
	})
}

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	v30 "haproxy-template-ic/pkg/generated/dataplaneapi/v30"
	v30ee "haproxy-template-ic/pkg/generated/dataplaneapi/v30ee"
	v31 "haproxy-template-ic/pkg/generated/dataplaneapi/v31"
	v31ee "haproxy-template-ic/pkg/generated/dataplaneapi/v31ee"
	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// GetVersion retrieves the current configuration version from the Dataplane API.
//
// The version is used for optimistic locking when making configuration changes.
// This prevents concurrent modifications from conflicting.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Example:
//
//	version, err := client.GetVersion(context.Background())
//	if err != nil {
//	    slog.Error("failed to get version", "error", err)
//	    os.Exit(1)
//	}
//	fmt.Printf("Current version: %d\n", version)
func (c *DataplaneClient) GetVersion(ctx context.Context) (int64, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v32.GetConfigurationVersionParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v31.GetConfigurationVersionParams{})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v30.GetConfigurationVersionParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v32ee.GetConfigurationVersionParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v31ee.GetConfigurationVersionParams{})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v30ee.GetConfigurationVersionParams{})
		},
	})

	if err != nil {
		return 0, fmt.Errorf("failed to get configuration version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to get configuration version: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse version from response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read version response: %w", err)
	}

	// Trim whitespace (including newlines) from the version string
	versionStr := strings.TrimSpace(string(body))
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version: %w", err)
	}

	return version, nil
}

// GetRawConfiguration retrieves the current HAProxy configuration as a string.
//
// This fetches the raw configuration file content from the Dataplane API.
// The configuration can be parsed using the parser package to get structured data.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Example:
//
//	config, err := client.GetRawConfiguration(context.Background())
//	if err != nil {
//	    slog.Error("failed to get config", "error", err)
//	    os.Exit(1)
//	}
//	fmt.Printf("Current config:\n%s\n", config)
func (c *DataplaneClient) GetRawConfiguration(ctx context.Context) (string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v32.GetHAProxyConfigurationParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v31.GetHAProxyConfigurationParams{})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v30.GetHAProxyConfigurationParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v32ee.GetHAProxyConfigurationParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v31ee.GetHAProxyConfigurationParams{})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v30ee.GetHAProxyConfigurationParams{})
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to get raw configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get raw configuration: status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read configuration response: %w", err)
	}

	return string(body), nil
}

// PushRawConfiguration pushes a new HAProxy configuration to the Dataplane API.
//
// WARNING: This triggers a full HAProxy reload. Use this only as a last resort
// when fine-grained operations are not possible. Prefer using transactions with
// specific API endpoints to avoid reloads.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Parameters:
//   - config: The complete HAProxy configuration string
//
// Returns:
//   - reloadID: The reload identifier from the Reload-ID header (if reload triggered)
//   - error: Error if the push fails
//
// Example:
//
//	reloadID, err := client.PushRawConfiguration(context.Background(), newConfig)
//	if err != nil {
//	    slog.Error("failed to push config", "error", err)
//	    os.Exit(1)
//	}
//	if reloadID != "" {
//	    slog.Info("HAProxy reloaded", "reload_id", reloadID)
//	}
func (c *DataplaneClient) PushRawConfiguration(ctx context.Context, config string) (string, error) {
	skipVersion := true

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v32.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v31.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v30.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v32ee.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v31ee.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v30ee.PostHAProxyConfigurationParams{SkipVersion: &skipVersion}, config)
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to push raw configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to push raw configuration: status %d: %s", resp.StatusCode, string(body))
	}

	// Extract reload ID from response header
	// Raw config push typically triggers a reload (status 202)
	reloadID := resp.Header.Get("Reload-ID")

	return reloadID, nil
}

// VersionConflictError represents a 409 conflict error with version information.
type VersionConflictError struct {
	ExpectedVersion int64
	ActualVersion   string
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict: expected %d, got %s", e.ExpectedVersion, e.ActualVersion)
}

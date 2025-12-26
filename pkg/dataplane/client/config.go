package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
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

// ReloadStatus represents the status of an HAProxy reload operation.
type ReloadStatus string

const (
	// ReloadStatusInProgress indicates the reload is still being processed.
	ReloadStatusInProgress ReloadStatus = "in_progress"
	// ReloadStatusSucceeded indicates the reload completed successfully.
	ReloadStatusSucceeded ReloadStatus = "succeeded"
	// ReloadStatusFailed indicates the reload failed (HAProxy reverted to previous config).
	ReloadStatusFailed ReloadStatus = "failed"
)

// ReloadInfo contains information about an HAProxy reload operation.
type ReloadInfo struct {
	// ID is the unique identifier for this reload operation.
	ID string
	// Status is the current status of the reload.
	Status ReloadStatus
	// Response contains error details if the reload failed.
	Response string
	// ReloadTimestamp is the Unix timestamp when the reload occurred.
	ReloadTimestamp int64
}

// GetReloadStatus retrieves the status of a specific HAProxy reload operation.
//
// This method polls the DataPlane API to check if an async reload has completed.
// Use this after receiving a 202 response from configuration changes to verify
// the reload succeeded.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Parameters:
//   - reloadID: The reload identifier from the Reload-ID header
//
// Returns:
//   - ReloadInfo: Current status and details of the reload
//   - error: Error if the API call fails or reload ID not found
//
// Example:
//
//	info, err := client.GetReloadStatus(ctx, "abc123")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	switch info.Status {
//	case ReloadStatusSucceeded:
//	    log.Println("Reload completed successfully")
//	case ReloadStatusFailed:
//	    log.Printf("Reload failed: %s", info.Response)
//	case ReloadStatusInProgress:
//	    log.Println("Reload still in progress")
//	}
func (c *DataplaneClient) GetReloadStatus(ctx context.Context, reloadID string) (*ReloadInfo, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get reload status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("reload ID not found: %s", reloadID)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get reload status: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response into ReloadInfo
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read reload status response: %w", err)
	}

	var reload struct {
		ID              *string `json:"id"`
		Status          *string `json:"status"`
		Response        *string `json:"response"`
		ReloadTimestamp *int64  `json:"reload_timestamp"`
	}

	if err := json.Unmarshal(body, &reload); err != nil {
		return nil, fmt.Errorf("failed to parse reload status response: %w", err)
	}

	info := &ReloadInfo{}
	if reload.ID != nil {
		info.ID = *reload.ID
	}
	if reload.Status != nil {
		info.Status = ReloadStatus(*reload.Status)
	}
	if reload.Response != nil {
		info.Response = *reload.Response
	}
	if reload.ReloadTimestamp != nil {
		info.ReloadTimestamp = *reload.ReloadTimestamp
	}

	return info, nil
}

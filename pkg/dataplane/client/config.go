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
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// GetVersion retrieves the current configuration version from the Dataplane API.
//
// The version is used for optimistic locking when making configuration changes.
// This prevents concurrent modifications from conflicting.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Example:
//
//	version, err := dpClient.GetVersion(context.Background())
//	if err != nil {
//	    slog.Error("failed to get version", "error", err)
//	    os.Exit(1)
//	}
//	fmt.Printf("Current version: %d\n", version)
func (c *DataplaneClient) GetVersion(ctx context.Context) (int64, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.GetConfigurationVersion(ctx, &v33.GetConfigurationVersionParams{})
		},
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
		return 0, fmt.Errorf("getting configuration version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("getting configuration version: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse version from response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading version response: %w", err)
	}

	// Trim whitespace (including newlines) from the version string
	versionStr := strings.TrimSpace(string(body))
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing version: %w", err)
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
//	config, err := dpClient.GetRawConfiguration(context.Background())
//	if err != nil {
//	    slog.Error("failed to get config", "error", err)
//	    os.Exit(1)
//	}
//	fmt.Printf("Current config:\n%s\n", config)
func (c *DataplaneClient) GetRawConfiguration(ctx context.Context) (string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.GetHAProxyConfiguration(ctx, &v33.GetHAProxyConfigurationParams{})
		},
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
		return "", fmt.Errorf("getting raw configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("getting raw configuration: status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading configuration response: %w", err)
	}

	return string(body), nil
}

// PushRawConfiguration pushes a new HAProxy configuration to the Dataplane API.
//
// This triggers a full HAProxy reload and is the production apply path for
// structural changes (anything outside the runtime-eligible server-field set).
// When every change is runtime-eligible, prefer PushRawConfigurationSkipReload,
// which writes the new config to disk and applies the server changes to the
// running worker without a reload.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Parameters:
//   - config: The complete HAProxy configuration string
//   - version: The expected configuration version for optimistic locking.
//     The version is incremented after a successful push.
//
// Returns:
//   - reloadID: The reload identifier from the Reload-ID header (if reload triggered)
//   - error: Error if the push fails
//
// Example:
//
//	reloadID, err := dpClient.PushRawConfiguration(context.Background(), newConfig, 1)
//	if err != nil {
//	    slog.Error("failed to push config", "error", err)
//	    os.Exit(1)
//	}
//	if reloadID != "" {
//	    slog.Info("HAProxy reloaded", "reload_id", reloadID)
//	}
func (c *DataplaneClient) PushRawConfiguration(ctx context.Context, config string, version int64) (string, error) {
	forceReload := true
	// NB: skip_version=true was tried here and rolled back. The
	// dataplane writes the config WITHOUT the `# _version=N` header
	// when skip_version is set (see client-native raw.go), and
	// GetVersion reads the version from that header. End result was
	// GetVersion always returning 1, trapping the orchestrator in the
	// "version==1 → raw push" branch forever (caught in CI as every
	// reconcile re-running raw push instead of reaching the
	// runtime-eligible fast path). Keep the version check on.
	resp, err := c.postHAProxyConfiguration(ctx, config, version, nil, &forceReload, nil, nil)
	if err != nil {
		return "", fmt.Errorf("pushing raw configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("pushing raw configuration: status %d: %s", resp.StatusCode, string(body))
	}

	// Extract reload ID from response header
	// Raw config push typically triggers a reload (status 202)
	reloadID := resp.Header.Get("Reload-ID")

	return reloadID, nil
}

// PushRawConfigurationSkipReload pushes the full config to disk without triggering a reload.
// The runtimeActions string is a semicolon-separated list of runtime socket commands that
// HAProxy applies immediately via stats socket after the config file is written.
// (e.g., "SetServerAddr backend srv 10.0.0.1 8080;SetServerState backend srv ready").
// This allows N server state changes to be applied atomically in a single API call,
// replacing N serial ReplaceServerBackend calls that each re-read haproxy.cfg from disk.
func (c *DataplaneClient) PushRawConfigurationSkipReload(ctx context.Context, config string, version int64, runtimeActions string) error {
	// Retry across a concurrent reload: while HAProxy re-execs its master, the
	// -S socket is briefly closed and SetServer* runtime actions fail with a 500
	// ("connection refused"). retryWhileReloadInProgress re-pushes immediately
	// (the HTTP round-trip paces it) until the listener returns, so the runtime
	// change lands inside option redispatch's window instead of waiting for the
	// next reconcile. A version conflict (409) is NOT a reload signature, so it
	// still returns immediately.
	return retryWhileReloadInProgress(ctx, c.logger, func() error {
		skipReload := true
		resp, err := c.postHAProxyConfiguration(ctx, config, version, &skipReload, nil, nil, &runtimeActions)
		if err != nil {
			return fmt.Errorf("pushing raw configuration without reload: %w", err)
		}
		defer resp.Body.Close()
		return CheckResponse(resp, "raw config push with skip_reload")
	})
}

// PushRawConfigurationSkipReloadSkipVersion is the queue-bypass variant of
// PushRawConfigurationSkipReload: it applies the runtime actions without a
// reload AND without the optimistic-locking version check (skip_version).
//
// The deployer's runtime bypass fires this immediately when a queued reconcile
// produces runtime-eligible server changes, OUTSIDE the deployment scheduler's
// serialization — so a pod-IP rotation reaches the live worker in ~ms instead
// of waiting in the pending slot behind an in-flight ~200ms structural reload.
// The caller passes the CURRENT on-disk config as the body (so a co-batched
// reconcile's structural changes are NOT written to disk without a reload); only
// the runtime actions take effect on the live worker. The push still bumps the
// config version, so skip_version drops the optimistic-lock check; the gated
// scheduled deploy runs only after the in-flight one finishes and re-fetches the
// version, so there is no collision. The runtime change persists across the
// scheduled deploy's structural reload because that deploy re-renders the
// current endpoints (config-driven; no server-state-file — ADR-0011).
func (c *DataplaneClient) PushRawConfigurationSkipReloadSkipVersion(ctx context.Context, config, runtimeActions string) error {
	// Same reload-clobber retry as PushRawConfigurationSkipReload (see there).
	return retryWhileReloadInProgress(ctx, c.logger, func() error {
		skipReload := true
		skipVersion := true
		resp, err := c.postHAProxyConfiguration(ctx, config, 0, &skipReload, nil, &skipVersion, &runtimeActions)
		if err != nil {
			return fmt.Errorf("pushing raw configuration without reload (skip_version): %w", err)
		}
		defer resp.Body.Close()
		return CheckResponse(resp, "raw config push with skip_reload+skip_version")
	})
}

// postHAProxyConfiguration dispatches a POST /haproxy/configuration call to all supported
// API version variants. skipReload, forceReload, skipVersion, and runtimeActions are
// optional — pass nil to omit them. When skipVersion=true the version query param is
// elided too: skip_version tells the dataplane to enforce the pushed config without an
// optimistic-locking check, so sending a stale (or zero) version alongside is at best
// noise and at worst confusing in API traces.
func (c *DataplaneClient) postHAProxyConfiguration(ctx context.Context, config string, version int64, skipReload, forceReload, skipVersion *bool, runtimeActions *string) (*http.Response, error) {
	// When skipVersion is true the dataplane enforces the pushed config without
	// an optimistic-locking check, so we elide the version query param entirely
	// (sending a value alongside skip_version is at best noise, at worst
	// confusing in API traces).
	skipVer := skipVersion != nil && *skipVersion

	v33Ver := v33.Version(version)
	v32Ver := v32.Version(version)
	v31Ver := v31.Version(version)
	v30Ver := v30.Version(version)
	v32eeVer := v32ee.Version(version)
	v31eeVer := v31ee.Version(version)
	v30eeVer := v30ee.Version(version)

	v33VerP := &v33Ver
	v32VerP := &v32Ver
	v31VerP := &v31Ver
	v30VerP := &v30Ver
	v32eeVerP := &v32eeVer
	v31eeVerP := &v31eeVer
	v30eeVerP := &v30eeVer
	if skipVer {
		v33VerP, v32VerP, v31VerP, v30VerP = nil, nil, nil, nil
		v32eeVerP, v31eeVerP, v30eeVerP = nil, nil, nil
	}

	return c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v33.PostHAProxyConfigurationParams{
				Version: v33VerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v32.PostHAProxyConfigurationParams{
				Version: v32VerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v31.PostHAProxyConfigurationParams{
				Version: v31VerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v30.PostHAProxyConfigurationParams{
				Version: v30VerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v32ee.PostHAProxyConfigurationParams{
				Version: v32eeVerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v31ee.PostHAProxyConfigurationParams{
				Version: v31eeVerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.PostHAProxyConfigurationWithTextBody(ctx, &v30ee.PostHAProxyConfigurationParams{
				Version: v30eeVerP, SkipReload: skipReload, ForceReload: forceReload, SkipVersion: skipVersion, XRuntimeActions: runtimeActions,
			}, config)
		},
	})
}

// VersionConflictError represents a 409 conflict error with version information.
type VersionConflictError struct {
	ExpectedVersion int64
	ActualVersion   string
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict: expected %d, got %s", e.ExpectedVersion, e.ActualVersion)
}

// ReloadStatus represents the status of a HAProxy reload operation.
type ReloadStatus string

const (
	// ReloadStatusInProgress indicates the reload is still being processed.
	ReloadStatusInProgress ReloadStatus = "in_progress"
	// ReloadStatusSucceeded indicates the reload completed successfully.
	ReloadStatusSucceeded ReloadStatus = "succeeded"
	// ReloadStatusFailed indicates the reload failed (HAProxy reverted to previous config).
	ReloadStatusFailed ReloadStatus = "failed"
)

// ReloadInfo contains information about a HAProxy reload operation.
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
//	info, err := dpClient.GetReloadStatus(ctx, "abc123")
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
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.GetReload(ctx, reloadID)
		},
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
		return nil, fmt.Errorf("getting reload status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("reload ID not found: %s", reloadID)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("getting reload status: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response into ReloadInfo
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading reload status response: %w", err)
	}

	var reload struct {
		ID              *string `json:"id"`
		Status          *string `json:"status"`
		Response        *string `json:"response"`
		ReloadTimestamp *int64  `json:"reload_timestamp"`
	}

	if err := json.Unmarshal(body, &reload); err != nil {
		return nil, fmt.Errorf("parsing reload status response: %w", err)
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

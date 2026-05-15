package client

import (
	"context"
	"fmt"
	"net/http"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// GetAllGeneralFiles retrieves all general file paths from the storage.
// Note: This returns only file paths, not the file contents.
// Use GetGeneralFileContent to retrieve the actual file contents.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetAllGeneralFiles(ctx context.Context) ([]string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
	})

	if err != nil {
		return nil, fmt.Errorf("getting all general files: %w", err)
	}
	defer resp.Body.Close()

	return decodeStorageNameListWithFallback(resp, "general files")
}

// GetGeneralFileContent retrieves the content of a specific general file by path.
// The API returns the raw file content as application/octet-stream.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetGeneralFileContent(ctx context.Context, path string) (string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
	})

	if err != nil {
		return "", fmt.Errorf("getting general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "general file", path)
}

// CreateGeneralFile creates a new general file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) CreateGeneralFile(ctx context.Context, path, content string) (string, error) {
	body, contentType, err := buildMultipartFilePayload(path, content, multipartField{name: "id", value: path})
	if err != nil {
		return "", fmt.Errorf("building payload for general file '%s': %w", path, err)
	}

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CreateStorageGeneralFileWithBody(ctx, contentType, body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("creating general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "general file", path)
}

// UpdateGeneralFile updates an existing general file using multipart form-data.
// Always sends skip_reload=true so the dataplane API does NOT auto-reload after
// the PUT. The new content is written to disk; HAProxy keeps using the
// in-memory copy until the next reload. This matches Create's 201-with-no-reload
// behavior and lets the orchestrator batch every aux-file change into the
// single reload that the main config sync triggers (or, when only aux files
// changed, the explicit force-reload at the end of fine-grained sync).
//
// The previous behavior (default reload after PUT) caused an auxiliary-reload
// race on route deletion: the new spoe.conf could land before the new
// haproxy.cfg, and the auto-reload would fire against the stale haproxy.cfg
// whose `send-spoe-group <name>` references no longer resolved.
//
// Returns the reload ID if a reload was triggered (always empty under
// skip_reload=true) and any error. Works with all HAProxy DataPlane API
// versions (v3.0+).
func (c *DataplaneClient) UpdateGeneralFile(ctx context.Context, path, content string) (string, error) {
	body, contentType, err := buildMultipartFilePayload(path, content)
	if err != nil {
		return "", fmt.Errorf("building payload for general file '%s': %w", path, err)
	}

	skipReload := true
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v33.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v32.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v31.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v30.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v32ee.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v31ee.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, &v30ee.ReplaceStorageGeneralFileParams{SkipReload: &skipReload}, contentType, body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("updating general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "general file", path)
}

// DeleteGeneralFile deletes a general file by path.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) DeleteGeneralFile(ctx context.Context, path string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
	})

	if err != nil {
		return fmt.Errorf("deleting general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "general file", path)
}

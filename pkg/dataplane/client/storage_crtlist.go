package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// requireCrtList is the capability guard for crt-list storage operations,
// which are only available in HAProxy DataPlane API v3.2+.
func requireCrtList(caps Capabilities) error {
	if !caps.SupportsCrtList {
		return ErrCrtListRequiresV32
	}
	return nil
}

// GetAllCRTListFiles retrieves all crt-list file names from the storage.
// Note: This returns only crt-list file names, not the file contents.
// Use GetCRTListFileContent to retrieve the actual file contents.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetAllCRTListFiles(ctx context.Context) ([]string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.GetAllStorageSSLCrtListFiles(ctx) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageSSLCrtListFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageSSLCrtListFiles(ctx) },
	}, requireCrtList)

	if err != nil {
		return nil, fmt.Errorf("getting all crt-list files: %w", err)
	}
	defer resp.Body.Close()

	names, err := decodeStorageNameList(resp, "crt-list files")
	if err != nil {
		return nil, err
	}

	// Unsanitize names to restore dots (e.g., "example_com.crtlist" -> "example.com.crtlist")
	for i, name := range names {
		names[i] = UnsanitizeStorageName(name)
	}

	return names, nil
}

// GetCRTListFileContent retrieves the content of a specific crt-list file by name.
// The name parameter can use dots (e.g., "example.com.crtlist"), which will be sanitized
// automatically before calling the API.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetCRTListFileContent(ctx context.Context, name string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := SanitizeStorageName(name)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) { return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName) },
		V32: func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName)
		},
	}, requireCrtList)

	if err != nil {
		return "", fmt.Errorf("getting crt-list file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "crt-list file", name)
}

// CreateCRTListFile creates a new crt-list file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// The name parameter can use dots (e.g., "example.com.crtlist"), which will be sanitized
// automatically before calling the API.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) CreateCRTListFile(ctx context.Context, name, content string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := SanitizeStorageName(name)

	body, contentType, err := buildMultipartFilePayload(sanitizedName, content)
	if err != nil {
		return "", fmt.Errorf("building payload for crt-list file '%s': %w", name, err)
	}

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.CreateStorageSSLCrtListFileWithBody(ctx, &v33.CreateStorageSSLCrtListFileParams{}, contentType, body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateStorageSSLCrtListFileWithBody(ctx, &v32.CreateStorageSSLCrtListFileParams{}, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateStorageSSLCrtListFileWithBody(ctx, &v32ee.CreateStorageSSLCrtListFileParams{}, contentType, body)
		},
	}, requireCrtList)

	if err != nil {
		return "", fmt.Errorf("creating crt-list file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "crt-list file", name)
}

// UpdateCRTListFile updates an existing crt-list file using text/plain content-type.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Note: The Dataplane API requires text/plain or application/json for UPDATE operations,
// while CREATE operations accept multipart/form-data.
// The name parameter can use dots (e.g., "example.com.crtlist"), which will be sanitized
// automatically before calling the API.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) UpdateCRTListFile(ctx context.Context, name, content string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := SanitizeStorageName(name)

	// Use text/plain content-type for UPDATE (API v3 requirement)
	body := bytes.NewReader([]byte(content))

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCrtListFileWithBody(ctx, sanitizedName, &v33.ReplaceStorageSSLCrtListFileParams{}, "text/plain", body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCrtListFileWithBody(ctx, sanitizedName, &v32.ReplaceStorageSSLCrtListFileParams{}, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCrtListFileWithBody(ctx, sanitizedName, &v32ee.ReplaceStorageSSLCrtListFileParams{}, "text/plain", body)
		},
	}, requireCrtList)

	if err != nil {
		return "", fmt.Errorf("updating crt-list file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "crt-list file", name)
}

// DeleteCRTListFile deletes a crt-list file by name.
// The name parameter can use dots (e.g., "example.com.crtlist"), which will be sanitized
// automatically before calling the API.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) DeleteCRTListFile(ctx context.Context, name string) error {
	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := SanitizeStorageName(name)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCrtListFile(ctx, sanitizedName, &v33.DeleteStorageSSLCrtListFileParams{})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCrtListFile(ctx, sanitizedName, &v32.DeleteStorageSSLCrtListFileParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCrtListFile(ctx, sanitizedName, &v32ee.DeleteStorageSSLCrtListFileParams{})
		},
	}, requireCrtList)

	if err != nil {
		return fmt.Errorf("deleting crt-list file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "crt-list file", name)
}

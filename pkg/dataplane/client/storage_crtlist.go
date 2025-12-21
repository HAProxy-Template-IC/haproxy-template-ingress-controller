package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// sanitizeCRTListName sanitizes a crt-list file name for HAProxy Data Plane API storage.
// The API replaces dots in the filename (excluding the extension) with underscores.
// For example: "example.com.crtlist" becomes "example_com.crtlist".
func sanitizeCRTListName(name string) string {
	return SanitizeStorageName(name)
}

// unsanitizeCRTListName attempts to reverse the sanitization.
// This is a best-effort conversion and may not be perfect for all cases.
func unsanitizeCRTListName(name string) string {
	return UnsanitizeStorageName(name)
}

// GetAllCRTListFiles retrieves all crt-list file names from the storage.
// Note: This returns only crt-list file names, not the file contents.
// Use GetCRTListFileContent to retrieve the actual file contents.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetAllCRTListFiles(ctx context.Context) ([]string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageSSLCrtListFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageSSLCrtListFiles(ctx) },
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all crt-list files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all crt-list files failed with status %d", resp.StatusCode)
	}

	// Parse response body
	var apiCRTLists []struct {
		StorageName *string `json:"storage_name"`
		Description *string `json:"description"`
		File        *string `json:"file"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiCRTLists); err != nil {
		return nil, fmt.Errorf("failed to decode crt-list files response: %w", err)
	}

	// Extract and unsanitize crt-list file names
	names := make([]string, 0, len(apiCRTLists))
	for _, apiCRTList := range apiCRTLists {
		if apiCRTList.StorageName != nil {
			// Unsanitize the name to restore dots (e.g., "example_com.crtlist" -> "example.com.crtlist")
			unsanitizedName := unsanitizeCRTListName(*apiCRTList.StorageName)
			names = append(names, unsanitizedName)
		}
	}

	return names, nil
}

// GetCRTListFileContent retrieves the content of a specific crt-list file by name.
// The name parameter can use dots (e.g., "example.com.crtlist"), which will be sanitized
// automatically before calling the API.
// CRT-list storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetCRTListFileContent(ctx context.Context, name string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := sanitizeCRTListName(name)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to get crt-list file '%s': %w", name, err)
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
	// Check if crt-list is supported
	if !c.clientset.Capabilities().SupportsCrtList {
		return "", fmt.Errorf("crt-list storage is not supported by DataPlane API version %s (requires v3.2+)", c.clientset.DetectedVersion())
	}

	// Sanitize the name for the API (e.g., "example.com.crtlist" -> "example_com.crtlist")
	sanitizedName := sanitizeCRTListName(name)

	body, contentType, err := buildMultipartFilePayload(sanitizedName, content)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for crt-list file '%s': %w", name, err)
	}

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateStorageSSLCrtListFileWithBody(ctx, &v32.CreateStorageSSLCrtListFileParams{}, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateStorageSSLCrtListFileWithBody(ctx, &v32ee.CreateStorageSSLCrtListFileParams{}, contentType, body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to create crt-list file '%s': %w", name, err)
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
	sanitizedName := sanitizeCRTListName(name)

	// Use text/plain content-type for UPDATE (API v3 requirement)
	body := bytes.NewReader([]byte(content))

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCrtListFileWithBody(ctx, sanitizedName, &v32.ReplaceStorageSSLCrtListFileParams{}, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCrtListFileWithBody(ctx, sanitizedName, &v32ee.ReplaceStorageSSLCrtListFileParams{}, "text/plain", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to update crt-list file '%s': %w", name, err)
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
	sanitizedName := sanitizeCRTListName(name)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCrtListFile(ctx, sanitizedName, &v32.DeleteStorageSSLCrtListFileParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCrtListFile(ctx, sanitizedName, &v32ee.DeleteStorageSSLCrtListFileParams{})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete crt-list file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "crt-list file", name)
}

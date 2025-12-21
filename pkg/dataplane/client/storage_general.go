package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v30 "haproxy-template-ic/pkg/generated/dataplaneapi/v30"
	v30ee "haproxy-template-ic/pkg/generated/dataplaneapi/v30ee"
	v31 "haproxy-template-ic/pkg/generated/dataplaneapi/v31"
	v31ee "haproxy-template-ic/pkg/generated/dataplaneapi/v31ee"
	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// GetAllGeneralFiles retrieves all general file paths from the storage.
// Note: This returns only file paths, not the file contents.
// Use GetGeneralFileContent to retrieve the actual file contents.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetAllGeneralFiles(ctx context.Context) ([]string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetAllStorageGeneralFiles(ctx) },
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all general files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all general files failed with status %d", resp.StatusCode)
	}

	// Parse response body
	var apiFiles []struct {
		Id          *string `json:"id"`
		Description *string `json:"description"`
		Size        *int    `json:"size"`
		StorageName *string `json:"storage_name"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiFiles); err != nil {
		return nil, fmt.Errorf("failed to decode general files response: %w", err)
	}

	// Extract paths
	// The API may populate either 'storage_name' or 'id', we check both
	paths := make([]string, 0, len(apiFiles))
	for _, apiFile := range apiFiles {
		// Prefer storage_name (consistent with SSL certificates), fallback to id
		if apiFile.StorageName != nil {
			paths = append(paths, *apiFile.StorageName)
		} else if apiFile.Id != nil {
			paths = append(paths, *apiFile.Id)
		}
	}

	return paths, nil
}

// GetGeneralFileContent retrieves the content of a specific general file by path.
// The API returns the raw file content as application/octet-stream.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetGeneralFileContent(ctx context.Context, path string) (string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetOneStorageGeneralFile(ctx, path) },
	})

	if err != nil {
		return "", fmt.Errorf("failed to get general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "general file", path)
}

// CreateGeneralFile creates a new general file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) CreateGeneralFile(ctx context.Context, path, content string) (string, error) {
	body, contentType, err := buildMultipartFilePayloadWithID(path, content, path)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for general file '%s': %w", path, err)
	}

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
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
		return "", fmt.Errorf("failed to create general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "general file", path)
}

// UpdateGeneralFile updates an existing general file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) UpdateGeneralFile(ctx context.Context, path, content string) (string, error) {
	body, contentType, err := buildMultipartFilePayload(path, content)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for general file '%s': %w", path, err)
	}

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceStorageGeneralFileWithBody(ctx, path, nil, contentType, body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to update general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "general file", path)
}

// DeleteGeneralFile deletes a general file by path.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) DeleteGeneralFile(ctx context.Context, path string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.DeleteStorageGeneralFile(ctx, path) },
	})

	if err != nil {
		return fmt.Errorf("failed to delete general file '%s': %w", path, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "general file", path)
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// GetAllMapFiles retrieves all map file names from the storage.
// Note: This returns only map file names, not the file contents.
// Use GetMapFileContent to retrieve the actual file contents.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetAllMapFiles(ctx context.Context) ([]string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all map files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all map files failed with status %d", resp.StatusCode)
	}

	// Parse response body
	var apiMaps []struct {
		StorageName *string `json:"storage_name"`
		Description *string `json:"description"`
		File        *string `json:"file"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiMaps); err != nil {
		return nil, fmt.Errorf("failed to decode map files response: %w", err)
	}

	// Extract map file names
	names := make([]string, 0, len(apiMaps))
	for _, apiMap := range apiMaps {
		if apiMap.StorageName != nil {
			names = append(names, *apiMap.StorageName)
		}
	}

	return names, nil
}

// GetMapFileContent retrieves the content of a specific map file by name.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetMapFileContent(ctx context.Context, name string) (string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetOneStorageMap(ctx, name) },
	})

	if err != nil {
		return "", fmt.Errorf("failed to get map file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "map file", name)
}

// CreateMapFile creates a new map file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) CreateMapFile(ctx context.Context, name, content string) (string, error) {
	body, contentType, err := buildMultipartFilePayload(name, content)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for map file '%s': %w", name, err)
	}

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CreateStorageMapFileWithBody(ctx, contentType, body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to create map file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "map file", name)
}

// UpdateMapFile updates an existing map file using text/plain content-type.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// Note: The Dataplane API requires text/plain or application/json for UPDATE operations,
// while CREATE operations accept multipart/form-data.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) UpdateMapFile(ctx context.Context, name, content string) (string, error) {
	// Use text/plain content-type for UPDATE (API v3 requirement)
	body := bytes.NewReader([]byte(content))

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceStorageMapFileWithBody(ctx, name, nil, "text/plain", body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to update map file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "map file", name)
}

// DeleteMapFile deletes a map file by name.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) DeleteMapFile(ctx context.Context, name string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.DeleteStorageMap(ctx, name) },
	})

	if err != nil {
		return fmt.Errorf("failed to delete map file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "map file", name)
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v32 "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// GetAllSSLCaFiles retrieves all SSL CA file names from the runtime storage.
// Note: This returns only CA file names, not the file contents.
// Use GetSSLCaFileContent to retrieve the actual file contents.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetAllSSLCaFiles(ctx context.Context) ([]string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllCaFiles(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllCaFiles(ctx) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCaFiles {
			return fmt.Errorf("SSL CA file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all SSL CA files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all SSL CA files failed with status %d", resp.StatusCode)
	}

	// Parse response body - the API returns an array of SslCaFile objects
	var apiCaFiles []struct {
		StorageName *string `json:"storage_name"`
		File        *string `json:"file"`
		Count       *string `json:"count"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiCaFiles); err != nil {
		return nil, fmt.Errorf("failed to decode SSL CA files response: %w", err)
	}

	// Extract CA file names
	names := make([]string, 0, len(apiCaFiles))
	for _, apiCaFile := range apiCaFiles {
		if apiCaFile.StorageName != nil {
			names = append(names, *apiCaFile.StorageName)
		}
	}

	return names, nil
}

// GetSSLCaFileContent retrieves the content of a specific SSL CA file by name.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetSSLCaFileContent(ctx context.Context, name string) (string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetCaFile(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetCaFile(ctx, name) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCaFiles {
			return fmt.Errorf("SSL CA file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to get SSL CA file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "SSL CA file", name)
}

// CreateSSLCaFile creates a new SSL CA file using multipart form-data.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) CreateSSLCaFile(ctx context.Context, name, content string) (string, error) {
	if !c.clientset.Capabilities().SupportsSslCaFiles {
		return "", fmt.Errorf("SSL CA file storage is not supported by DataPlane API version %s (requires v3.2+)", c.clientset.DetectedVersion())
	}

	body, contentType, err := buildMultipartFilePayload(name, content)
	if err != nil {
		return "", fmt.Errorf("failed to build payload for SSL CA file '%s': %w", name, err)
	}

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateCaFileWithBody(ctx, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateCaFileWithBody(ctx, contentType, body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCaFiles {
			return fmt.Errorf("SSL CA file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to create SSL CA file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "SSL CA file", name)
}

// UpdateSSLCaFile updates an existing SSL CA file using text/plain content-type.
// Returns the reload ID if a reload was triggered (empty string if not) and any error.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) UpdateSSLCaFile(ctx context.Context, name, content string) (string, error) {
	body := bytes.NewReader([]byte(content))

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.SetCaFileWithBody(ctx, name, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.SetCaFileWithBody(ctx, name, "text/plain", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCaFiles {
			return fmt.Errorf("SSL CA file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to update SSL CA file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "SSL CA file", name)
}

// DeleteSSLCaFile deletes an SSL CA file by name.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) DeleteSSLCaFile(ctx context.Context, name string) error {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteCaFile(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteCaFile(ctx, name) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCaFiles {
			return fmt.Errorf("SSL CA file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete SSL CA file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "SSL CA file", name)
}

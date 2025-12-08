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

// GetAllSSLCrlFiles retrieves all SSL CRL file names from the runtime storage.
// Note: This returns only CRL file names, not the file contents.
// Use GetSSLCrlFileContent to retrieve the actual file contents.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetAllSSLCrlFiles(ctx context.Context) ([]string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllCrl(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllCrl(ctx) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCrlFiles {
			return fmt.Errorf("SSL CRL file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all SSL CRL files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all SSL CRL files failed with status %d", resp.StatusCode)
	}

	// Parse response body - the API returns an array of SslCrl objects
	var apiCrlFiles []struct {
		StorageName *string `json:"storage_name"`
		File        *string `json:"file"`
		Description *string `json:"description"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiCrlFiles); err != nil {
		return nil, fmt.Errorf("failed to decode SSL CRL files response: %w", err)
	}

	// Extract CRL file names
	names := make([]string, 0, len(apiCrlFiles))
	for _, apiCrlFile := range apiCrlFiles {
		if apiCrlFile.StorageName != nil {
			names = append(names, *apiCrlFile.StorageName)
		}
	}

	return names, nil
}

// GetSSLCrlFileContent retrieves the content of a specific SSL CRL file by name.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetSSLCrlFileContent(ctx context.Context, name string) (string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetCrl(ctx, name, nil) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetCrl(ctx, name, nil) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCrlFiles {
			return fmt.Errorf("SSL CRL file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to get SSL CRL file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return readRawStorageContent(resp, "SSL CRL file", name)
}

// CreateSSLCrlFile creates a new SSL CRL file using multipart form-data.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) CreateSSLCrlFile(ctx context.Context, name, content string) error {
	if !c.clientset.Capabilities().SupportsSslCrlFiles {
		return fmt.Errorf("SSL CRL file storage is not supported by DataPlane API version %s (requires v3.2+)", c.clientset.DetectedVersion())
	}

	body, contentType, err := buildMultipartFilePayload(name, content)
	if err != nil {
		return fmt.Errorf("failed to build payload for SSL CRL file '%s': %w", name, err)
	}

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateCrlWithBody(ctx, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateCrlWithBody(ctx, contentType, body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCrlFiles {
			return fmt.Errorf("SSL CRL file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create SSL CRL file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "SSL CRL file", name)
}

// UpdateSSLCrlFile updates an existing SSL CRL file using text/plain content-type.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) UpdateSSLCrlFile(ctx context.Context, name, content string) error {
	body := bytes.NewReader([]byte(content))

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceCrlWithBody(ctx, name, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceCrlWithBody(ctx, name, "text/plain", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCrlFiles {
			return fmt.Errorf("SSL CRL file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to update SSL CRL file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "SSL CRL file", name)
}

// DeleteSSLCrlFile deletes an SSL CRL file by name.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) DeleteSSLCrlFile(ctx context.Context, name string) error {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteCrl(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteCrl(ctx, name) },
	}, func(caps Capabilities) error {
		if !caps.SupportsSslCrlFiles {
			return fmt.Errorf("SSL CRL file storage requires DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete SSL CRL file '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "SSL CRL file", name)
}

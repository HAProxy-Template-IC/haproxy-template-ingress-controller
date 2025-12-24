package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v31 "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// LogProfile represents a HAProxy log profile configuration.
type LogProfile struct {
	Name   string `json:"name"`
	LogTag string `json:"log_tag,omitempty"`
}

// GetAllLogProfiles retrieves all log profile names from the configuration.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) GetAllLogProfiles(ctx context.Context) ([]string, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetLogProfiles(ctx, &v32.GetLogProfilesParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetLogProfiles(ctx, &v32ee.GetLogProfilesParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetLogProfiles(ctx, &v31.GetLogProfilesParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetLogProfiles(ctx, &v31ee.GetLogProfilesParams{})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsLogProfiles {
			return fmt.Errorf("log profiles require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get all log profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all log profiles failed with status %d", resp.StatusCode)
	}

	// Parse response body - the API returns an array of log profile objects
	var apiLogProfiles []struct {
		Name   *string `json:"name"`
		LogTag *string `json:"log_tag"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiLogProfiles); err != nil {
		return nil, fmt.Errorf("failed to decode log profiles response: %w", err)
	}

	// Extract log profile names
	names := make([]string, 0, len(apiLogProfiles))
	for _, profile := range apiLogProfiles {
		if profile.Name != nil {
			names = append(names, *profile.Name)
		}
	}

	return names, nil
}

// GetLogProfile retrieves a specific log profile by name.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) GetLogProfile(ctx context.Context, name string) (*LogProfile, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetLogProfile(ctx, name, &v32.GetLogProfileParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetLogProfile(ctx, name, &v32ee.GetLogProfileParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.GetLogProfile(ctx, name, &v31.GetLogProfileParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetLogProfile(ctx, name, &v31ee.GetLogProfileParams{})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsLogProfiles {
			return fmt.Errorf("log profiles require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get log profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("log profile '%s' not found", name)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get log profile '%s' failed with status %d", name, resp.StatusCode)
	}

	var profile LogProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to decode log profile response: %w", err)
	}

	return &profile, nil
}

// CreateLogProfile creates a new log profile.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) CreateLogProfile(ctx context.Context, profile *LogProfile, transactionID string) error {
	if !c.clientset.Capabilities().SupportsLogProfiles {
		return fmt.Errorf("log profiles are not supported by DataPlane API version %s (requires v3.1+)", c.clientset.DetectedVersion())
	}

	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal log profile '%s': %w", profile.Name, err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateLogProfileWithBody(ctx, &v32.CreateLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateLogProfileWithBody(ctx, &v32ee.CreateLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CreateLogProfileWithBody(ctx, &v31.CreateLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateLogProfileWithBody(ctx, &v31ee.CreateLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsLogProfiles {
			return fmt.Errorf("log profiles require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create log profile '%s': %w", profile.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create log profile '%s' failed with status %d", profile.Name, resp.StatusCode)
	}

	return nil
}

// UpdateLogProfile updates an existing log profile.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) UpdateLogProfile(ctx context.Context, name string, profile *LogProfile, transactionID string) error {
	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal log profile '%s': %w", name, err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.EditLogProfileWithBody(ctx, name, &v32.EditLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.EditLogProfileWithBody(ctx, name, &v32ee.EditLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.EditLogProfileWithBody(ctx, name, &v31.EditLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.EditLogProfileWithBody(ctx, name, &v31ee.EditLogProfileParams{TransactionId: &transactionID}, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsLogProfiles {
			return fmt.Errorf("log profiles require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to update log profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update log profile '%s' failed with status %d", name, resp.StatusCode)
	}

	return nil
}

// DeleteLogProfile deletes a log profile by name.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func (c *DataplaneClient) DeleteLogProfile(ctx context.Context, name, transactionID string) error {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteLogProfile(ctx, name, &v32.DeleteLogProfileParams{TransactionId: &transactionID})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteLogProfile(ctx, name, &v32ee.DeleteLogProfileParams{TransactionId: &transactionID})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.DeleteLogProfile(ctx, name, &v31.DeleteLogProfileParams{TransactionId: &transactionID})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteLogProfile(ctx, name, &v31ee.DeleteLogProfileParams{TransactionId: &transactionID})
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsLogProfiles {
			return fmt.Errorf("log profiles require DataPlane API v3.1+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete log profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Already deleted, not an error
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete log profile '%s' failed with status %d", name, resp.StatusCode)
	}

	return nil
}

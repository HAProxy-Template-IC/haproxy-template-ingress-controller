package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// MiscOperations provides miscellaneous HAProxy Enterprise operations.
type MiscOperations struct {
	client *client.DataplaneClient
}

// NewMiscOperations creates a new miscellaneous operations client.
func NewMiscOperations(c *client.DataplaneClient) *MiscOperations {
	return &MiscOperations{client: c}
}

// =============================================================================
// Facts Operations
// =============================================================================

// Facts represents system facts information.
type Facts = v32ee.Facts

// GetFacts retrieves system facts.
func (m *MiscOperations) GetFacts(ctx context.Context, refresh bool) (*Facts, error) {
	resp, err := m.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetFactsParams{}
			if refresh {
				params.Refresh = &refresh
			}
			return c.GetFacts(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetFactsParams{}
			if refresh {
				params.Refresh = &refresh
			}
			return c.GetFacts(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetFactsParams{}
			if refresh {
				params.Refresh = &refresh
			}
			return c.GetFacts(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get facts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get facts failed with status %d", resp.StatusCode)
	}

	var result Facts
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode facts response: %w", err)
	}
	return &result, nil
}

// =============================================================================
// Ping Operations
// =============================================================================

// ErrPingRequiresV32 is returned when Ping is called on v3.0 or v3.1.
var ErrPingRequiresV32 = errors.New("ping endpoint requires HAProxy Enterprise v3.2+")

// Ping checks if the DataPlane API is responsive.
// Note: This method is only available in HAProxy Enterprise v3.2+.
func (m *MiscOperations) Ping(ctx context.Context) error {
	if m.client.Clientset().MinorVersion() < 2 {
		return ErrPingRequiresV32
	}

	resp, err := m.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetPing(ctx)
		},
		// V31EE and V30EE don't have Ping endpoint
	})
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}
	return nil
}

// =============================================================================
// Structured Configuration Operations
// =============================================================================

// StructuredConfig represents the HAProxy configuration in structured format.
type StructuredConfig = v32ee.Structured

// GetStructuredConfig retrieves the HAProxy configuration in structured JSON format.
func (m *MiscOperations) GetStructuredConfig(ctx context.Context, txID string) (*StructuredConfig, error) {
	resp, err := m.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetHAProxyConfigurationStructuredParams{TransactionId: &txID}
			return c.GetHAProxyConfigurationStructured(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetHAProxyConfigurationStructuredParams{TransactionId: &txID}
			return c.GetHAProxyConfigurationStructured(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetHAProxyConfigurationStructuredParams{TransactionId: &txID}
			return c.GetHAProxyConfigurationStructured(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get structured config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get structured config failed with status %d", resp.StatusCode)
	}

	var result StructuredConfig
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode structured config response: %w", err)
	}
	return &result, nil
}

// ReplaceStructuredConfig replaces the HAProxy configuration using structured format.
func (m *MiscOperations) ReplaceStructuredConfig(ctx context.Context, txID string, config *StructuredConfig) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal structured config: %w", err)
	}

	resp, err := m.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var cfg v32ee.Structured
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceStructuredParams{TransactionId: &txID}
			return c.ReplaceStructured(ctx, params, cfg)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var cfg v31ee.Structured
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceStructuredParams{TransactionId: &txID}
			return c.ReplaceStructured(ctx, params, cfg)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var cfg v30ee.Structured
			if err := json.Unmarshal(jsonData, &cfg); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceStructuredParams{TransactionId: &txID}
			return c.ReplaceStructured(ctx, params, cfg)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace structured config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace structured config failed with status %d", resp.StatusCode)
	}
	return nil
}

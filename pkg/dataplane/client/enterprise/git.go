package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// GitOperations provides operations for HAProxy Enterprise Git integration.
type GitOperations struct {
	client *client.DataplaneClient
}

// NewGitOperations creates a new Git operations client.
func NewGitOperations(c *client.DataplaneClient) *GitOperations {
	return &GitOperations{client: c}
}

// GitSettings represents Git integration settings.
type GitSettings = v32ee.GitSettings

// GetSettings retrieves the current Git settings.
func (g *GitOperations) GetSettings(ctx context.Context) (*GitSettings, error) {
	resp, err := g.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetGitSettings(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetGitSettings(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetGitSettings(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Git settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get Git settings failed with status %d", resp.StatusCode)
	}

	var result GitSettings
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Git settings response: %w", err)
	}
	return &result, nil
}

// ReplaceSettings replaces the Git settings.
func (g *GitOperations) ReplaceSettings(ctx context.Context, settings *GitSettings) error {
	jsonData, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal Git settings: %w", err)
	}

	resp, err := g.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var s v32ee.GitSettings
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceGitSettingsParams{}
			return c.ReplaceGitSettings(ctx, params, s)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var s v31ee.GitSettings
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceGitSettingsParams{}
			return c.ReplaceGitSettings(ctx, params, s)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var s v30ee.GitSettings
			if err := json.Unmarshal(jsonData, &s); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceGitSettingsParams{}
			return c.ReplaceGitSettings(ctx, params, s)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace Git settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace Git settings failed with status %d", resp.StatusCode)
	}
	return nil
}

// GitAction represents a Git action.
type GitAction = v32ee.GitAction

// GetAllActions retrieves all available Git actions.
func (g *GitOperations) GetAllActions(ctx context.Context) ([]GitAction, error) {
	resp, err := g.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetGitActions(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetGitActions(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetGitActions(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Git actions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get Git actions failed with status %d", resp.StatusCode)
	}

	var result []GitAction
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Git actions response: %w", err)
	}
	return result, nil
}

// GetAction retrieves a specific Git action by ID.
func (g *GitOperations) GetAction(ctx context.Context, id string) (*GitAction, error) {
	resp, err := g.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetGitAction(ctx, id)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetGitAction(ctx, id)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetGitAction(ctx, id)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Git action '%s': %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get Git action '%s' failed with status %d", id, resp.StatusCode)
	}

	var result GitAction
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Git action response: %w", err)
	}
	return &result, nil
}

// ExecuteAction executes a Git action.
func (g *GitOperations) ExecuteAction(ctx context.Context, action *GitAction) (*GitAction, error) {
	jsonData, err := json.Marshal(action)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Git action: %w", err)
	}

	resp, err := g.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var a v32ee.GitAction
			if err := json.Unmarshal(jsonData, &a); err != nil {
				return nil, err
			}
			return c.ExecuteGitAction(ctx, a)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var a v31ee.GitAction
			if err := json.Unmarshal(jsonData, &a); err != nil {
				return nil, err
			}
			return c.ExecuteGitAction(ctx, a)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var a v30ee.GitAction
			if err := json.Unmarshal(jsonData, &a); err != nil {
				return nil, err
			}
			return c.ExecuteGitAction(ctx, a)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute Git action: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("execute Git action failed with status %d", resp.StatusCode)
	}

	var result GitAction
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Git action response: %w", err)
	}
	return &result, nil
}

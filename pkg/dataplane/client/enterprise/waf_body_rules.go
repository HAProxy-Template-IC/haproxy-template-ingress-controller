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

// GetAllBodyRulesBackend retrieves all WAF body rules for a backend.
func (w *WAFOperations) GetAllBodyRulesBackend(ctx context.Context, txID, backendName string) ([]WafBodyRule, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllWafBodyRuleBackendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleBackend(ctx, backendName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetAllWafBodyRuleBackendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleBackend(ctx, backendName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetAllWafBodyRuleBackendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleBackend(ctx, backendName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF body rules for backend '%s': %w", backendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF body rules for backend '%s' failed with status %d", backendName, resp.StatusCode)
	}

	var result []WafBodyRule
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF body rules response: %w", err)
	}
	return result, nil
}

// CreateBodyRuleBackend creates a new WAF body rule for a backend at the specified index.
func (w *WAFOperations) CreateBodyRuleBackend(ctx context.Context, txID, backendName string, index int, rule *WafBodyRule) error {
	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF body rule: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.CreateWafBodyRuleBackendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var r v31ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v31ee.CreateWafBodyRuleBackendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var r v30ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v30ee.CreateWafBodyRuleBackendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF body rule for backend '%s': %w", backendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF body rule for backend '%s' failed with status %d", backendName, resp.StatusCode)
	}
	return nil
}

// ReplaceBodyRuleBackend replaces a WAF body rule for a backend at the specified index.
func (w *WAFOperations) ReplaceBodyRuleBackend(ctx context.Context, txID, backendName string, index int, rule *WafBodyRule) error {
	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF body rule: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceWafBodyRuleBackendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var r v31ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceWafBodyRuleBackendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var r v30ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceWafBodyRuleBackendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleBackend(ctx, backendName, index, params, r)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF body rule for backend '%s': %w", backendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF body rule for backend '%s' failed with status %d", backendName, resp.StatusCode)
	}
	return nil
}

// DeleteBodyRuleBackend deletes a WAF body rule for a backend at the specified index.
func (w *WAFOperations) DeleteBodyRuleBackend(ctx context.Context, txID, backendName string, index int) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafBodyRuleBackendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleBackend(ctx, backendName, index, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteWafBodyRuleBackendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleBackend(ctx, backendName, index, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteWafBodyRuleBackendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleBackend(ctx, backendName, index, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF body rule for backend '%s' at index %d: %w", backendName, index, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF body rule for backend '%s' at index %d failed with status %d", backendName, index, resp.StatusCode)
	}
	return nil
}

// GetAllBodyRulesFrontend retrieves all WAF body rules for a frontend.
func (w *WAFOperations) GetAllBodyRulesFrontend(ctx context.Context, txID, frontendName string) ([]WafBodyRule, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetAllWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleFrontend(ctx, frontendName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetAllWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleFrontend(ctx, frontendName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetAllWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.GetAllWafBodyRuleFrontend(ctx, frontendName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF body rules for frontend '%s': %w", frontendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF body rules for frontend '%s' failed with status %d", frontendName, resp.StatusCode)
	}

	var result []WafBodyRule
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF body rules response: %w", err)
	}
	return result, nil
}

// CreateBodyRuleFrontend creates a new WAF body rule for a frontend at the specified index.
func (w *WAFOperations) CreateBodyRuleFrontend(ctx context.Context, txID, frontendName string, index int, rule *WafBodyRule) error {
	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF body rule: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.CreateWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var r v31ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v31ee.CreateWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var r v30ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v30ee.CreateWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.CreateWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF body rule for frontend '%s': %w", frontendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF body rule for frontend '%s' failed with status %d", frontendName, resp.StatusCode)
	}
	return nil
}

// ReplaceBodyRuleFrontend replaces a WAF body rule for a frontend at the specified index.
func (w *WAFOperations) ReplaceBodyRuleFrontend(ctx context.Context, txID, frontendName string, index int, rule *WafBodyRule) error {
	jsonData, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF body rule: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var r v32ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var r v31ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v31ee.ReplaceWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var r v30ee.WafBodyRule
			if err := json.Unmarshal(jsonData, &r); err != nil {
				return nil, err
			}
			params := &v30ee.ReplaceWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.ReplaceWafBodyRuleFrontend(ctx, frontendName, index, params, r)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF body rule for frontend '%s': %w", frontendName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF body rule for frontend '%s' failed with status %d", frontendName, resp.StatusCode)
	}
	return nil
}

// DeleteBodyRuleFrontend deletes a WAF body rule for a frontend at the specified index.
func (w *WAFOperations) DeleteBodyRuleFrontend(ctx context.Context, txID, frontendName string, index int) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleFrontend(ctx, frontendName, index, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleFrontend(ctx, frontendName, index, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteWafBodyRuleFrontendParams{TransactionId: &txID}
			return c.DeleteWafBodyRuleFrontend(ctx, frontendName, index, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF body rule for frontend '%s' at index %d: %w", frontendName, index, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF body rule for frontend '%s' at index %d failed with status %d", frontendName, index, resp.StatusCode)
	}
	return nil
}

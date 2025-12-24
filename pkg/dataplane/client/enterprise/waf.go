package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// ErrWAFGlobalRequiresV32 is returned when WAF Global operations are attempted
// on HAProxy Enterprise v3.0 or v3.1 (WAF Global is v3.2+ only).
var ErrWAFGlobalRequiresV32 = errors.New("WAF global configuration requires HAProxy Enterprise DataPlane API v3.2+")

// ErrWAFProfilesRequiresV32 is returned when WAF Profile operations are attempted
// on HAProxy Enterprise v3.0 or v3.1 (WAF Profiles are v3.2+ only).
var ErrWAFProfilesRequiresV32 = errors.New("WAF profiles require HAProxy Enterprise DataPlane API v3.2+")

// WAFOperations provides operations for HAProxy Enterprise WAF management.
// This includes WAF global settings, profiles, body rules, and rulesets.
type WAFOperations struct {
	client *client.DataplaneClient
}

// NewWAFOperations creates a new WAF operations client.
func NewWAFOperations(c *client.DataplaneClient) *WAFOperations {
	return &WAFOperations{client: c}
}

// =============================================================================
// WAF Global Operations
// =============================================================================

// WafGlobal represents WAF global configuration.
type WafGlobal = v32ee.WafGlobal

// GetGlobal retrieves the WAF global configuration.
// Note: WAF Global is only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) GetGlobal(ctx context.Context, txID string) (*WafGlobal, error) {
	// WAF Global is v3.2+ only - check version first
	if w.client.Clientset().MinorVersion() < 2 {
		return nil, ErrWAFGlobalRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafGlobalParams{TransactionId: &txID}
			return c.GetWafGlobal(ctx, params)
		},
		// V31EE and V30EE don't have WAF Global
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF global config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF global failed with status %d", resp.StatusCode)
	}

	var result WafGlobal
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF global response: %w", err)
	}
	return &result, nil
}

// CreateGlobal creates the WAF global configuration.
// Note: WAF Global is only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) CreateGlobal(ctx context.Context, txID string, global *WafGlobal) error {
	// WAF Global is v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFGlobalRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateWafGlobalParams{TransactionId: &txID}
			return c.CreateWafGlobal(ctx, params, *global)
		},
		// V31EE and V30EE don't have WAF Global
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF global config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF global failed with status %d", resp.StatusCode)
	}
	return nil
}

// ReplaceGlobal replaces the WAF global configuration.
// Note: WAF Global is only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) ReplaceGlobal(ctx context.Context, txID string, global *WafGlobal) error {
	// WAF Global is v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFGlobalRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.ReplaceWafGlobalParams{TransactionId: &txID}
			return c.ReplaceWafGlobal(ctx, params, *global)
		},
		// V31EE and V30EE don't have WAF Global
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF global config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF global failed with status %d", resp.StatusCode)
	}
	return nil
}

// DeleteGlobal deletes the WAF global configuration.
// Note: WAF Global is only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) DeleteGlobal(ctx context.Context, txID string) error {
	// WAF Global is v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFGlobalRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafGlobalParams{TransactionId: &txID}
			return c.DeleteWafGlobal(ctx, params)
		},
		// V31EE and V30EE don't have WAF Global
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF global config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF global failed with status %d", resp.StatusCode)
	}
	return nil
}

// =============================================================================
// WAF Profile Operations
// =============================================================================

// WafProfile represents a WAF profile configuration.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
type WafProfile = v32ee.WafProfile

// GetAllProfiles retrieves all WAF profiles.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) GetAllProfiles(ctx context.Context, txID string) ([]WafProfile, error) {
	// WAF Profiles are v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return nil, ErrWAFProfilesRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafProfilesParams{TransactionId: &txID}
			return c.GetWafProfiles(ctx, params)
		},
		// V31EE and V30EE don't have WAF Profiles
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF profiles failed with status %d", resp.StatusCode)
	}

	var result []WafProfile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF profiles response: %w", err)
	}
	return result, nil
}

// GetProfile retrieves a specific WAF profile by name.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) GetProfile(ctx context.Context, txID, name string) (*WafProfile, error) {
	// WAF Profiles are v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return nil, ErrWAFProfilesRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafProfileParams{TransactionId: &txID}
			return c.GetWafProfile(ctx, name, params)
		},
		// V31EE and V30EE don't have WAF Profiles
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF profile '%s' failed with status %d", name, resp.StatusCode)
	}

	var result WafProfile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF profile response: %w", err)
	}
	return &result, nil
}

// CreateProfile creates a new WAF profile.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) CreateProfile(ctx context.Context, txID string, profile *WafProfile) error {
	// WAF Profiles are v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFProfilesRequiresV32
	}

	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF profile: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var p v32ee.WafProfile
			if err := json.Unmarshal(jsonData, &p); err != nil {
				return nil, err
			}
			params := &v32ee.CreateWafProfileParams{TransactionId: &txID}
			return c.CreateWafProfile(ctx, params, p)
		},
		// V31EE and V30EE don't have WAF Profiles
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF profile failed with status %d", resp.StatusCode)
	}
	return nil
}

// ReplaceProfile replaces an existing WAF profile.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) ReplaceProfile(ctx context.Context, txID, name string, profile *WafProfile) error {
	// WAF Profiles are v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFProfilesRequiresV32
	}

	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal WAF profile: %w", err)
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var p v32ee.WafProfile
			if err := json.Unmarshal(jsonData, &p); err != nil {
				return nil, err
			}
			params := &v32ee.ReplaceWafProfileParams{TransactionId: &txID}
			return c.ReplaceWafProfile(ctx, name, params, p)
		},
		// V31EE and V30EE don't have WAF Profiles
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF profile '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// DeleteProfile deletes a WAF profile.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
func (w *WAFOperations) DeleteProfile(ctx context.Context, txID, name string) error {
	// WAF Profiles are v3.2+ only
	if w.client.Clientset().MinorVersion() < 2 {
		return ErrWAFProfilesRequiresV32
	}

	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafProfileParams{TransactionId: &txID}
			return c.DeleteWafProfile(ctx, name, params)
		},
		// V31EE and V30EE don't have WAF Profiles
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF profile '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// WAF Body Rule Operations (Backend)
// =============================================================================

// WafBodyRule represents a WAF body rule configuration.
type WafBodyRule = v32ee.WafBodyRule

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

// =============================================================================
// WAF Body Rule Operations (Frontend)
// =============================================================================

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

// =============================================================================
// WAF Ruleset Operations
// =============================================================================

// WafRuleset represents a WAF ruleset.
type WafRuleset = v32ee.WafRuleset

// GetAllRulesets retrieves all WAF rulesets.
func (w *WAFOperations) GetAllRulesets(ctx context.Context) ([]WafRuleset, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetWafRulesets(ctx)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF rulesets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF rulesets failed with status %d", resp.StatusCode)
	}

	var result []WafRuleset
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF rulesets response: %w", err)
	}
	return result, nil
}

// GetRuleset retrieves a specific WAF ruleset by name.
func (w *WAFOperations) GetRuleset(ctx context.Context, name string) (*WafRuleset, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetWafRuleset(ctx, name)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}

	var result WafRuleset
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF ruleset response: %w", err)
	}
	return &result, nil
}

// CreateRuleset creates a new WAF ruleset from a file.
func (w *WAFOperations) CreateRuleset(ctx context.Context, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CreateWafRulesetWithBody(ctx, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF ruleset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF ruleset failed with status %d", resp.StatusCode)
	}
	return nil
}

// ReplaceRuleset replaces a WAF ruleset.
func (w *WAFOperations) ReplaceRuleset(ctx context.Context, name string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceWafRulesetWithBody(ctx, name, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// DeleteRuleset deletes a WAF ruleset.
func (w *WAFOperations) DeleteRuleset(ctx context.Context, name string) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteWafRuleset(ctx, name)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF ruleset '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF ruleset '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// WAF Ruleset File Operations
// =============================================================================

// GetAllRulesetFiles retrieves all files in a WAF ruleset.
func (w *WAFOperations) GetAllRulesetFiles(ctx context.Context, rulesetName, subDir string) ([]string, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetWafFilesParams{SubDir: &subDir}
			return c.GetWafFiles(ctx, rulesetName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF files for ruleset '%s': %w", rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF files for ruleset '%s' failed with status %d", rulesetName, resp.StatusCode)
	}

	var result []string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode WAF files response: %w", err)
	}
	return result, nil
}

// GetRulesetFile retrieves a specific file from a WAF ruleset.
func (w *WAFOperations) GetRulesetFile(ctx context.Context, rulesetName, fileName, subDir string) ([]byte, error) {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetWafFileParams{SubDir: &subDir}
			return c.GetWafFile(ctx, rulesetName, fileName, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get WAF file '%s' from ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get WAF file '%s' from ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// CreateRulesetFile creates a new file in a WAF ruleset.
func (w *WAFOperations) CreateRulesetFile(ctx context.Context, rulesetName, subDir string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.CreateWafFileParams{SubDir: &subDir}
			return c.CreateWafFileWithBody(ctx, rulesetName, params, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create WAF file in ruleset '%s': %w", rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create WAF file in ruleset '%s' failed with status %d", rulesetName, resp.StatusCode)
	}
	return nil
}

// ReplaceRulesetFile replaces a file in a WAF ruleset.
func (w *WAFOperations) ReplaceRulesetFile(ctx context.Context, rulesetName, fileName, subDir string, content io.Reader) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.ReplaceWafFileParams{SubDir: &subDir}
			return c.ReplaceWafFileWithBody(ctx, rulesetName, fileName, params, "application/octet-stream", content)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to replace WAF file '%s' in ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace WAF file '%s' in ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}
	return nil
}

// DeleteRulesetFile deletes a file from a WAF ruleset.
func (w *WAFOperations) DeleteRulesetFile(ctx context.Context, rulesetName, fileName, subDir string) error {
	resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteWafFileParams{SubDir: &subDir}
			return c.DeleteWafFile(ctx, rulesetName, fileName, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete WAF file '%s' from ruleset '%s': %w", fileName, rulesetName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete WAF file '%s' from ruleset '%s' failed with status %d", fileName, rulesetName, resp.StatusCode)
	}
	return nil
}

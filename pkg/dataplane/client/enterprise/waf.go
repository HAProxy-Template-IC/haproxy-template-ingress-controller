package enterprise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
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

// WafGlobal represents WAF global configuration.
type WafGlobal = v32ee.WafGlobal

// WafProfile represents a WAF profile configuration.
// Note: WAF Profiles are only available in HAProxy Enterprise v3.2+.
type WafProfile = v32ee.WafProfile

// WafBodyRule represents a WAF body rule configuration.
type WafBodyRule = v32ee.WafBodyRule

// WafRuleset represents a WAF ruleset.
type WafRuleset = v32ee.WafRuleset

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

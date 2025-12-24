package enterprise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
	v30ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v30ee"
	v31ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v31ee"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// BotManagementOperations provides operations for HAProxy Enterprise bot management.
// This includes bot management profiles and CAPTCHA configurations.
type BotManagementOperations struct {
	client *client.DataplaneClient
}

// NewBotManagementOperations creates a new bot management operations client.
func NewBotManagementOperations(c *client.DataplaneClient) *BotManagementOperations {
	return &BotManagementOperations{client: c}
}

// =============================================================================
// Bot Management Profile Operations
// =============================================================================

// BotmgmtProfile represents a bot management profile configuration.
type BotmgmtProfile = v32ee.BotmgmtProfile

// GetAllProfiles retrieves all bot management profiles.
func (b *BotManagementOperations) GetAllProfiles(ctx context.Context, txID string) ([]BotmgmtProfile, error) {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetBotmgmtProfilesParams{TransactionId: &txID}
			return c.GetBotmgmtProfiles(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetBotmgmtProfilesParams{TransactionId: &txID}
			return c.GetBotmgmtProfiles(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetBotmgmtProfilesParams{TransactionId: &txID}
			return c.GetBotmgmtProfiles(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get bot management profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get bot management profiles failed with status %d", resp.StatusCode)
	}

	var result []BotmgmtProfile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode bot management profiles response: %w", err)
	}
	return result, nil
}

// GetProfile retrieves a specific bot management profile by name.
func (b *BotManagementOperations) GetProfile(ctx context.Context, txID, name string) (*BotmgmtProfile, error) {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetBotmgmtProfileParams{TransactionId: &txID}
			return c.GetBotmgmtProfile(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetBotmgmtProfileParams{TransactionId: &txID}
			return c.GetBotmgmtProfile(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetBotmgmtProfileParams{TransactionId: &txID}
			return c.GetBotmgmtProfile(ctx, name, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get bot management profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get bot management profile '%s' failed with status %d", name, resp.StatusCode)
	}

	var result BotmgmtProfile
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode bot management profile response: %w", err)
	}
	return &result, nil
}

// CreateProfile creates a new bot management profile.
func (b *BotManagementOperations) CreateProfile(ctx context.Context, txID string, profile *BotmgmtProfile) error {
	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal bot management profile: %w", err)
	}

	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var p v32ee.BotmgmtProfile
			if err := json.Unmarshal(jsonData, &p); err != nil {
				return nil, err
			}
			params := &v32ee.CreateBotmgmtProfileParams{TransactionId: &txID}
			return c.CreateBotmgmtProfile(ctx, params, p)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var p v31ee.BotmgmtProfile
			if err := json.Unmarshal(jsonData, &p); err != nil {
				return nil, err
			}
			params := &v31ee.CreateBotmgmtProfileParams{TransactionId: &txID}
			return c.CreateBotmgmtProfile(ctx, params, p)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var p v30ee.BotmgmtProfile
			if err := json.Unmarshal(jsonData, &p); err != nil {
				return nil, err
			}
			params := &v30ee.CreateBotmgmtProfileParams{TransactionId: &txID}
			return c.CreateBotmgmtProfile(ctx, params, p)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create bot management profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create bot management profile failed with status %d", resp.StatusCode)
	}
	return nil
}

// DeleteProfile deletes a bot management profile.
func (b *BotManagementOperations) DeleteProfile(ctx context.Context, txID, name string) error {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteBotmgmtProfileParams{TransactionId: &txID}
			return c.DeleteBotmgmtProfile(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteBotmgmtProfileParams{TransactionId: &txID}
			return c.DeleteBotmgmtProfile(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteBotmgmtProfileParams{TransactionId: &txID}
			return c.DeleteBotmgmtProfile(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete bot management profile '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete bot management profile '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

// =============================================================================
// CAPTCHA Operations
// =============================================================================

// Captcha represents a CAPTCHA configuration.
type Captcha = v32ee.Captcha

// GetAllCaptchas retrieves all CAPTCHA configurations.
func (b *BotManagementOperations) GetAllCaptchas(ctx context.Context, txID string) ([]Captcha, error) {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetCaptchasParams{TransactionId: &txID}
			return c.GetCaptchas(ctx, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetCaptchasParams{TransactionId: &txID}
			return c.GetCaptchas(ctx, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetCaptchasParams{TransactionId: &txID}
			return c.GetCaptchas(ctx, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get CAPTCHAs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get CAPTCHAs failed with status %d", resp.StatusCode)
	}

	var result []Captcha
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode CAPTCHAs response: %w", err)
	}
	return result, nil
}

// GetCaptcha retrieves a specific CAPTCHA configuration by name.
func (b *BotManagementOperations) GetCaptcha(ctx context.Context, txID, name string) (*Captcha, error) {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetCaptchaParams{TransactionId: &txID}
			return c.GetCaptcha(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.GetCaptchaParams{TransactionId: &txID}
			return c.GetCaptcha(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.GetCaptchaParams{TransactionId: &txID}
			return c.GetCaptcha(ctx, name, params)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get CAPTCHA '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get CAPTCHA '%s' failed with status %d", name, resp.StatusCode)
	}

	var result Captcha
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode CAPTCHA response: %w", err)
	}
	return &result, nil
}

// CreateCaptcha creates a new CAPTCHA configuration.
func (b *BotManagementOperations) CreateCaptcha(ctx context.Context, txID string, captcha *Captcha) error {
	jsonData, err := json.Marshal(captcha)
	if err != nil {
		return fmt.Errorf("failed to marshal CAPTCHA: %w", err)
	}

	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			var cap v32ee.Captcha
			if err := json.Unmarshal(jsonData, &cap); err != nil {
				return nil, err
			}
			params := &v32ee.CreateCaptchaParams{TransactionId: &txID}
			return c.CreateCaptcha(ctx, params, cap)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			var cap v31ee.Captcha
			if err := json.Unmarshal(jsonData, &cap); err != nil {
				return nil, err
			}
			params := &v31ee.CreateCaptchaParams{TransactionId: &txID}
			return c.CreateCaptcha(ctx, params, cap)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			var cap v30ee.Captcha
			if err := json.Unmarshal(jsonData, &cap); err != nil {
				return nil, err
			}
			params := &v30ee.CreateCaptchaParams{TransactionId: &txID}
			return c.CreateCaptcha(ctx, params, cap)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create CAPTCHA: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create CAPTCHA failed with status %d", resp.StatusCode)
	}
	return nil
}

// DeleteCaptcha deletes a CAPTCHA configuration.
func (b *BotManagementOperations) DeleteCaptcha(ctx context.Context, txID, name string) error {
	resp, err := b.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteCaptchaParams{TransactionId: &txID}
			return c.DeleteCaptcha(ctx, name, params)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			params := &v31ee.DeleteCaptchaParams{TransactionId: &txID}
			return c.DeleteCaptcha(ctx, name, params)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			params := &v30ee.DeleteCaptchaParams{TransactionId: &txID}
			return c.DeleteCaptcha(ctx, name, params)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete CAPTCHA '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete CAPTCHA '%s' failed with status %d", name, resp.StatusCode)
	}
	return nil
}

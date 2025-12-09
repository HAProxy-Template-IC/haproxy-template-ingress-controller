// Package executors provides pre-built executor functions for HAProxy configuration operations.
//
// This file contains executors for HAProxy Enterprise Edition (EE) only features:
// - Bot Management Profiles (v3.0+ EE)
// - Captcha (v3.0+ EE)
// - WAF Profile (v3.2+ EE only)
// - WAF Global (v3.2+ EE only)
package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"haproxy-template-ic/pkg/dataplane/client"
	v30ee "haproxy-template-ic/pkg/generated/dataplaneapi/v30ee"
	v31ee "haproxy-template-ic/pkg/generated/dataplaneapi/v31ee"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// =============================================================================
// Bot Management Profile Executors (v3.0+ EE)
// =============================================================================

// BotMgmtProfileCreate returns an executor for creating bot management profiles.
// Bot management profiles are only available in HAProxy Enterprise Edition.
func BotMgmtProfileCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.BotmgmtProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.BotmgmtProfile, _ string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.CreateBotmgmtProfile(ctx, &v32ee.CreateBotmgmtProfileParams{TransactionId: &txID}, *model)
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				var m v31ee.BotmgmtProfile
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.CreateBotmgmtProfile(ctx, &v31ee.CreateBotmgmtProfileParams{TransactionId: &txID}, m)
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				var m v30ee.BotmgmtProfile
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.CreateBotmgmtProfile(ctx, &v30ee.CreateBotmgmtProfileParams{TransactionId: &txID}, m)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create botmgmt profile: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "botmgmt profile creation")
	}
}

// BotMgmtProfileUpdate returns an executor for updating bot management profiles.
func BotMgmtProfileUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.BotmgmtProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.BotmgmtProfile, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.EditBotmgmtProfile(ctx, name, &v32ee.EditBotmgmtProfileParams{TransactionId: &txID}, *model)
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				var m v31ee.BotmgmtProfile
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.EditBotmgmtProfile(ctx, name, &v31ee.EditBotmgmtProfileParams{TransactionId: &txID}, m)
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				var m v30ee.BotmgmtProfile
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.EditBotmgmtProfile(ctx, name, &v30ee.EditBotmgmtProfileParams{TransactionId: &txID}, m)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update botmgmt profile '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "botmgmt profile update")
	}
}

// BotMgmtProfileDelete returns an executor for deleting bot management profiles.
func BotMgmtProfileDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.BotmgmtProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.BotmgmtProfile, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.DeleteBotmgmtProfile(ctx, name, &v32ee.DeleteBotmgmtProfileParams{TransactionId: &txID})
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				return ec.DeleteBotmgmtProfile(ctx, name, &v31ee.DeleteBotmgmtProfileParams{TransactionId: &txID})
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				return ec.DeleteBotmgmtProfile(ctx, name, &v30ee.DeleteBotmgmtProfileParams{TransactionId: &txID})
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete botmgmt profile '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "botmgmt profile deletion")
	}
}

// =============================================================================
// Captcha Executors (v3.0+ EE)
// =============================================================================

// CaptchaCreate returns an executor for creating captcha sections.
// Captcha is only available in HAProxy Enterprise Edition.
func CaptchaCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.Captcha, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.Captcha, _ string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.CreateCaptcha(ctx, &v32ee.CreateCaptchaParams{TransactionId: &txID}, *model)
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				var m v31ee.Captcha
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.CreateCaptcha(ctx, &v31ee.CreateCaptchaParams{TransactionId: &txID}, m)
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				var m v30ee.Captcha
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.CreateCaptcha(ctx, &v30ee.CreateCaptchaParams{TransactionId: &txID}, m)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create captcha: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "captcha creation")
	}
}

// CaptchaUpdate returns an executor for updating captcha sections.
func CaptchaUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.Captcha, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.Captcha, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.EditCaptcha(ctx, name, &v32ee.EditCaptchaParams{TransactionId: &txID}, *model)
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				var m v31ee.Captcha
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.EditCaptcha(ctx, name, &v31ee.EditCaptchaParams{TransactionId: &txID}, m)
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				var m v30ee.Captcha
				if err := convertModel(model, &m); err != nil {
					return nil, err
				}
				return ec.EditCaptcha(ctx, name, &v30ee.EditCaptchaParams{TransactionId: &txID}, m)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update captcha '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "captcha update")
	}
}

// CaptchaDelete returns an executor for deleting captcha sections.
func CaptchaDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.Captcha, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.Captcha, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.DeleteCaptcha(ctx, name, &v32ee.DeleteCaptchaParams{TransactionId: &txID})
			},
			V31EE: func(ec *v31ee.Client) (*http.Response, error) {
				return ec.DeleteCaptcha(ctx, name, &v31ee.DeleteCaptchaParams{TransactionId: &txID})
			},
			V30EE: func(ec *v30ee.Client) (*http.Response, error) {
				return ec.DeleteCaptcha(ctx, name, &v30ee.DeleteCaptchaParams{TransactionId: &txID})
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete captcha '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "captcha deletion")
	}
}

// =============================================================================
// WAF Profile Executors (v3.2+ EE only)
// =============================================================================

// WAFProfileCreate returns an executor for creating WAF profiles.
// WAF profiles are only available in HAProxy Enterprise Edition v3.2+.
func WAFProfileCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafProfile, _ string) error {
		// WAF profiles require v3.2+ EE - only V32EE is provided
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.CreateWafProfile(ctx, &v32ee.CreateWafProfileParams{TransactionId: &txID}, *model)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create WAF profile: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF profile creation")
	}
}

// WAFProfileUpdate returns an executor for updating WAF profiles.
func WAFProfileUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafProfile, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.ReplaceWafProfile(ctx, name, &v32ee.ReplaceWafProfileParams{TransactionId: &txID}, *model)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update WAF profile '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF profile update")
	}
}

// WAFProfileDelete returns an executor for deleting WAF profiles.
func WAFProfileDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.WafProfile, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.WafProfile, name string) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.DeleteWafProfile(ctx, name, &v32ee.DeleteWafProfileParams{TransactionId: &txID})
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete WAF profile '%s': %w", name, err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF profile deletion")
	}
}

// =============================================================================
// WAF Global Executors (v3.2+ EE only, singleton - only update/create)
// =============================================================================

// WAFGlobalCreate returns an executor for creating the WAF global configuration.
// WAF global is a singleton section. If it doesn't exist, it can be created.
func WAFGlobalCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafGlobal) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafGlobal) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.CreateWafGlobal(ctx, &v32ee.CreateWafGlobalParams{TransactionId: &txID}, *model)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create WAF global: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF global creation")
	}
}

// WAFGlobalUpdate returns an executor for updating the WAF global configuration.
// WAF global is a singleton section - it can only be updated, not created/deleted.
func WAFGlobalUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafGlobal) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafGlobal) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.ReplaceWafGlobal(ctx, &v32ee.ReplaceWafGlobalParams{TransactionId: &txID}, *model)
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update WAF global: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF global update")
	}
}

// WAFGlobalDelete returns an executor for deleting the WAF global configuration.
func WAFGlobalDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, model *v32ee.WafGlobal) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *v32ee.WafGlobal) error {
		resp, err := c.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
			V32EE: func(ec *v32ee.Client) (*http.Response, error) {
				return ec.DeleteWafGlobal(ctx, &v32ee.DeleteWafGlobalParams{TransactionId: &txID})
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete WAF global: %w", err)
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "WAF global deletion")
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// convertModel converts a model from one version to another via JSON marshaling.
// This is necessary because EE types differ across API versions.
func convertModel(src, dst any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal source model: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal to target model: %w", err)
	}
	return nil
}

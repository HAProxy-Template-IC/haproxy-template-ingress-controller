// Package sections provides factory functions for creating HAProxy Enterprise Edition operations.
//
// This file contains factory functions for EE-only sections:
// - Bot Management Profiles (v3.0+ EE)
// - Captcha (v3.0+ EE)
// - WAF Profile (v3.2+ EE)
// - WAF Global (v3.2+ EE)
package sections

import (
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// Top-level CRUD builders for Enterprise Edition sections.
var (
	BotMgmtProfileOps = NewTopLevelCRUD("botmgmt-profile", "botmgmt-profile", botMgmtProfileName)
	CaptchaOps        = NewTopLevelCRUD("captcha", "captcha", captchaEEName)
	WafProfileOps     = NewTopLevelCRUD("waf-profile", "waf-profile", wafProfileName)
)

// NewWAFGlobalCreate creates an operation to create the WAF global configuration.
// WAF global is a singleton section (only one per configuration).
func NewWAFGlobalCreate(_ *v32ee.WafGlobal) Operation {
	return newOp(
		OperationCreate, "waf-global",
		describeSingleton(OperationCreate, "waf-global"),
	)
}

// NewWAFGlobalUpdate creates an operation to update the WAF global configuration.
func NewWAFGlobalUpdate(_ *v32ee.WafGlobal) Operation {
	return newOp(
		OperationUpdate, "waf-global",
		describeSingleton(OperationUpdate, "waf-global"),
	)
}

// NewWAFGlobalDelete creates an operation to delete the WAF global configuration.
func NewWAFGlobalDelete(_ *v32ee.WafGlobal) Operation {
	return newOp(
		OperationDelete, "waf-global",
		describeSingleton(OperationDelete, "waf-global"),
	)
}

// botMgmtProfileName extracts the name from a BotmgmtProfile model.
func botMgmtProfileName(p *v32ee.BotmgmtProfile) string {
	return p.Name
}

// captchaEEName extracts the name from a Captcha model.
// Named captchaEEName to avoid conflict with CaptchaName in helpers.go.
func captchaEEName(c *v32ee.Captcha) string {
	return c.Name
}

// wafProfileName extracts the name from a WafProfile model.
func wafProfileName(p *v32ee.WafProfile) string {
	return p.Name
}

// describeSingleton returns a description function for singleton operations.
func describeSingleton(op OperationType, section string) func() string {
	verb := opVerb(op)
	return func() string {
		return verb + " " + section + " configuration"
	}
}

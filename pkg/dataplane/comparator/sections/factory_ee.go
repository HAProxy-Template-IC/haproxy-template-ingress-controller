// Package sections provides factory functions for creating HAProxy Enterprise Edition operations.
//
// This file contains factory functions for EE-only sections:
// - Bot Management Profiles (v3.0+ EE)
// - Captcha (v3.0+ EE)
// - WAF Profile (v3.2+ EE)
// - WAF Global (v3.2+ EE)
package sections

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// Priority constants for Enterprise Edition sections.
const (
	// PriorityEEBotMgmtProfile is the priority for bot management profile operations.
	// These are top-level EE sections, similar to resolvers and caches.
	PriorityEEBotMgmtProfile = 15

	// PriorityEECaptcha is the priority for captcha operations.
	PriorityEECaptcha = 15

	// PriorityEEWAFGlobal is the priority for WAF global operations.
	// WAF global should be created before WAF profiles.
	PriorityEEWAFGlobal = 12

	// PriorityEEWAFProfile is the priority for WAF profile operations.
	PriorityEEWAFProfile = 15
)

// Top-level CRUD builders for Enterprise Edition sections.
var (
	botMgmtProfileOps = NewTopLevelCRUD(
		"botmgmt-profile", "botmgmt-profile", PriorityEEBotMgmtProfile, BotMgmtProfileName,
		executors.BotMgmtProfileCreate(), executors.BotMgmtProfileUpdate(), executors.BotMgmtProfileDelete(),
	)
	captchaOps = NewTopLevelCRUD(
		"captcha", "captcha", PriorityEECaptcha, CaptchaEEName,
		executors.CaptchaCreate(), executors.CaptchaUpdate(), executors.CaptchaDelete(),
	)
	wafProfileOps = NewTopLevelCRUD(
		"waf-profile", "waf-profile", PriorityEEWAFProfile, WAFProfileName,
		executors.WAFProfileCreate(), executors.WAFProfileUpdate(), executors.WAFProfileDelete(),
	)
)

// NewBotMgmtProfileCreate creates an operation to create a bot management profile.
// Bot management profiles are only available in HAProxy Enterprise Edition.
func NewBotMgmtProfileCreate(profile *v32ee.BotmgmtProfile) Operation {
	return botMgmtProfileOps.Create(profile)
}

// NewBotMgmtProfileUpdate creates an operation to update a bot management profile.
func NewBotMgmtProfileUpdate(profile *v32ee.BotmgmtProfile) Operation {
	return botMgmtProfileOps.Update(profile)
}

// NewBotMgmtProfileDelete creates an operation to delete a bot management profile.
func NewBotMgmtProfileDelete(profile *v32ee.BotmgmtProfile) Operation {
	return botMgmtProfileOps.Delete(profile)
}

// NewCaptchaCreate creates an operation to create a captcha section.
// Captcha is only available in HAProxy Enterprise Edition.
func NewCaptchaCreate(captcha *v32ee.Captcha) Operation { return captchaOps.Create(captcha) }

// NewCaptchaUpdate creates an operation to update a captcha section.
func NewCaptchaUpdate(captcha *v32ee.Captcha) Operation { return captchaOps.Update(captcha) }

// NewCaptchaDelete creates an operation to delete a captcha section.
func NewCaptchaDelete(captcha *v32ee.Captcha) Operation { return captchaOps.Delete(captcha) }

// NewWAFProfileCreate creates an operation to create a WAF profile.
// WAF profiles are only available in HAProxy Enterprise Edition v3.2+.
func NewWAFProfileCreate(profile *v32ee.WafProfile) Operation {
	return wafProfileOps.Create(profile)
}

// NewWAFProfileUpdate creates an operation to update a WAF profile.
func NewWAFProfileUpdate(profile *v32ee.WafProfile) Operation {
	return wafProfileOps.Update(profile)
}

// NewWAFProfileDelete creates an operation to delete a WAF profile.
func NewWAFProfileDelete(profile *v32ee.WafProfile) Operation {
	return wafProfileOps.Delete(profile)
}

// NewWAFGlobalCreate creates an operation to create the WAF global configuration.
// WAF global is a singleton section (only one per configuration).
func NewWAFGlobalCreate(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationCreate, "waf-global", PriorityEEWAFGlobal, wafGlobal,
		Identity[*v32ee.WafGlobal], executors.WAFGlobalCreate(),
		DescribeSingleton(OperationCreate, "waf-global"),
	)
}

// NewWAFGlobalUpdate creates an operation to update the WAF global configuration.
func NewWAFGlobalUpdate(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationUpdate, "waf-global", PriorityEEWAFGlobal, wafGlobal,
		Identity[*v32ee.WafGlobal], executors.WAFGlobalUpdate(),
		DescribeSingleton(OperationUpdate, "waf-global"),
	)
}

// NewWAFGlobalDelete creates an operation to delete the WAF global configuration.
func NewWAFGlobalDelete(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationDelete, "waf-global", PriorityEEWAFGlobal, wafGlobal,
		Identity[*v32ee.WafGlobal], executors.WAFGlobalDelete(),
		DescribeSingleton(OperationDelete, "waf-global"),
	)
}

// BotMgmtProfileName extracts the name from a BotmgmtProfile model.
func BotMgmtProfileName(p *v32ee.BotmgmtProfile) string {
	return p.Name
}

// CaptchaEEName extracts the name from a Captcha model.
// Named CaptchaEEName to avoid conflict with CaptchaName in helpers.go.
func CaptchaEEName(c *v32ee.Captcha) string {
	return c.Name
}

// WAFProfileName extracts the name from a WafProfile model.
func WAFProfileName(p *v32ee.WafProfile) string {
	return p.Name
}

// DescribeSingleton returns a description function for singleton operations.
func DescribeSingleton(op OperationType, section string) func() string {
	verb := opVerb(op)
	return func() string {
		return verb + " " + section + " configuration"
	}
}

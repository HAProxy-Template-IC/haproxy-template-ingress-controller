// Package sections provides factory functions for creating HAProxy Enterprise Edition operations.
//
// This file contains factory functions for EE-only sections:
// - Bot Management Profiles (v3.0+ EE)
// - Captcha (v3.0+ EE)
// - WAF Profile (v3.2+ EE)
// - WAF Global (v3.2+ EE)
// - UDP LB (v3.0+ EE) - TODO when API support is added
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

	// PriorityEEUDPLB is the priority for UDP load balancer operations.
	// Similar priority to backends/frontends.
	PriorityEEUDPLB = 30
)

// =============================================================================
// Bot Management Profile Factory Functions
// =============================================================================

// NewBotMgmtProfileCreate creates an operation to create a bot management profile.
// Bot management profiles are only available in HAProxy Enterprise Edition.
func NewBotMgmtProfileCreate(profile *v32ee.BotmgmtProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationCreate,
		"botmgmt-profile",
		PriorityEEBotMgmtProfile,
		profile,
		IdentityBotMgmtProfile,
		BotMgmtProfileName,
		executors.BotMgmtProfileCreate(),
		DescribeTopLevel(OperationCreate, "botmgmt-profile", name),
	)
}

// NewBotMgmtProfileUpdate creates an operation to update a bot management profile.
func NewBotMgmtProfileUpdate(profile *v32ee.BotmgmtProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationUpdate,
		"botmgmt-profile",
		PriorityEEBotMgmtProfile,
		profile,
		IdentityBotMgmtProfile,
		BotMgmtProfileName,
		executors.BotMgmtProfileUpdate(),
		DescribeTopLevel(OperationUpdate, "botmgmt-profile", name),
	)
}

// NewBotMgmtProfileDelete creates an operation to delete a bot management profile.
func NewBotMgmtProfileDelete(profile *v32ee.BotmgmtProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationDelete,
		"botmgmt-profile",
		PriorityEEBotMgmtProfile,
		profile,
		NilBotMgmtProfile,
		BotMgmtProfileName,
		executors.BotMgmtProfileDelete(),
		DescribeTopLevel(OperationDelete, "botmgmt-profile", name),
	)
}

// =============================================================================
// Captcha Factory Functions
// =============================================================================

// NewCaptchaCreate creates an operation to create a captcha section.
// Captcha is only available in HAProxy Enterprise Edition.
func NewCaptchaCreate(captcha *v32ee.Captcha) Operation {
	name := captcha.Name
	return NewTopLevelOp(
		OperationCreate,
		"captcha",
		PriorityEECaptcha,
		captcha,
		IdentityCaptchaEE,
		CaptchaEEName,
		executors.CaptchaCreate(),
		DescribeTopLevel(OperationCreate, "captcha", name),
	)
}

// NewCaptchaUpdate creates an operation to update a captcha section.
func NewCaptchaUpdate(captcha *v32ee.Captcha) Operation {
	name := captcha.Name
	return NewTopLevelOp(
		OperationUpdate,
		"captcha",
		PriorityEECaptcha,
		captcha,
		IdentityCaptchaEE,
		CaptchaEEName,
		executors.CaptchaUpdate(),
		DescribeTopLevel(OperationUpdate, "captcha", name),
	)
}

// NewCaptchaDelete creates an operation to delete a captcha section.
func NewCaptchaDelete(captcha *v32ee.Captcha) Operation {
	name := captcha.Name
	return NewTopLevelOp(
		OperationDelete,
		"captcha",
		PriorityEECaptcha,
		captcha,
		NilCaptchaEE,
		CaptchaEEName,
		executors.CaptchaDelete(),
		DescribeTopLevel(OperationDelete, "captcha", name),
	)
}

// =============================================================================
// WAF Profile Factory Functions (v3.2+ EE only)
// =============================================================================

// NewWAFProfileCreate creates an operation to create a WAF profile.
// WAF profiles are only available in HAProxy Enterprise Edition v3.2+.
func NewWAFProfileCreate(profile *v32ee.WafProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationCreate,
		"waf-profile",
		PriorityEEWAFProfile,
		profile,
		IdentityWAFProfile,
		WAFProfileName,
		executors.WAFProfileCreate(),
		DescribeTopLevel(OperationCreate, "waf-profile", name),
	)
}

// NewWAFProfileUpdate creates an operation to update a WAF profile.
func NewWAFProfileUpdate(profile *v32ee.WafProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationUpdate,
		"waf-profile",
		PriorityEEWAFProfile,
		profile,
		IdentityWAFProfile,
		WAFProfileName,
		executors.WAFProfileUpdate(),
		DescribeTopLevel(OperationUpdate, "waf-profile", name),
	)
}

// NewWAFProfileDelete creates an operation to delete a WAF profile.
func NewWAFProfileDelete(profile *v32ee.WafProfile) Operation {
	name := profile.Name
	return NewTopLevelOp(
		OperationDelete,
		"waf-profile",
		PriorityEEWAFProfile,
		profile,
		NilWAFProfile,
		WAFProfileName,
		executors.WAFProfileDelete(),
		DescribeTopLevel(OperationDelete, "waf-profile", name),
	)
}

// =============================================================================
// WAF Global Factory Functions (v3.2+ EE only, singleton)
// =============================================================================

// NewWAFGlobalCreate creates an operation to create the WAF global configuration.
// WAF global is a singleton section (only one per configuration).
func NewWAFGlobalCreate(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationCreate,
		"waf-global",
		PriorityEEWAFGlobal,
		wafGlobal,
		IdentityWAFGlobal,
		executors.WAFGlobalCreate(),
		DescribeSingleton(OperationCreate, "waf-global"),
	)
}

// NewWAFGlobalUpdate creates an operation to update the WAF global configuration.
func NewWAFGlobalUpdate(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"waf-global",
		PriorityEEWAFGlobal,
		wafGlobal,
		IdentityWAFGlobal,
		executors.WAFGlobalUpdate(),
		DescribeSingleton(OperationUpdate, "waf-global"),
	)
}

// NewWAFGlobalDelete creates an operation to delete the WAF global configuration.
func NewWAFGlobalDelete(wafGlobal *v32ee.WafGlobal) Operation {
	return NewSingletonOp(
		OperationDelete,
		"waf-global",
		PriorityEEWAFGlobal,
		wafGlobal,
		IdentityWAFGlobal,
		executors.WAFGlobalDelete(),
		DescribeSingleton(OperationDelete, "waf-global"),
	)
}

// =============================================================================
// EE Helper Functions - Name Extractors
// =============================================================================

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

// =============================================================================
// EE Helper Functions - Identity Transforms
// =============================================================================

// IdentityBotMgmtProfile returns the model as-is.
func IdentityBotMgmtProfile(p *v32ee.BotmgmtProfile) *v32ee.BotmgmtProfile { return p }

// IdentityCaptchaEE returns the model as-is.
// Named IdentityCaptchaEE to avoid conflict with IdentityCapture in helpers.go.
func IdentityCaptchaEE(c *v32ee.Captcha) *v32ee.Captcha { return c }

// IdentityWAFProfile returns the model as-is.
func IdentityWAFProfile(p *v32ee.WafProfile) *v32ee.WafProfile { return p }

// IdentityWAFGlobal returns the model as-is.
func IdentityWAFGlobal(w *v32ee.WafGlobal) *v32ee.WafGlobal { return w }

// =============================================================================
// EE Helper Functions - Nil Transforms (for delete operations)
// =============================================================================

// NilBotMgmtProfile returns nil, used for delete operations where model isn't needed.
func NilBotMgmtProfile(_ *v32ee.BotmgmtProfile) *v32ee.BotmgmtProfile { return nil }

// NilCaptchaEE returns nil, used for delete operations where model isn't needed.
// Named NilCaptchaEE to avoid conflict with NilCapture in helpers.go.
func NilCaptchaEE(_ *v32ee.Captcha) *v32ee.Captcha { return nil }

// NilWAFProfile returns nil, used for delete operations where model isn't needed.
func NilWAFProfile(_ *v32ee.WafProfile) *v32ee.WafProfile { return nil }

// NilWAFGlobal returns nil, used for delete operations where model isn't needed.
func NilWAFGlobal(_ *v32ee.WafGlobal) *v32ee.WafGlobal { return nil }

// =============================================================================
// EE Helper Functions - Describe Functions
// =============================================================================

// DescribeSingleton returns a description function for singleton operations.
func DescribeSingleton(op OperationType, section string) func() string {
	verb := opVerb(op)
	return func() string {
		return verb + " " + section + " configuration"
	}
}

// Package comparator provides comparison functions for HAProxy Enterprise Edition sections.
//
// This file contains comparison functions for EE-only sections:
// - Bot Management Profiles (v3.0+ EE)
// - Captcha (v3.0+ EE)
// - WAF Profile (v3.2+ EE)
// - WAF Global (v3.2+ EE)
package comparator

import (
	"bytes"
	"encoding/json"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// compareEnterpriseSections compares all Enterprise Edition sections.
// This function is extracted from the main Compare function to reduce statement count.
func (c *Comparator) compareEnterpriseSections(current, desired *parser.StructuredConfig) []Operation {
	// Bot management profiles (v3.0+ EE)
	botMgmtOps := c.compareBotMgmtProfiles(current, desired)
	// Captchas (v3.0+ EE)
	captchaOps := c.compareCaptchas(current, desired)
	// WAF global (v3.2+ EE, singleton)
	wafGlobalOps := c.compareWAFGlobal(current, desired)
	// WAF profiles (v3.2+ EE)
	wafProfilesOps := c.compareWAFProfiles(current, desired)

	operations := make([]Operation, 0, len(botMgmtOps)+len(captchaOps)+len(wafGlobalOps)+len(wafProfilesOps))
	operations = append(operations, botMgmtOps...)
	operations = append(operations, captchaOps...)
	operations = append(operations, wafGlobalOps...)
	operations = append(operations, wafProfilesOps...)

	return operations
}

// compareBotMgmtProfiles compares bot management profile sections between current and desired configurations.
// Bot management profiles are only available in HAProxy Enterprise Edition.
func (c *Comparator) compareBotMgmtProfiles(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.BotMgmtProfiles,
		desired.BotMgmtProfiles,
		func(p *v32ee.BotmgmtProfile) string { return p.Name },
		eeModelEqual[v32ee.BotmgmtProfile],
		sections.BotMgmtProfileOps.Create,
		sections.BotMgmtProfileOps.Delete,
		sections.BotMgmtProfileOps.Update,
	)
}

// compareCaptchas compares captcha sections between current and desired configurations.
// Captcha sections are only available in HAProxy Enterprise Edition.
func (c *Comparator) compareCaptchas(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.Captchas,
		desired.Captchas,
		func(cap *v32ee.Captcha) string { return cap.Name },
		eeModelEqual[v32ee.Captcha],
		sections.CaptchaOps.Create,
		sections.CaptchaOps.Delete,
		sections.CaptchaOps.Update,
	)
}

// compareWAFProfiles compares WAF profile sections between current and desired configurations.
// WAF profiles are only available in HAProxy Enterprise Edition v3.2+.
func (c *Comparator) compareWAFProfiles(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.WAFProfiles,
		desired.WAFProfiles,
		func(p *v32ee.WafProfile) string { return p.Name },
		eeModelEqual[v32ee.WafProfile],
		sections.WafProfileOps.Create,
		sections.WafProfileOps.Delete,
		sections.WafProfileOps.Update,
	)
}

// compareWAFGlobal compares the WAF global section between current and desired configurations.
// WAF global is a singleton section (only one per configuration).
// WAF global is only available in HAProxy Enterprise Edition v3.2+.
func (c *Comparator) compareWAFGlobal(current, desired *parser.StructuredConfig) []Operation {
	switch {
	case desired.WAFGlobal == nil && current.WAFGlobal != nil:
		return []Operation{sections.NewWAFGlobalDelete(current.WAFGlobal)}
	case desired.WAFGlobal == nil:
		return nil
	case current.WAFGlobal == nil:
		return []Operation{sections.NewWAFGlobalCreate(desired.WAFGlobal)}
	case !eeModelEqual(current.WAFGlobal, desired.WAFGlobal):
		return []Operation{sections.NewWAFGlobalUpdate(desired.WAFGlobal)}
	default:
		return nil
	}
}

// eeModelEqual compares two EE models for equality using JSON serialization.
// EE types from v32ee don't have built-in Equal methods like client-native models,
// so we use JSON comparison as a reliable equality check.
func eeModelEqual[T any](a, b *T) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Marshal both to JSON and compare
	aJSON, errA := json.Marshal(a)
	bJSON, errB := json.Marshal(b)

	if errA != nil || errB != nil {
		// If marshaling fails, assume not equal
		return false
	}

	return bytes.Equal(aJSON, bJSON)
}

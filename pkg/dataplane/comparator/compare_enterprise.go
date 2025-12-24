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

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser"
	v32ee "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/dataplaneapi/v32ee"
)

// compareEnterpriseSections compares all Enterprise Edition sections.
// This function is extracted from the main Compare function to reduce statement count.
func (c *Comparator) compareEnterpriseSections(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// Bot management profiles (v3.0+ EE)
	operations = append(operations, c.compareBotMgmtProfiles(current, desired)...)

	// Captchas (v3.0+ EE)
	operations = append(operations, c.compareCaptchas(current, desired)...)

	// WAF global (v3.2+ EE, singleton)
	operations = append(operations, c.compareWAFGlobal(current, desired)...)

	// WAF profiles (v3.2+ EE)
	operations = append(operations, c.compareWAFProfiles(current, desired)...)

	return operations
}

// compareBotMgmtProfiles compares bot management profile sections between current and desired configurations.
// Bot management profiles are only available in HAProxy Enterprise Edition.
func (c *Comparator) compareBotMgmtProfiles(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.BotMgmtProfiles,
		desired.BotMgmtProfiles,
		func(p *v32ee.BotmgmtProfile) string {
			return p.Name
		},
		eeModelEqual[v32ee.BotmgmtProfile],
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileCreate(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileDelete(p) },
		func(p *v32ee.BotmgmtProfile) Operation { return sections.NewBotMgmtProfileUpdate(p) },
	)
}

// compareCaptchas compares captcha sections between current and desired configurations.
// Captcha sections are only available in HAProxy Enterprise Edition.
func (c *Comparator) compareCaptchas(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.Captchas,
		desired.Captchas,
		func(cap *v32ee.Captcha) string {
			return cap.Name
		},
		eeModelEqual[v32ee.Captcha],
		func(cap *v32ee.Captcha) Operation { return sections.NewCaptchaCreate(cap) },
		func(cap *v32ee.Captcha) Operation { return sections.NewCaptchaDelete(cap) },
		func(cap *v32ee.Captcha) Operation { return sections.NewCaptchaUpdate(cap) },
	)
}

// compareWAFProfiles compares WAF profile sections between current and desired configurations.
// WAF profiles are only available in HAProxy Enterprise Edition v3.2+.
func (c *Comparator) compareWAFProfiles(current, desired *parser.StructuredConfig) []Operation {
	return compareNamedSections(
		current.WAFProfiles,
		desired.WAFProfiles,
		func(p *v32ee.WafProfile) string {
			return p.Name
		},
		eeModelEqual[v32ee.WafProfile],
		func(p *v32ee.WafProfile) Operation { return sections.NewWAFProfileCreate(p) },
		func(p *v32ee.WafProfile) Operation { return sections.NewWAFProfileDelete(p) },
		func(p *v32ee.WafProfile) Operation { return sections.NewWAFProfileUpdate(p) },
	)
}

// compareWAFGlobal compares the WAF global section between current and desired configurations.
// WAF global is a singleton section (only one per configuration).
// WAF global is only available in HAProxy Enterprise Edition v3.2+.
func (c *Comparator) compareWAFGlobal(current, desired *parser.StructuredConfig) []Operation {
	var operations []Operation

	// If desired has no WAF global, nothing to do (can't delete singleton)
	if desired.WAFGlobal == nil {
		// If current has WAF global but desired doesn't, generate delete
		if current.WAFGlobal != nil {
			operations = append(operations, sections.NewWAFGlobalDelete(current.WAFGlobal))
		}
		return operations
	}

	// If current is nil but desired has WAF global, create it
	if current.WAFGlobal == nil {
		operations = append(operations, sections.NewWAFGlobalCreate(desired.WAFGlobal))
		return operations
	}

	// Compare using JSON equality (EE types don't have Equal methods)
	if !eeModelEqual(current.WAFGlobal, desired.WAFGlobal) {
		operations = append(operations, sections.NewWAFGlobalUpdate(desired.WAFGlobal))
	}

	return operations
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

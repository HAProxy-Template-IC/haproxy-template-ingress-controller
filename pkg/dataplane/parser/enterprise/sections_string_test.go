// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The five *String wrappers (IsEESectionString, IsCESectionString,
// IsAnySectionString, IsSingletonSectionString, IsNamedSectionString)
// each convert a raw string to a Section and dispatch to the typed
// classifier. They were untested until now — pin that the wrappers
// agree with their typed counterparts and that the unknown/empty
// string branches behave the same way the typed versions do.
func TestSectionString_AgreesWithTypedVariants(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		section Section // for cross-checking with the typed variant
	}{
		// EE sections
		{name: "udp-lb", input: "udp-lb", section: SectionUDPLB},
		{name: "waf-global", input: "waf-global", section: SectionWAFGlobal},
		{name: "waf-profile", input: "waf-profile", section: SectionWAFProfile},
		{name: "botmgmt-profile", input: "botmgmt-profile", section: SectionBotMgmtProfile},
		{name: "captcha", input: "captcha", section: SectionCaptcha},
		{name: "dynamic-update", input: "dynamic-update", section: SectionDynamicUpdate},

		// CE sections
		{name: "global", input: "global", section: SectionGlobal},
		{name: "defaults", input: "defaults", section: SectionDefaults},
		{name: "frontend", input: "frontend", section: SectionFrontend},
		{name: "backend", input: "backend", section: SectionBackend},
		{name: "comments", input: "#", section: SectionComments},

		// Unknown / edge cases
		{name: "empty string", input: "", section: Section("")},
		{name: "unknown section", input: "not-a-section", section: Section("not-a-section")},
		{name: "case mismatch (Frontend != frontend)", input: "Frontend", section: Section("Frontend")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, IsEESection(tt.section), IsEESectionString(tt.input),
				"IsEESectionString must agree with IsEESection")
			assert.Equal(t, IsCESection(tt.section), IsCESectionString(tt.input),
				"IsCESectionString must agree with IsCESection")
			assert.Equal(t, IsAnySection(tt.section), IsAnySectionString(tt.input),
				"IsAnySectionString must agree with IsAnySection")
			assert.Equal(t, IsSingletonSection(tt.section), IsSingletonSectionString(tt.input),
				"IsSingletonSectionString must agree with IsSingletonSection")
			assert.Equal(t, IsNamedSection(tt.section), IsNamedSectionString(tt.input),
				"IsNamedSectionString must agree with IsNamedSection")
		})
	}
}

// TestIsNamedSection covers the documented carve-out: comments are
// the only non-singleton section that is NOT a named section. Every
// other non-singleton section requires a name. Pin both halves.
func TestIsNamedSection_CommentsCarveOut(t *testing.T) {
	assert.False(t, IsNamedSection(SectionComments),
		"comments are explicitly excluded from the 'named section' set")

	// Every other non-singleton CE section must be a named section.
	for _, s := range []Section{
		SectionFrontend,
		SectionBackend,
		SectionListen,
		SectionResolvers,
		SectionPeers,
		SectionMailers,
		SectionCache,
		SectionProgram,
		SectionHTTPErrors,
		SectionRing,
		SectionLogForward,
		SectionFCGIApp,
		SectionCrtStore,
		SectionTraces,
		SectionLogProfile,
		SectionACME,
		SectionUserlist,
	} {
		assert.True(t, IsNamedSection(s), "%q must be classified as a named section", s)
	}

	// Singleton sections must NOT be named sections (mutually exclusive).
	for _, s := range []Section{SectionGlobal, SectionDefaults, SectionWAFGlobal} {
		assert.False(t, IsNamedSection(s), "%q is a singleton; must not be a named section", s)
	}

	// Unknown sections fall through the comments-guard and the
	// singleton-check, ending up classified as named (it's the
	// liberal default — pin it so a future refactor doesn't quietly
	// flip to deny-by-default).
	assert.True(t, IsNamedSection(Section("not-a-section")),
		"unknown sections fall through to named-section classification (liberal default)")
}

// GetAllEESections returns the canonical list of EE section names.
// Adding a new EE section must update both the eeSections map AND
// this list — pin them with a cross-check so they can't drift.
func TestGetAllEESections_AgreesWithEESectionsMap(t *testing.T) {
	got := GetAllEESections()

	// Every returned section must classify as EE.
	for _, s := range got {
		assert.True(t, IsEESection(s), "%q is in GetAllEESections but IsEESection rejects it", s)
	}

	// Every EE section in the classifier must appear in the returned list.
	// Build a set of returned sections for comparison.
	returned := make(map[Section]bool, len(got))
	for _, s := range got {
		returned[s] = true
	}
	for _, s := range []Section{
		SectionUDPLB,
		SectionWAFGlobal,
		SectionWAFProfile,
		SectionBotMgmtProfile,
		SectionCaptcha,
		SectionDynamicUpdate,
	} {
		assert.True(t, returned[s], "EE section %q must appear in GetAllEESections", s)
	}
}

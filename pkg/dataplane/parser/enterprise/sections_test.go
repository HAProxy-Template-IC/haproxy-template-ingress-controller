package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsEESection(t *testing.T) {
	tests := []struct {
		section  Section
		expected bool
	}{
		{SectionUDPLB, true},
		{SectionWAFGlobal, true},
		{SectionWAFProfile, true},
		{SectionBotMgmtProfile, true},
		{SectionCaptcha, true},
		{SectionDynamicUpdate, true},
		{SectionGlobal, false},
		{SectionFrontend, false},
		{SectionBackend, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.section), func(t *testing.T) {
			assert.Equal(t, tt.expected, IsEESection(tt.section))
		})
	}
}

func TestIsCESection(t *testing.T) {
	tests := []struct {
		section  Section
		expected bool
	}{
		{SectionGlobal, true},
		{SectionDefaults, true},
		{SectionFrontend, true},
		{SectionBackend, true},
		{SectionListen, true},
		{SectionUDPLB, false},
		{SectionWAFGlobal, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.section), func(t *testing.T) {
			assert.Equal(t, tt.expected, IsCESection(tt.section))
		})
	}
}

func TestIsAnySection(t *testing.T) {
	// All EE sections should return true
	for _, s := range GetAllEESections() {
		assert.True(t, IsAnySection(s), "EE section %s should be recognized", s)
	}

	// All CE sections should return true
	for _, s := range GetAllCESections() {
		assert.True(t, IsAnySection(s), "CE section %s should be recognized", s)
	}

	// Unknown section should return false
	assert.False(t, IsAnySection(Section("unknown-section")))
}

func TestIsSingletonSection(t *testing.T) {
	tests := []struct {
		section  Section
		expected bool
	}{
		{SectionGlobal, true},
		{SectionWAFGlobal, true},
		{SectionDefaults, true}, // treated as singleton for simplicity
		{SectionFrontend, false},
		{SectionBackend, false},
		{SectionUDPLB, false},
		{SectionWAFProfile, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.section), func(t *testing.T) {
			assert.Equal(t, tt.expected, IsSingletonSection(tt.section))
		})
	}
}

func TestIsNamedSection(t *testing.T) {
	tests := []struct {
		section  Section
		expected bool
	}{
		{SectionGlobal, false},    // singleton
		{SectionWAFGlobal, false}, // singleton
		{SectionDefaults, false},  // singleton
		{SectionFrontend, true},
		{SectionBackend, true},
		{SectionUDPLB, true},
		{SectionWAFProfile, true},
		{SectionComments, false}, // special case
	}

	for _, tt := range tests {
		t.Run(string(tt.section), func(t *testing.T) {
			assert.Equal(t, tt.expected, IsNamedSection(tt.section))
		})
	}
}

func TestStringVariants(t *testing.T) {
	// Test that string variants work the same as Section variants
	assert.Equal(t, IsEESection(SectionUDPLB), IsEESectionString("udp-lb"))
	assert.Equal(t, IsCESection(SectionGlobal), IsCESectionString("global"))
	assert.Equal(t, IsAnySection(SectionFrontend), IsAnySectionString("frontend"))
	assert.Equal(t, IsSingletonSection(SectionGlobal), IsSingletonSectionString("global"))
	assert.Equal(t, IsNamedSection(SectionBackend), IsNamedSectionString("backend"))
}

func TestGetSectionInfo(t *testing.T) {
	info := GetSectionInfo(SectionUDPLB)
	assert.Equal(t, SectionUDPLB, info.Section)
	assert.True(t, info.IsEnterprise)
	assert.False(t, info.IsSingleton)
	assert.True(t, info.RequiresName)
	assert.Equal(t, "UDP load balancing", info.Description)

	info = GetSectionInfo(SectionWAFGlobal)
	assert.Equal(t, SectionWAFGlobal, info.Section)
	assert.True(t, info.IsEnterprise)
	assert.True(t, info.IsSingleton)
	assert.False(t, info.RequiresName)

	info = GetSectionInfo(SectionGlobal)
	assert.Equal(t, SectionGlobal, info.Section)
	assert.False(t, info.IsEnterprise)
	assert.True(t, info.IsSingleton)
	assert.False(t, info.RequiresName)
}

func TestGetAllSections(t *testing.T) {
	all := GetAllSections()
	ee := GetAllEESections()
	ce := GetAllCESections()

	assert.Equal(t, len(ee)+len(ce), len(all))

	// Verify no duplicates
	seen := make(map[Section]bool)
	for _, s := range all {
		assert.False(t, seen[s], "duplicate section: %s", s)
		seen[s] = true
	}
}

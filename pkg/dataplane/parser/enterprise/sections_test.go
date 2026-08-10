package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAnySectionString(t *testing.T) {
	// EE and CE sections are both recognized
	assert.True(t, IsAnySectionString("udp-lb"))
	assert.True(t, IsAnySectionString("waf-profile"))
	assert.True(t, IsAnySectionString("frontend"))
	assert.True(t, IsAnySectionString("global"))

	assert.False(t, IsAnySectionString("unknown-section"))
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

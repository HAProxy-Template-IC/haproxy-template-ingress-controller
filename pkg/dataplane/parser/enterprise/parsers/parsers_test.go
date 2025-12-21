package parsers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRequests_Parse_EEActions(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		parts       []string
		wantEE      bool
		wantType    string
		wantProfile string
		wantCond    string
		wantErr     bool
	}{
		{
			name:        "waf-evaluate basic",
			line:        "http-request waf-evaluate",
			parts:       []string{"http-request", "waf-evaluate"},
			wantEE:      true,
			wantType:    "waf-evaluate",
			wantProfile: "",
		},
		{
			name:        "waf-evaluate with profile",
			line:        "http-request waf-evaluate profile myprofile",
			parts:       []string{"http-request", "waf-evaluate", "profile", "myprofile"},
			wantEE:      true,
			wantType:    "waf-evaluate",
			wantProfile: "myprofile",
		},
		{
			name:     "waf-evaluate with condition",
			line:     "http-request waf-evaluate if is_api",
			parts:    []string{"http-request", "waf-evaluate", "if", "is_api"},
			wantEE:   true,
			wantType: "waf-evaluate",
			wantCond: "if",
		},
		{
			name:     "botmgmt-evaluate basic",
			line:     "http-request botmgmt-evaluate",
			parts:    []string{"http-request", "botmgmt-evaluate"},
			wantEE:   true,
			wantType: "botmgmt-evaluate",
		},
		{
			name:        "botmgmt-evaluate with profile",
			line:        "http-request botmgmt-evaluate profile bot-protection",
			parts:       []string{"http-request", "botmgmt-evaluate", "profile", "bot-protection"},
			wantEE:      true,
			wantType:    "botmgmt-evaluate",
			wantProfile: "bot-protection",
		},
		{
			name:    "CE action delegated",
			line:    "http-request deny",
			parts:   []string{"http-request", "deny"},
			wantEE:  false, // Should be handled by CE parser
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewHTTPRequests()

			_, err := p.Parse(tt.line, tt.parts, "")

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			eeActions := p.GetEEActions()
			if tt.wantEE {
				require.Len(t, eeActions, 1, "expected 1 EE action")
				assert.Equal(t, tt.wantType, eeActions[0].Type)
				if tt.wantProfile != "" {
					assert.Equal(t, tt.wantProfile, eeActions[0].Profile)
				}
				if tt.wantCond != "" {
					assert.Equal(t, tt.wantCond, eeActions[0].Cond)
				}
			} else {
				assert.Empty(t, eeActions, "expected no EE actions for CE directive")
			}
		})
	}
}

func TestFilters_Parse_EEFilters(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		parts         []string
		wantEE        bool
		wantType      string
		wantRulesFile string
		wantLearning  bool
		wantProfile   string
		wantErr       bool
	}{
		{
			name:     "filter waf basic",
			line:     "filter waf",
			parts:    []string{"filter", "waf"},
			wantEE:   true,
			wantType: "waf",
		},
		{
			name:          "filter waf with rules-file",
			line:          "filter waf rules-file /etc/haproxy/waf.rules",
			parts:         []string{"filter", "waf", "rules-file", "/etc/haproxy/waf.rules"},
			wantEE:        true,
			wantType:      "waf",
			wantRulesFile: "/etc/haproxy/waf.rules",
		},
		{
			name:         "filter waf with learning",
			line:         "filter waf learning",
			parts:        []string{"filter", "waf", "learning"},
			wantEE:       true,
			wantType:     "waf",
			wantLearning: true,
		},
		{
			name:     "filter botmgmt basic",
			line:     "filter botmgmt",
			parts:    []string{"filter", "botmgmt"},
			wantEE:   true,
			wantType: "botmgmt",
		},
		{
			name:        "filter botmgmt with profile",
			line:        "filter botmgmt profile myprofile",
			parts:       []string{"filter", "botmgmt", "profile", "myprofile"},
			wantEE:      true,
			wantType:    "botmgmt",
			wantProfile: "myprofile",
		},
		{
			name:    "CE filter compression",
			line:    "filter compression",
			parts:   []string{"filter", "compression"},
			wantEE:  false, // Should be handled by CE parser
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewFilters()

			_, err := p.Parse(tt.line, tt.parts, "")

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			eeFilters := p.GetEEFilters()
			if tt.wantEE {
				require.Len(t, eeFilters, 1, "expected 1 EE filter")
				assert.Equal(t, tt.wantType, eeFilters[0].Type)
				if tt.wantRulesFile != "" {
					assert.Equal(t, tt.wantRulesFile, eeFilters[0].RulesFile)
				}
				if tt.wantLearning {
					assert.True(t, eeFilters[0].Learning)
				}
				if tt.wantProfile != "" {
					assert.Equal(t, tt.wantProfile, eeFilters[0].Profile)
				}
			} else {
				assert.Empty(t, eeFilters, "expected no EE filters for CE directive")
			}
		})
	}
}

func TestGlobalEE_Parse(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		parts     []string
		wantType  string
		wantParts []string
		wantErr   bool
	}{
		{
			name:      "maxmind-load",
			line:      "maxmind-load GeoIP2-City.mmdb",
			parts:     []string{"maxmind-load", "GeoIP2-City.mmdb"},
			wantType:  "maxmind-load",
			wantParts: []string{"GeoIP2-City.mmdb"},
		},
		{
			name:      "maxmind-update with url",
			line:      "maxmind-update url https://example.com/geodb.mmdb delay 1d",
			parts:     []string{"maxmind-update", "url", "https://example.com/geodb.mmdb", "delay", "1d"},
			wantType:  "maxmind-update",
			wantParts: []string{"url", "https://example.com/geodb.mmdb", "delay", "1d"},
		},
		{
			name:      "waf-load",
			line:      "waf-load /etc/haproxy/waf/core.rules",
			parts:     []string{"waf-load", "/etc/haproxy/waf/core.rules"},
			wantType:  "waf-load",
			wantParts: []string{"/etc/haproxy/waf/core.rules"},
		},
		{
			name:    "non-EE directive",
			line:    "daemon",
			parts:   []string{"daemon"},
			wantErr: true, // Not an EE directive
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewGlobalEE()

			_, err := p.Parse(tt.line, tt.parts, "")

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			directives := p.GetDirectives()
			require.Len(t, directives, 1, "expected 1 directive")
			assert.Equal(t, tt.wantType, directives[0].Type)
			assert.Equal(t, tt.wantParts, directives[0].Parts)
		})
	}
}

func TestFilters_ResultAll(t *testing.T) {
	p := NewFilters()

	// Add an EE filter
	_, err := p.Parse("filter waf learning rules-file /etc/waf.rules", []string{"filter", "waf", "learning", "rules-file", "/etc/waf.rules"}, "test comment")
	require.NoError(t, err)

	results, _, err := p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Check serialized line
	assert.Contains(t, results[0].Data, "filter waf")
	assert.Contains(t, results[0].Data, "learning")
	assert.Contains(t, results[0].Data, "rules-file /etc/waf.rules")
	assert.Equal(t, "test comment", results[0].Comment)
}

func TestHTTPRequests_ResultAll(t *testing.T) {
	p := NewHTTPRequests()

	// Add an EE action
	_, err := p.Parse("http-request waf-evaluate profile myprofile if is_api", []string{"http-request", "waf-evaluate", "profile", "myprofile", "if", "is_api"}, "WAF check")
	require.NoError(t, err)

	results, _, err := p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Check serialized line
	assert.Contains(t, results[0].Data, "http-request waf-evaluate")
	assert.Contains(t, results[0].Data, "profile myprofile")
	assert.Contains(t, results[0].Data, "if is_api")
	assert.Equal(t, "WAF check", results[0].Comment)
}

func TestGlobalEE_GetDirectivesByType(t *testing.T) {
	p := NewGlobalEE()

	// Add multiple directives
	_, _ = p.Parse("maxmind-load db1.mmdb", []string{"maxmind-load", "db1.mmdb"}, "")
	_, _ = p.Parse("maxmind-load db2.mmdb", []string{"maxmind-load", "db2.mmdb"}, "")
	_, _ = p.Parse("waf-load rules.conf", []string{"waf-load", "rules.conf"}, "")

	// Get maxmind-load directives
	maxmindDirs := p.GetDirectivesByType("maxmind-load")
	assert.Len(t, maxmindDirs, 2)

	// Get waf-load directives
	wafDirs := p.GetDirectivesByType("waf-load")
	assert.Len(t, wafDirs, 1)

	// Get non-existent type
	emptyDirs := p.GetDirectivesByType("non-existent")
	assert.Empty(t, emptyDirs)
}

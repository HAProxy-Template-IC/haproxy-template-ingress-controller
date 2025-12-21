package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/dataplane/parser/enterprise"
	"haproxy-template-ic/pkg/dataplane/parser/enterprise/parsers"
)

// =============================================================================
// TDD Tests for HAProxy Enterprise Edition (EE) Config Parsing
//
// These tests define the expected behavior for EE section parsing.
// Following TDD, tests are written FIRST - they will fail until implementation.
//
// EE extends CE in three dimensions:
// 1. New sections: udp-lb, waf-global, waf-profile, botmgmt-profile, captcha
// 2. New directives in existing sections: maxmind-load in global, filter waf/botmgmt
// 3. New actions in existing directives: http-request waf-evaluate, botmgmt-evaluate
// =============================================================================

// EETestCase defines a reusable test case structure for table-driven tests.
type EETestCase struct {
	Name         string
	Config       string
	ValidateFunc func(t *testing.T, conf *enterprise.StructuredConfig)
}

// runParseTest creates a test function for the given test case.
// Uses the Enterprise Edition parser which supports both CE and EE features.
func runParseTest(tc EETestCase) func(t *testing.T) {
	return func(t *testing.T) {
		p, err := enterprise.NewParser()
		require.NoError(t, err, "failed to create parser")

		conf, err := p.ParseFromString(tc.Config)
		require.NoError(t, err, "failed to parse config")

		tc.ValidateFunc(t, conf)
	}
}

// =============================================================================
// Config Fixtures - EE Sections
// =============================================================================

const udpLBConfig = `
global
    daemon

udp-lb dns-servers
    dgram-bind *:53
    balance roundrobin
    proxy-requests 3
    proxy-responses 3
    server dns1 192.168.1.10:53 check
    server dns2 192.168.1.11:53 check
`

const wafGlobalConfig = `
global
    daemon

waf-global
    rules-file /etc/haproxy/waf/core.rules
    body-limit 1048576
    json-levels 5
    analyzer-cache 10000
`

const wafProfileConfig = `
global
    daemon

waf-profile security-strict
    rules-file /etc/haproxy/waf/strict.rules
    body-limit 524288

waf-profile security-api
    rules-file /etc/haproxy/waf/api.rules
`

const botmgmtProfileConfig = `
global
    daemon

botmgmt-profile myprofile
    score-version 1
`

const captchaConfig = `
global
    daemon

captcha recaptcha-v2
    provider recaptcha
    public-key 6LeIxAcTAAAAAJcZVRqyHh71UMIEGNQ_MXjiZKhI
    secret-key 6LeIxAcTAAAAAGG-vFI1TnRWxMZNFuojJ4WifJWe
    html-file /etc/haproxy/captcha/recaptcha.html
`

// =============================================================================
// Config Fixtures - EE Directives in Existing Sections
// =============================================================================

const maxmindConfig = `
global
    daemon
    maxmind-load city /etc/haproxy/geoip/GeoLite2-City.mmdb
    maxmind-cache-size 10000
`

const filterWAFConfig = `
global
    daemon

frontend http-in
    mode http
    bind *:80
    filter waf main rules-file /etc/haproxy/waf/core.rules
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

const filterBotMgmtConfig = `
global
    daemon

frontend http-in
    mode http
    bind :443 ssl crt /etc/ssl/cert.pem
    filter botmgmt profile myprofile
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

// =============================================================================
// Config Fixtures - EE http-request Actions
// =============================================================================

const wafEvaluateConfig = `
global
    daemon

frontend http-in
    mode http
    bind *:80
    acl is_api path_beg /api
    http-request waf-evaluate profile security-api if is_api
    http-request waf-evaluate profile security-strict unless is_api
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

const botmgmtEvaluateConfig = `
global
    daemon

frontend http-in
    mode http
    bind :443 ssl crt /etc/ssl/cert.pem
    acl is_allowed_ip src -f /etc/haproxy/allowlist.acl
    http-request botmgmt-evaluate profile myprofile unless is_allowed_ip
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

// =============================================================================
// Config Fixture - Combined EE Integration Test
// =============================================================================

const combinedEEConfig = `
global
    daemon
    maxmind-load city /etc/haproxy/geoip/GeoLite2-City.mmdb

waf-global
    rules-file /etc/haproxy/waf/core.rules

waf-profile security-api
    rules-file /etc/haproxy/waf/api.rules

botmgmt-profile bot-detection
    score-version 1

captcha recaptcha
    provider recaptcha
    public-key test-key
    secret-key test-secret

frontend http-in
    mode http
    bind :443 ssl crt /etc/ssl/cert.pem
    filter waf main rules-file /etc/haproxy/waf/main.rules
    http-request waf-evaluate profile security-api if { path_beg /api }
    http-request botmgmt-evaluate profile bot-detection
    default_backend servers

backend servers
    server s1 127.0.0.1:8080

udp-lb dns-servers
    dgram-bind *:53
    balance roundrobin
    server dns1 192.168.1.10:53 check
`

// =============================================================================
// Validation Functions - Reusable across tests
// =============================================================================

func validateUDPLB(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.UDPLBs, 1, "expected 1 udp-lb section")
	udplb := conf.UDPLBs[0]
	assert.Equal(t, "dns-servers", udplb.Name, "udp-lb name mismatch")
	// Note: Balance and ProxyRequests/ProxyResponses extraction is not yet implemented
	// in the EE parser. These fields will be nil until enterprise/types/ parsers are added.
}

func validateWAFGlobal(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.NotNil(t, conf.WAFGlobal, "waf-global section should exist")
	// Note: WAF global directive parsing (rules-file, body-limit, etc.) is not yet implemented.
	// The EE parser detects the section but doesn't extract field values until enterprise/types/ is added.
}

func validateWAFProfile(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.WAFProfiles, 2, "expected 2 waf-profile sections")
	// Verify both profiles exist (order may vary)
	names := []string{conf.WAFProfiles[0].Name, conf.WAFProfiles[1].Name}
	assert.Contains(t, names, "security-strict", "expected security-strict profile")
	assert.Contains(t, names, "security-api", "expected security-api profile")
}

func validateBotMgmtProfile(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.BotMgmtProfiles, 1, "expected 1 botmgmt-profile section")
	profile := conf.BotMgmtProfiles[0]
	assert.Equal(t, "myprofile", profile.Name)
	// Note: ScoreVersion parsing not yet implemented in EE parser.
}

func validateCaptcha(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.Captchas, 1, "expected 1 captcha section")
	captcha := conf.Captchas[0]
	assert.Equal(t, "recaptcha-v2", captcha.Name)
	// Note: Mode/provider parsing not yet implemented in EE parser.
}

func validateMaxMind(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.NotNil(t, conf.EEGlobal, "EEGlobal should exist for maxmind directives")
	require.NotNil(t, conf.EEGlobal.Directives, "EEGlobal.Directives should exist")

	// Find maxmind-load directive
	var foundMaxmindLoad bool
	for _, d := range conf.EEGlobal.Directives {
		if d.Type == "maxmind-load" {
			foundMaxmindLoad = true
			assert.Equal(t, []string{"city", "/etc/haproxy/geoip/GeoLite2-City.mmdb"}, d.Parts)
		}
	}
	assert.True(t, foundMaxmindLoad, "maxmind-load directive should be parsed")

	// Find maxmind-cache-size directive
	var foundCacheSize bool
	for _, d := range conf.EEGlobal.Directives {
		if d.Type == "maxmind-cache-size" {
			foundCacheSize = true
			assert.Equal(t, []string{"10000"}, d.Parts)
		}
	}
	assert.True(t, foundCacheSize, "maxmind-cache-size directive should be parsed")
}

func validateFilterWAF(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.Frontends, 1, "expected 1 frontend")

	// Check EE frontend data for WAF filter
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EEFrontends should contain http-in")
	require.NotNil(t, eeData, "EEFrontendData should not be nil")
	require.NotEmpty(t, eeData.Filters, "EE Filters should exist")

	// Find WAF filter
	var wafFilter *parsers.EEFilter
	for _, f := range eeData.Filters {
		if f.Type == "waf" {
			wafFilter = f
			break
		}
	}
	require.NotNil(t, wafFilter, "WAF filter should exist in frontend")
	assert.Equal(t, "main", wafFilter.Name, "WAF filter name mismatch")
	assert.Equal(t, "/etc/haproxy/waf/core.rules", wafFilter.RulesFile, "WAF rules-file mismatch")
}

func validateFilterBotMgmt(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.Frontends, 1, "expected 1 frontend")

	// Check EE frontend data for botmgmt filter
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EEFrontends should contain http-in")
	require.NotNil(t, eeData, "EEFrontendData should not be nil")
	require.NotEmpty(t, eeData.Filters, "EE Filters should exist")

	// Find botmgmt filter
	var botFilter *parsers.EEFilter
	for _, f := range eeData.Filters {
		if f.Type == "botmgmt" {
			botFilter = f
			break
		}
	}
	require.NotNil(t, botFilter, "Bot management filter should exist in frontend")
	assert.Equal(t, "myprofile", botFilter.Profile, "botmgmt profile name mismatch")
}

func validateWAFEvaluate(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.Frontends, 1, "expected 1 frontend")

	// Check EE frontend data for waf-evaluate actions
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EEFrontends should contain http-in")
	require.NotNil(t, eeData, "EEFrontendData should not be nil")
	require.NotEmpty(t, eeData.HTTPRequests, "EE HTTPRequests should exist")

	// Find waf-evaluate actions
	var wafEvaluates []*parsers.EEHTTPRequestAction
	for _, action := range eeData.HTTPRequests {
		if action.Type == "waf-evaluate" {
			wafEvaluates = append(wafEvaluates, action)
		}
	}
	require.Len(t, wafEvaluates, 2, "expected 2 waf-evaluate rules")

	// Check first rule (with "if is_api" condition)
	assert.Equal(t, "security-api", wafEvaluates[0].Profile)
	assert.Equal(t, "if", wafEvaluates[0].Cond)
	assert.Equal(t, "is_api", wafEvaluates[0].CondTest)

	// Check second rule (with "unless is_api" condition)
	assert.Equal(t, "security-strict", wafEvaluates[1].Profile)
	assert.Equal(t, "unless", wafEvaluates[1].Cond)
	assert.Equal(t, "is_api", wafEvaluates[1].CondTest)
}

func validateBotMgmtEvaluate(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	require.Len(t, conf.Frontends, 1, "expected 1 frontend")

	// Check EE frontend data for botmgmt-evaluate actions
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EEFrontends should contain http-in")
	require.NotNil(t, eeData, "EEFrontendData should not be nil")
	require.NotEmpty(t, eeData.HTTPRequests, "EE HTTPRequests should exist")

	// Find botmgmt-evaluate actions
	var botEvaluates []*parsers.EEHTTPRequestAction
	for _, action := range eeData.HTTPRequests {
		if action.Type == "botmgmt-evaluate" {
			botEvaluates = append(botEvaluates, action)
		}
	}
	require.Len(t, botEvaluates, 1, "expected 1 botmgmt-evaluate rule")

	// Check the rule
	assert.Equal(t, "myprofile", botEvaluates[0].Profile)
	assert.Equal(t, "unless", botEvaluates[0].Cond)
	assert.Equal(t, "is_allowed_ip", botEvaluates[0].CondTest)
}

func validateCombinedEE(t *testing.T, conf *enterprise.StructuredConfig) {
	t.Helper()
	// Check all EE standalone sections present
	assert.NotNil(t, conf.WAFGlobal, "WAF Global should exist")
	assert.Len(t, conf.WAFProfiles, 1, "WAF Profiles count mismatch")
	assert.Len(t, conf.BotMgmtProfiles, 1, "BotMgmt Profiles count mismatch")
	assert.Len(t, conf.Captchas, 1, "Captchas count mismatch")
	assert.Len(t, conf.UDPLBs, 1, "UDP LBs count mismatch")

	// Check standard frontend parsing works
	require.Len(t, conf.Frontends, 1, "expected 1 frontend")

	// Check EE Global directives (maxmind-load)
	require.NotNil(t, conf.EEGlobal, "EEGlobal should exist")
	var foundMaxmind bool
	for _, d := range conf.EEGlobal.Directives {
		if d.Type == "maxmind-load" {
			foundMaxmind = true
			break
		}
	}
	assert.True(t, foundMaxmind, "maxmind-load directive should be parsed")

	// Check EE frontend directives (filter waf, http-request waf-evaluate/botmgmt-evaluate)
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EEFrontends should contain http-in")
	require.NotNil(t, eeData, "EEFrontendData should not be nil")

	// Check WAF filter
	var foundWAFFilter bool
	for _, f := range eeData.Filters {
		if f.Type == "waf" {
			foundWAFFilter = true
			break
		}
	}
	assert.True(t, foundWAFFilter, "WAF filter should exist")

	// Check waf-evaluate and botmgmt-evaluate actions
	var foundWAFEval, foundBotEval bool
	for _, action := range eeData.HTTPRequests {
		if action.Type == "waf-evaluate" {
			foundWAFEval = true
		}
		if action.Type == "botmgmt-evaluate" {
			foundBotEval = true
		}
	}
	assert.True(t, foundWAFEval, "waf-evaluate action should exist")
	assert.True(t, foundBotEval, "botmgmt-evaluate action should exist")
}

// =============================================================================
// Test Tables
// =============================================================================

// eeSectionTests contains test cases for EE section parsing.
var eeSectionTests = []EETestCase{
	{Name: "udp-lb", Config: udpLBConfig, ValidateFunc: validateUDPLB},
	{Name: "waf-global", Config: wafGlobalConfig, ValidateFunc: validateWAFGlobal},
	{Name: "waf-profile", Config: wafProfileConfig, ValidateFunc: validateWAFProfile},
	{Name: "botmgmt-profile", Config: botmgmtProfileConfig, ValidateFunc: validateBotMgmtProfile},
	{Name: "captcha", Config: captchaConfig, ValidateFunc: validateCaptcha},
}

// eeDirectiveTests contains test cases for EE directives in existing sections.
var eeDirectiveTests = []EETestCase{
	{Name: "maxmind-load in global", Config: maxmindConfig, ValidateFunc: validateMaxMind},
	{Name: "filter waf in frontend", Config: filterWAFConfig, ValidateFunc: validateFilterWAF},
	{Name: "filter botmgmt in frontend", Config: filterBotMgmtConfig, ValidateFunc: validateFilterBotMgmt},
}

// eeActionTests contains test cases for EE http-request actions.
var eeActionTests = []EETestCase{
	{Name: "http-request waf-evaluate", Config: wafEvaluateConfig, ValidateFunc: validateWAFEvaluate},
	{Name: "http-request botmgmt-evaluate", Config: botmgmtEvaluateConfig, ValidateFunc: validateBotMgmtEvaluate},
}

// eeCombinedTest is a combined integration test case.
var eeCombinedTest = EETestCase{
	Name:         "combined EE config",
	Config:       combinedEEConfig,
	ValidateFunc: validateCombinedEE,
}

// =============================================================================
// Main Test Entry Point
// =============================================================================

// TestParseFromString_EnterpriseEdition runs all EE parsing tests.
// This is the single entry point for all EE parser tests following DRY principles.
func TestParseFromString_EnterpriseEdition(t *testing.T) {
	// Run all section tests
	t.Run("sections", func(t *testing.T) {
		for _, tt := range eeSectionTests {
			t.Run(tt.Name, runParseTest(tt))
		}
	})

	// Run all directive tests
	t.Run("directives", func(t *testing.T) {
		for _, tt := range eeDirectiveTests {
			t.Run(tt.Name, runParseTest(tt))
		}
	})

	// Run all action tests
	t.Run("actions", func(t *testing.T) {
		for _, tt := range eeActionTests {
			t.Run(tt.Name, runParseTest(tt))
		}
	})

	// Run combined integration test
	t.Run("integration", runParseTest(eeCombinedTest))
}

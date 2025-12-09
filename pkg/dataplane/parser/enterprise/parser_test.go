package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// TDD Tests for Enterprise Parser
//
// These tests verify that the enterprise parser:
// 1. Parses CE sections (global, defaults, frontend, backend) correctly
// 2. Parses EE standalone sections (udp-lb, waf-global, waf-profile, etc.)
// 3. Captures EE directives in CE sections (filter waf, http-request waf-evaluate)
// =============================================================================

// --- Test Configs ---

const simpleConfig = `
global
    daemon

defaults
    mode http

frontend http-in
    bind *:80
    default_backend servers

backend servers
    mode http
`

const mixedCEandEEConfig = `
global
    daemon
    maxmind-load city /etc/haproxy/geoip/GeoLite2-City.mmdb

defaults
    mode http

frontend http-in
    bind *:80
    mode http
    filter waf rules-file /etc/haproxy/waf/core.rules
    http-request waf-evaluate profile security-api if { path_beg /api }
    default_backend servers

backend servers
    mode http
    filter botmgmt profile bot-protection
    http-request botmgmt-evaluate profile bot-protection

waf-global
    rules-file /etc/haproxy/waf/core.rules

waf-profile security-api
    rules-file /etc/haproxy/waf/api.rules

botmgmt-profile bot-protection
    score-version 1

captcha recaptcha
    provider recaptcha

udp-lb dns-servers
    balance roundrobin
`

const filterWAFConfig = `
global
    daemon

frontend http-in
    mode http
    bind *:80
    filter waf learning rules-file /etc/haproxy/waf/core.rules
    default_backend servers

backend servers
    mode http
`

const httpRequestWAFConfig = `
global
    daemon

frontend http-in
    mode http
    bind *:80
    http-request waf-evaluate profile security-api if { path_beg /api }
    http-request waf-evaluate profile security-strict
    default_backend servers

backend servers
    mode http
`

const globalEEDirectivesConfig = `
global
    daemon
    maxmind-load city /etc/haproxy/geoip/GeoLite2-City.mmdb
    maxmind-load country /etc/haproxy/geoip/GeoLite2-Country.mmdb
    waf-load /etc/haproxy/waf/core.rules

defaults
    mode http
`

// --- Tests ---

func TestParser_ParseFromString_Empty(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	_, err = p.ParseFromString("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestParser_ParseFromString_SimpleCEConfig(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(simpleConfig)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check CE sections parsed
	require.NotNil(t, conf.Global, "global section should exist")
	assert.True(t, conf.Global.Daemon, "daemon should be enabled")

	require.Len(t, conf.Defaults, 1, "should have 1 defaults section")
	assert.Equal(t, "http", conf.Defaults[0].Mode)

	require.Len(t, conf.Frontends, 1, "should have 1 frontend")
	assert.Equal(t, "http-in", conf.Frontends[0].Name)

	require.Len(t, conf.Backends, 1, "should have 1 backend")
	assert.Equal(t, "servers", conf.Backends[0].Name)
}

func TestParser_ParseFromString_EESections(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(mixedCEandEEConfig)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check EE standalone sections
	require.NotNil(t, conf.WAFGlobal, "waf-global should exist")

	require.Len(t, conf.WAFProfiles, 1, "should have 1 waf-profile")
	assert.Equal(t, "security-api", conf.WAFProfiles[0].Name)

	require.Len(t, conf.BotMgmtProfiles, 1, "should have 1 botmgmt-profile")
	assert.Equal(t, "bot-protection", conf.BotMgmtProfiles[0].Name)

	require.Len(t, conf.Captchas, 1, "should have 1 captcha")
	assert.Equal(t, "recaptcha", conf.Captchas[0].Name)

	require.Len(t, conf.UDPLBs, 1, "should have 1 udp-lb")
	assert.Equal(t, "dns-servers", conf.UDPLBs[0].Name)
}

func TestParser_ParseFromString_FilterWAF(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(filterWAFConfig)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check EE filters captured
	require.Len(t, conf.Frontends, 1, "should have 1 frontend")
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EE frontend data should exist for http-in")
	require.NotNil(t, eeData, "EE frontend data should not be nil")

	require.Len(t, eeData.Filters, 1, "should have 1 EE filter")
	filter := eeData.Filters[0]
	assert.Equal(t, "waf", filter.Type)
	assert.True(t, filter.Learning, "filter should be in learning mode")
	assert.Equal(t, "/etc/haproxy/waf/core.rules", filter.RulesFile)
}

func TestParser_ParseFromString_HTTPRequestWAF(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(httpRequestWAFConfig)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check EE http-request actions captured
	require.Len(t, conf.Frontends, 1, "should have 1 frontend")
	eeData, ok := conf.EEFrontends["http-in"]
	require.True(t, ok, "EE frontend data should exist for http-in")
	require.NotNil(t, eeData, "EE frontend data should not be nil")

	require.Len(t, eeData.HTTPRequests, 2, "should have 2 EE http-request actions")

	// First action with profile and condition
	action1 := eeData.HTTPRequests[0]
	assert.Equal(t, "waf-evaluate", action1.Type)
	assert.Equal(t, "security-api", action1.Profile)
	assert.Equal(t, "if", action1.Cond)

	// Second action with profile only
	action2 := eeData.HTTPRequests[1]
	assert.Equal(t, "waf-evaluate", action2.Type)
	assert.Equal(t, "security-strict", action2.Profile)
}

func TestParser_ParseFromString_GlobalEEDirectives(t *testing.T) {
	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(globalEEDirectivesConfig)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check EE global directives captured
	require.NotNil(t, conf.EEGlobal, "EE global data should exist")
	require.NotNil(t, conf.EEGlobal.Directives, "EE global directives should exist")

	require.Len(t, conf.EEGlobal.Directives, 3, "should have 3 EE global directives")

	// Check maxmind-load directives
	maxmindCount := 0
	for _, d := range conf.EEGlobal.Directives {
		if d.Type == "maxmind-load" {
			maxmindCount++
		}
	}
	assert.Equal(t, 2, maxmindCount, "should have 2 maxmind-load directives")

	// Check waf-load directive
	wafCount := 0
	for _, d := range conf.EEGlobal.Directives {
		if d.Type == "waf-load" {
			wafCount++
		}
	}
	assert.Equal(t, 1, wafCount, "should have 1 waf-load directive")
}

func TestParser_ParseFromString_BackendEEFilters(t *testing.T) {
	config := `
global
    daemon

frontend http-in
    bind *:80
    default_backend servers

backend servers
    mode http
    filter botmgmt profile bot-protection
    http-request botmgmt-evaluate profile bot-protection
`

	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(config)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Check EE filters in backend
	require.Len(t, conf.Backends, 1, "should have 1 backend")
	eeData, ok := conf.EEBackends["servers"]
	require.True(t, ok, "EE backend data should exist for servers")
	require.NotNil(t, eeData, "EE backend data should not be nil")

	require.Len(t, eeData.Filters, 1, "should have 1 EE filter")
	filter := eeData.Filters[0]
	assert.Equal(t, "botmgmt", filter.Type)
	assert.Equal(t, "bot-protection", filter.Profile)

	require.Len(t, eeData.HTTPRequests, 1, "should have 1 EE http-request action")
	action := eeData.HTTPRequests[0]
	assert.Equal(t, "botmgmt-evaluate", action.Type)
	assert.Equal(t, "bot-protection", action.Profile)
}

func TestParser_ParseFromString_CEDirectivesDelegated(t *testing.T) {
	// Verify that CE directives are properly delegated to client-native parsers
	config := `
global
    daemon

frontend http-in
    mode http
    bind *:80
    filter compression
    default_backend servers

backend servers
    mode http
    balance roundrobin
`

	p, err := NewParser()
	require.NoError(t, err)

	conf, err := p.ParseFromString(config)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// CE directives should be parsed
	require.Len(t, conf.Frontends, 1)
	assert.Equal(t, "http", conf.Frontends[0].Mode)

	require.Len(t, conf.Backends, 1)
	assert.Equal(t, "http", conf.Backends[0].Mode)

	// No EE data for CE-only config
	_, hasEEFrontend := conf.EEFrontends["http-in"]
	assert.False(t, hasEEFrontend, "should not have EE frontend data for CE-only config")

	_, hasEEBackend := conf.EEBackends["servers"]
	assert.False(t, hasEEBackend, "should not have EE backend data for CE-only config")
}

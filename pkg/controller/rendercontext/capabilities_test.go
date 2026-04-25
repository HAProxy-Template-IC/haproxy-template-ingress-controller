// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package rendercontext

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// CapabilitiesToMap converts the typed Capabilities struct into the
// snake_case-keyed map that templates use to gate version-specific
// features. The tests below pin every contract:
//   - nil input yields an empty (non-nil) map (templates iterate
//     `capabilities[...]` so nil would NPE)
//   - every documented snake_case key is present
//   - each Capability bool flows through verbatim (no inversion, no
//     additional logic except is_enterprise)
//   - is_enterprise is derived from SupportsWAF (any enterprise
//     capability indicates Enterprise edition)

func TestCapabilitiesToMap_NilInput(t *testing.T) {
	got := CapabilitiesToMap(nil)
	assert.NotNil(t, got, "nil input must produce empty map (templates iterate 'capabilities')")
	assert.Empty(t, got)
}

func TestCapabilitiesToMap_AllZeroHasEveryKey(t *testing.T) {
	got := CapabilitiesToMap(&dataplane.Capabilities{})

	// Every documented key must be present; missing keys would surface
	// as nil in templates and fail truthiness checks.
	expectedKeys := []string{
		"supports_crt_list", "supports_map_storage", "supports_general_storage",
		"supports_http2", "supports_quic",
		"supports_runtime_maps", "supports_runtime_servers",
		"supports_waf", "supports_waf_global", "supports_waf_profiles",
		"supports_udp_lb_acls", "supports_udp_lb_server_switching",
		"supports_keepalived", "supports_udp_load_balancing",
		"supports_bot_management", "supports_git_integration",
		"supports_dynamic_update", "supports_aloha", "supports_advanced_logging",
		"supports_ping",
		"is_enterprise",
	}
	for _, key := range expectedKeys {
		val, ok := got[key]
		if assert.True(t, ok, "key %q must be present in CapabilitiesToMap output", key) {
			assert.Equal(t, false, val, "all-zero Capabilities must produce false for %q", key)
		}
	}
}

func TestCapabilitiesToMap_PerFieldFlowsThroughVerbatim(t *testing.T) {
	// Set ONE field at a time and confirm only the matching key flips.
	// This catches any accidental cross-wiring (e.g. supports_waf_global
	// pointing at SupportsWAFProfiles).
	tests := []struct {
		name    string
		set     func(*dataplane.Capabilities)
		wantKey string
	}{
		{name: "SupportsCrtList -> supports_crt_list", set: func(c *dataplane.Capabilities) { c.SupportsCrtList = true }, wantKey: "supports_crt_list"},
		{name: "SupportsMapStorage -> supports_map_storage", set: func(c *dataplane.Capabilities) { c.SupportsMapStorage = true }, wantKey: "supports_map_storage"},
		{name: "SupportsGeneralStorage -> supports_general_storage", set: func(c *dataplane.Capabilities) { c.SupportsGeneralStorage = true }, wantKey: "supports_general_storage"},
		{name: "SupportsHTTP2 -> supports_http2", set: func(c *dataplane.Capabilities) { c.SupportsHTTP2 = true }, wantKey: "supports_http2"},
		{name: "SupportsQUIC -> supports_quic", set: func(c *dataplane.Capabilities) { c.SupportsQUIC = true }, wantKey: "supports_quic"},
		{name: "SupportsRuntimeMaps -> supports_runtime_maps", set: func(c *dataplane.Capabilities) { c.SupportsRuntimeMaps = true }, wantKey: "supports_runtime_maps"},
		{name: "SupportsRuntimeServers -> supports_runtime_servers", set: func(c *dataplane.Capabilities) { c.SupportsRuntimeServers = true }, wantKey: "supports_runtime_servers"},
		{name: "SupportsWAFGlobal -> supports_waf_global", set: func(c *dataplane.Capabilities) { c.SupportsWAFGlobal = true }, wantKey: "supports_waf_global"},
		{name: "SupportsWAFProfiles -> supports_waf_profiles", set: func(c *dataplane.Capabilities) { c.SupportsWAFProfiles = true }, wantKey: "supports_waf_profiles"},
		{name: "SupportsUDPLBACLs -> supports_udp_lb_acls", set: func(c *dataplane.Capabilities) { c.SupportsUDPLBACLs = true }, wantKey: "supports_udp_lb_acls"},
		{name: "SupportsUDPLBServerSwitchingRules -> supports_udp_lb_server_switching", set: func(c *dataplane.Capabilities) { c.SupportsUDPLBServerSwitchingRules = true }, wantKey: "supports_udp_lb_server_switching"},
		{name: "SupportsKeepalived -> supports_keepalived", set: func(c *dataplane.Capabilities) { c.SupportsKeepalived = true }, wantKey: "supports_keepalived"},
		{name: "SupportsUDPLoadBalancing -> supports_udp_load_balancing", set: func(c *dataplane.Capabilities) { c.SupportsUDPLoadBalancing = true }, wantKey: "supports_udp_load_balancing"},
		{name: "SupportsBotManagement -> supports_bot_management", set: func(c *dataplane.Capabilities) { c.SupportsBotManagement = true }, wantKey: "supports_bot_management"},
		{name: "SupportsGitIntegration -> supports_git_integration", set: func(c *dataplane.Capabilities) { c.SupportsGitIntegration = true }, wantKey: "supports_git_integration"},
		{name: "SupportsDynamicUpdate -> supports_dynamic_update", set: func(c *dataplane.Capabilities) { c.SupportsDynamicUpdate = true }, wantKey: "supports_dynamic_update"},
		{name: "SupportsALOHA -> supports_aloha", set: func(c *dataplane.Capabilities) { c.SupportsALOHA = true }, wantKey: "supports_aloha"},
		{name: "SupportsAdvancedLogging -> supports_advanced_logging", set: func(c *dataplane.Capabilities) { c.SupportsAdvancedLogging = true }, wantKey: "supports_advanced_logging"},
		{name: "SupportsPing -> supports_ping", set: func(c *dataplane.Capabilities) { c.SupportsPing = true }, wantKey: "supports_ping"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := &dataplane.Capabilities{}
			tt.set(caps)
			got := CapabilitiesToMap(caps)
			assert.Equal(t, true, got[tt.wantKey], "%s must light up after setting the matching field", tt.wantKey)
		})
	}
}

func TestCapabilitiesToMap_SupportsWAFFeedsIsEnterprise(t *testing.T) {
	// is_enterprise is the documented convenience flag; it's derived
	// from SupportsWAF because any EE capability implies Enterprise
	// edition. Pin both sides flip together.
	caps := &dataplane.Capabilities{SupportsWAF: true}
	got := CapabilitiesToMap(caps)
	assert.Equal(t, true, got["supports_waf"])
	assert.Equal(t, true, got["is_enterprise"], "is_enterprise must derive from SupportsWAF")
}

func TestCapabilitiesToMap_NonWAFEECapabilityDoesNotSetIsEnterprise(t *testing.T) {
	// Pin the documented derivation: is_enterprise tracks SupportsWAF
	// specifically. A future refactor that broadened this (e.g. to
	// SupportsKeepalived || SupportsBotManagement) would need to
	// update this test too.
	caps := &dataplane.Capabilities{SupportsBotManagement: true, SupportsKeepalived: true}
	got := CapabilitiesToMap(caps)
	assert.Equal(t, false, got["is_enterprise"],
		"is_enterprise is derived ONLY from SupportsWAF, not other EE-only flags")
}

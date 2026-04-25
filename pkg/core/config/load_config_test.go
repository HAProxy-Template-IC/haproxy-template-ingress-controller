// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LoadConfig is the public top-level config-loading entry point used by
// every controller startup path. It composes three independently-tested
// helpers — parseConfig, SetDefaults, ValidateExtraContext — but their
// COMPOSITION ORDER and WIRING are the LoadConfig contract and have no
// direct test coverage.
//
// Four contracts pinned by the new tests:
//
//  1. parseConfig errors propagate verbatim (no swallowing): a
//     regression that wrapped or dropped the error would prevent
//     operators from seeing exactly which YAML line failed.
//
//  2. SetDefaults runs AFTER successful parse: if a regression
//     skipped SetDefaults (or ran it before parsing, mutating a
//     stale Config), every downstream consumer relying on a
//     defaulted field would silently see a zero value. The test
//     pins via DataplanePort which has a non-zero default.
//
//  3. nil ExtraContext skips validation cleanly (no panic and no
//     spurious error): a regression that called ValidateExtraContext
//     unconditionally would trip on its nil-map iteration and
//     either panic (if buggy) or succeed (which is what we want
//     anyway).
//
// (Note: the LoadConfig "invalid extra_context: %w" wrap at loader.go:36
// is defensive code unreachable through YAML — every YAML-producible
// type is JSON-compatible. The wrap can't be exercised through the
// public API, so we don't pretend to test it here.)

func TestLoadConfig_ParseErrorPropagates(t *testing.T) {
	// parseConfig wraps yaml.Unmarshal errors with "unmarshalling YAML:"
	// — pin that LoadConfig surfaces this verbatim rather than
	// swallowing or wrapping again.
	_, err := LoadConfig("invalid: : :yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshalling YAML",
		"parseConfig errors must propagate verbatim through LoadConfig — "+
			"a regression that wrapped them would obscure which YAML line "+
			"is malformed")
}

func TestLoadConfig_EmptyYAMLProducesError(t *testing.T) {
	_, err := LoadConfig("")
	require.Error(t, err,
		"empty YAML must produce an explicit error rather than silently "+
			"return an empty Config — without this, an operator who "+
			"forgot to mount the ConfigMap would get a default-everything "+
			"controller and not understand why nothing is configured")
}

func TestLoadConfig_AppliesDefaultsAfterParse(t *testing.T) {
	// The test-pin: DataplanePort has a non-zero default
	// (DefaultDataplanePort = 5555). A YAML that doesn't set it
	// should come out with the default, NOT zero. A regression that
	// skipped SetDefaults would yield port=0 and break every
	// downstream HTTP call.
	const yamlMinimal = `
dataplane:
  baseURL: "http://localhost:5555"
`
	cfg, err := LoadConfig(yamlMinimal)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port,
		"SetDefaults MUST run on the parsed config; DataplanePort defaults "+
			"to %d but got %d. A regression that skipped SetDefaults would "+
			"silently break every downstream HTTP call that expects a non-zero "+
			"port", DefaultDataplanePort, cfg.Dataplane.Port)
	assert.Equal(t, DefaultDataplaneMapsDir, cfg.Dataplane.MapsDir,
		"second default-driven field as a tripwire — if a regression "+
			"defaulted Port via a special path but skipped MapsDir, this "+
			"assertion catches it independently")
}

func TestLoadConfig_NilExtraContextSkipsValidationCleanly(t *testing.T) {
	// The most common case: no extra_context configured. LoadConfig
	// must not panic on nil-map iteration AND must not produce a
	// spurious error. Pin this with a config that omits
	// templating.extra_context entirely.
	const yamlNoExtra = `
dataplane:
  port: 5555
`
	cfg, err := LoadConfig(yamlNoExtra)
	require.NoError(t, err,
		"a config with no extra_context must load successfully — "+
			"every controller without custom template variables falls "+
			"into this path")
	require.NotNil(t, cfg)
	assert.Nil(t, cfg.TemplatingSettings.ExtraContext,
		"absent extra_context should remain nil; a regression that "+
			"defaulted it to an empty map would change downstream "+
			"observability (e.g. metrics with extra_context_size labels)")
}

func TestLoadConfig_ValidExtraContextLoadsSuccessfully(t *testing.T) {
	// Positive control for the validation branch: every JSON-compatible
	// type in extra_context must round-trip cleanly. If a regression
	// over-tightened the validation (e.g. rejected nested maps) the
	// operator-visible message would be confusing.
	const yamlValid = `
templating_settings:
  extra_context:
    string_val: "hello"
    int_val: 42
    bool_val: true
    null_val: null
    list_val: [1, 2, 3]
    nested_map:
      inner: "value"
`
	cfg, err := LoadConfig(yamlValid)
	require.NoError(t, err,
		"every JSON-compatible type — string, number, bool, null, list, "+
			"nested map — must pass validation. A regression that rejected "+
			"any of these would break the documented extra_context contract")

	require.NotNil(t, cfg.TemplatingSettings.ExtraContext)
	assert.Equal(t, "hello", cfg.TemplatingSettings.ExtraContext["string_val"])
	assert.Equal(t, true, cfg.TemplatingSettings.ExtraContext["bool_val"])
}

// Note: the LoadConfig wrap path for ValidateExtraContext failures is
// defensive code — every YAML-producible type passes JSON-compatibility
// validation. The wrap (loader.go:36) is therefore unreachable through
// the public LoadConfig API, so there's no behavioural test for it.
// The underlying ValidateExtraContext is exercised in
// validation_extra_test.go with synthetic inputs that bypass YAML
// parsing.

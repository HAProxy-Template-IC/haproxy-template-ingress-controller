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
	"time"

	"github.com/stretchr/testify/assert"
)

// parseDurationOr is the shared helper that every "Get<Duration>" accessor in
// defaults.go funnels through. The empty/parse-error/valid branches are the
// contract callers depend on for the "missing or invalid value falls back to
// the documented default" behaviour, so pin it directly.
func TestParseDurationOr(t *testing.T) {
	const fallback = 7 * time.Second

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "empty string falls back", value: "", want: fallback},
		{name: "valid duration is parsed", value: "30s", want: 30 * time.Second},
		{name: "valid sub-second duration", value: "250ms", want: 250 * time.Millisecond},
		{name: "valid compound duration", value: "1h30m", want: 90 * time.Minute},
		{name: "zero duration is honored, not treated as missing", value: "0s", want: 0},
		{name: "invalid duration falls back without error", value: "not-a-duration", want: fallback},
		{name: "trailing garbage is rejected and falls back", value: "30sX", want: fallback},
		{name: "missing unit is rejected and falls back", value: "30", want: fallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDurationOr(tt.value, fallback)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDataplaneConfig_Getters_Defaults pins the previously-uncovered
// GetDeploymentTimeout / GetConfigPublishInterval accessors and confirms
// every Get* duration accessor on DataplaneConfig and LeaderElectionConfig
// falls back to the documented default when its field is empty.
//
// Each row is the zero-value struct → expected default; keeping these in a
// table form a tripwire for accidentally changing any default.
func TestDataplaneConfig_Getters_Defaults(t *testing.T) {
	cfg := &DataplaneConfig{}

	assert.Equal(t, DefaultMinDeploymentInterval, cfg.GetMinDeploymentInterval(),
		"empty MinDeploymentInterval falls back to DefaultMinDeploymentInterval")
	assert.Equal(t, DefaultDriftPreventionInterval, cfg.GetDriftPreventionInterval(),
		"empty DriftPreventionInterval falls back to DefaultDriftPreventionInterval")
	assert.Equal(t, DefaultDeploymentTimeout, cfg.GetDeploymentTimeout(),
		"empty DeploymentTimeout falls back to DefaultDeploymentTimeout")
	assert.Equal(t, DefaultConfigPublishInterval, cfg.GetConfigPublishInterval(),
		"empty ConfigPublishInterval falls back to DefaultConfigPublishInterval")
}

// TestDataplaneConfig_Getters_Overrides pins that valid duration strings on
// every getter override the default, and that invalid strings fall back
// through parseDurationOr instead of crashing.
func TestDataplaneConfig_Getters_Overrides(t *testing.T) {
	cfg := &DataplaneConfig{
		MinDeploymentInterval:   "11s",
		DriftPreventionInterval: "12m",
		DeploymentTimeout:       "13s",
		ConfigPublishInterval:   "14s",
	}

	assert.Equal(t, 11*time.Second, cfg.GetMinDeploymentInterval())
	assert.Equal(t, 12*time.Minute, cfg.GetDriftPreventionInterval())
	assert.Equal(t, 13*time.Second, cfg.GetDeploymentTimeout())
	assert.Equal(t, 14*time.Second, cfg.GetConfigPublishInterval())

	bad := &DataplaneConfig{
		MinDeploymentInterval:   "garbage",
		DriftPreventionInterval: "garbage",
		DeploymentTimeout:       "garbage",
		ConfigPublishInterval:   "garbage",
	}

	assert.Equal(t, DefaultMinDeploymentInterval, bad.GetMinDeploymentInterval(),
		"invalid MinDeploymentInterval falls back to DefaultMinDeploymentInterval")
	assert.Equal(t, DefaultDriftPreventionInterval, bad.GetDriftPreventionInterval(),
		"invalid DriftPreventionInterval falls back to DefaultDriftPreventionInterval")
	assert.Equal(t, DefaultDeploymentTimeout, bad.GetDeploymentTimeout(),
		"invalid DeploymentTimeout falls back to DefaultDeploymentTimeout")
	assert.Equal(t, DefaultConfigPublishInterval, bad.GetConfigPublishInterval(),
		"invalid ConfigPublishInterval falls back to DefaultConfigPublishInterval")
}

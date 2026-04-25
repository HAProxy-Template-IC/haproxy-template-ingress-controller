// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package lifecycle

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOption_LeaderOnly(t *testing.T) {
	cfg := registrationConfig{}
	LeaderOnly()(&cfg)

	assert.True(t, cfg.leaderOnly, "LeaderOnly must set leaderOnly=true")

	// Re-applying must remain idempotent (no toggle).
	LeaderOnly()(&cfg)
	assert.True(t, cfg.leaderOnly, "applying LeaderOnly twice must remain true")
}

func TestOption_DependsOn(t *testing.T) {
	tests := []struct {
		name    string
		initial []string
		add     [][]string
		want    []string
	}{
		{
			name:    "single call records all names in order",
			initial: nil,
			add:     [][]string{{"renderer", "validator"}},
			want:    []string{"renderer", "validator"},
		},
		{
			name:    "multiple calls accumulate (Option is appendish)",
			initial: nil,
			add:     [][]string{{"renderer"}, {"validator", "executor"}},
			want:    []string{"renderer", "validator", "executor"},
		},
		{
			name:    "no names is a no-op",
			initial: []string{"existing"},
			add:     [][]string{nil, {}},
			want:    []string{"existing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := registrationConfig{dependencies: tt.initial}
			for _, names := range tt.add {
				DependsOn(names...)(&cfg)
			}
			assert.Equal(t, tt.want, cfg.dependencies)
		})
	}
}

func TestOption_Criticality(t *testing.T) {
	tests := []struct {
		name  string
		level CriticalityLevel
	}{
		{name: "critical", level: CriticalityCritical},
		{name: "degradable", level: CriticalityDegradable},
		{name: "optional", level: CriticalityOptional},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := registrationConfig{}
			Criticality(tt.level)(&cfg)
			assert.Equal(t, tt.level, cfg.criticality)
		})
	}

	t.Run("later call overrides earlier call", func(t *testing.T) {
		cfg := registrationConfig{}
		Criticality(CriticalityCritical)(&cfg)
		Criticality(CriticalityOptional)(&cfg)
		assert.Equal(t, CriticalityOptional, cfg.criticality)
	})
}

func TestOption_OnError(t *testing.T) {
	cfg := registrationConfig{}
	assert.Nil(t, cfg.onError, "onError starts unset")

	var capturedName string
	var capturedErr error
	OnError(func(name string, err error) {
		capturedName = name
		capturedErr = err
	})(&cfg)

	if assert.NotNil(t, cfg.onError, "OnError must install the handler") {
		want := errors.New("boom")
		cfg.onError("renderer", want)
		assert.Equal(t, "renderer", capturedName)
		assert.Same(t, want, capturedErr)
	}
}

func TestOptions_Compose(t *testing.T) {
	// Real Register applies a Critical default before opts; mirror that here so
	// the test pins the realistic call sequence.
	cfg := registrationConfig{criticality: CriticalityCritical}

	for _, opt := range []Option{
		LeaderOnly(),
		DependsOn("validator"),
		DependsOn("renderer"),
		Criticality(CriticalityDegradable),
		OnError(func(string, error) {}),
	} {
		opt(&cfg)
	}

	assert.True(t, cfg.leaderOnly)
	assert.Equal(t, []string{"validator", "renderer"}, cfg.dependencies)
	assert.Equal(t, CriticalityDegradable, cfg.criticality)
	assert.NotNil(t, cfg.onError)
}

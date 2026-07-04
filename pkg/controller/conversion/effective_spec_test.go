// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package conversion

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

func specForEffectiveTest() *v1alpha1.HAProxyTemplateConfigSpec {
	return &v1alpha1.HAProxyTemplateConfigSpec{
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"tcproutes": {
				APIVersions: []string{"example.io/v1", "example.io/v1beta1"},
				Resources:   "tcproutes",
				Optional:    true,
			},
			"udproutes": {
				APIVersions: []string{"example.io/v1"},
				Resources:   "udproutes",
				Optional:    true,
			},
			"services": {
				APIVersion: "v1",
				Resources:  "services",
			},
		},
		TemplateSnippets: map[string]v1alpha1.TemplateSnippet{
			"shared":      {Template: "x"},
			"tcp-feature": {Template: "x", Requires: []string{"tcproutes"}},
			"udp-feature": {Template: "x", Requires: []string{"udproutes"}},
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"test-shared": {},
			"test-udp":    {Requires: []string{"udproutes"}},
		},
	}
}

func TestResolveEffectiveSpec_WithAvailabilitySignal(t *testing.T) {
	// Serves only example.io/v1beta1 tcproutes (v1 exists nowhere).
	served := func(apiVersion, resources string) bool {
		return apiVersion == "example.io/v1beta1" && resources == "tcproutes"
	}

	spec := specForEffectiveTest()
	ResolveEffectiveSpec(spec, served, slog.Default())

	// tcproutes resolves to the served v1beta1 candidate.
	assert.Equal(t, "example.io/v1beta1", spec.WatchedResources["tcproutes"].APIVersion)
	assert.Empty(t, spec.WatchedResources["tcproutes"].APIVersions)
	assert.Contains(t, spec.TemplateSnippets, "tcp-feature")

	// udproutes has no served candidate and is optional → stripped with its feature.
	assert.NotContains(t, spec.WatchedResources, "udproutes")
	assert.NotContains(t, spec.TemplateSnippets, "udp-feature")
	assert.NotContains(t, spec.ValidationTests, "test-udp")
	assert.Contains(t, spec.ValidationTests, "test-shared")

	// services is required but unserved → lenient first-candidate fallback.
	assert.Equal(t, "v1", spec.WatchedResources["services"].APIVersion)
}

func TestResolveEffectiveSpec_WithoutAvailabilitySignal(t *testing.T) {
	spec := specForEffectiveTest()
	ResolveEffectiveSpec(spec, nil, slog.Default())

	// No availability signal: every candidate list collapses to its first
	// entry and nothing is stripped.
	assert.Equal(t, "example.io/v1", spec.WatchedResources["tcproutes"].APIVersion)
	assert.Equal(t, "example.io/v1", spec.WatchedResources["udproutes"].APIVersion)
	assert.Contains(t, spec.TemplateSnippets, "tcp-feature")
	assert.Contains(t, spec.TemplateSnippets, "udp-feature")
	assert.Contains(t, spec.ValidationTests, "test-udp")
}

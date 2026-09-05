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
	"github.com/stretchr/testify/require"

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
	res, err := ResolveEffectiveSpec(spec, served, nil, slog.Default())
	require.NoError(t, err)

	// tcproutes resolves to the served v1beta1 candidate.
	assert.Equal(t, "example.io/v1beta1", spec.WatchedResources["tcproutes"].APIVersion)
	assert.Empty(t, spec.WatchedResources["tcproutes"].APIVersions)
	assert.Contains(t, spec.TemplateSnippets, "tcp-feature")

	// udproutes has no served candidate and is optional → stripped with its feature.
	assert.NotContains(t, spec.WatchedResources, "udproutes")
	assert.NotContains(t, spec.TemplateSnippets, "udp-feature")
	assert.NotContains(t, spec.ValidationTests, "test-udp")
	assert.Contains(t, spec.ValidationTests, "test-shared")
	assert.Contains(t, res.StrippedTests, "test-udp")
	assert.Contains(t, res.StrippedTests["test-udp"], "udproutes")

	// services is required but unserved → lenient first-candidate fallback.
	assert.Equal(t, "v1", spec.WatchedResources["services"].APIVersion)
}

func TestResolveEffectiveSpec_WithoutAvailabilitySignal(t *testing.T) {
	spec := specForEffectiveTest()
	res, err := ResolveEffectiveSpec(spec, nil, nil, slog.Default())
	require.NoError(t, err)

	// No availability signal: every candidate list collapses to its first
	// entry and nothing is stripped.
	assert.Equal(t, "example.io/v1", spec.WatchedResources["tcproutes"].APIVersion)
	assert.Equal(t, "example.io/v1", spec.WatchedResources["udproutes"].APIVersion)
	assert.Contains(t, spec.TemplateSnippets, "tcp-feature")
	assert.Contains(t, spec.TemplateSnippets, "udp-feature")
	assert.Contains(t, spec.ValidationTests, "test-udp")
	assert.Empty(t, res.StrippedTests)
}

func TestResolveEffectiveSpecAuthenticatesStrippedIncrementalGroup(t *testing.T) {
	spec := specForEffectiveTest()
	policy := spec.TemplateSnippets["udp-feature"]
	policy.Incremental = &v1alpha1.IncrementalTemplate{
		BindingsTemplate: "{}",
		Group:            "udp-policies",
		Effects:          []v1alpha1.IncrementalEffect{v1alpha1.IncrementalEffectPublishValue},
	}
	spec.TemplateSnippets["udp-feature"] = policy
	consumer := spec.TemplateSnippets["tcp-feature"]
	consumer.Incremental = &v1alpha1.IncrementalTemplate{
		BindingsTemplate: "{}",
		Group:            "tcp-routes",
		OptionalConsumes: []string{"udp-policies"},
	}
	spec.TemplateSnippets["tcp-feature"] = consumer

	_, err := ResolveEffectiveSpec(spec, func(_, resources string) bool {
		return resources == "tcproutes"
	}, nil, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, []string{"udp-policies"}, spec.AbsentIncrementalGroups)

	converted, err := ConvertSpec(spec)
	require.NoError(t, err)
	assert.Equal(t, map[string]struct{}{"udp-policies": {}}, converted.AbsentIncrementalGroups)
}

// TestResolveEffectiveSpec_RequiresFields pins the offline mirror of the
// field-level stripping: a validationTest whose requiresFields names a field
// the schema-dir's resolved generation lacks is stripped and reported with a
// field-specific reason, while tests on present fields (or on resources whose
// schema isn't bundled — leniency delegated to the callback) survive.
func TestResolveEffectiveSpec_RequiresFields(t *testing.T) {
	baseSpec := func() *v1alpha1.HAProxyTemplateConfigSpec {
		return &v1alpha1.HAProxyTemplateConfigSpec{
			WatchedResources: map[string]v1alpha1.WatchedResource{
				"httproutes": {APIVersion: "example.io/v1", Resources: "httproutes"},
				"tcproutes":  {APIVersion: "example.io/v1", Resources: "tcproutes", Optional: true},
			},
			ValidationTests: map[string]v1alpha1.ValidationTest{
				"test-plain":          {},
				"test-cors":           {RequiresFields: []string{"httproutes.spec.rules.filters.cors"}},
				"test-present":        {RequiresFields: []string{"httproutes.spec.rules"}},
				"test-on-unavailable": {RequiresFields: []string{"tcproutes.spec.rules"}},
			},
		}
	}
	served := func(_, resources string) bool { return resources == "httproutes" }

	t.Run("absent field strips with a field reason", func(t *testing.T) {
		fieldServed := func(_, _, fieldPath string) (bool, error) {
			return fieldPath == "spec.rules", nil
		}
		spec := baseSpec()
		res, err := ResolveEffectiveSpec(spec, served, fieldServed, slog.Default())
		require.NoError(t, err)

		assert.NotContains(t, spec.ValidationTests, "test-cors")
		assert.Contains(t, spec.ValidationTests, "test-present")
		assert.Contains(t, spec.ValidationTests, "test-plain")
		assert.NotContains(t, spec.ValidationTests, "test-on-unavailable",
			"a field on an unavailable optional resource is trivially absent")
		assert.Contains(t, res.StrippedTests["test-cors"], "field")
		assert.Contains(t, res.StrippedTests["test-cors"], "httproutes.spec.rules.filters.cors")
	})

	t.Run("callback error fails the resolution instead of stripping", func(t *testing.T) {
		fieldServed := func(_, _, _ string) (bool, error) { return false, assert.AnError }
		_, err := ResolveEffectiveSpec(baseSpec(), served, fieldServed, slog.Default())
		require.Error(t, err)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("nil fieldServed skips field checks", func(t *testing.T) {
		spec := baseSpec()
		res, err := ResolveEffectiveSpec(spec, served, nil, slog.Default())
		require.NoError(t, err)
		assert.Contains(t, spec.ValidationTests, "test-cors")
		assert.NotContains(t, res.StrippedTests, "test-cors")
	})
}

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

// servedSet is a ServedVersionChecker backed by a fixed set of
// "apiVersion|resources" keys.
type servedSet map[string]bool

func (s servedSet) IsServed(apiVersion, resources string) bool {
	return s[apiVersion+"|"+resources]
}

func TestResolveEffective(t *testing.T) {
	cfg := &Config{
		WatchedResources: map[string]WatchedResource{
			"httproutes": {
				APIVersions: []string{"example.io/v1", "example.io/v1beta1"},
				Resources:   "httproutes",
				IndexBy:     []string{"metadata.name"},
			},
			"tcproutes": {
				APIVersions: []string{"example.io/v1", "example.io/v1alpha2"},
				Resources:   "tcproutes",
				Optional:    true,
				IndexBy:     []string{"metadata.name"},
			},
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.name"},
			},
		},
		TemplateSnippets: map[string]TemplateSnippet{
			"shared":      {Name: "shared", Template: "x"},
			"tcp-feature": {Name: "tcp-feature", Template: "x", Requires: []string{"tcproutes"}},
			"http-only":   {Name: "http-only", Template: "x", Requires: []string{"httproutes"}},
		},
		ValidationTests: map[string]ValidationTest{
			"test-shared": {},
			"test-tcp":    {Requires: []string{"tcproutes"}},
		},
	}

	t.Run("resolves first served candidate and strips unavailable optional features", func(t *testing.T) {
		served := servedSet{
			"example.io/v1beta1|httproutes": true, // v1 NOT served -> falls back
			"v1|services":                   true,
			// tcproutes served at no candidate
		}

		effective, res, err := ResolveEffective(cfg, served)
		require.NoError(t, err)

		assert.Equal(t, "example.io/v1beta1", effective.WatchedResources["httproutes"].APIVersion)
		assert.Empty(t, effective.WatchedResources["httproutes"].APIVersions,
			"resolved entries must clear the candidate list so downstream consumers see one version")
		assert.Equal(t, "v1", effective.WatchedResources["services"].APIVersion)
		assert.NotContains(t, effective.WatchedResources, "tcproutes")

		assert.NotContains(t, effective.TemplateSnippets, "tcp-feature")
		assert.Contains(t, effective.TemplateSnippets, "shared")
		assert.Contains(t, effective.TemplateSnippets, "http-only",
			"requires on an AVAILABLE resource must not strip")
		assert.NotContains(t, effective.ValidationTests, "test-tcp")
		assert.Contains(t, effective.ValidationTests, "test-shared")

		assert.Equal(t, map[string]string{
			"httproutes": "example.io/v1beta1",
			"services":   "v1",
		}, res.ResolvedVersions)
		assert.Equal(t, []string{"tcproutes"}, res.Unavailable)
		assert.Equal(t, []string{"tcp-feature"}, res.StrippedSnippets)
		assert.Equal(t, []string{"test-tcp"}, res.StrippedTests)

		// Input config untouched.
		assert.Contains(t, cfg.WatchedResources, "tcproutes")
		assert.Contains(t, cfg.TemplateSnippets, "tcp-feature")
		assert.NotEmpty(t, cfg.WatchedResources["httproutes"].APIVersions)
	})

	t.Run("preference order wins when several candidates are served", func(t *testing.T) {
		served := servedSet{
			"example.io/v1|httproutes":      true,
			"example.io/v1beta1|httproutes": true,
			"example.io/v1|tcproutes":       true,
			"example.io/v1alpha2|tcproutes": true,
			"v1|services":                   true,
		}

		effective, res, err := ResolveEffective(cfg, served)
		require.NoError(t, err)
		assert.Equal(t, "example.io/v1", effective.WatchedResources["httproutes"].APIVersion)
		assert.Equal(t, "example.io/v1", effective.WatchedResources["tcproutes"].APIVersion)
		assert.Empty(t, res.Unavailable)
		assert.Contains(t, effective.TemplateSnippets, "tcp-feature")
		assert.Contains(t, effective.ValidationTests, "test-tcp")
	})

	t.Run("required resource with no served candidate fails fast with a named error", func(t *testing.T) {
		served := servedSet{
			"example.io/v1beta1|httproutes": true,
			// services (required) not served
		}

		_, _, err := ResolveEffective(cfg, served)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"services"`)
		assert.Contains(t, err.Error(), "required but no candidate version is served")
		assert.Contains(t, err.Error(), "[v1]")
	})

	t.Run("no unavailable resources leaves snippet and test maps untouched", func(t *testing.T) {
		served := servedSet{
			"example.io/v1|httproutes": true,
			"example.io/v1|tcproutes":  true,
			"v1|services":              true,
		}

		effective, res, err := ResolveEffective(cfg, served)
		require.NoError(t, err)
		assert.Empty(t, res.Unavailable)
		assert.Empty(t, res.StrippedSnippets)
		assert.Empty(t, res.StrippedTests)
		assert.Len(t, effective.TemplateSnippets, 3)
		assert.Len(t, effective.ValidationTests, 2)
	})
}

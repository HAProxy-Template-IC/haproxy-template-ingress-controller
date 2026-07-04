// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"fmt"
	"sort"
)

// ServedVersionChecker reports whether the cluster (or an offline schema
// bundle) serves a resource at an exact API version. Implementations live in
// the coordination layer (live discovery) and the offline validate path
// (schema-dir CRD manifests); this package stays free of Kubernetes imports.
type ServedVersionChecker interface {
	// IsServed returns true when the plural resource is served at the given
	// group/version (e.g. "gateway.networking.k8s.io/v1", "tcproutes").
	IsServed(apiVersion, resources string) bool
}

// Resolution describes the outcome of ResolveEffective: which version each
// watched resource resolved to, which optional resources are unavailable, and
// which config elements were stripped as a consequence.
type Resolution struct {
	// ResolvedVersions maps watched-resource names to the apiVersion the
	// controller actually watches (the first served candidate).
	ResolvedVersions map[string]string

	// Unavailable lists optional watched-resource names with no served
	// candidate version, sorted alphabetically.
	Unavailable []string

	// StrippedSnippets and StrippedTests list the names of templateSnippets /
	// validationTests removed because a resource they require is unavailable,
	// sorted alphabetically.
	StrippedSnippets []string
	StrippedTests    []string
}

// ResolveEffective resolves every watched resource to the first candidate
// version the checker reports as served and returns the EFFECTIVE config the
// rest of the controller consumes:
//
//   - each surviving WatchedResources entry has APIVersion set to the resolved
//     version and APIVersions cleared, so every downstream consumer of the
//     literal APIVersion (informer GVR, stores, schema fetch, webhook
//     registration, dry-run mapping, fixture defaulting) transparently uses
//     the resolved version;
//   - an optional resource with no served candidate is removed, together with
//     every TemplateSnippet / ValidationTest whose Requires names it;
//   - a required resource with no served candidate is an error (fail fast —
//     the alternative is an informer that never syncs and a controller that
//     never becomes Ready, with nothing in the logs naming the cause).
//
// The input config is not mutated: the returned config shares everything
// except the three affected maps. The transformation is resource-agnostic —
// it consumes only candidate lists, plural names, and Requires declarations
// from the configuration.
func ResolveEffective(cfg *Config, served ServedVersionChecker) (*Config, *Resolution, error) {
	res := &Resolution{ResolvedVersions: make(map[string]string, len(cfg.WatchedResources))}
	unavailable := make(map[string]bool)

	watched := make(map[string]WatchedResource, len(cfg.WatchedResources))
	// Deterministic iteration so multi-resource error messages are stable.
	names := make([]string, 0, len(cfg.WatchedResources))
	for name := range cfg.WatchedResources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		resource := cfg.WatchedResources[name]
		resolved := ""
		for _, candidate := range resource.CandidateVersions() {
			if served.IsServed(candidate, resource.Resources) {
				resolved = candidate
				break
			}
		}
		switch {
		case resolved != "":
			resource.APIVersion = resolved
			resource.APIVersions = nil
			watched[name] = resource
			res.ResolvedVersions[name] = resolved
		case resource.Optional:
			unavailable[name] = true
			res.Unavailable = append(res.Unavailable, name)
		default:
			return nil, nil, fmt.Errorf(
				"watched resource %q is required but no candidate version is served (candidates: %v, resource: %s)",
				name, resource.CandidateVersions(), resource.Resources)
		}
	}

	snippets := cfg.TemplateSnippets
	tests := cfg.ValidationTests
	if len(unavailable) > 0 {
		snippets = make(map[string]TemplateSnippet, len(cfg.TemplateSnippets))
		for name, snippet := range cfg.TemplateSnippets {
			if requiresAny(snippet.Requires, unavailable) {
				res.StrippedSnippets = append(res.StrippedSnippets, name)
				continue
			}
			snippets[name] = snippet
		}
		tests = make(map[string]ValidationTest, len(cfg.ValidationTests))
		for name := range cfg.ValidationTests {
			if requiresAny(cfg.ValidationTests[name].Requires, unavailable) {
				res.StrippedTests = append(res.StrippedTests, name)
				continue
			}
			tests[name] = cfg.ValidationTests[name]
		}
		sort.Strings(res.StrippedSnippets)
		sort.Strings(res.StrippedTests)
	}

	effective := *cfg
	effective.WatchedResources = watched
	effective.TemplateSnippets = snippets
	effective.ValidationTests = tests
	return &effective, res, nil
}

func requiresAny(requires []string, set map[string]bool) bool {
	for _, r := range requires {
		if set[r] {
			return true
		}
	}
	return false
}

// Equal reports whether two resolutions describe the same outcome — the same
// resolved version per resource and the same unavailable set. Used by the CRD
// watch to decide whether a CRD change actually alters the effective config.
// Stripped-element lists are derived from Unavailable and need no comparison
// of their own.
func (r *Resolution) Equal(other *Resolution) bool {
	if r == nil || other == nil {
		return r == other
	}
	if len(r.ResolvedVersions) != len(other.ResolvedVersions) || len(r.Unavailable) != len(other.Unavailable) {
		return false
	}
	for name, version := range r.ResolvedVersions {
		if other.ResolvedVersions[name] != version {
			return false
		}
	}
	for i := range r.Unavailable {
		if r.Unavailable[i] != other.Unavailable[i] {
			return false
		}
	}
	return true
}

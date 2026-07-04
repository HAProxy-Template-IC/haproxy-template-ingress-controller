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

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// ResolveEffectiveSpec mirrors the controller's effective-config resolution
// (pkg/core/config.ResolveEffective) for the offline validate path, operating
// in place on the CRD spec form the validate CLI's helpers consume. Semantics:
//
//   - each watched resource resolves to the first apiVersions candidate the
//     `served` callback reports as available (in the CLI: a CRD manifest in
//     --schema-dir listing the version as served);
//   - an OPTIONAL resource with no served candidate is dropped together with
//     every templateSnippet / validationTest whose requires names it — this is
//     what makes degraded cluster profiles testable offline;
//   - unlike the live controller, a REQUIRED resource with no served candidate
//     falls back to its first candidate instead of failing: offline validation
//     has no cluster to interrogate, and a schema-dir that simply doesn't
//     bundle some watched resource must keep validating through the untyped
//     path exactly as it always has (same leniency as the GVK-resolution skip
//     in the offline type bootstrap). The same fallback applies to every
//     resource when `served` is nil (no --schema-dir at all).
func ResolveEffectiveSpec(spec *v1alpha1.HAProxyTemplateConfigSpec, served func(apiVersion, resources string) bool, logger *slog.Logger) {
	unavailable := map[string]bool{}
	for name := range spec.WatchedResources {
		wr := spec.WatchedResources[name]
		resolved := resolveSpecResource(name, &wr, served, logger)
		if resolved == "" {
			unavailable[name] = true
			delete(spec.WatchedResources, name)
			continue
		}
		wr.APIVersion = resolved
		wr.APIVersions = nil
		spec.WatchedResources[name] = wr
	}

	if len(unavailable) > 0 {
		stripSpecRequiring(spec, unavailable, logger)
	}
}

// resolveSpecResource returns the version the resource resolves to, or ""
// when it is optional and no candidate is available (→ strip). See
// ResolveEffectiveSpec for the leniency rules.
func resolveSpecResource(name string, wr *v1alpha1.WatchedResource, served func(apiVersion, resources string) bool, logger *slog.Logger) string {
	candidates := wr.APIVersions
	if len(candidates) == 0 {
		candidates = []string{wr.APIVersion}
	}

	if served != nil {
		for _, candidate := range candidates {
			if served(candidate, wr.Resources) {
				return candidate
			}
		}
		if wr.Optional {
			return ""
		}
		// Required-but-unbundled: lenient first-candidate fallback.
		logger.Debug("Offline resolution: no schema for required resource; using first candidate",
			"resource", name, "api_version", candidates[0])
	}
	return candidates[0]
}

// stripSpecRequiring removes every templateSnippet / validationTest whose
// requires names an unavailable resource.
func stripSpecRequiring(spec *v1alpha1.HAProxyTemplateConfigSpec, unavailable map[string]bool, logger *slog.Logger) {
	strippedSnippets, strippedTests := 0, 0
	for name := range spec.TemplateSnippets {
		if requiresAnyOf(spec.TemplateSnippets[name].Requires, unavailable) {
			delete(spec.TemplateSnippets, name)
			strippedSnippets++
		}
	}
	for name := range spec.ValidationTests {
		if requiresAnyOf(spec.ValidationTests[name].Requires, unavailable) {
			delete(spec.ValidationTests, name)
			strippedTests++
		}
	}
	names := make([]string, 0, len(unavailable))
	for name := range unavailable {
		names = append(names, name)
	}
	logger.Info("Optional watched resources absent from schema directory — dependent features stripped",
		"unavailable", names,
		"stripped_snippets", strippedSnippets,
		"stripped_tests", strippedTests)
}

func requiresAnyOf(requires []string, set map[string]bool) bool {
	for _, r := range requires {
		if set[r] {
			return true
		}
	}
	return false
}

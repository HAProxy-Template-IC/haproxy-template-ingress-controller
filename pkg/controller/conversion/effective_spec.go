// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package conversion

import (
	"fmt"
	"log/slog"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// SpecResolution reports what ResolveEffectiveSpec stripped, so the
// validate CLI can list the stripped tests (the degraded-profile harness
// asserts the exact set).
type SpecResolution struct {
	// StrippedTests maps each stripped validation-test name to a
	// human-readable reason ("requires unavailable resource X" vs
	// "field X absent from resolved schema").
	StrippedTests map[string]string
}

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
//   - a validationTest whose requiresFields names a field the `fieldServed`
//     callback reports absent from the resolved schema is dropped too — the
//     field-level analogue for schema generations that serve the resource at
//     the same version string but without individual fields. fieldServed may
//     be nil (no --schema-dir): field checks are then skipped entirely, the
//     same leniency the untyped path has always had. A fieldServed error
//     fails the resolution (never silently strips);
//   - unlike the live controller, a REQUIRED resource with no served candidate
//     falls back to its first candidate instead of failing: offline validation
//     has no cluster to interrogate, and a schema-dir that simply doesn't
//     bundle some watched resource must keep validating through the untyped
//     path exactly as it always has (same leniency as the GVK-resolution skip
//     in the offline type bootstrap). The same fallback applies to every
//     resource when `served` is nil (no --schema-dir at all).
func ResolveEffectiveSpec(
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	served func(apiVersion, resources string) bool,
	fieldServed func(apiVersion, resources, fieldPath string) (bool, error),
	logger *slog.Logger,
) (*SpecResolution, error) {
	res := &SpecResolution{StrippedTests: map[string]string{}}
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
		stripSpecRequiring(spec, unavailable, res, logger)
	}
	if err := stripSpecRequiringFields(spec, unavailable, fieldServed, res, logger); err != nil {
		return nil, err
	}
	return res, nil
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
func stripSpecRequiring(spec *v1alpha1.HAProxyTemplateConfigSpec, unavailable map[string]bool, res *SpecResolution, logger *slog.Logger) {
	strippedSnippets, strippedTests := 0, 0
	for name := range spec.TemplateSnippets {
		if req := firstRequiredOf(spec.TemplateSnippets[name].Requires, unavailable); req != "" {
			delete(spec.TemplateSnippets, name)
			strippedSnippets++
		}
	}
	for name := range spec.ValidationTests {
		if req := firstRequiredOf(spec.ValidationTests[name].Requires, unavailable); req != "" {
			res.StrippedTests[name] = fmt.Sprintf("requires unavailable resource %q", req)
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

// stripSpecRequiringFields removes every surviving validationTest whose
// requiresFields names a field absent from the resolved schema generation.
// Mirrors pkg/core/config.missingRequiredField, with the offline leniency
// that fieldServed itself encodes (the validate CLI's callback reports
// "present" for resources whose schema isn't bundled at all).
func stripSpecRequiringFields(
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	unavailable map[string]bool,
	fieldServed func(apiVersion, resources, fieldPath string) (bool, error),
	res *SpecResolution,
	logger *slog.Logger,
) error {
	if fieldServed == nil {
		return nil
	}
	stripped := 0
	for name := range spec.ValidationTests {
		test := spec.ValidationTests[name]
		missing, err := specMissingRequiredField(spec, &test, unavailable, fieldServed)
		if err != nil {
			return fmt.Errorf("validation test %q: %w", name, err)
		}
		if missing != "" {
			res.StrippedTests[name] = fmt.Sprintf("field %q absent from resolved schema", missing)
			delete(spec.ValidationTests, name)
			stripped++
		}
	}
	if stripped > 0 {
		logger.Info("Validation tests requiring schema fields absent from this generation stripped",
			"stripped_tests", stripped)
	}
	return nil
}

// specMissingRequiredField probes one test's requiresFields entries and
// returns the first entry whose field is absent ("" when all are present).
func specMissingRequiredField(
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	test *v1alpha1.ValidationTest,
	unavailable map[string]bool,
	fieldServed func(apiVersion, resources, fieldPath string) (bool, error),
) (string, error) {
	for _, entry := range test.RequiresFields {
		key, fieldPath, ok := strings.Cut(entry, ".")
		if !ok || fieldPath == "" {
			return "", fmt.Errorf("requiresFields entry %q is not of the form \"<watchedResource>.<field.path>\"", entry)
		}
		if unavailable[key] {
			return entry, nil
		}
		wr, exists := spec.WatchedResources[key]
		if !exists {
			return "", fmt.Errorf("requiresFields entry %q does not name a watched resource", entry)
		}
		servedField, err := fieldServed(wr.APIVersion, wr.Resources, fieldPath)
		if err != nil {
			return "", fmt.Errorf("probing schema field %q: %w", entry, err)
		}
		if !servedField {
			return entry, nil
		}
	}
	return "", nil
}

// firstRequiredOf returns the first requires entry present in the set, or "".
func firstRequiredOf(requires []string, set map[string]bool) string {
	for _, r := range requires {
		if set[r] {
			return r
		}
	}
	return ""
}

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testrunner

import (
	"context"
	"fmt"

	"github.com/pmezard/go-difflib/difflib"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// RenderDependencies holds all dependencies needed to re-render templates.
// This is used by the deterministic assertion to render again and compare.
type RenderDependencies struct {
	Engine          templating.Engine
	Stores          map[string]stores.Store
	ValidationPaths *dataplane.ValidationPaths
	HTTPStore       *FixtureHTTPStoreWrapper
	CurrentConfig   *parserconfig.StructuredConfig
	ExtraContext    map[string]any
}

// assertDeterministic validates that rendering the template twice produces identical output.
// This catches non-deterministic template behavior (e.g., map iteration order).
func (r *Runner) assertDeterministic(
	assertion *config.ValidationAssertion,
	firstConfig string,
	firstAuxFiles *dataplane.AuxiliaryFiles,
	deps *RenderDependencies,
) AssertionResult {
	result := AssertionResult{
		Type:        "deterministic",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = "Template rendering must be deterministic (identical output on repeated renders)"
	}

	// If first render failed, we can't check determinism
	if firstConfig == "" && firstAuxFiles == nil {
		result.Passed = false
		result.Error = "cannot verify determinism: first render produced no output"
		return result
	}

	// Render a second time
	second, err := r.renderWithStores(
		deps.Engine,
		deps.Stores,
		deps.ValidationPaths,
		deps.HTTPStore,
		deps.CurrentConfig,
		deps.ExtraContext,
	)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("second render failed: %v", dataplane.SimplifyRenderingError(err))
		return result
	}

	// Compare main HAProxy config
	if firstConfig != second.HAProxyConfig {
		result.Passed = false
		diff := generateUnifiedDiff(names.MainTemplateName+" (render 1)", names.MainTemplateName+" (render 2)", firstConfig, second.HAProxyConfig)
		result.Error = fmt.Sprintf("%s differs between renders:\n%s", names.MainTemplateName, diff)
		r.populateTargetMetadata(&result, firstConfig, names.MainTemplateName, true)
		return result
	}

	// Compare auxiliary files
	if diffResult := compareAuxiliaryFiles(firstAuxFiles, second.AuxiliaryFiles); diffResult != "" {
		result.Passed = false
		result.Error = diffResult
		return result
	}

	return result
}

// generateUnifiedDiff generates a unified diff between two strings.
func generateUnifiedDiff(fromName, toName, from, to string) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(from),
		B:        difflib.SplitLines(to),
		FromFile: fromName,
		ToFile:   toName,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return fmt.Sprintf("(failed to generate diff: %v)", err)
	}
	if text == "" {
		return "(no visible diff - whitespace or newline difference)"
	}
	return text
}

// compareAuxiliaryFiles compares two sets of auxiliary files and returns a diff description if they differ.
func compareAuxiliaryFiles(first, second *dataplane.AuxiliaryFiles) string {
	if first == nil && second == nil {
		return ""
	}
	if first == nil || second == nil {
		return "auxiliary files: one render produced files, the other did not"
	}

	if diff := compareFileList("map files", extractFileMap(first.MapFiles), extractFileMap(second.MapFiles)); diff != "" {
		return diff
	}
	if diff := compareFileList("general files", extractFileMap(first.GeneralFiles), extractFileMap(second.GeneralFiles)); diff != "" {
		return diff
	}
	if diff := compareFileList("SSL certificates", extractFileMap(first.SSLCertificates), extractFileMap(second.SSLCertificates)); diff != "" {
		return diff
	}
	if diff := compareFileList("crt-list files", extractFileMap(first.CRTListFiles), extractFileMap(second.CRTListFiles)); diff != "" {
		return diff
	}

	return ""
}

// compareFileList compares two maps of filename to content and returns a diff if they differ.
func compareFileList(fileType string, first, second map[string]string) string {
	// Check for files only in first
	for name := range first {
		if _, ok := second[name]; !ok {
			return fmt.Sprintf("%s: file %q exists in first render but not in second", fileType, name)
		}
	}

	// Check for files only in second
	for name := range second {
		if _, ok := first[name]; !ok {
			return fmt.Sprintf("%s: file %q exists in second render but not in first", fileType, name)
		}
	}

	// Compare content of each file
	for name, content1 := range first {
		content2 := second[name]
		if content1 != content2 {
			diff := generateUnifiedDiff(
				fmt.Sprintf("%s (render 1)", name),
				fmt.Sprintf("%s (render 2)", name),
				content1,
				content2,
			)
			return fmt.Sprintf("%s: %s differs between renders:\n%s", fileType, name, diff)
		}
	}

	return ""
}

// extractFileMap converts a slice of FileItem to an identifier→content map for
// comparison. Works for any auxiliary file type since they all implement
// FileItem (GetIdentifier returns Filename for general files and Path for the
// rest).
func extractFileMap[T auxiliaryfiles.FileItem](files []T) map[string]string {
	result := make(map[string]string, len(files))
	for _, f := range files {
		result[f.GetIdentifier()] = f.GetContent()
	}
	return result
}

// executeAssertions runs all assertions for a test and updates the result.
func (r *Runner) executeAssertions(
	ctx context.Context,
	result *TestResult,
	test *config.ValidationTest,
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	k8sResources map[string]string,
	statusPatches map[string]string,
	templateContext map[string]any,
	validationPaths *dataplane.ValidationPaths,
	renderDeps *RenderDependencies,
) {
	for i := range test.Assertions {
		assertionResult := r.runAssertion(ctx, &test.Assertions[i], haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, templateContext, result.RenderError, validationPaths, renderDeps)
		result.Assertions = append(result.Assertions, assertionResult)

		if !assertionResult.Passed {
			result.Passed = false
		}
	}
}

// hasRenderingErrorAssertions checks if the test has any assertions targeting rendering_error.
// This is used to determine if a test expects rendering to fail (negative test).
func hasRenderingErrorAssertions(assertions []config.ValidationAssertion) bool {
	for _, assertion := range assertions {
		if assertion.Target == "rendering_error" {
			return true
		}
	}
	return false
}

// runAssertion executes a single assertion.
func (r *Runner) runAssertion(
	ctx context.Context,
	assertion *config.ValidationAssertion,
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	k8sResources map[string]string,
	statusPatches map[string]string,
	templateContext map[string]any,
	renderError string,
	validationPaths *dataplane.ValidationPaths,
	renderDeps *RenderDependencies,
) AssertionResult {
	result := AssertionResult{
		Type:        assertion.Type,
		Description: assertion.Description,
		Passed:      true,
	}

	switch assertion.Type {
	case "haproxy_valid":
		result = r.assertHAProxyValid(ctx, haproxyConfig, auxiliaryFiles, assertion, validationPaths)

	case "contains":
		result = r.assertContains(haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, assertion, renderError)

	case "not_contains":
		result = r.assertNotContains(haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, assertion, renderError)

	case "match_count":
		result = r.assertMatchCount(haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, assertion, renderError)

	case "equals":
		result = r.assertEquals(haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, assertion, renderError)

	case "jsonpath":
		result = r.assertJSONPath(templateContext, assertion)

	case "match_order":
		result = r.assertMatchOrder(haproxyConfig, auxiliaryFiles, k8sResources, statusPatches, assertion, renderError)

	case "deterministic":
		if renderDeps == nil {
			result.Passed = false
			result.Error = "deterministic assertion requires render dependencies (internal error)"
		} else {
			result = r.assertDeterministic(assertion, haproxyConfig, auxiliaryFiles, renderDeps)
		}

	default:
		result.Passed = false
		result.Error = fmt.Sprintf("unknown assertion type: %s", assertion.Type)
	}

	return result
}

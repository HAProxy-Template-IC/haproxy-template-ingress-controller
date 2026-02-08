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
	"fmt"

	"github.com/pmezard/go-difflib/difflib"

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
	ExtraContext    map[string]interface{}
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
	secondConfig, secondAuxFiles, _, err := r.renderWithStores(
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
	if firstConfig != secondConfig {
		result.Passed = false
		diff := generateUnifiedDiff("haproxy.cfg (render 1)", "haproxy.cfg (render 2)", firstConfig, secondConfig)
		result.Error = fmt.Sprintf("haproxy.cfg differs between renders:\n%s", diff)
		r.populateTargetMetadata(&result, firstConfig, "haproxy.cfg", true)
		return result
	}

	// Compare auxiliary files
	if diffResult := compareAuxiliaryFiles(firstAuxFiles, secondAuxFiles); diffResult != "" {
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

	// Compare map files
	if diff := compareFileList("map files", extractMapFileMap(first.MapFiles), extractMapFileMap(second.MapFiles)); diff != "" {
		return diff
	}

	// Compare general files
	if diff := compareFileList("general files", extractGeneralFileMap(first.GeneralFiles), extractGeneralFileMap(second.GeneralFiles)); diff != "" {
		return diff
	}

	// Compare SSL certificates
	if diff := compareFileList("SSL certificates", extractCertFileMap(first.SSLCertificates), extractCertFileMap(second.SSLCertificates)); diff != "" {
		return diff
	}

	// Compare CRT-list files
	if diff := compareFileList("crt-list files", extractCRTListFileMap(first.CRTListFiles), extractCRTListFileMap(second.CRTListFiles)); diff != "" {
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

// extractMapFileMap converts a slice of MapFile to a map for comparison.
func extractMapFileMap(files []auxiliaryfiles.MapFile) map[string]string {
	result := make(map[string]string, len(files))
	for _, f := range files {
		result[f.Path] = f.Content
	}
	return result
}

// extractGeneralFileMap converts a slice of GeneralFile to a map for comparison.
func extractGeneralFileMap(files []auxiliaryfiles.GeneralFile) map[string]string {
	result := make(map[string]string, len(files))
	for _, f := range files {
		result[f.Filename] = f.Content
	}
	return result
}

// extractCertFileMap converts a slice of SSLCertificate to a map for comparison.
func extractCertFileMap(files []auxiliaryfiles.SSLCertificate) map[string]string {
	result := make(map[string]string, len(files))
	for _, f := range files {
		result[f.Path] = f.Content
	}
	return result
}

// extractCRTListFileMap converts a slice of CRTListFile to a map for comparison.
func extractCRTListFileMap(files []auxiliaryfiles.CRTListFile) map[string]string {
	result := make(map[string]string, len(files))
	for _, f := range files {
		result[f.Path] = f.Content
	}
	return result
}

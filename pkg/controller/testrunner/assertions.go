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
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/client-go/util/jsonpath"

	"haptic/pkg/core/config"
	"haptic/pkg/dataplane"
)

// assertHAProxyValid validates that the HAProxy configuration is syntactically valid.
//
// This assertion uses the HAProxy binary to validate the configuration.
func (r *Runner) assertHAProxyValid(
	_ context.Context,
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	validationPaths *dataplane.ValidationPaths,
) AssertionResult {
	result := AssertionResult{
		Type:        "haproxy_valid",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = "HAProxy configuration must be syntactically valid"
	}

	// Use dataplane.ValidateConfiguration to validate HAProxy config with worker-specific paths
	// Pass nil version to use default v3.0 schema (safest for validation)
	// Use strict validation (skipDNSValidation=false) for CLI to catch DNS issues during local validation
	err := dataplane.ValidateConfiguration(haproxyConfig, auxiliaryFiles, validationPaths, nil, false)
	failed := err != nil
	if failed {
		result.Passed = false
		simplifiedError := dataplane.SimplifyValidationError(err)
		result.Error = fmt.Sprintf("HAProxy validation failed (config size: %d bytes): %s", len(haproxyConfig), simplifiedError)
	}

	// Populate target metadata (target is haproxy.cfg for this assertion)
	r.populateTargetMetadata(&result, haproxyConfig, "haproxy.cfg", failed)

	return result
}

// assertContains validates that the target content contains the specified pattern.
func (r *Runner) assertContains(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	renderError string,
) AssertionResult {
	result := AssertionResult{
		Type:        "contains",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("Target %s must contain pattern %q", assertion.Target, assertion.Pattern)
	}

	// Resolve target to actual content
	target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

	// Check if pattern matches
	matched, err := regexp.MatchString(assertion.Pattern, target)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("invalid regex pattern %q: %v", assertion.Pattern, err)
		r.populateTargetMetadata(&result, target, assertion.Target, true)
		return result
	}

	if !matched {
		result.Passed = false
		result.Error = fmt.Sprintf("pattern %q not found in %s (target size: %d bytes). Hint: Use --verbose to see content preview",
			assertion.Pattern, assertion.Target, len(target))
	}

	// Populate target metadata for observability
	r.populateTargetMetadata(&result, target, assertion.Target, !matched)

	return result
}

// assertNotContains validates that the target content does NOT contain the specified pattern.
func (r *Runner) assertNotContains(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	renderError string,
) AssertionResult {
	result := AssertionResult{
		Type:        "not_contains",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("Target %s must not contain pattern %q", assertion.Target, assertion.Pattern)
	}

	// Resolve target to actual content
	target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

	// Check if pattern matches
	matched, err := regexp.MatchString(assertion.Pattern, target)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("invalid regex pattern %q: %v", assertion.Pattern, err)
		r.populateTargetMetadata(&result, target, assertion.Target, true)
		return result
	}

	if matched {
		result.Passed = false
		result.Error = fmt.Sprintf("pattern %q unexpectedly found in %s (target size: %d bytes). Hint: Use --verbose to see content preview",
			assertion.Pattern, assertion.Target, len(target))
	}

	// Populate target metadata for observability
	r.populateTargetMetadata(&result, target, assertion.Target, matched)

	return result
}

// assertMatchCount validates that the target content contains exactly the expected number of pattern matches.
func (r *Runner) assertMatchCount(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	renderError string,
) AssertionResult {
	result := AssertionResult{
		Type:        "match_count",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("Target %s must contain exactly %s matches of pattern %q", assertion.Target, assertion.Expected, assertion.Pattern)
	}

	// Resolve target to actual content
	target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

	// Compile regex pattern
	re, err := regexp.Compile(assertion.Pattern)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("invalid regex pattern %q: %v", assertion.Pattern, err)
		r.populateTargetMetadata(&result, target, assertion.Target, true)
		return result
	}

	// Find all matches
	matches := re.FindAllString(target, -1)
	actualCount := len(matches)

	// Parse expected count from string
	var expectedCount int
	_, err = fmt.Sscanf(assertion.Expected, "%d", &expectedCount)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("invalid expected count %q: must be an integer", assertion.Expected)
		r.populateTargetMetadata(&result, target, assertion.Target, true)
		return result
	}

	// Compare counts
	if actualCount != expectedCount {
		result.Passed = false
		result.Error = fmt.Sprintf("expected %d matches, got %d matches of pattern %q in %s (target size: %d bytes). Hint: Use --verbose to see content preview",
			expectedCount, actualCount, assertion.Pattern, assertion.Target, len(target))
	}

	// Populate target metadata for observability
	r.populateTargetMetadata(&result, target, assertion.Target, actualCount != expectedCount)

	return result
}

// assertEquals validates that the target content exactly equals the expected value.
func (r *Runner) assertEquals(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	renderError string,
) AssertionResult {
	result := AssertionResult{
		Type:        "equals",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("Target %s must equal expected value", assertion.Target)
	}

	// Resolve target to actual content
	target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

	// Compare values
	failed := target != assertion.Expected
	if failed {
		result.Passed = false
		// Truncate long values for error message
		targetPreview := truncateString(target, 100)
		expectedPreview := truncateString(assertion.Expected, 100)

		// Add hint for long values
		if len(target) > 100 || len(assertion.Expected) > 100 {
			result.Error = fmt.Sprintf("expected %q, got %q. Hint: Use --verbose for full preview", expectedPreview, targetPreview)
		} else {
			result.Error = fmt.Sprintf("expected %q, got %q", expectedPreview, targetPreview)
		}
	}

	// Populate target metadata for observability
	r.populateTargetMetadata(&result, target, assertion.Target, failed)

	return result
}

// assertJSONPath evaluates a JSONPath expression against the template context.
func (r *Runner) assertJSONPath(
	templateContext map[string]interface{},
	assertion *config.ValidationAssertion,
) AssertionResult {
	result := AssertionResult{
		Type:        "jsonpath",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("JSONPath %s must match expected value", assertion.JSONPath)
	}

	// Parse JSONPath expression
	jp := jsonpath.New("assertion")
	if err := jp.Parse(assertion.JSONPath); err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("invalid JSONPath expression %q: %v", assertion.JSONPath, err)
		return result
	}

	// Execute JSONPath against template context
	results, err := jp.FindResults(templateContext)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("JSONPath execution failed: %v", err)
		return result
	}

	// Check if we got results
	if len(results) == 0 || len(results[0]) == 0 {
		result.Passed = false
		result.Error = "JSONPath expression returned no results"
		return result
	}

	// If Expected is provided, check against it
	actualValue := fmt.Sprintf("%v", results[0][0].Interface())
	failed := false
	if assertion.Expected != "" {
		if actualValue != assertion.Expected {
			result.Passed = false
			result.Error = fmt.Sprintf("expected %q, got %q", assertion.Expected, actualValue)
			failed = true
		}
	}

	// Populate target metadata (target is the JSONPath query result)
	result.Target = assertion.JSONPath
	result.TargetSize = len(actualValue)
	if failed {
		result.TargetPreview = truncateString(actualValue, 200)
	}

	return result
}

// assertMatchOrder validates that patterns appear in the specified order in the target content.
// This is critical for Gateway API precedence rules where more specific routes must be checked
// before less specific ones (first match wins in HAProxy).
func (r *Runner) assertMatchOrder(
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	assertion *config.ValidationAssertion,
	renderError string,
) AssertionResult {
	result := AssertionResult{
		Type:        "match_order",
		Description: assertion.Description,
		Passed:      true,
	}

	if result.Description == "" {
		result.Description = fmt.Sprintf("Patterns in target %s must appear in specified order", assertion.Target)
	}

	// Resolve target to actual content
	target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

	// Check that we have patterns to match
	if len(assertion.Patterns) == 0 {
		result.Passed = false
		result.Error = "no patterns specified for match_order assertion"
		r.populateTargetMetadata(&result, target, assertion.Target, true)
		return result
	}

	// Find the position of each pattern's first match
	type patternMatch struct {
		pattern string
		index   int
		found   bool
	}
	matches := make([]patternMatch, len(assertion.Patterns))

	for i, pattern := range assertion.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			result.Passed = false
			result.Error = fmt.Sprintf("invalid regex pattern %q: %v", pattern, err)
			r.populateTargetMetadata(&result, target, assertion.Target, true)
			return result
		}

		loc := re.FindStringIndex(target)
		if loc == nil {
			matches[i] = patternMatch{
				pattern: pattern,
				index:   -1,
				found:   false,
			}
		} else {
			matches[i] = patternMatch{
				pattern: pattern,
				index:   loc[0],
				found:   true,
			}
		}
	}

	// Check if all patterns were found
	for i, match := range matches {
		if !match.found {
			result.Passed = false
			result.Error = fmt.Sprintf("pattern %d %q not found in %s (target size: %d bytes). Hint: Use --verbose to see content preview",
				i+1, match.pattern, assertion.Target, len(target))
			r.populateTargetMetadata(&result, target, assertion.Target, true)
			return result
		}
	}

	// Verify patterns appear in order
	for i := 1; i < len(matches); i++ {
		if matches[i].index < matches[i-1].index {
			result.Passed = false
			result.Error = fmt.Sprintf("patterns out of order: pattern %d %q (at position %d) appears before pattern %d %q (at position %d) in %s. Expected order: 1→2→3...",
				i+1, matches[i].pattern, matches[i].index,
				i, matches[i-1].pattern, matches[i-1].index,
				assertion.Target)
			r.populateTargetMetadata(&result, target, assertion.Target, true)
			return result
		}
	}

	// All patterns found in correct order
	r.populateTargetMetadata(&result, target, assertion.Target, false)
	return result
}

// resolveTarget resolves the target content based on the target specification.
//
// Target format: "haproxy.cfg" or "map:<name>" or "file:<name>" or "cert:<name>" or "rendering_error".
func (r *Runner) resolveTarget(target, haproxyConfig string, auxiliaryFiles *dataplane.AuxiliaryFiles, renderError string) string {
	if target == "rendering_error" {
		return renderError
	}

	if target == "haproxy.cfg" || target == "" {
		return haproxyConfig
	}

	// Check for auxiliary file targets with type prefix
	if content := r.resolveAuxiliaryFile(target, auxiliaryFiles); content != "" {
		return content
	}

	// Default to haproxy.cfg if target format is unknown
	return haproxyConfig
}

// resolveAuxiliaryFile resolves auxiliary file content based on target prefix.
func (r *Runner) resolveAuxiliaryFile(target string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	// Handle nil auxiliaryFiles (can happen when rendering fails)
	if auxiliaryFiles == nil {
		return ""
	}

	if strings.HasPrefix(target, "map:") {
		return r.findMapFile(strings.TrimPrefix(target, "map:"), auxiliaryFiles)
	}

	if strings.HasPrefix(target, "file:") {
		return r.findGeneralFile(strings.TrimPrefix(target, "file:"), auxiliaryFiles)
	}

	if strings.HasPrefix(target, "cert:") {
		return r.findCertificate(strings.TrimPrefix(target, "cert:"), auxiliaryFiles)
	}

	if strings.HasPrefix(target, "crt-list:") {
		return r.findCRTListFile(strings.TrimPrefix(target, "crt-list:"), auxiliaryFiles)
	}

	return ""
}

// findMapFile searches for a map file by name.
func (r *Runner) findMapFile(mapName string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	if auxiliaryFiles == nil {
		return ""
	}
	for _, mapFile := range auxiliaryFiles.MapFiles {
		if mapFile.Path == mapName {
			return mapFile.Content
		}
	}
	return ""
}

// findGeneralFile searches for a general file by filename.
func (r *Runner) findGeneralFile(fileName string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	if auxiliaryFiles == nil {
		return ""
	}
	for _, generalFile := range auxiliaryFiles.GeneralFiles {
		if generalFile.Filename == fileName {
			return generalFile.Content
		}
	}
	return ""
}

// findCertificate searches for a certificate by path.
// The certName parameter should be just the filename (e.g., "certs.crt-list"),
// and this method will match it against the basename of the certificate's Path.
func (r *Runner) findCertificate(certName string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	if auxiliaryFiles == nil {
		return ""
	}
	for _, sslCert := range auxiliaryFiles.SSLCertificates {
		// Extract basename from the absolute path for comparison
		// sslCert.Path is like "/tmp/.../ssl/certs.crt-list"
		// certName is like "certs.crt-list"
		if filepath.Base(sslCert.Path) == certName {
			return sslCert.Content
		}
	}
	return ""
}

// findCRTListFile searches for a crt-list file by name.
// The crtListName parameter should be just the filename (e.g., "certificate-list.txt"),
// and this method will match it against the basename of the crt-list file's Path.
func (r *Runner) findCRTListFile(crtListName string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	if auxiliaryFiles == nil {
		return ""
	}
	for _, crtList := range auxiliaryFiles.CRTListFiles {
		// Extract basename from the absolute path for comparison
		// crtList.Path is like "/tmp/.../ssl/certificate-list.txt"
		// crtListName is like "certificate-list.txt"
		if filepath.Base(crtList.Path) == crtListName {
			return crtList.Content
		}
	}
	return ""
}

// populateTargetMetadata populates the target metadata fields for an assertion result.
// This should be called for ALL assertions (passed or failed) to provide visibility.
func (r *Runner) populateTargetMetadata(result *AssertionResult, target, targetName string, hasFailed bool) {
	result.Target = targetName
	result.TargetSize = len(target)

	// Only add preview for failed assertions to keep output size manageable
	if hasFailed && target != "" {
		result.TargetPreview = truncateString(target, 200)
	}
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

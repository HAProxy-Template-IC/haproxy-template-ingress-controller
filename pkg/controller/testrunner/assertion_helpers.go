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
	"path"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// resolveTarget resolves the target content based on the target specification.
//
// Target format: "haproxy.cfg", "map:<name>", "file:<name>", "cert:<name>",
// "crt-list:<name>", "k8s:<template-name>", "status:<ns>/<name>:<phase>",
// or "rendering_error".
func (r *Runner) resolveTarget(target, haproxyConfig string, auxiliaryFiles *dataplane.AuxiliaryFiles, k8sResources, statusPatches map[string]string, renderError string) string {
	if target == "rendering_error" {
		return renderError
	}

	if target == names.MainTemplateName || target == "" {
		return haproxyConfig
	}

	// k8sResources lookup by template name. Returns the rendered YAML
	// (potentially multi-doc with `---` separators) for `k8s:<name>`.
	if after, ok := strings.CutPrefix(target, "k8s:"); ok {
		if k8sResources != nil {
			return k8sResources[after]
		}
		return ""
	}

	// Status-patch lookup by `<namespace>/<name>:<phase>`. Returns the
	// JSON-marshalled status payload that the corresponding
	// statusPatch() template call emitted. Phase is one of
	// rendered / deployed / renderFailed / deployFailed.
	if after, ok := strings.CutPrefix(target, "status:"); ok {
		if statusPatches != nil {
			return statusPatches[after]
		}
		return ""
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

	if after, ok := strings.CutPrefix(target, "map:"); ok {
		return r.findMapFile(after, auxiliaryFiles)
	}

	if after, ok := strings.CutPrefix(target, "file:"); ok {
		return r.findGeneralFile(after, auxiliaryFiles)
	}

	if after, ok := strings.CutPrefix(target, "cert:"); ok {
		return r.findCertificate(after, auxiliaryFiles)
	}

	if after, ok := strings.CutPrefix(target, "crt-list:"); ok {
		return r.findCRTListFile(after, auxiliaryFiles)
	}

	return ""
}

// findMapFile searches for a map file by name.
// The mapName parameter can be just the filename (e.g., "host.map") or a path.
// This method first tries exact match, then falls back to basename matching
// for dynamically registered maps that have full paths.
func (r *Runner) findMapFile(mapName string, auxiliaryFiles *dataplane.AuxiliaryFiles) string {
	if auxiliaryFiles == nil {
		return ""
	}
	for _, mapFile := range auxiliaryFiles.MapFiles {
		if mapFile.Path == mapName || path.Base(mapFile.Path) == mapName {
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
		if path.Base(sslCert.Path) == certName {
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
		if path.Base(crtList.Path) == crtListName {
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

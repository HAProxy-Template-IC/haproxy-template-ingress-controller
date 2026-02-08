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
	"path/filepath"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

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

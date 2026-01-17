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

package templating

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// sanitizeStorageNameRegex matches all characters that are NOT alphanumeric, underscore, or hyphen.
// This regex mirrors the HAProxy client-native library's misc.SanitizeFilename behavior.
// See: github.com/haproxytech/client-native/v6/misc/stringutil.go (lines 220-245).
var sanitizeStorageNameRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-]+`)

// sanitizeStorageName sanitizes a filename for HAProxy Dataplane API storage.
// The client-native library replaces ALL non-alphanumeric characters (except underscore and hyphen)
// with underscores in the basename, preserving the extension.
//
// Examples:
//   - "api.example.com.pem" becomes "api_example_com.pem"
//   - "my-service.pem" becomes "my-service.pem" (hyphen preserved)
//   - "namespace_name.pem" becomes "namespace_name.pem" (underscore preserved)
//   - "file with spaces.pem" becomes "file_with_spaces.pem"
//
// This replicates the logic from github.com/haproxytech/client-native/v6/misc/stringutil.go
// to avoid introducing a dependency on the dataplane package (pkg/templating is a pure library).
func sanitizeStorageName(name string) string {
	ext := filepath.Ext(name)

	// Get the base name without extension
	base := name
	if ext != "" {
		base = strings.TrimSuffix(name, ext)
	}

	// Replace all non-alphanumeric characters (except _ and -) with underscores
	sanitizedBase := sanitizeStorageNameRegex.ReplaceAllString(base, "_")

	return sanitizedBase + ext
}

// PathResolver resolves auxiliary file names to paths based on file type.
// This is used via the GetPath method in templates to construct paths
// for HAProxy auxiliary files (maps, SSL certificates, crt-list files, general files).
//
// The paths are relative (maps/, ssl/, files/) and rely on HAProxy's
// "default-path origin <BaseDir>" directive to resolve to absolute locations.
// This enables the same rendered config to work for both local validation
// and DataPlane API deployment.
type PathResolver struct {
	// BaseDir is the absolute base path for HAProxy auxiliary files.
	// This is used with "default-path origin" in HAProxy's global section
	// to resolve relative paths regardless of where the config file is located.
	// Example: "/etc/haproxy"
	BaseDir string

	// MapsDir is the relative path to the HAProxy maps directory.
	// Example: "maps"
	MapsDir string

	// SSLDir is the relative path to the HAProxy SSL certificates directory.
	// Example: "ssl"
	SSLDir string

	// CRTListDir is the relative path to the HAProxy crt-list files directory.
	// Example: "ssl"
	CRTListDir string

	// GeneralDir is the relative path to the HAProxy general files directory.
	// Example: "files"
	GeneralDir string
}

// GetBaseDir returns the BaseDir field for use in templates.
// This method exists because Scriggo runtime variables (declared with nil pointers)
// support method calls but may not support direct field access.
func (pr *PathResolver) GetBaseDir() string {
	return pr.BaseDir
}

// GetPath resolves a filename to a full path based on the file type.
//
// This method is called from templates via the pathResolver context variable:
//
//	{{ pathResolver.GetPath("host.map", "map") }}              → maps/host.map (relative) or /etc/haproxy/maps/host.map (absolute)
//	{{ pathResolver.GetPath("504.http", "file") }}             → files/504.http (relative) or /etc/haproxy/general/504.http (absolute)
//	{{ pathResolver.GetPath("cert.pem", "cert") }}             → ssl/cert.pem (relative) or /etc/haproxy/ssl/cert.pem (absolute)
//	{{ pathResolver.GetPath("certificate-list.txt", "crt-list") }} → ssl/certificate-list.txt (relative)
//	{{ pathResolver.GetPath("", "cert") }}                     → ssl (directory only)
//
// Parameters:
//   - args[0]: filename (string) - The base filename (without directory path), or empty string for directory only
//   - args[1]: fileType (string) - File type: "map", "file", "cert", or "crt-list"
//
// Returns:
//   - Path to the file (relative or absolute depending on PathResolver configuration)
//   - Error if argument count is wrong, arguments are not strings, file type is invalid, or path construction fails
//
// Note: The pathResolver must be added to the rendering context for templates to access this method.
// Relative paths work with HAProxy's working directory resolution during validation.
func (pr *PathResolver) GetPath(args ...interface{}) (interface{}, error) {
	// Validate argument count
	if len(args) != 2 {
		return nil, fmt.Errorf("GetPath requires 2 arguments (filename, fileType), got %d", len(args))
	}

	// Validate filename is a string
	filenameStr, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("GetPath: filename must be a string, got %T", args[0])
	}

	// Validate and extract file type
	fileTypeStr, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("GetPath: file type must be a string, got %T", args[1])
	}

	// Resolve path based on file type
	var basePath string
	switch fileTypeStr {
	case "map":
		basePath = pr.MapsDir
	case "file":
		basePath = pr.GeneralDir
	case "cert":
		basePath = pr.SSLDir
	case "crt-list":
		basePath = pr.CRTListDir
	default:
		return nil, fmt.Errorf("GetPath: invalid file type %q, must be \"map\", \"file\", \"cert\", or \"crt-list\"", fileTypeStr)
	}

	// If filename is empty, return just the base directory
	if filenameStr == "" {
		return basePath, nil
	}

	// Sanitize filename for SSL certificates and crt-list files only.
	// The HAProxy client-native library sanitizes filenames when storing SSL certificates
	// to avoid issues with domain names containing dots (e.g., "api.example.com.pem").
	// Map files and general files do NOT need sanitization.
	// See: github.com/haproxytech/client-native/v6/storage/storage.go (lines 198, 270)
	if fileTypeStr == "cert" || fileTypeStr == "crt-list" {
		filenameStr = sanitizeStorageName(filenameStr)
	}

	// Construct full path by joining base directory with filename
	fullPath := filepath.Join(basePath, filenameStr)

	return fullPath, nil
}

// convertToString converts any value to its string representation.
// This is used by shared filter implementations for lenient type conversion.
//
// Conversion rules:
//   - nil → ""
//   - string → unchanged
//   - int → decimal representation
//   - int64 → decimal representation
//   - float64 → decimal representation
//   - bool → "true" or "false"
//   - other → fmt.Sprintf("%v", value)
func convertToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GlobMatch filters a list of strings by glob pattern.
//
// Usage in templates:
//
//	{%- set matching = template_snippets | glob_match("backend-annotation-*") %}
//	{%- for snippet_name in matching %}
//	  {% include snippet_name %}
//	{%- endfor %}
//
// Parameters:
//   - in: List of strings to filter ([]interface{} or []string)
//   - args: Single argument specifying glob pattern (supports * and ? wildcards)
//
// Returns:
//   - Filtered list containing only matching strings
//   - Error if input is not a list, pattern is missing, or pattern is invalid
func GlobMatch(in interface{}, args ...interface{}) (interface{}, error) {
	// Convert input to []interface{}
	var list []interface{}

	switch v := in.(type) {
	case []interface{}:
		list = v
	case []string:
		// Convert []string to []interface{}
		list = make([]interface{}, len(v))
		for i, s := range v {
			list[i] = s
		}
	default:
		return nil, fmt.Errorf("glob_match: input must be a list, got %T", in)
	}

	// Validate pattern argument
	if len(args) == 0 {
		return nil, fmt.Errorf("glob_match: pattern argument required")
	}

	pattern, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("glob_match: pattern must be a string, got %T", args[0])
	}

	// Filter by glob pattern
	var result []interface{}
	for _, item := range list {
		str, ok := item.(string)
		if !ok {
			continue // Skip non-string items
		}

		matched, err := filepath.Match(pattern, str)
		if err != nil {
			return nil, fmt.Errorf("glob_match: invalid pattern %q: %w", pattern, err)
		}

		if matched {
			result = append(result, str)
		}
	}

	return result, nil
}

// B64Decode decodes a base64-encoded value.
// The input is converted to string using lenient type conversion.
//
// Usage in templates:
//
//	{{ secret.data.username | b64decode }}
//	{{ secret.data.password | b64decode }}
//
// Parameters:
//   - in: Base64-encoded value to decode (converted to string)
//
// Returns:
//   - Decoded string
//   - Error if decoding fails
//
// Note: Kubernetes secrets automatically base64-encode all data values,
// so this filter is needed to access the plain-text content.
func B64Decode(in interface{}, args ...interface{}) (interface{}, error) {
	str := convertToString(in)

	decoded, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return nil, fmt.Errorf("b64decode: %w", err)
	}

	return string(decoded), nil
}

// Strip removes leading and trailing whitespace from a string.
//
// This is the shared core implementation used by the Scriggo engine.
//
// Usage in templates:
//
//	{{ "  hello world  " | strip }}  → "hello world"
//
// Parameters:
//   - s: String to strip whitespace from
//
// Returns:
//   - String with leading and trailing whitespace removed
func Strip(s string) string {
	return strings.TrimSpace(s)
}

// Debug formats a value as JSON-formatted HAProxy comments.
//
// This is the shared core implementation used by the Scriggo engine.
// Useful for debugging template data during development.
//
// Usage in templates:
//
//	{{ routes | debug }}           → "# DEBUG:\n# [...]"
//	{{ routes | debug("label") }}  → "# DEBUG label:\n# [...]"
//
// Parameters:
//   - value: Any value to debug (will be JSON serialized)
//   - label: Optional label to identify the debug output
//
// Returns:
//   - Formatted string with JSON data as HAProxy comments
func Debug(value interface{}, label string) string {
	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(value, "# ", "  ")
	if err != nil {
		// Fallback to simple string representation
		data = []byte(fmt.Sprintf("%v", value))
	}

	// Format as HAProxy comments
	if label != "" {
		return fmt.Sprintf("# DEBUG %s:\n# %s\n", label, string(data))
	}
	return fmt.Sprintf("# DEBUG:\n# %s\n", string(data))
}

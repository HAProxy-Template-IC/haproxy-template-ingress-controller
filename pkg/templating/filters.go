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
	"strconv"
	"strings"
)

// sanitizeStorageName sanitizes a filename for HAProxy Dataplane API storage.
// The API replaces dots in the filename (excluding the extension) with underscores.
// For example: "example.com.pem" becomes "example_com.pem".
// This applies to SSL certificates and CRT-list files.
//
// This duplicates the logic from pkg/dataplane/client/storage_helpers.go to avoid
// introducing a dependency on the dataplane package (pkg/templating is a pure library).
func sanitizeStorageName(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		// No extension, replace all dots
		return strings.ReplaceAll(name, ".", "_")
	}

	// Get the base name without extension
	base := strings.TrimSuffix(name, ext)

	// Replace dots in the base name with underscores
	sanitizedBase := strings.ReplaceAll(base, ".", "_")

	return sanitizedBase + ext
}

// PathResolver resolves auxiliary file names to absolute paths based on file type.
// This is used via the GetPath method in templates to automatically construct absolute paths
// for HAProxy auxiliary files (maps, SSL certificates, crt-list files, general files).
type PathResolver struct {
	// MapsDir is the absolute path to the HAProxy maps directory.
	// Default: /etc/haproxy/maps
	MapsDir string

	// SSLDir is the absolute path to the HAProxy SSL certificates directory.
	// Default: /etc/haproxy/ssl
	SSLDir string

	// CRTListDir is the absolute path to the HAProxy crt-list files directory.
	// Default: /etc/haproxy/ssl (same as SSL certificates)
	CRTListDir string

	// GeneralDir is the absolute path to the HAProxy general files directory.
	// Default: /etc/haproxy/general
	GeneralDir string
}

// GetPath resolves a filename to its absolute path based on the file type.
//
// This method is called from templates via the pathResolver context variable:
//
//	{{ pathResolver.GetPath("host.map", "map") }}              → /etc/haproxy/maps/host.map
//	{{ pathResolver.GetPath("504.http", "file") }}             → /etc/haproxy/general/504.http
//	{{ pathResolver.GetPath("cert.pem", "cert") }}             → /etc/haproxy/ssl/cert.pem
//	{{ pathResolver.GetPath("certificate-list.txt", "crt-list") }} → /etc/haproxy/ssl/certificate-list.txt
//	{{ pathResolver.GetPath("", "cert") }}                     → /etc/haproxy/ssl (directory only)
//
// Parameters:
//   - args[0]: filename (string) - The base filename (without directory path), or empty string for directory only
//   - args[1]: fileType (string) - File type: "map", "file", "cert", or "crt-list"
//
// Returns:
//   - Absolute path to the file, or base directory if filename is empty
//   - Error if argument count is wrong, arguments are not strings, file type is invalid, or path construction fails
//
// Note: The pathResolver must be added to the rendering context for templates to access this method.
// Different PathResolver instances can be used for production paths vs validation paths.
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

	// For SSL certificates and CRT-list files, sanitize the filename to match
	// HAProxy Dataplane API behavior (dots replaced with underscores in basename)
	if fileTypeStr == "cert" || fileTypeStr == "crt-list" {
		filenameStr = sanitizeStorageName(filenameStr)
	}

	// Construct absolute path
	absolutePath := filepath.Join(basePath, filenameStr)

	return absolutePath, nil
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

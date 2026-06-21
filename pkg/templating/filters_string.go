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
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"gitlab.com/haproxy-haptic/scriggo/native"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// scriggoStrip removes leading and trailing whitespace from a value.
// The input is converted to string using lenient type conversion.
// Uses shared Strip implementation from filters.go.
//
// Example:
//
//	strip("  hello world  ") => "hello world"
//	strip(123) => "123"
func scriggoStrip(s any) string {
	str := scriggoToString(s)
	return strip(str)
}

// scriggoTrim is an alias for scriggoStrip for compatibility.
// Both "strip" and "trim" are common filter names for whitespace removal.
var scriggoTrim = scriggoStrip

// scriggoB64Decode decodes a base64-encoded value.
// The input is converted to string using lenient type conversion.
// Useful for decoding Kubernetes secret values.
//
// Example:
//
//	b64decode("SGVsbG8gV29ybGQ=") => "Hello World"
func scriggoB64Decode(s any) (string, error) {
	str := scriggoToString(s)
	decoded, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// scriggoDebug outputs the structure of a variable as formatted JSON comments.
// Uses the shared debug implementation from filters.go.
//
// Example:
//
//	debug(routes)           => "# DEBUG:\n# [...]"
//	debug(routes, "label")  => "# DEBUG label:\n# [...]"
func scriggoDebug(value any, label ...string) string {
	// Extract label if provided
	labelStr := ""
	if len(label) > 0 {
		labelStr = label[0]
	}
	return debug(value, labelStr)
}

// scriggoStringsContains checks if a value contains a substring.
// Both parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% if strings_contains(path, "/api/") %}
func scriggoStringsContains(s, substr any) bool {
	str := scriggoToString(s)
	substrStr := scriggoToString(substr)
	return strings.Contains(str, substrStr)
}

// scriggoStringsSplit splits a value by a separator.
// Both parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% for _, part := range strings_split(path, "/") %}
func scriggoStringsSplit(s, sep any) []string {
	str := scriggoToString(s)
	sepStr := scriggoToString(sep)
	return strings.Split(str, sepStr)
}

// scriggoStringsTrim trims whitespace from a value.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var trimmed = strings_trim(value) %}
func scriggoStringsTrim(s any) string {
	str := scriggoToString(s)
	return strings.TrimSpace(str)
}

// scriggoTrimSpace trims whitespace from a value. Alias for scriggoStringsTrim
// (registered under the shorter "trimSpace" filter name in filter_names.go),
// mirroring the scriggoTrim = scriggoStrip alias above.
var scriggoTrimSpace = scriggoStringsTrim

// scriggoStringsLower converts a value to lowercase.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var lower = strings_lower(method) %}
func scriggoStringsLower(s any) string {
	str := scriggoToString(s)
	return strings.ToLower(str)
}

// scriggoStringsReplace replaces all occurrences of old with new in s.
// All three parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var escaped = strings_replace(path, "/", "\\/") %}
func scriggoStringsReplace(s, old, replacement any) string {
	str := scriggoToString(s)
	oldStr := scriggoToString(old)
	replacementStr := scriggoToString(replacement)
	return strings.ReplaceAll(str, oldStr, replacementStr)
}

// scriggoStringsSplitN splits a value by separator with a maximum number of parts.
// String parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var parts = strings_splitn(line, " ", 2) %}
//	{% var first = parts[0] %}
//	{% var rest = parts[1] %}
func scriggoStringsSplitN(s, sep any, n int) []string {
	str := scriggoToString(s)
	sepStr := scriggoToString(sep)
	return strings.SplitN(str, sepStr, n)
}

// scriggoIndent indents each line of a value by width spaces or a custom prefix.
// The input is converted to string using lenient type conversion.
// By default, the first line and blank lines are not indented.
//
// Args:
//
//	s: Input value to indent (converted to string)
//	args[0] (optional): width - int (spaces) or string (prefix), default=4
//	args[1] (optional): first - bool, indent first line, default=false
//	args[2] (optional): blank - bool, indent blank lines, default=false
//
// Usage in Scriggo templates:
//
//	{{ indent(text) }}                  {# 4 spaces, skip first/blank #}
//	{{ indent(text, 8) }}               {# 8 spaces #}
//	{{ indent(text, ">>") }}            {# custom prefix #}
//	{{ indent(text, 4, true) }}         {# include first line #}
//	{{ indent(text, 4, false, true) }}  {# include blank lines #}
//
// Returns indented string or error on invalid arguments.
func scriggoIndent(s any, args ...any) (string, error) {
	str := scriggoToString(s)

	// Parse arguments
	indentStr, err := parseIndentWidth(args)
	if err != nil {
		return "", err
	}

	indentFirst, err := parseIndentBool(args, 1, "first")
	if err != nil {
		return "", err
	}

	indentBlank, err := parseIndentBool(args, 2, "blank")
	if err != nil {
		return "", err
	}

	// Process lines
	return applyIndentation(str, indentStr, indentFirst, indentBlank), nil
}

// parseIndentWidth extracts and validates the width argument.
func parseIndentWidth(args []any) (string, error) {
	if len(args) == 0 || args[0] == nil {
		return "    ", nil // 4 spaces default
	}

	switch v := args[0].(type) {
	case int:
		if v < 0 {
			return "", fmt.Errorf("indent width must be non-negative, got %d", v)
		}
		return strings.Repeat(" ", v), nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("indent width must be int or string, got %T", v)
	}
}

// parseIndentBool extracts and validates a boolean argument at the given index.
func parseIndentBool(args []any, index int, name string) (bool, error) {
	if len(args) <= index || args[index] == nil {
		return false, nil
	}

	val, ok := args[index].(bool)
	if !ok {
		return false, fmt.Errorf("indent '%s' must be bool, got %T", name, args[index])
	}
	return val, nil
}

// applyIndentation applies indentation to each line based on the flags.
// Uses index-based scanning instead of strings.Split to avoid allocating a []string slice.
func applyIndentation(s, indentStr string, indentFirst, indentBlank bool) string {
	if s == "" {
		return s
	}

	// Pre-allocate builder: original length + indent per line
	newlines := strings.Count(s, "\n")
	var result strings.Builder
	result.Grow(len(s) + (newlines+1)*len(indentStr))

	lineIndex := 0
	for {
		nlPos := strings.IndexByte(s, '\n')
		var line string
		if nlPos == -1 {
			line = s
		} else {
			line = s[:nlPos]
		}

		if shouldIndentLine(lineIndex, line, indentFirst, indentBlank) {
			result.WriteString(indentStr)
		}
		result.WriteString(line)

		if nlPos == -1 {
			break
		}
		result.WriteByte('\n')
		s = s[nlPos+1:]
		lineIndex++
	}

	return result.String()
}

// shouldIndentLine determines if a line should be indented based on flags.
func shouldIndentLine(lineIndex int, line string, indentFirst, indentBlank bool) bool {
	isFirst := lineIndex == 0
	isBlank := strings.TrimSpace(line) == ""

	if isFirst && !indentFirst {
		return false
	}
	if isBlank && !indentBlank {
		return false
	}
	return true
}

// scriggoTitle converts a value to title case using Unicode word
// segmentation (golang.org/x/text/cases). This keeps internal mid-word
// runes such as apostrophes ("don't" -> "Don't") and digit runs
// ("abc2def" -> "Abc2def") inside the word, matching the long-standing
// behaviour. The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var title = title(resource_type) %}
func scriggoTitle(s any) string {
	str := scriggoToString(s)
	return cases.Title(language.English).String(str)
}

// scriggoSanitizeRegex escapes special regex characters in a value.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var escaped = sanitize_regex(path) %}
func scriggoSanitizeRegex(s any) string {
	str := scriggoToString(s)
	return regexp.QuoteMeta(str)
}

// scriggoRegexSearch checks if a value matches a regex pattern.
// Both string parameters are converted to string using lenient type conversion.
// Returns true if pattern is found in string.
//
// Usage in Scriggo templates:
//
//	{% if regex_search(name, "ssl.*passthrough") %}
func scriggoRegexSearch(env native.Env, s, pattern any) bool {
	str := scriggoToString(s)
	patternStr := scriggoToString(pattern)
	re, err := regexp.Compile(patternStr)
	if err != nil {
		env.Fatal(fmt.Errorf("regex_search: invalid pattern %q: %w", patternStr, err))
		return false
	}
	return re.MatchString(str)
}

// scriggoIsDigit checks if a value contains only digits.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% if isdigit(value) %}
func scriggoIsDigit(s any) bool {
	str := scriggoToString(s)
	if str == "" {
		return false
	}
	for _, r := range str {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// scriggoBasename extracts the filename from a path (like Unix basename command).
// Returns the last element of path. Trailing slashes are removed before extracting.
//
// This is useful for extracting sanitized filenames from paths returned by
// fileRegistry.Register(), which returns full paths like:
//
//	/etc/haproxy/ssl/default_api_example_com-tls.pem
//
// Templates can use basename() to get just the filename for use in crt-list content,
// which needs relative paths (HAProxy uses crt-base to resolve them).
//
// Usage in Scriggo templates:
//
//	{%- var certPath, _ = fileRegistry.Register("cert", filename, content) -%}
//	{%- var certFilename = basename(certPath) -%}
//	{# certFilename is now "default_api_example_com-tls.pem" #}
func scriggoBasename(filePath string) string {
	// path.Base (not filepath.Base): the input is always a slash-separated
	// HAProxy target path, regardless of the OS the controller runs on.
	return path.Base(filePath)
}

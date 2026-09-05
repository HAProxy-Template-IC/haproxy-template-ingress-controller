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
	"strconv"
	"strings"
)

// scriggoSemverGte checks if a version is greater than or equal to a minimum version.
// Compares major.minor components only (patch is ignored).
// Returns false for empty or unparseable version strings.
//
// Usage in Scriggo templates:
//
//	{%- if semver_gte(extraContext | dig("haproxyVersion") | fallback(""), "3.3") -%}
func scriggoSemverGte(version, minVersion any) bool {
	vMajor, vMinor, ok := parseSemver(mustDeterministicScalarText("semver_gte", version))
	if !ok {
		return false
	}

	minMajor, minMinor, ok := parseSemver(mustDeterministicScalarText("semver_gte", minVersion))
	if !ok {
		return false
	}

	if vMajor != minMajor {
		return vMajor > minMajor
	}

	return vMinor >= minMinor
}

// parseSemver extracts major and minor version numbers from a version string.
// Accepts formats like "3.3", "v3.3", "3.3.1", "v3.3.1".
// Returns (0, 0, false) for empty or unparseable strings.
func parseSemver(s string) (major, minor int, ok bool) {
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return 0, 0, false
	}

	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}

	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}

	return major, minor, true
}

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

package resourcestore

import "strings"

// SingularizeResourceType converts a plural resource type to singular kind.
//
// This is a simple heuristic that handles common English pluralization rules.
// For proper kind resolution, use RESTMapper.KindFor().
//
// Examples:
//   - "ingresses" → "Ingress"
//   - "services" → "Service"
//   - "pods" → "Pod"
//   - "configmaps" → "ConfigMap"
func SingularizeResourceType(plural string) string {
	// "-es" is only the plural suffix when the singular ends in a sibilant
	// (s, x, z, ch, sh). Otherwise "-es" is just "-s" plural after a non-sibilant
	// stem (e.g. "services" = "service" + "s", not "servic" + "es").
	if before, ok := strings.CutSuffix(plural, "es"); ok && endsInSibilant(before) {
		return capitalizeFirst(before)
	}

	if before, ok := strings.CutSuffix(plural, "s"); ok {
		return capitalizeFirst(before)
	}

	// Already singular or unknown, just capitalize
	return capitalizeFirst(plural)
}

// endsInSibilant reports whether s ends in an English sibilant sound that takes
// "-es" as its plural suffix (s, x, z, ch, sh).
func endsInSibilant(s string) bool {
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") {
		return true
	}
	return strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh")
}

func capitalizeFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

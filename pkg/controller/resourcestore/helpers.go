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
	// Remove trailing 's' for simple plurals
	if before, ok := strings.CutSuffix(plural, "es"); ok {
		// "ingresses" → "ingress"
		singular := before
		// Capitalize first letter: "ingress" → "Ingress"
		return strings.ToUpper(singular[:1]) + singular[1:]
	}

	if before, ok := strings.CutSuffix(plural, "s"); ok {
		// "pods" → "pod"
		singular := before
		// Capitalize first letter: "pod" → "Pod"
		return strings.ToUpper(singular[:1]) + singular[1:]
	}

	// Already singular or unknown, just capitalize
	return strings.ToUpper(plural[:1]) + plural[1:]
}

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

package parser

// NormalizeMetadata converts nested API metadata format to flat client-native format.
//
// When the Dataplane API stores configurations, comments containing metadata are stored
// in JSON format like: # {"comment":{"value":"Pod: echo-server"}}
//
// When client-native parses this JSON comment, it returns the nested structure directly.
// However, when templates render configs, they produce flat metadata: {"comment": "Pod: echo-server"}
//
// This format mismatch causes false positive updates during comparison. This function
// normalizes nested metadata to flat format for consistent comparison.
//
// Input (nested):  {"comment": {"value": "Pod: echo-server"}}
// Output (flat):   {"comment": "Pod: echo-server"}
//
// If the input is already flat, nil, or empty, it returns unchanged (or nil for nil/empty).
func NormalizeMetadata(m map[string]interface{}) map[string]interface{} {
	if len(m) == 0 {
		return nil
	}

	result := make(map[string]interface{}, len(m))
	for key, value := range m {
		if nested, ok := value.(map[string]interface{}); ok {
			// Check if this is the API nested format with "value" key
			if v, hasValue := nested["value"]; hasValue {
				result[key] = v
			} else {
				// Not nested API format (e.g., a complex metadata structure), keep as-is
				result[key] = value
			}
		} else {
			// Already flat, keep as-is
			result[key] = value
		}
	}

	// Return nil if result is empty to match the flat format behavior
	if len(result) == 0 {
		return nil
	}

	return result
}

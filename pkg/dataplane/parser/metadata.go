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
// Input (nested):  {"comment": {"value": "Pod: echo-server"}, "custom": {"value": "foo"}}
// Output (flat):   {"comment": "Pod: echo-server", "custom": "foo"}
//
// If the input is already flat, nil, or empty, it returns unchanged (or nil for nil/empty).
//
// This function mutates the map in-place to avoid allocating a new map on every call.
// This is safe because maps come from the client-native parser (freshly allocated per parse)
// and parsed configs are cached after normalization.
func NormalizeMetadata(m map[string]interface{}) map[string]interface{} {
	if len(m) == 0 {
		return nil
	}

	for key, value := range m {
		if nested, ok := value.(map[string]interface{}); ok {
			if v, hasValue := nested["value"]; hasValue {
				m[key] = v
			}
		}
	}

	return m
}

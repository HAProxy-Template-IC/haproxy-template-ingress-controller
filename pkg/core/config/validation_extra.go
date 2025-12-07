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

package config

import "fmt"

// ValidateExtraContext validates that ExtraContext contains only JSON-compatible types.
//
// This validation catches type issues at config load time rather than at template
// render time. YAML unmarshaling can produce unexpected types (e.g., integers
// unmarshal as float64), and this validation ensures all values are representable
// in JSON.
//
// Supported types:
//   - nil (null)
//   - bool
//   - float64 (all YAML numbers become float64)
//   - string
//   - []interface{} (arrays)
//   - map[string]interface{} (objects)
func ValidateExtraContext(ctx map[string]interface{}) error {
	for key, val := range ctx {
		if err := validateJSONValue(val); err != nil {
			return fmt.Errorf("extra_context.%s: %w", key, err)
		}
	}
	return nil
}

// validateJSONValue recursively validates that a value is JSON-compatible.
func validateJSONValue(val interface{}) error {
	switch v := val.(type) {
	case nil, bool, float64, string:
		// Primitive types that JSON supports directly
		return nil

	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		// These shouldn't appear from YAML unmarshaling (they become float64),
		// but allow them anyway as they're JSON-compatible
		return nil

	case []interface{}:
		// Validate each array element
		for i, elem := range v {
			if err := validateJSONValue(elem); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return nil

	case map[string]interface{}:
		// Validate each map value
		for k, elem := range v {
			if err := validateJSONValue(elem); err != nil {
				return fmt.Errorf(".%s: %w", k, err)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported type %T (expected JSON-compatible type)", val)
	}
}

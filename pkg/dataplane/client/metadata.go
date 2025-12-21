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

package client

import "encoding/json"

// ConvertClientMetadataToAPI converts client-native flat metadata to Dataplane API nested format.
//
// The client-native library uses a flat map structure for metadata:
//
//	map[string]interface{}{"comment": "Pod: echo-server-v2"}
//
// The Dataplane API expects a nested map structure:
//
//	map[string]map[string]interface{}{"comment": {"value": "Pod: echo-server-v2"}}
//
// This conversion preserves server comments and other metadata throughout the
// fine-grained sync process, ensuring synced config remains close to the original.
func ConvertClientMetadataToAPI(clientMetadata map[string]interface{}) map[string]map[string]interface{} {
	if len(clientMetadata) == 0 {
		return nil
	}

	nested := make(map[string]map[string]interface{})
	for key, value := range clientMetadata {
		nested[key] = map[string]interface{}{
			"value": value,
		}
	}

	return nested
}

// ConvertAPIMetadataToClient converts Dataplane API nested metadata to client-native flat format.
//
// This is the reverse operation of ConvertClientMetadataToAPI, used when reading
// configurations from the Dataplane API and converting them back to client-native models.
//
// Dataplane API format:
//
//	map[string]map[string]interface{}{"comment": {"value": "Pod: echo-server-v2"}}
//
// Converts to client-native format:
//
//	map[string]interface{}{"comment": "Pod: echo-server-v2"}
func ConvertAPIMetadataToClient(apiMetadata map[string]map[string]interface{}) map[string]interface{} {
	if len(apiMetadata) == 0 {
		return nil
	}

	flat := make(map[string]interface{})
	for key, nested := range apiMetadata {
		if value, ok := nested["value"]; ok {
			flat[key] = value
		}
	}

	return flat
}

// TransformClientMetadataInJSON converts metadata within a JSON object from
// client-native flat format to API nested format.
//
// This is used when converting client-native models to version-specific API models
// via JSON marshal/unmarshal. The metadata field format differs between the two:
//
// Input (client-native):
//
//	{"metadata": {"comment": "string value"}, ...}
//
// Output (API format):
//
//	{"metadata": {"comment": {"value": "string value"}}, ...}
//
// This function recursively transforms metadata in nested objects (like servers
// within backends, binds within frontends, etc.).
//
// If the JSON doesn't contain a metadata field, or the metadata is not in the
// expected format, the original JSON is returned unchanged.
func TransformClientMetadataInJSON(jsonData []byte) ([]byte, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal(jsonData, &obj); err != nil {
		return nil, err
	}

	// Recursively transform metadata in the object tree
	transformMetadataRecursive(obj)

	return json.Marshal(obj)
}

// transformMetadataRecursive walks the JSON object tree and transforms any
// metadata fields from client-native flat format to API nested format.
// It handles both nested objects and arrays (e.g., http_request_rule_list).
func transformMetadataRecursive(obj map[string]interface{}) {
	for key, value := range obj {
		switch v := value.(type) {
		case map[string]interface{}:
			if key == "metadata" {
				// Check if this is a flat metadata map (needs transformation)
				// vs already nested (has "value" sub-key)
				if needsMetadataTransformation(v) {
					obj[key] = ConvertClientMetadataToAPI(v)
				}
			} else {
				// Recurse into nested objects
				transformMetadataRecursive(v)
			}
		case []interface{}:
			// Recurse into array elements (e.g., http_request_rule_list, servers, binds)
			for _, elem := range v {
				if elemMap, ok := elem.(map[string]interface{}); ok {
					transformMetadataRecursive(elemMap)
				}
			}
		}
	}
}

// needsMetadataTransformation checks if a metadata map needs to be transformed.
// Returns true if the map contains flat key-value pairs (client-native format).
// Returns false if it's already in nested format (API format with {"key": {"value": ...}}).
func needsMetadataTransformation(metadata map[string]interface{}) bool {
	for _, v := range metadata {
		// If any value is not a map with a "value" key, it needs transformation
		if nested, ok := v.(map[string]interface{}); ok {
			if _, hasValue := nested["value"]; hasValue {
				// Already in API format
				return false
			}
		}
		// Value is not a nested map or doesn't have "value" key - needs transformation
		return true
	}
	// Empty map - no transformation needed
	return false
}

package config

import (
	"encoding/base64"
	"fmt"
)

// ParseSecretData extracts and base64-decodes Secret data from an unstructured map.
//
// Kubernetes Secrets store data as base64-encoded strings in the API. When accessed
// through unstructured.Unstructured, the values remain base64-encoded and must be
// decoded before use.
//
// Parameters:
//   - dataRaw: The raw Secret data map from unstructured.NestedMap(resource.Object, "data")
//
// Returns:
//   - map[string][]byte: Decoded secret data ready for use
//   - error: If any value is not a string or fails base64 decoding
func ParseSecretData(dataRaw map[string]interface{}) (map[string][]byte, error) {
	data := make(map[string][]byte)
	for key, value := range dataRaw {
		strValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("secret data key %q has invalid type: %T", key, value)
		}
		decoded, err := base64.StdEncoding.DecodeString(strValue)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 for key %q: %w", key, err)
		}
		data[key] = decoded
	}
	return data, nil
}

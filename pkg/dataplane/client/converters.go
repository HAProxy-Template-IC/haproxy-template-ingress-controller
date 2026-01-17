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

import (
	"encoding/json"
	"fmt"
)

// MarshalForVersion marshals a unified client-native model to JSON and transforms
// metadata from flat to nested format. Returns JSON ready for version-specific unmarshaling.
//
// This centralizes the marshal + metadata transformation that both the dispatcher
// helpers and validator need when converting client-native models to API models.
func MarshalForVersion(model interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal model: %w", err)
	}

	// Transform metadata from client-native (flat) to API (nested) format
	return TransformClientMetadataInJSON(jsonData)
}

// ConvertToVersioned unmarshals JSON into a version-specific API model.
// Uses generics for type-safe conversion without needing a converter registry.
//
// The versionMinor parameter determines which type to unmarshal into:
//   - minor >= 2: uses TV32 (DataPlane API v3.2+)
//   - minor >= 1: uses TV31 (DataPlane API v3.1)
//   - minor < 1:  uses TV30 (DataPlane API v3.0)
//
// Usage:
//
//	apiModel, err := ConvertToVersioned[v32.Server, v31.Server, v30.Server](jsonData, version.Minor)
func ConvertToVersioned[TV32, TV31, TV30 any](jsonData []byte, versionMinor int) (interface{}, error) {
	switch {
	case versionMinor >= 2:
		var m TV32
		if err := json.Unmarshal(jsonData, &m); err != nil {
			return nil, fmt.Errorf("failed to unmarshal for v3.2: %w", err)
		}
		return &m, nil
	case versionMinor >= 1:
		var m TV31
		if err := json.Unmarshal(jsonData, &m); err != nil {
			return nil, fmt.Errorf("failed to unmarshal for v3.1: %w", err)
		}
		return &m, nil
	default:
		var m TV30
		if err := json.Unmarshal(jsonData, &m); err != nil {
			return nil, fmt.Errorf("failed to unmarshal for v3.0: %w", err)
		}
		return &m, nil
	}
}

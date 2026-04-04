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

package introspection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/client-go/util/jsonpath"
)

// ExtractField extracts a specific field from data using JSONPath syntax.
//
// Uses k8s.io/client-go/util/jsonpath (kubectl-style JSONPath) for field extraction.
// This provides familiar syntax for Kubernetes users.
//
// JSONPath syntax examples:
//   - {.version} - simple field
//   - {.config.templates} - nested field
//   - {.items[0]} - array index
//   - {.items[*].name} - all names from array
//
// The data parameter should be JSON-serializable.
// The jsonPathExpr parameter should include braces (e.g., "{.version}").
//
// Example:
//
//	config := map[string]any{
//	    "version": "1.2.3",
//	    "templates": map[string]string{"main": "..."},
//	}
//
//	version, err := ExtractField(config, "{.version}")
//	// Returns: "1.2.3"
//
//	templates, err := ExtractField(config, "{.templates}")
//	// Returns: map[string]string{"main": "..."}
func ExtractField(data any, jsonPathExpr string) (any, error) {
	if jsonPathExpr == "" {
		return data, nil
	}

	// Create JSONPath parser
	j := jsonpath.New("field-extractor").AllowMissingKeys(true)

	// Parse the JSONPath expression
	if err := j.Parse(jsonPathExpr); err != nil {
		return nil, fmt.Errorf("invalid jsonpath expression %q: %w", jsonPathExpr, err)
	}

	// Convert data to JSON and back to ensure it's in the right format
	// This is necessary because jsonpath expects data in a specific structure
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshalling data: %w", err)
	}

	var unmarshaled any
	if err := json.Unmarshal(jsonData, &unmarshaled); err != nil {
		return nil, fmt.Errorf("unmarshalling data: %w", err)
	}

	// Execute JSONPath and capture output
	buf := new(bytes.Buffer)
	if err := j.Execute(buf, unmarshaled); err != nil {
		return nil, fmt.Errorf("executing jsonpath: %w", err)
	}

	// Parse the result back to any
	// The jsonpath library returns formatted text, so we need to parse it
	var result any
	if buf.Len() > 0 {
		// Try to parse as JSON first (for objects/arrays)
		if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
			// If not valid JSON, return as string
			result = buf.String()
		}
	}

	return result, nil
}

// ParseFieldQuery extracts the "field" query parameter from an HTTP request.
//
// Returns the field parameter value if present, or empty string otherwise.
//
// Example:
//
//	// GET /debug/vars/config?field={.version}
//	field := ParseFieldQuery(r)  // Returns: "{.version}"
//
//	// GET /debug/vars/config
//	field := ParseFieldQuery(r)  // Returns: ""
func ParseFieldQuery(r *http.Request) string {
	return r.URL.Query().Get("field")
}

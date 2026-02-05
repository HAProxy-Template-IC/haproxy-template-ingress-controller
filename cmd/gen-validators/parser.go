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

package main

import (
	"encoding/json"
)

// OpenAPISpec represents a simplified OpenAPI 3.x specification.
type OpenAPISpec struct {
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

// SchemaDefinition represents an OpenAPI schema definition.
type SchemaDefinition struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AllOf                []json.RawMessage          `json:"allOf"`
	Ref                  string                     `json:"$ref"`
	Pattern              string                     `json:"pattern"`
	Enum                 []interface{}              `json:"enum"` // Can be strings or integers
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
	Nullable             bool                       `json:"nullable"`
	AdditionalProperties interface{}                `json:"additionalProperties"`
	Items                json.RawMessage            `json:"items"`
}

// Property represents a resolved property with its constraints.
type Property struct {
	Name      string
	Type      string
	Pattern   string
	Enum      []string // String enum values (for string types)
	EnumInt   []int64  // Integer enum values (for integer types)
	Minimum   *float64
	Maximum   *float64
	Nullable  bool
	IsPointer bool
	IsArray   bool
}

// ResolvedSchema represents a fully resolved schema with all allOf merged.
type ResolvedSchema struct {
	Name       string
	Properties map[string]*Property
	Required   []string
}

// parseSchemaDefinition parses a JSON schema definition.
func parseSchemaDefinition(data json.RawMessage) (*SchemaDefinition, error) {
	var def SchemaDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

// parsePropertyConstraints extracts property constraints from raw JSON.
func parsePropertyConstraints(name string, data json.RawMessage) (*Property, error) {
	var def SchemaDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}

	prop := &Property{
		Name:      name,
		Type:      def.Type,
		Pattern:   def.Pattern,
		Minimum:   def.Minimum,
		Maximum:   def.Maximum,
		Nullable:  def.Nullable,
		IsPointer: def.Nullable,
	}

	// Convert enum values based on type.
	convertEnumValues(prop, &def)

	// Set type-specific flags.
	setTypeFlags(prop, &def)

	return prop, nil
}

// convertEnumValues converts raw enum values to typed slices.
func convertEnumValues(prop *Property, def *SchemaDefinition) {
	if len(def.Enum) == 0 {
		return
	}
	if def.Type == "integer" || def.Type == "number" {
		prop.EnumInt = convertIntEnumValues(def.Enum)
	} else {
		prop.Enum = convertStringEnumValues(def.Enum)
	}
}

// convertIntEnumValues converts interface slice to int64 slice.
func convertIntEnumValues(values []interface{}) []int64 {
	result := make([]int64, 0, len(values))
	for _, v := range values {
		switch val := v.(type) {
		case float64:
			result = append(result, int64(val))
		case int:
			result = append(result, int64(val))
		case int64:
			result = append(result, val)
		}
	}
	return result
}

// convertStringEnumValues converts interface slice to string slice.
func convertStringEnumValues(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// setTypeFlags sets IsArray and IsPointer based on type.
func setTypeFlags(prop *Property, def *SchemaDefinition) {
	// Arrays are not pointer types but have IsArray set.
	if def.Type == "array" {
		prop.IsArray = true
		prop.IsPointer = false
	}

	// Integer pointers are common in client-native models.
	if def.Type == "integer" && def.Nullable {
		prop.IsPointer = true
	}
}

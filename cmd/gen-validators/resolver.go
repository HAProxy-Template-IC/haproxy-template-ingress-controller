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
	"fmt"
	"strings"
)

// resolveSchema resolves a schema by name, handling allOf composition.
func resolveSchema(spec *OpenAPISpec, schemaName string) (*ResolvedSchema, error) {
	raw, ok := spec.Components.Schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("schema %q not found", schemaName)
	}

	def, err := parseSchemaDefinition(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse schema %q: %w", schemaName, err)
	}

	resolved := &ResolvedSchema{
		Name:       schemaName,
		Properties: make(map[string]*Property),
		Required:   make([]string, 0),
	}

	if len(def.AllOf) > 0 {
		if err := resolveAllOf(spec, def.AllOf, resolved); err != nil {
			return nil, err
		}
	} else {
		inlineResolved, err := resolveInlineSchema(def)
		if err != nil {
			return nil, err
		}
		mergeSchema(resolved, inlineResolved)
	}

	return resolved, nil
}

// resolveAllOf resolves allOf composition into the target schema.
func resolveAllOf(spec *OpenAPISpec, allOfItems []json.RawMessage, target *ResolvedSchema) error {
	for _, allOfItem := range allOfItems {
		subDef, err := parseSchemaDefinition(allOfItem)
		if err != nil {
			return fmt.Errorf("failed to parse allOf item: %w", err)
		}

		if subDef.Ref != "" {
			if err := resolveRefIntoSchema(spec, subDef.Ref, target); err != nil {
				return err
			}
		} else {
			inlineResolved, err := resolveInlineSchema(subDef)
			if err != nil {
				return fmt.Errorf("failed to resolve inline schema: %w", err)
			}
			mergeSchema(target, inlineResolved)
		}
	}
	return nil
}

// resolveRefIntoSchema resolves a $ref and merges it into the target schema.
func resolveRefIntoSchema(spec *OpenAPISpec, ref string, target *ResolvedSchema) error {
	refName := extractRefName(ref)
	refResolved, err := resolveSchema(spec, refName)
	if err != nil {
		return fmt.Errorf("failed to resolve $ref %q: %w", ref, err)
	}
	mergeSchema(target, refResolved)
	return nil
}

// resolveInlineSchema resolves an inline schema definition (not a $ref).
func resolveInlineSchema(def *SchemaDefinition) (*ResolvedSchema, error) {
	resolved := &ResolvedSchema{
		Name:       "",
		Properties: make(map[string]*Property),
		Required:   append([]string{}, def.Required...),
	}

	// Parse properties
	for propName, propData := range def.Properties {
		prop, err := parsePropertyConstraints(propName, propData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse property %q: %w", propName, err)
		}
		resolved.Properties[propName] = prop
	}

	return resolved, nil
}

// mergeSchema merges source schema into target.
func mergeSchema(target, source *ResolvedSchema) {
	// Merge properties (source overwrites target)
	for name, prop := range source.Properties {
		target.Properties[name] = prop
	}

	// Merge required fields (deduplicate)
	seen := make(map[string]bool)
	for _, r := range target.Required {
		seen[r] = true
	}
	for _, r := range source.Required {
		if !seen[r] {
			target.Required = append(target.Required, r)
			seen[r] = true
		}
	}
}

// extractRefName extracts the schema name from a $ref path.
// Example: "#/components/schemas/server_params" -> "server_params".
func extractRefName(ref string) string {
	const prefix = "#/components/schemas/"
	if strings.HasPrefix(ref, prefix) {
		return strings.TrimPrefix(ref, prefix)
	}
	return ref
}

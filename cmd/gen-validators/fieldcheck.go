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
	"reflect"

	"github.com/haproxytech/client-native/v6/models"
)

// modelTypes maps Go model type names (as returned by goModelType) to their
// reflect.Type. This is used to verify that generated code only references
// fields that actually exist on the client-native model structs.
var modelTypes = map[string]reflect.Type{
	"Server":                reflect.TypeFor[models.Server](),
	"ServerTemplate":        reflect.TypeFor[models.ServerTemplate](),
	"Bind":                  reflect.TypeFor[models.Bind](),
	"HTTPRequestRule":       reflect.TypeFor[models.HTTPRequestRule](),
	"HTTPResponseRule":      reflect.TypeFor[models.HTTPResponseRule](),
	"TCPRequestRule":        reflect.TypeFor[models.TCPRequestRule](),
	"TCPResponseRule":       reflect.TypeFor[models.TCPResponseRule](),
	"HTTPAfterResponseRule": reflect.TypeFor[models.HTTPAfterResponseRule](),
	"HTTPErrorRule":         reflect.TypeFor[models.HTTPErrorRule](),
	"ServerSwitchingRule":   reflect.TypeFor[models.ServerSwitchingRule](),
	"BackendSwitchingRule":  reflect.TypeFor[models.BackendSwitchingRule](),
	"StickRule":             reflect.TypeFor[models.StickRule](),
	"ACL":                   reflect.TypeFor[models.ACL](),
	"Filter":                reflect.TypeFor[models.Filter](),
	"LogTarget":             reflect.TypeFor[models.LogTarget](),
	"HTTPCheck":             reflect.TypeFor[models.HTTPCheck](),
	"TCPCheck":              reflect.TypeFor[models.TCPCheck](),
	"Capture":               reflect.TypeFor[models.Capture](),
}

// modelHasField checks whether the given client-native model type has a
// struct field with the specified Go name. Returns true if the model type
// is unknown (conservative: don't filter fields we can't check).
func modelHasField(modelTypeName, goFieldName string) bool {
	t, ok := modelTypes[modelTypeName]
	if !ok {
		// Unknown model type - don't filter
		return true
	}
	_, found := t.FieldByName(goFieldName)
	return found
}

// filterSchemaProperties removes properties whose Go field names don't exist
// on the corresponding client-native model struct. This handles cases where
// newer OpenAPI specs define fields not yet present in the client-native library.
func filterSchemaProperties(schemaName string, schema *ResolvedSchema) *ResolvedSchema {
	modelTypeName := goModelType(schemaName)

	filtered := &ResolvedSchema{
		Name:       schema.Name,
		Properties: make(map[string]*Property, len(schema.Properties)),
		Required:   make([]string, 0, len(schema.Required)),
	}

	for propName, prop := range schema.Properties {
		goField := goFieldName(propName)
		if modelHasField(modelTypeName, goField) {
			filtered.Properties[propName] = prop
		}
	}

	for _, req := range schema.Required {
		goField := goFieldName(req)
		if modelHasField(modelTypeName, goField) {
			filtered.Required = append(filtered.Required, req)
		}
	}

	return filtered
}

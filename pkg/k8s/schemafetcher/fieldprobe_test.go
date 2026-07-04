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

package schemafetcher

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func parseSchema(t *testing.T, raw string) *spec.Schema {
	t.Helper()
	var s spec.Schema
	require.NoError(t, json.Unmarshal([]byte(raw), &s))
	return &s
}

func parseComponents(t *testing.T, raw string) map[string]spec.Schema {
	t.Helper()
	var m map[string]spec.Schema
	require.NoError(t, json.Unmarshal([]byte(raw), &m))
	return m
}

// TestSchemaHasField_NestedArrays pins the transparent array descent that the
// RequiresFields dot-path syntax relies on: "spec.rules.filters.cors" must
// match "spec.properties.rules.items.properties.filters.items.properties.cors"
// — the exact shape of the HTTPRoute CORS-filter probe from issue #59.
func TestSchemaHasField_NestedArrays(t *testing.T) {
	sch := parseSchema(t, `{
		"type": "object",
		"properties": {
			"spec": {
				"type": "object",
				"properties": {
					"rules": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"filters": {
									"type": "array",
									"items": {
										"type": "object",
										"properties": {
											"cors": {"type": "object"},
											"requestMirror": {
												"type": "object",
												"properties": {"percent": {"type": "integer"}}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}`)

	assert.True(t, SchemaHasField(sch, nil, "spec.rules.filters.cors"))
	assert.True(t, SchemaHasField(sch, nil, "spec.rules.filters.requestMirror.percent"))
	assert.True(t, SchemaHasField(sch, nil, "spec.rules"))
	assert.False(t, SchemaHasField(sch, nil, "spec.rules.filters.requestMirror.fraction"),
		"absent leaf under a present parent")
	assert.False(t, SchemaHasField(sch, nil, "spec.infrastructure"), "absent branch")
	assert.False(t, SchemaHasField(sch, nil, "spec.rules.filters.cors.allowOrigins"),
		"descending past a leaf object with no properties")
	assert.False(t, SchemaHasField(sch, nil, ""), "empty path never matches")
	assert.False(t, SchemaHasField(nil, nil, "spec"), "nil schema never matches")
}

// TestSchemaHasField_PreserveUnknownFields pins that a
// x-kubernetes-preserve-unknown-fields subtree counts as containing ANY
// field — the apiserver persists arbitrary fields there, so probing must not
// report them absent.
func TestSchemaHasField_PreserveUnknownFields(t *testing.T) {
	sch := parseSchema(t, `{
		"type": "object",
		"properties": {
			"spec": {
				"type": "object",
				"x-kubernetes-preserve-unknown-fields": true
			},
			"status": {"type": "object", "properties": {}}
		}
	}`)

	assert.True(t, SchemaHasField(sch, nil, "spec.anything.at.all"))
	assert.True(t, SchemaHasField(sch, nil, "spec"))
	assert.False(t, SchemaHasField(sch, nil, "status.anything"))
}

// TestSchemaHasField_AdditionalProperties pins map-shaped schemas: a typed
// additionalProperties map contains every key (the segment resolves to the
// value schema); a boolean-true one accepts anything below it.
func TestSchemaHasField_AdditionalProperties(t *testing.T) {
	sch := parseSchema(t, `{
		"type": "object",
		"properties": {
			"typedMap": {
				"type": "object",
				"additionalProperties": {
					"type": "object",
					"properties": {"inner": {"type": "string"}}
				}
			},
			"freeMap": {
				"type": "object",
				"additionalProperties": true
			}
		}
	}`)

	assert.True(t, SchemaHasField(sch, nil, "typedMap.anyKey.inner"))
	assert.False(t, SchemaHasField(sch, nil, "typedMap.anyKey.absent"))
	assert.True(t, SchemaHasField(sch, nil, "freeMap.anything.below"))
}

// TestSchemaHasField_RefsAndAllOf pins the aggregated-OpenAPI shapes: shared
// types arrive as `allOf: [$ref: ...]` wrappers resolved through the
// components map (the built-in-resource path of the cluster fetcher).
func TestSchemaHasField_RefsAndAllOf(t *testing.T) {
	sch := parseSchema(t, `{
		"type": "object",
		"properties": {
			"metadata": {
				"allOf": [{"$ref": "#/components/schemas/io.k8s.ObjectMeta"}],
				"default": {}
			},
			"spec": {"$ref": "#/components/schemas/io.k8s.WidgetSpec"}
		}
	}`)
	components := parseComponents(t, `{
		"io.k8s.ObjectMeta": {
			"type": "object",
			"properties": {"name": {"type": "string"}}
		},
		"io.k8s.WidgetSpec": {
			"type": "object",
			"properties": {
				"replicas": {"type": "integer"},
				"selfRef": {"$ref": "#/components/schemas/io.k8s.WidgetSpec"}
			}
		}
	}`)

	assert.True(t, SchemaHasField(sch, components, "metadata.name"))
	assert.False(t, SchemaHasField(sch, components, "metadata.absent"))
	assert.True(t, SchemaHasField(sch, components, "spec.replicas"))
	assert.True(t, SchemaHasField(sch, components, "spec.selfRef.replicas"),
		"refs resolve repeatedly along the path")
	assert.False(t, SchemaHasField(sch, components, "metadata.name.deeper"),
		"cannot descend past a string leaf")
	assert.False(t, SchemaHasField(sch, nil, "spec.replicas"),
		"unresolvable ref (nil components) contributes nothing")
}

// TestSchemaHasField_RefCycleTerminates pins the depth guard: a pathological
// self-referencing schema must terminate instead of recursing forever.
func TestSchemaHasField_RefCycleTerminates(t *testing.T) {
	components := parseComponents(t, `{
		"Loop": {"allOf": [{"$ref": "#/components/schemas/Loop"}]}
	}`)
	sch := parseSchema(t, `{
		"type": "object",
		"properties": {"spec": {"$ref": "#/components/schemas/Loop"}}
	}`)
	assert.False(t, SchemaHasField(sch, components, "spec.anything"))
}

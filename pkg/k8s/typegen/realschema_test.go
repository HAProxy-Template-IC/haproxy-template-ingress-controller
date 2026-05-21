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

package typegen

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// realGatewaySchemaJSON is a verbatim copy of the openAPIV3Schema field
// extracted from the live `gateways.gateway.networking.k8s.io` CRD on a
// kind-haptic-e2e cluster (Gateway API v1.x). It's chunked down to the
// fields the chart's templates actually touch (apiVersion, kind,
// metadata, spec.listeners[].{name,protocol,port,hostname}) so the
// fixture stays small enough to maintain, while still exercising every
// converter path in one go: $ref-less inline objects, nested objects,
// arrays of objects, scalars at three different types, and a free-form
// labels map. The full upstream schema is ~3000 lines and changes per
// Gateway API release; matching the upstream verbatim here would turn
// this test into a noise generator.
const realGatewaySchemaJSON = `{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {
      "type": "object",
      "properties": {
        "name": {"type": "string"},
        "namespace": {"type": "string"},
        "generation": {"type": "integer", "format": "int64"},
        "labels": {
          "type": "object",
          "additionalProperties": {"type": "string"}
        }
      }
    },
    "spec": {
      "type": "object",
      "properties": {
        "gatewayClassName": {"type": "string"},
        "listeners": {
          "type": "array",
          "items": {
            "type": "object",
            "required": ["name", "port", "protocol"],
            "properties": {
              "name": {"type": "string"},
              "hostname": {"type": "string"},
              "port": {"type": "integer", "format": "int32"},
              "protocol": {"type": "string"}
            }
          }
        }
      }
    }
  }
}`

// TestRealGatewaySchema is the integration test that proves the typegen
// pipeline works against a real Kubernetes resource schema. Every other
// test in this package uses hand-rolled fixtures; this one starts from
// a JSON-marshalled schema (the form schemas live in on the wire), runs
// the same kube-openapi parser the cluster uses, and verifies that
// every field the chart's templates actually touch reaches the
// generated type at the right kind.
//
// The check list mirrors the chart's util-resource-helpers macros:
//
//   - .Metadata.Namespace, .Metadata.Name, .Metadata.Generation
//     (every status patch's identity, every routing-tree partition)
//   - .Metadata.Labels[...] (selectors for chart-emitted Services)
//   - .Spec.GatewayClassName (controller-ownership filter)
//   - .Spec.Listeners[i].{Name,Protocol,Port,Hostname} (the inner
//     loop of every routing-tree pass in libraries/gateway/*)
//
// A failure here would mean we're shipping a typegen package that
// passes its synthetic unit tests but breaks on shapes K8s actually
// emits — usually due to format-keyword handling or additionalProperties
// edge cases that hand-rolled fixtures miss.
func TestRealGatewaySchema(t *testing.T) {
	var schema spec.Schema
	require.NoError(t, json.Unmarshal([]byte(realGatewaySchemaJSON), &schema))
	gwType, err := NewConverter(nil).Convert(&schema)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, gwType.Kind())

	t.Run("typed envelope", func(t *testing.T) {
		assertField(t, gwType, "ApiVersion", reflect.String)
		assertField(t, gwType, "Kind", reflect.String)
	})
	t.Run("metadata fields the chart touches", func(t *testing.T) {
		meta := assertFieldType(t, gwType, "Metadata", reflect.Struct)
		assertField(t, meta, "Namespace", reflect.String)
		assertField(t, meta, "Name", reflect.String)
		assertField(t, meta, "Generation", reflect.Int64) // int64 regardless of format keyword
		labels := assertFieldType(t, meta, "Labels", reflect.Map)
		assert.Equal(t, reflect.String, labels.Key().Kind())
		assert.Equal(t, reflect.String, labels.Elem().Kind(),
			"additionalProperties.type=string must produce map[string]string, not map[string]any")
	})
	t.Run("spec.listeners inner loop", func(t *testing.T) {
		specT := assertFieldType(t, gwType, "Spec", reflect.Struct)
		assertField(t, specT, "GatewayClassName", reflect.String)
		listeners := assertFieldType(t, specT, "Listeners", reflect.Slice)
		listenerT := listeners.Elem()
		require.Equal(t, reflect.Struct, listenerT.Kind())
		for _, f := range []struct {
			name string
			kind reflect.Kind
		}{
			{"Name", reflect.String}, {"Hostname", reflect.String},
			{"Port", reflect.Int64}, {"Protocol", reflect.String},
		} {
			assertField(t, listenerT, f.name, f.kind)
		}
	})
	t.Run("round-trip from unstructured map", func(t *testing.T) {
		v, err := WrapInto(realGatewayFixture(), gwType)
		require.NoError(t, err)
		assertGatewayRoundTrip(t, v)
	})
}

// realGatewayFixture is the unstructured shape a StoreWrapper snapshot
// would hand WrapInto. Includes one listener with all fields and one
// that omits hostname to confirm missing-key handling.
func realGatewayFixture() map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":       "public",
			"namespace":  "ingress",
			"generation": int64(3),
			"labels":     map[string]any{"team": "platform"},
		},
		"spec": map[string]any{
			"gatewayClassName": "haptic",
			"listeners": []any{
				map[string]any{
					"name": "http", "protocol": "HTTP",
					"port": int64(80), "hostname": "*.example.com",
				},
				map[string]any{
					"name": "https", "protocol": "HTTPS",
					"port": int64(443),
				},
			},
		},
	}
}

func assertGatewayRoundTrip(t *testing.T, v reflect.Value) {
	t.Helper()
	assert.Equal(t, "ingress", v.FieldByName("Metadata").FieldByName("Namespace").String())
	specV := v.FieldByName("Spec")
	assert.Equal(t, "haptic", specV.FieldByName("GatewayClassName").String())
	lis := specV.FieldByName("Listeners")
	require.Equal(t, 2, lis.Len())
	assert.Equal(t, "http", lis.Index(0).FieldByName("Name").String())
	assert.Equal(t, int64(80), lis.Index(0).FieldByName("Port").Int())
	assert.Equal(t, "*.example.com", lis.Index(0).FieldByName("Hostname").String())
	assert.Equal(t, "https", lis.Index(1).FieldByName("Name").String())
	assert.Equal(t, int64(443), lis.Index(1).FieldByName("Port").Int())
	assert.Equal(t, "", lis.Index(1).FieldByName("Hostname").String(),
		"omitted hostname must round-trip as zero value, not panic")
}

func assertField(t *testing.T, parent reflect.Type, name string, want reflect.Kind) {
	t.Helper()
	f, ok := parent.FieldByName(name)
	require.True(t, ok, "field %q must exist on %s", name, parent)
	assert.Equal(t, want, f.Type.Kind(), "field %q kind", name)
}

func assertFieldType(t *testing.T, parent reflect.Type, name string, want reflect.Kind) reflect.Type {
	t.Helper()
	f, ok := parent.FieldByName(name)
	require.True(t, ok, "field %q must exist on %s", name, parent)
	require.Equal(t, want, f.Type.Kind(), "field %q kind", name)
	return f.Type
}

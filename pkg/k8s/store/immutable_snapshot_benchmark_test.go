// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

var immutableSnapshotBenchmarkSink any

func BenchmarkImmutableSnapshotHTTPRoute3000(b *testing.B) {
	store := NewMemoryStore(2)
	for index := range 3000 {
		name := fmt.Sprintf("route-%d", index)
		if err := store.Add(immutableSnapshotBenchmarkHTTPRoute(name, fmt.Sprintf("svc-%d", index)), []string{"default", name}); err != nil {
			b.Fatal(err)
		}
	}
	snapshot, err := store.Pin()
	if err != nil {
		b.Fatal(err)
	}
	elementType, err := typegen.NewConverter(nil).Convert(immutableSnapshotBenchmarkHTTPRouteSchema())
	if err != nil {
		b.Fatal(err)
	}
	projection, supported, err := ProjectImmutableSnapshotList(b.Context(), snapshot)
	if err != nil || !supported {
		b.Fatalf("ProjectImmutableSnapshotList() = %v, %t", err, supported)
	}

	b.Run("projection", func(b *testing.B) {
		benchmarkImmutableSnapshotProjection(b, snapshot)
	})
	b.Run("encode", func(b *testing.B) {
		benchmarkImmutableSnapshotEncode(b, projection)
	})
	b.Run("projection_and_encode", func(b *testing.B) {
		benchmarkImmutableSnapshotProjectionAndEncode(b, snapshot)
	})
	b.Run("get_projection_and_encode", func(b *testing.B) {
		benchmarkImmutableSnapshotGetProjectionAndEncode(b, snapshot)
	})
	b.Run("direct_typed", func(b *testing.B) {
		benchmarkImmutableSnapshotDirectTyped(b, projection, elementType)
	})
	b.Run("projection_and_direct_typed", func(b *testing.B) {
		benchmarkImmutableSnapshotProjectionAndDirectTyped(b, snapshot, elementType)
	})
	b.Run("legacy_clone_json_typed", func(b *testing.B) {
		benchmarkImmutableSnapshotLegacyCloneJSONTyped(b, snapshot, elementType)
	})
}

func benchmarkImmutableSnapshotProjection(b *testing.B, snapshot stores.ReadSnapshot) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, ok, projectErr := ProjectImmutableSnapshotList(b.Context(), snapshot)
		if projectErr != nil || !ok {
			b.Fatalf("ProjectImmutableSnapshotList() = %v, %t", projectErr, ok)
		}
		immutableSnapshotBenchmarkSink = value
	}
}

func benchmarkImmutableSnapshotEncode(b *testing.B, projection *ImmutableSnapshotProjection) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, encodeErr := projection.Encode()
		if encodeErr != nil {
			b.Fatal(encodeErr)
		}
		immutableSnapshotBenchmarkSink = value
	}
}

func benchmarkImmutableSnapshotProjectionAndEncode(b *testing.B, snapshot stores.ReadSnapshot) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, ok, projectErr := ProjectImmutableSnapshotList(b.Context(), snapshot)
		if projectErr != nil || !ok {
			b.Fatalf("ProjectImmutableSnapshotList() = %v, %t", projectErr, ok)
		}
		encoded, encodeErr := value.Encode()
		if encodeErr != nil {
			b.Fatal(encodeErr)
		}
		immutableSnapshotBenchmarkSink = encoded
	}
}

func benchmarkImmutableSnapshotGetProjectionAndEncode(b *testing.B, snapshot stores.ReadSnapshot) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, ok, projectErr := ProjectImmutableSnapshotGet(
			b.Context(), snapshot, "default", "route-2999",
		)
		if projectErr != nil || !ok {
			b.Fatalf("ProjectImmutableSnapshotGet() = %v, %t", projectErr, ok)
		}
		encoded, encodeErr := value.Encode()
		if encodeErr != nil {
			b.Fatal(encodeErr)
		}
		immutableSnapshotBenchmarkSink = encoded
	}
}

func benchmarkImmutableSnapshotDirectTyped(
	b *testing.B,
	projection *ImmutableSnapshotProjection,
	elementType reflect.Type,
) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, projectErr := projection.ProjectItems(elementType)
		if projectErr != nil {
			b.Fatal(projectErr)
		}
		immutableSnapshotBenchmarkSink = value
	}
}

func benchmarkImmutableSnapshotProjectionAndDirectTyped(
	b *testing.B,
	snapshot stores.ReadSnapshot,
	elementType reflect.Type,
) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, ok, projectErr := ProjectImmutableSnapshotList(b.Context(), snapshot)
		if projectErr != nil || !ok {
			b.Fatalf("ProjectImmutableSnapshotList() = %v, %t", projectErr, ok)
		}
		items, projectErr := value.ProjectItems(elementType)
		if projectErr != nil {
			b.Fatal(projectErr)
		}
		immutableSnapshotBenchmarkSink = items
	}
}

func benchmarkImmutableSnapshotLegacyCloneJSONTyped(
	b *testing.B,
	snapshot stores.ReadSnapshot,
	elementType reflect.Type,
) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		items, listErr := snapshot.List()
		if listErr != nil {
			b.Fatal(listErr)
		}
		encoded, encodeErr := json.Marshal(items)
		if encodeErr != nil {
			b.Fatal(encodeErr)
		}
		var canonical []map[string]any
		if decodeErr := json.Unmarshal(encoded, &canonical); decodeErr != nil {
			b.Fatal(decodeErr)
		}
		values := make([]reflect.Value, len(canonical))
		for index := range canonical {
			value, wrapErr := typegen.WrapInto(canonical[index], elementType)
			if wrapErr != nil {
				b.Fatal(wrapErr)
			}
			values[index] = value
		}
		immutableSnapshotBenchmarkSink = values
	}
}

func immutableSnapshotBenchmarkHTTPRoute(name, service string) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "main-gateway", "namespace": "default"}},
			"hostnames":  []any{name + ".example.com"},
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{
					"path":        map[string]any{"type": "PathPrefix", "value": "/api"},
					"headers":     []any{map[string]any{"name": "X-Version", "value": "v1"}},
					"queryParams": []any{map[string]any{"name": "debug", "value": "true"}},
				}},
				"backendRefs": []any{map[string]any{
					"name": service, "port": int64(80), "weight": int64(1),
				}},
			}},
		},
	}
}

func immutableSnapshotBenchmarkHTTPRouteSchema() *spec.Schema {
	stringSchema := immutableSnapshotBenchmarkSchema("string", nil, nil)
	integerSchema := immutableSnapshotBenchmarkSchema("integer", nil, nil)
	object := func(properties map[string]spec.Schema) spec.Schema {
		return immutableSnapshotBenchmarkSchema("object", properties, nil)
	}
	array := func(item spec.Schema) spec.Schema {
		return immutableSnapshotBenchmarkSchema("array", nil, &item)
	}
	nameValue := object(map[string]spec.Schema{"name": stringSchema, "value": stringSchema})
	path := object(map[string]spec.Schema{"type": stringSchema, "value": stringSchema})
	match := object(map[string]spec.Schema{
		"path": path, "headers": array(nameValue), "queryParams": array(nameValue),
	})
	backend := object(map[string]spec.Schema{
		"name": stringSchema, "port": integerSchema, "weight": integerSchema,
	})
	rule := object(map[string]spec.Schema{
		"matches": array(match), "backendRefs": array(backend),
	})
	parentRef := object(map[string]spec.Schema{"name": stringSchema, "namespace": stringSchema})
	root := object(map[string]spec.Schema{
		"apiVersion": stringSchema,
		"kind":       stringSchema,
		"metadata": object(map[string]spec.Schema{
			"name": stringSchema, "namespace": stringSchema,
		}),
		"spec": object(map[string]spec.Schema{
			"parentRefs": array(parentRef), "hostnames": array(stringSchema), "rules": array(rule),
		}),
	})
	return &root
}

func immutableSnapshotBenchmarkSchema(
	typeName string,
	properties map[string]spec.Schema,
	item *spec.Schema,
) spec.Schema {
	result := spec.Schema{SchemaProps: spec.SchemaProps{
		Type:       spec.StringOrArray{typeName},
		Properties: properties,
	}}
	if item != nil {
		result.Items = &spec.SchemaOrArray{Schema: item}
	}
	return result
}

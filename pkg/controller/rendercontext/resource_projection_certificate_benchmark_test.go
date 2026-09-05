// Copyright 2026 Philipp Hossner
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

package rendercontext

import (
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

var benchmarkResourceProjectionCertificate any

func BenchmarkTypedResourceProjectionCertificateHTTPRoute3000(b *testing.B) {
	elementType, err := typegen.NewConverter(nil).Convert(resourceProjectionHTTPRouteSchema())
	if err != nil {
		b.Fatal(err)
	}
	items := make([]any, 3000)
	for index := range items {
		name := fmt.Sprintf("route-%d", index)
		items[index] = resourceProjectionHTTPRoute(name, fmt.Sprintf("svc-%d", index))
	}
	returnType := reflect.SliceOf(reflect.PointerTo(elementType))

	b.Run("projection", func(b *testing.B) {
		benchmarkResourceProjection(b, items, elementType, returnType)
	})

	projected, err := projectResourceBenchmarkItems(items, elementType, returnType)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("certificate", func(b *testing.B) {
		benchmarkResourceCertificate(b, projected)
	})

	b.Run("projection_and_certificate", func(b *testing.B) {
		benchmarkResourceProjectionAndCertificate(b, items, elementType, returnType)
	})
}

func benchmarkResourceProjection(
	b *testing.B,
	items []any,
	elementType, returnType reflect.Type,
) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, projectErr := projectResourceBenchmarkItems(items, elementType, returnType)
		if projectErr != nil {
			b.Fatal(projectErr)
		}
		benchmarkResourceProjectionCertificate = value
	}
}

func benchmarkResourceCertificate(b *testing.B, projected reflect.Value) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		certificate := templating.CertifyIncrementalImmutableInputs(projected.Interface())
		if certificate == nil || !certificate.Guards(projected.Interface()) {
			b.Fatal("invalid immutable certificate")
		}
		benchmarkResourceProjectionCertificate = certificate
	}
}

func benchmarkResourceProjectionAndCertificate(
	b *testing.B,
	items []any,
	elementType, returnType reflect.Type,
) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		value, projectErr := projectResourceBenchmarkItems(items, elementType, returnType)
		if projectErr != nil {
			b.Fatal(projectErr)
		}
		certificate := templating.CertifyIncrementalImmutableInputs(value.Interface())
		if certificate == nil || !certificate.Guards(value.Interface()) {
			b.Fatal("invalid immutable certificate")
		}
		benchmarkResourceProjectionCertificate = certificate
	}
}

func projectResourceBenchmarkItems(
	items []any,
	elementType reflect.Type,
	returnType reflect.Type,
) (reflect.Value, error) {
	result := reflect.MakeSlice(returnType, len(items), len(items))
	for index, item := range items {
		value, err := wrapImmutableItemToPointer(item, elementType)
		if err != nil {
			return reflect.Value{}, err
		}
		result.Index(index).Set(value)
	}
	return result, nil
}

func resourceProjectionHTTPRoute(name, service string) map[string]any {
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

func resourceProjectionHTTPRouteSchema() *spec.Schema {
	stringSchema := resourceProjectionSchema("string", nil, nil)
	integerSchema := resourceProjectionSchema("integer", nil, nil)
	object := func(properties map[string]spec.Schema) spec.Schema {
		return resourceProjectionSchema("object", properties, nil)
	}
	array := func(item spec.Schema) spec.Schema {
		return resourceProjectionSchema("array", nil, &item)
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

func resourceProjectionSchema(
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

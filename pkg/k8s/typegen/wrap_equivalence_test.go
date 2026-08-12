// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package typegen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// wrapViaJSON is WrapInto's original implementation: marshal the unstructured
// map, then unmarshal it into the generated type.
func wrapViaJSON(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return reflect.Value{}, err
	}
	ptr := reflect.New(typ)
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return ptr.Elem(), nil
}

// wrapViaConverter is the reflect-based alternative: apimachinery's own
// converter, which is driven by the same json struct tags but never serialises.
func wrapViaConverter(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	ptr := reflect.New(typ)
	if err := k8sruntime.DefaultUnstructuredConverter.FromUnstructured(obj, ptr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return ptr.Elem(), nil
}

// The two converters must agree on every shape a watched resource can take.
//
// They are not interchangeable by construction: FromUnstructured is stricter
// than encoding/json about number types and rejects what json would coerce. A
// difference here is silently wrong template input, so this pins equivalence on
// the shapes the generated types actually produce before the hot path may use
// the cheaper one.
func TestWrapIntoConverterEquivalence(t *testing.T) {
	typ := buildRealGatewayType(t)

	cases := []struct {
		name string
		obj  map[string]any
	}{
		{
			name: "fully populated",
			obj: map[string]any{
				"apiVersion": "gateway.networking.k8s.io/v1",
				"kind":       "Gateway",
				"metadata": map[string]any{
					"name":      "prod",
					"namespace": "edge",
					"labels":    map[string]any{"team": "net", "tier": "0"},
				},
				"spec": map[string]any{
					"gatewayClassName": "haptic",
					"listeners": []any{
						map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80)},
						map[string]any{"name": "https", "protocol": "HTTPS", "port": int64(443), "hostname": "a.example.com"},
					},
				},
			},
		},
		{
			name: "absent optional fields",
			obj: map[string]any{
				"apiVersion": "gateway.networking.k8s.io/v1",
				"kind":       "Gateway",
				"metadata":   map[string]any{"name": "bare"},
			},
		},
		{
			name: "empty listener list",
			obj: map[string]any{
				"kind":     "Gateway",
				"metadata": map[string]any{"name": "empty"},
				"spec":     map[string]any{"gatewayClassName": "haptic", "listeners": []any{}},
			},
		},
		{
			name: "explicit nulls",
			obj: map[string]any{
				"kind":     "Gateway",
				"metadata": map[string]any{"name": "nulls", "labels": nil},
				"spec":     nil,
			},
		},
		{
			name: "port at the int64 boundary",
			obj: map[string]any{
				"kind":     "Gateway",
				"metadata": map[string]any{"name": "big"},
				"spec": map[string]any{
					"listeners": []any{map[string]any{"name": "l", "port": int64(65535)}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viaJSON, errJSON := wrapViaJSON(tc.obj, typ)
			viaConv, errConv := wrapViaConverter(tc.obj, typ)

			require.NoError(t, errJSON, "json round-trip failed")
			require.NoError(t, errConv, "reflect converter failed on a shape json accepts")

			require.Equal(t, viaJSON.Interface(), viaConv.Interface(),
				"converters disagree — template input would silently change")
		})
	}
}

// A float where the schema says integer is the shape most likely to diverge:
// encoding/json coerces it, and apimachinery's converter is documented as
// stricter about number types. Measured here rather than assumed — they agree,
// which is what makes the cheaper converter a safe substitution.
func TestWrapIntoConverterAgreesOnUnnormalisedFloat(t *testing.T) {
	typ := buildRealGatewayType(t)
	obj := map[string]any{
		"kind":     "Gateway",
		"metadata": map[string]any{"name": "float-port"},
		"spec": map[string]any{
			"listeners": []any{map[string]any{"name": "l", "port": float64(8080)}},
		},
	}

	viaJSON, errJSON := wrapViaJSON(obj, typ)
	require.NoError(t, errJSON)
	viaConv, errConv := wrapViaConverter(obj, typ)
	require.NoError(t, errConv)

	require.Equal(t, viaJSON.Interface(), viaConv.Interface(),
		"a float in an integer field must not decode to different values")
}

// buildRealGatewayType generates the typed struct from the same real Gateway
// schema the rest of this package's tests use.
func buildRealGatewayType(t *testing.T) reflect.Type {
	t.Helper()
	typ, err := typeFromRealGatewaySchema()
	require.NoError(t, err)
	require.NotNil(t, typ)
	return typ
}

func typeFromRealGatewaySchema() (reflect.Type, error) {
	var schema spec.Schema
	if err := json.Unmarshal([]byte(realGatewaySchemaJSON), &schema); err != nil {
		return nil, fmt.Errorf("parsing real gateway schema: %w", err)
	}
	typ, err := NewConverter(nil).Convert(&schema)
	if err != nil {
		return nil, fmt.Errorf("converting real gateway schema: %w", err)
	}
	return typ, nil
}

// benchGatewayObject is a realistic watched resource: nested objects, an array
// of objects, a free-form label map and scalars at three types.
func benchGatewayObject(i int) map[string]any {
	return map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("gw-%d", i),
			"namespace": fmt.Sprintf("ns-%d", i%50),
			"labels": map[string]any{
				"app": "haptic", "tier": "edge", "shard": fmt.Sprintf("%d", i%4),
			},
		},
		"spec": map[string]any{
			"gatewayClassName": "haptic",
			"listeners": []any{
				map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80)},
				map[string]any{"name": "https", "protocol": "HTTPS", "port": int64(443),
					"hostname": fmt.Sprintf("h%d.example.com", i)},
			},
		},
	}
}

// The typed materialization runs per resource per render, and the result is
// memoized for the render's duration — so both its allocation rate and what it
// retains land directly on peak memory.
func BenchmarkWrapInto_JSONRoundTrip(b *testing.B) {
	typ := benchType(b)
	obj := benchGatewayObject(1)
	b.ReportAllocs()
	for b.Loop() {
		v, err := wrapViaJSON(obj, typ)
		if err != nil {
			b.Fatal(err)
		}
		_ = v
	}
}

func BenchmarkWrapInto_ReflectConverter(b *testing.B) {
	typ := benchType(b)
	obj := benchGatewayObject(1)
	b.ReportAllocs()
	for b.Loop() {
		v, err := wrapViaConverter(obj, typ)
		if err != nil {
			b.Fatal(err)
		}
		_ = v
	}
}

func benchType(b *testing.B) reflect.Type {
	b.Helper()
	typ, err := typeFromRealGatewaySchema()
	if err != nil {
		b.Fatal(err)
	}
	return typ
}

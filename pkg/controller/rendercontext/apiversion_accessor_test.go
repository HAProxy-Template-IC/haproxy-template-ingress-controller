// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package rendercontext

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildResourcesValue_APIVersionAccessor pins the resolved-version
// metadata surface (template-engine spec: "Watched Resource Metadata in
// Render Context"): `resources.<name>.APIVersion()` yields the version the
// effective config carries for that resource — for typed and untyped
// resources alike — so status macros can target whatever version the cluster
// actually serves.
func TestBuildResourcesValue_APIVersionAccessor(t *testing.T) {
	versions := map[string]string{
		"httproutes": "gateway.example.io/v1beta1",
		"services":   "v1",
	}

	value := BuildResourcesValue(
		nil, // no live stores needed: the accessor is pure metadata
		nil, // untyped path
		[]string{"httproutes", "services"},
		nil,
		nil,
		func(name string) string { return versions[name] },
		slog.Default(),
	)

	outer := reflect.ValueOf(value).Elem()
	for field, want := range map[string]string{
		"Httproutes": "gateway.example.io/v1beta1",
		"Services":   "v1",
	} {
		inner := outer.FieldByName(field)
		require.True(t, inner.IsValid(), "resources struct must have field %s", field)
		accessor := inner.Elem().FieldByName("APIVersion")
		require.True(t, accessor.IsValid(), "per-resource store must expose APIVersion()")
		got := accessor.Call(nil)
		require.Len(t, got, 1)
		assert.Equal(t, want, got[0].String())
	}
}

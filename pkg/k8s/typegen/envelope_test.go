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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvelopeType_Shape pins the envelope's field set + types. If
// a future change widens the envelope, the existing fields MUST
// still resolve to their current types and json tags — chart
// templates compiled against the previous shape would otherwise
// break in production.
//
// What's NOT here (deliberately): Spec, Status. Chart code that
// touches those needs a real schema; the envelope keeping engine
// compilation alive in the fail-open path must not silently bind
// to an always-empty Spec.
func TestEnvelopeType_Shape(t *testing.T) {
	envelope := EnvelopeType()
	require.Equal(t, reflect.Struct, envelope.Kind())

	// Top-level scalars: apiVersion + kind. Both json-tagged with
	// the lower-case wire form chart templates expect.
	apiVersion, ok := envelope.FieldByName("ApiVersion")
	require.True(t, ok)
	assert.Equal(t, reflect.String, apiVersion.Type.Kind())
	assert.Equal(t, `json:"apiVersion"`, string(apiVersion.Tag))

	kind, ok := envelope.FieldByName("Kind")
	require.True(t, ok)
	assert.Equal(t, reflect.String, kind.Type.Kind())
	assert.Equal(t, `json:"kind"`, string(kind.Tag))

	// Metadata sub-struct: the four universal fields.
	meta, ok := envelope.FieldByName("Metadata")
	require.True(t, ok)
	require.Equal(t, reflect.Struct, meta.Type.Kind())

	for _, want := range []struct {
		fieldName string
		kind      reflect.Kind
		jsonName  string
	}{
		{"Name", reflect.String, "name"},
		{"Namespace", reflect.String, "namespace"},
		{"Labels", reflect.Map, "labels"},
		{"Annotations", reflect.Map, "annotations"},
	} {
		t.Run("metadata."+want.jsonName, func(t *testing.T) {
			f, ok := meta.Type.FieldByName(want.fieldName)
			require.True(t, ok, "Metadata.%s missing", want.fieldName)
			assert.Equal(t, want.kind, f.Type.Kind())
			assert.Contains(t, string(f.Tag), `json:"`+want.jsonName+`"`)
		})
	}

	// Negative assertions: Spec and Status MUST NOT exist on the
	// envelope. The fail-open path uses this type when the real
	// schema didn't load; chart code reaching into Spec / Status
	// must hit a compile error so the author knows the real schema
	// is required.
	_, hasSpec := envelope.FieldByName("Spec")
	assert.False(t, hasSpec)
	_, hasStatus := envelope.FieldByName("Status")
	assert.False(t, hasStatus)
}

// TestEnvelopeType_WrapInto verifies the envelope round-trips an
// unstructured object through WrapInto without losing the
// metadata fields. This is the property that matters at render
// time: the StoreWrapper hands the engine an envelope-wrapped
// value, and chart templates must see the original values.
func TestEnvelopeType_WrapInto(t *testing.T) {
	envelope := EnvelopeType()
	v, err := WrapInto(map[string]any{
		"apiVersion": "v1",
		"kind":       "Foo",
		"metadata": map[string]any{
			"name":        "alpha",
			"namespace":   "ns",
			"labels":      map[string]any{"app": "bar"},
			"annotations": map[string]any{"hint": "true"},
		},
		"spec": map[string]any{"port": 8080}, // ignored by envelope (no Spec field)
	}, envelope)
	require.NoError(t, err)
	require.Equal(t, reflect.Struct, v.Kind())

	assert.Equal(t, "v1", v.FieldByName("ApiVersion").String())
	assert.Equal(t, "Foo", v.FieldByName("Kind").String())
	meta := v.FieldByName("Metadata")
	assert.Equal(t, "alpha", meta.FieldByName("Name").String())
	assert.Equal(t, "ns", meta.FieldByName("Namespace").String())
	labels := meta.FieldByName("Labels").Interface().(map[string]string)
	assert.Equal(t, "bar", labels["app"])
	annos := meta.FieldByName("Annotations").Interface().(map[string]string)
	assert.Equal(t, "true", annos["hint"])
}

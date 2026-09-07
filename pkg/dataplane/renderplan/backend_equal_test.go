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

package renderplan

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleBackend() Backend {
	weight := 7
	return Backend{
		Name: "be", Profile: "p", Mode: "http", GUID: "g", Balance: "roundrobin", HashType: "consistent",
		Shape: ShapeDynamic, ShapeReason: "",
		Servers: []Server{{
			Name: "SRV_1", Address: "10.0.0.1", Port: 8080, Weight: &weight, Disabled: false, GUID: "sg",
			Comment: "c", Extra: []KeywordArg{{Name: "check", Args: []string{"inter", "2s"}}},
		}},
		DefaultServer: []KeywordArg{{Name: "maxconn", Args: []string{"100"}}},
		BodyDigest:    "b", CommentsDigest: "c", RecordDigest: "r", TextDigest: "t",
		Body:         []string{"body"},
		Comments:     []string{"# c"},
		ContentKnown: true,
	}
}

// mutate changes one field of a struct value so it compares unequal to the
// original under reflect.DeepEqual.
func mutate(t *testing.T, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "x")
	case reflect.Bool:
		value.SetBool(!value.Bool())
	case reflect.Int:
		value.SetInt(value.Int() + 1)
	case reflect.Pointer:
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
			return
		}
		value.Set(reflect.Zero(value.Type()))
	case reflect.Slice:
		if value.Len() == 0 {
			value.Set(reflect.MakeSlice(value.Type(), 1, 1))
			return
		}
		value.Set(value.Slice(0, value.Len()-1))
	default:
		t.Fatalf("no mutation for kind %s", value.Kind())
	}
}

// TestBackendComparatorsCoverEveryField pins which comparator notices each
// field, so a field added to Backend or Server cannot be skipped by both.
func TestBackendComparatorsCoverEveryField(t *testing.T) {
	contentFields := map[string]bool{
		"BodyDigest": true, "CommentsDigest": true, "RecordDigest": true,
		"Body": true, "Comments": true, "ContentKnown": true,
	}
	base := sampleBackend()
	backendType := reflect.TypeFor[Backend]()
	for i := range backendType.NumField() {
		field := backendType.Field(i)
		changed := sampleBackend()
		mutate(t, reflect.ValueOf(&changed).Elem().Field(i))
		require.False(t, reflect.DeepEqual(base, changed), "field %s did not mutate", field.Name)
		switch {
		case field.Name == "TextDigest":
			require.True(t, base.EqualRecord(&changed), "TextDigest is not part of the record")
			require.True(t, base.EqualContent(&changed), "TextDigest is not part of the content")
		case contentFields[field.Name]:
			require.True(t, base.EqualRecord(&changed), "content field %s leaked into EqualRecord", field.Name)
			require.False(t, base.EqualContent(&changed), "EqualContent missed field %s", field.Name)
		default:
			require.False(t, base.EqualRecord(&changed), "EqualRecord missed field %s", field.Name)
			require.True(t, base.EqualContent(&changed), "record field %s leaked into EqualContent", field.Name)
		}
	}

	serverType := reflect.TypeFor[Server]()
	for i := range serverType.NumField() {
		field := serverType.Field(i)
		changed := sampleBackend()
		mutate(t, reflect.ValueOf(&changed.Servers[0]).Elem().Field(i))
		require.False(t, base.EqualRecord(&changed), "EqualRecord missed server field %s", field.Name)
	}

	changed := sampleBackend()
	changed.Servers[0].Extra[0].Args = append(changed.Servers[0].Extra[0].Args, "more")
	require.False(t, base.EqualRecord(&changed), "server keyword arguments are part of the record")
	changed = sampleBackend()
	changed.DefaultServer[0].Name = "other"
	require.False(t, base.EqualRecord(&changed), "default-server keywords are part of the record")
}

// TestBackendComparatorsTreatEmptyAndNilAlike pins that a list absent from
// the plan compares equal to an empty one: encoding/json drops both, so a plan
// decoded from a blob carries nil where the render carried an empty list.
func TestBackendComparatorsTreatEmptyAndNilAlike(t *testing.T) {
	left := sampleBackend()
	right := sampleBackend()
	left.Servers, right.Servers = nil, []Server{}
	left.DefaultServer, right.DefaultServer = []KeywordArg{}, nil
	left.Body, right.Body = nil, []string{}
	require.True(t, left.EqualRecord(&right))
	require.True(t, left.EqualContent(&right))

	var missing *Backend
	require.False(t, left.EqualRecord(missing))
	require.False(t, missing.EqualContent(&right))
	require.True(t, missing.EqualRecord(nil))
}

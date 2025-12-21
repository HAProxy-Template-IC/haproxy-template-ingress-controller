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

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testModel is a simple struct for testing marshaling.
type testModel struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// testV32 represents a v3.2 model type.
type testV32 struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// testV31 represents a v3.1 model type.
type testV31 struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// testV30 represents a v3.0 model type.
type testV30 struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestMarshalForVersion(t *testing.T) {
	tests := []struct {
		name    string
		model   interface{}
		wantErr bool
	}{
		{
			name: "simple struct",
			model: testModel{
				Name:  "test",
				Value: 42,
			},
			wantErr: false,
		},
		{
			name:    "nil model",
			model:   nil,
			wantErr: false,
		},
		{
			name: "nested struct",
			model: struct {
				Outer string `json:"outer"`
				Inner struct {
					Field string `json:"field"`
				} `json:"inner"`
			}{
				Outer: "outer value",
				Inner: struct {
					Field string `json:"field"`
				}{
					Field: "inner value",
				},
			},
			wantErr: false,
		},
		{
			name:    "empty map",
			model:   map[string]interface{}{},
			wantErr: false,
		},
		{
			name: "map with values",
			model: map[string]interface{}{
				"key1": "value1",
				"key2": 123,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := MarshalForVersion(tt.model)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, result)
		})
	}
}

func TestMarshalForVersion_Unmarshalable(t *testing.T) {
	// Channels cannot be marshaled to JSON
	ch := make(chan int)

	_, err := MarshalForVersion(ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal model")
}

func TestConvertToVersioned_V32(t *testing.T) {
	jsonData := []byte(`{"name":"test","value":42}`)

	result, err := ConvertToVersioned[testV32, testV31, testV30](jsonData, 2)

	require.NoError(t, err)
	require.NotNil(t, result)

	v32, ok := result.(*testV32)
	require.True(t, ok, "result should be *testV32")
	assert.Equal(t, "test", v32.Name)
	assert.Equal(t, 42, v32.Value)
}

func TestConvertToVersioned_V31(t *testing.T) {
	jsonData := []byte(`{"name":"test","value":42}`)

	result, err := ConvertToVersioned[testV32, testV31, testV30](jsonData, 1)

	require.NoError(t, err)
	require.NotNil(t, result)

	v31, ok := result.(*testV31)
	require.True(t, ok, "result should be *testV31")
	assert.Equal(t, "test", v31.Name)
	assert.Equal(t, 42, v31.Value)
}

func TestConvertToVersioned_V30(t *testing.T) {
	jsonData := []byte(`{"name":"test","value":42}`)

	result, err := ConvertToVersioned[testV32, testV31, testV30](jsonData, 0)

	require.NoError(t, err)
	require.NotNil(t, result)

	v30, ok := result.(*testV30)
	require.True(t, ok, "result should be *testV30")
	assert.Equal(t, "test", v30.Name)
	assert.Equal(t, 42, v30.Value)
}

func TestConvertToVersioned_NegativeMinor(t *testing.T) {
	jsonData := []byte(`{"name":"test","value":42}`)

	// Negative minor version should fall through to v3.0
	result, err := ConvertToVersioned[testV32, testV31, testV30](jsonData, -1)

	require.NoError(t, err)
	require.NotNil(t, result)

	v30, ok := result.(*testV30)
	require.True(t, ok, "result should be *testV30 for negative minor")
	assert.Equal(t, "test", v30.Name)
}

func TestConvertToVersioned_HighMinor(t *testing.T) {
	jsonData := []byte(`{"name":"test","value":42}`)

	// High minor version (e.g., v3.5) should use v3.2 types
	result, err := ConvertToVersioned[testV32, testV31, testV30](jsonData, 5)

	require.NoError(t, err)
	require.NotNil(t, result)

	_, ok := result.(*testV32)
	require.True(t, ok, "result should be *testV32 for minor >= 2")
}

func TestConvertToVersioned_InvalidJSON(t *testing.T) {
	tests := []struct {
		name        string
		jsonData    []byte
		minor       int
		errContains string
	}{
		{
			name:        "v3.2 invalid JSON",
			jsonData:    []byte(`{invalid}`),
			minor:       2,
			errContains: "failed to unmarshal for v3.2",
		},
		{
			name:        "v3.1 invalid JSON",
			jsonData:    []byte(`{invalid}`),
			minor:       1,
			errContains: "failed to unmarshal for v3.1",
		},
		{
			name:        "v3.0 invalid JSON",
			jsonData:    []byte(`{invalid}`),
			minor:       0,
			errContains: "failed to unmarshal for v3.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ConvertToVersioned[testV32, testV31, testV30](tt.jsonData, tt.minor)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestVersionMinorFromPtr(t *testing.T) {
	tests := []struct {
		name         string
		versionMinor *int
		want         int
	}{
		{
			name:         "nil pointer returns 0",
			versionMinor: nil,
			want:         0,
		},
		{
			name:         "pointer to 0",
			versionMinor: intPtr(0),
			want:         0,
		},
		{
			name:         "pointer to 1",
			versionMinor: intPtr(1),
			want:         1,
		},
		{
			name:         "pointer to 2",
			versionMinor: intPtr(2),
			want:         2,
		},
		{
			name:         "pointer to negative",
			versionMinor: intPtr(-1),
			want:         -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := VersionMinorFromPtr(tt.versionMinor)
			assert.Equal(t, tt.want, result)
		})
	}
}

// intPtr is a helper to create *int from int literal.
func intPtr(i int) *int {
	return &i
}

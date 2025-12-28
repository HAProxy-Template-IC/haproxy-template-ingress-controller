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
)

func TestConvertClientMetadataToAPI(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  map[string]map[string]interface{}
	}{
		{
			name:  "nil input returns nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty map returns nil",
			input: map[string]interface{}{},
			want:  nil,
		},
		{
			name: "single comment is converted",
			input: map[string]interface{}{
				"comment": "Pod: echo-server-v2",
			},
			want: map[string]map[string]interface{}{
				"comment": {"value": "Pod: echo-server-v2"},
			},
		},
		{
			name: "multiple metadata fields including comment",
			input: map[string]interface{}{
				"comment":  "server comment",
				"disabled": true,
				"weight":   100,
			},
			want: map[string]map[string]interface{}{
				"comment":  {"value": "server comment"},
				"disabled": {"value": true},
				"weight":   {"value": 100},
			},
		},
		{
			name: "non-comment metadata is preserved",
			input: map[string]interface{}{
				"custom_field": "custom value",
				"number":       42,
			},
			want: map[string]map[string]interface{}{
				"custom_field": {"value": "custom value"},
				"number":       {"value": 42},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertClientMetadataToAPI(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertAPIMetadataToClient(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]map[string]interface{}
		want  map[string]interface{}
	}{
		{
			name:  "nil input returns nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty map returns nil",
			input: map[string]map[string]interface{}{},
			want:  nil,
		},
		{
			name: "single comment",
			input: map[string]map[string]interface{}{
				"comment": {"value": "Pod: echo-server-v2"},
			},
			want: map[string]interface{}{
				"comment": "Pod: echo-server-v2",
			},
		},
		{
			name: "multiple metadata fields",
			input: map[string]map[string]interface{}{
				"comment":  {"value": "server comment"},
				"disabled": {"value": true},
				"weight":   {"value": 100},
			},
			want: map[string]interface{}{
				"comment":  "server comment",
				"disabled": true,
				"weight":   100,
			},
		},
		{
			name: "nested map without value key is ignored",
			input: map[string]map[string]interface{}{
				"comment": {"value": "has value"},
				"invalid": {"other_key": "no value key"},
			},
			want: map[string]interface{}{
				"comment": "has value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertAPIMetadataToClient(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	// Test that converting to API and back yields original
	original := map[string]interface{}{
		"custom_field": "custom value",
		"weight":       100,
	}

	api := ConvertClientMetadataToAPI(original)
	roundTripped := ConvertAPIMetadataToClient(api)

	assert.Equal(t, original, roundTripped)
}

func TestMetadataRoundTrip_WithComment(t *testing.T) {
	// Comments are included in the round-trip
	original := map[string]interface{}{
		"comment": "Pod: echo-server-v2",
		"weight":  100,
	}

	api := ConvertClientMetadataToAPI(original)
	roundTripped := ConvertAPIMetadataToClient(api)

	assert.Equal(t, original, roundTripped)
}

func TestTransformClientMetadataInJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "comment-only metadata is transformed",
			input: `{"name":"test","metadata":{"comment":"Pod: test-pod"}}`,
			want:  `{"metadata":{"comment":{"value":"Pod: test-pod"}},"name":"test"}`,
		},
		{
			name:  "comment in server metadata is transformed",
			input: `{"name":"backend","servers":{"SRV_1":{"name":"SRV_1","metadata":{"comment":"Pod: test-pod"}}}}`,
			want:  `{"name":"backend","servers":{"SRV_1":{"metadata":{"comment":{"value":"Pod: test-pod"}},"name":"SRV_1"}}}`,
		},
		{
			name:  "non-comment metadata is preserved and transformed",
			input: `{"level1":{"level2":{"metadata":{"key":"value"}}}}`,
			want:  `{"level1":{"level2":{"metadata":{"key":{"value":"value"}}}}}`,
		},
		{
			name:  "already transformed non-comment metadata is not double-transformed",
			input: `{"metadata":{"custom":{"value":"already nested"}}}`,
			want:  `{"metadata":{"custom":{"value":"already nested"}}}`,
		},
		{
			name:  "no metadata field unchanged",
			input: `{"name":"test","address":"127.0.0.1"}`,
			want:  `{"address":"127.0.0.1","name":"test"}`,
		},
		{
			name:  "empty object unchanged",
			input: `{}`,
			want:  `{}`,
		},
		{
			name:    "invalid JSON returns error",
			input:   `{invalid}`,
			wantErr: true,
		},
		{
			name:  "comment in metadata in array elements is transformed",
			input: `{"http_request_rule_list":[{"type":"add-header","metadata":{"comment":"rule1"}},{"type":"set-header","metadata":{"comment":"rule2"}}]}`,
			want:  `{"http_request_rule_list":[{"metadata":{"comment":{"value":"rule1"}},"type":"add-header"},{"metadata":{"comment":{"value":"rule2"}},"type":"set-header"}]}`,
		},
		{
			name:  "comment in deeply nested arrays with metadata is transformed",
			input: `{"backend":{"name":"test","servers":[{"name":"srv1","metadata":{"comment":"server1"}}]}}`,
			want:  `{"backend":{"name":"test","servers":[{"metadata":{"comment":{"value":"server1"}},"name":"srv1"}]}}`,
		},
		{
			name:  "mixed metadata preserves all fields including comment",
			input: `{"name":"test","metadata":{"comment":"included","custom":"preserved"}}`,
			want:  `{"metadata":{"comment":{"value":"included"},"custom":{"value":"preserved"}},"name":"test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TransformClientMetadataInJSON([]byte(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestNeedsMetadataTransformation(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		want     bool
	}{
		{
			name:     "empty map does not need transformation",
			metadata: map[string]interface{}{},
			want:     false,
		},
		{
			name: "flat string value needs transformation",
			metadata: map[string]interface{}{
				"comment": "Pod: test-pod",
			},
			want: true,
		},
		{
			name: "nested map with value key does not need transformation",
			metadata: map[string]interface{}{
				"comment": map[string]interface{}{
					"value": "Pod: test-pod",
				},
			},
			want: false,
		},
		{
			name: "nested map without value key needs transformation",
			metadata: map[string]interface{}{
				"comment": map[string]interface{}{
					"other": "Pod: test-pod",
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsMetadataTransformation(tt.metadata)
			assert.Equal(t, tt.want, got)
		})
	}
}

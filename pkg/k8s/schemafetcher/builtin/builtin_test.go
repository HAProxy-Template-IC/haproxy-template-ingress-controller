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

package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestNewFetcher_LoadsGateway verifies the embedded Gateway schema
// round-trips through filename parsing → JSON unmarshal → MapFetcher
// lookup and that the result has the property chart templates rely on
// (a metadata.namespace path resolvable to the type-converted field).
//
// This test exists primarily so a malformed JSON or a misnamed file
// trips CI at build time rather than at the next chart-author's first
// typed-access attempt. The deep schema-correctness checks live in
// pkg/k8s/typegen — here we just need the load path to work.
func TestNewFetcher_LoadsGateway(t *testing.T) {
	f, err := NewFetcher()
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "Gateway",
	}
	sch, _, err := f.Fetch(context.Background(), gvk)
	require.NoError(t, err, "embedded Gateway schema must be reachable by GVK")
	require.NotNil(t, sch)

	assert.Contains(t, sch.Properties, "metadata",
		"schema must include the metadata subtree — chart macros depend on it")
	assert.Contains(t, sch.Properties, "spec",
		"schema must include spec — typed access to spec.gatewayClassName etc. is the point")

	meta := sch.Properties["metadata"]
	assert.Contains(t, meta.Properties, "name")
	assert.Contains(t, meta.Properties, "namespace")
}

// TestParseFilename is the exhaustive table for the filename
// convention. Documents the supported and rejected forms so future
// schema additions follow the same shape.
func TestParseFilename(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		wantGVK   schema.GroupVersionKind
		wantError bool
	}{
		{
			name:     "gateway-api group with multi-segment domain",
			filename: "gateway-networking-k8s-io-v1-Gateway.json",
			wantGVK: schema.GroupVersionKind{
				Group:   "gateway.networking.k8s.io",
				Version: "v1",
				Kind:    "Gateway",
			},
		},
		{
			name:     "core group sentinel collapses to empty string",
			filename: "core-v1-Service.json",
			wantGVK: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Service",
			},
		},
		{
			name:     "single-segment group",
			filename: "apps-v1-Deployment.json",
			wantGVK: schema.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
		},
		{
			name:      "no dashes is malformed",
			filename:  "Gateway.json",
			wantError: true,
		},
		{
			name:      "only one dash is malformed (no version separator)",
			filename:  "v1-Gateway.json",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFilename(tt.filename)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantGVK, got)
		})
	}
}

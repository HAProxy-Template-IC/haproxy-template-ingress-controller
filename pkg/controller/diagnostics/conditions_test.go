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

package diagnostics

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestResourceConditionsSupportArbitraryStatusLayouts(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "routing.example.test/v1", "kind": "CustomRoutingResource",
		"metadata": map[string]any{"namespace": "tenant", "name": "route", "generation": int64(4)},
		"status": map[string]any{"arbitraryGroups": []any{map[string]any{
			"conditions": []any{map[string]any{
				"type": "Accepted", "status": "False", "reason": "MissingReference",
				"message": "payload-must-never-escape", "observedGeneration": int64(4),
			}},
		}}},
	}}
	view := resourceView(object, "custom")
	require.Equal(t, "CustomRoutingResource", view.Kind)
	require.Equal(t, int64(4), view.Generation)
	require.Equal(t, "custom", view.Watch)
	require.Len(t, view.Conditions, 1)
	require.Equal(t, "status.arbitraryGroups[0].conditions[0]", view.Conditions[0].Path)
	require.Equal(t, "MissingReference", view.Conditions[0].Reason)
	require.Equal(t, int64(4), view.Conditions[0].ObservedGeneration)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "payload-must-never-escape")
}

func TestResourceViewOmitsPayloadsAndMalformedConditions(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "OpaqueInput",
		"metadata": map[string]any{"name": "input", "annotations": map[string]any{"note": "annotation-payload"}},
		"data":     map[string]any{"password": "secret-payload"},
		"spec":     map[string]any{"content": "rendered-payload"},
		"status": map[string]any{
			"content": "status-payload",
			"conditions": []any{
				"invalid",
				map[string]any{"type": "Ready", "status": "payload-status"},
				map[string]any{"type": "Ready", "status": "True", "reason": "payload with spaces", "message": "message-payload"},
			},
		},
	}}
	view := resourceView(object, "inputs")
	require.Len(t, view.Conditions, 1)
	require.Empty(t, view.Conditions[0].Reason)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "payload")
}

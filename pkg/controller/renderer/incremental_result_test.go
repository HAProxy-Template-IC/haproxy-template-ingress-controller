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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func uniqueResult(t *testing.T, cell, key, value string) incrementalComponentResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	recorder.Unique(cell, key, value)
	result, err := recorder.result("")
	require.NoError(t, err)
	return result
}

func TestAssembleIncrementalGroupTransfersWinnerWithoutReexecution(t *testing.T) {
	instances := []incrementalInstanceResult{
		{component: "210-grpc", namespace: "default", name: "b", result: uniqueResult(t, "backends", "shared", "grpc\n")},
		{component: "200-http", namespace: "default", name: "z", result: uniqueResult(t, "backends", "shared", "http-z\n")},
		{component: "200-http", namespace: "default", name: "a", result: uniqueResult(t, "backends", "shared", "http-a\n")},
		{component: "210-grpc", namespace: "default", name: "a", result: uniqueResult(t, "backends", "grpc", "grpc-only\n")},
	}

	outputs, err := assembleIncrementalGroup(instances)
	require.NoError(t, err)
	assert.Equal(t, "http-a\n", outputs["200-http"])
	assert.Equal(t, "grpc-only\n", outputs["210-grpc"])

	outputs, err = assembleIncrementalGroup(instances[:1])
	require.NoError(t, err)
	assert.Equal(t, "grpc\n", outputs["210-grpc"])
}

func TestAssembleIncrementalGroupOrdersSourcesCanonically(t *testing.T) {
	instances := []incrementalInstanceResult{
		{
			component: "component",
			source:    "z-routes",
			namespace: "default",
			name:      "a",
			result:    incrementalComponentResult{Text: "z\n"},
		},
		{
			component: "component",
			source:    "a-routes",
			namespace: "default",
			name:      "z",
			result:    incrementalComponentResult{Text: "a\n"},
		},
	}

	outputs, err := assembleIncrementalGroup(instances)
	require.NoError(t, err)
	assert.Equal(t, "a\nz\n", outputs["component"])

	instances[0].result = uniqueResult(t, "routes", "shared", "z\n")
	instances[1].result = uniqueResult(t, "routes", "shared", "a\n")
	outputs, err = assembleIncrementalGroup(instances)
	require.NoError(t, err)
	assert.Equal(t, "a\n", outputs["component"])
}

func TestIncrementalRecorderRejectsMixedOutput(t *testing.T) {
	recorder := &incrementalRecorder{}
	recorder.Unique("cell", "key", "value")
	_, err := recorder.result("text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot mix text")
}

func TestAssembleIncrementalGroupRejectsPoisonedResults(t *testing.T) {
	tests := map[string]incrementalComponentResult{
		"mixed output": {
			Text:   "text",
			Unique: []incrementalContribution{{Cell: "cell", Key: "key", Value: "value"}},
		},
		"empty cell": {
			Unique: []incrementalContribution{{Key: "key", Value: "value"}},
		},
	}
	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := assembleIncrementalGroup([]incrementalInstanceResult{{
				component: "component",
				source:    "routes",
				namespace: "default",
				name:      "route",
				result:    result,
			}})
			require.Error(t, err)
		})
	}
}

func TestValidateIncrementalCallsRequiresCompleteCanonicalGroup(t *testing.T) {
	groups := map[string][]incrementalComponent{
		"backends": {{name: "200-http"}, {name: "210-grpc"}},
	}

	tests := map[string]struct {
		calls   []incrementalCall
		wantErr string
	}{
		"complete": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
			},
		},
		"three complete mounts in distinct roots": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
				{scope: "routes.map", component: "200-http"},
				{scope: "routes.map", component: "210-grpc"},
				{scope: "errors.http", component: "200-http"},
				{scope: "errors.http", component: "210-grpc"},
			},
		},
		"interleaved complete mounts in distinct roots": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "routes.map", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
				{scope: "routes.map", component: "210-grpc"},
			},
		},
		"missing": {
			calls:   []incrementalCall{{scope: "haproxy.cfg", component: "200-http"}},
			wantErr: "complete canonical sequences",
		},
		"reordered": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "210-grpc"},
				{scope: "haproxy.cfg", component: "200-http"},
			},
			wantErr: "canonical order",
		},
		"one sequence spans roots": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "routes.map", component: "210-grpc"},
			},
			wantErr: "complete canonical sequences",
		},
		"duplicate": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
			},
			wantErr: "canonical order",
		},
		"partial second mount": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
				{scope: "routes.map", component: "200-http"},
			},
			wantErr: "1 trailing calls",
		},
		"reordered second mount": {
			calls: []incrementalCall{
				{scope: "haproxy.cfg", component: "200-http"},
				{scope: "haproxy.cfg", component: "210-grpc"},
				{scope: "routes.map", component: "210-grpc"},
				{scope: "routes.map", component: "200-http"},
			},
			wantErr: "canonical order",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateIncrementalCalls(groups, map[string][]incrementalCall{"backends": test.calls})
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}

	t.Run("group omitted and unread", func(t *testing.T) {
		require.NoError(t, validateIncrementalCalls(groups, nil))
	})

	t.Run("group omitted but read", func(t *testing.T) {
		err := validateIncrementalCallsWithValues(groups, nil, map[string]int{"backends": 1})
		require.ErrorContains(t, err, "got 0 calls")
	})
}

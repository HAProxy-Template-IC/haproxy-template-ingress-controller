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

package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func TestRunnerRendersIncrementalComponents(t *testing.T) {
	cfg := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.io/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"route-lines": {
				Template:    `route {{ dig(item, "metadata", "namespace") }}/{{ dig(item, "metadata", "name") }}`,
				Requires:    []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "route-lines" }}`},
		ValidationTests: map[string]config.ValidationTest{
			"incremental": {
				Fixtures: map[string][]any{
					"routes": {
						map[string]any{
							"apiVersion": "example.io/v1",
							"kind":       "Route",
							"metadata": map[string]any{
								"namespace": "default",
								"name":      "second",
							},
						},
						map[string]any{
							"apiVersion": "example.io/v1",
							"kind":       "Route",
							"metadata": map[string]any{
								"namespace": "default",
								"name":      "first",
							},
						},
					},
				},
				Assertions: []config.ValidationAssertion{
					{Type: "contains", Target: "haproxy.cfg", Pattern: `route default/first`},
					{Type: "contains", Target: "haproxy.cfg", Pattern: `route default/second`},
				},
			},
		},
	}
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, helpers.EngineOptions{})
	require.NoError(t, err)
	runner := New(cfg, engine, &dataplane.ValidationPaths{}, &Options{Workers: 1})

	results, err := runner.RunTests(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, results.TestResults, 1)
	assert.True(t, results.TestResults[0].Passed, results.TestResults[0].RenderError)
	assert.Equal(t, 1, results.PassedTests)
}

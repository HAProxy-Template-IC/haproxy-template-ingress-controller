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
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func sharedAnalysisConfig(mainTemplate string, tests int) *config.Config {
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
		HAProxyConfig:   config.HAProxyConfig{Template: mainTemplate},
		ValidationTests: map[string]config.ValidationTest{},
	}
	for index := range tests {
		name := fmt.Sprintf("route-%d", index)
		cfg.ValidationTests[name] = config.ValidationTest{
			Fixtures: map[string][]any{
				"routes": {map[string]any{
					"apiVersion": "example.io/v1",
					"kind":       "Route",
					"metadata":   map[string]any{"namespace": "default", "name": name},
				}},
			},
			Assertions: []config.ValidationAssertion{
				{Type: "contains", Target: "haproxy.cfg", Pattern: "route default/" + name + "\n"},
			},
		}
	}
	return cfg
}

func runSharedAnalysisGate(t *testing.T, cfg *config.Config, functions map[string]templating.GlobalFunc) *TestResults {
	t.Helper()
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, functions, nil, nil, helpers.EngineOptions{})
	require.NoError(t, err)
	paths := &dataplane.ValidationPaths{ConfigFile: filepath.Join(t.TempDir(), "haproxy.cfg")}
	runner := New(cfg, engine, paths, &Options{Workers: 4})
	results, err := runner.RunTests(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, results.TestResults, len(cfg.ValidationTests))
	return results
}

func TestRunnerSharesOneAnalysisAcrossConcurrentTests(t *testing.T) {
	results := runSharedAnalysisGate(t, sharedAnalysisConfig("{{ render \"route-lines\" }}\n", 16), nil)

	for _, result := range results.TestResults {
		assert.True(t, result.Passed, "%s: %s %+v", result.TestName, result.RenderError, result.Assertions)
		assert.Contains(t, result.RenderedConfig, "route default/"+result.TestName+"\n",
			"each test must render its own fixtures, not another test's")
	}
}

func TestRunnerStillCatchesNondeterministicTemplateWithSharedAnalysis(t *testing.T) {
	var sequence atomic.Int64
	functions := map[string]templating.GlobalFunc{
		"next": func(...any) (any, error) { return sequence.Add(1), nil },
	}
	cfg := sharedAnalysisConfig("{{ next() }}\n{{ render \"route-lines\" }}\n", 4)
	results := runSharedAnalysisGate(t, cfg, functions)

	for _, result := range results.TestResults {
		assert.False(t, result.Passed, "%s passed despite rendering a different sequence number", result.TestName)
		var determinism *AssertionResult
		for index := range result.Assertions {
			if result.Assertions[index].Type == "deterministic" {
				determinism = &result.Assertions[index]
			}
		}
		require.NotNil(t, determinism, "%s ran no determinism check", result.TestName)
		assert.False(t, determinism.Passed, result.TestName)
		assert.Contains(t, determinism.Error, "differs between renders", result.TestName)
	}
}

type nonComparableTestEngine struct {
	templating.Engine
	values []string
}

func TestRunnerDoesNotShareAnalysisForNonComparableEngine(t *testing.T) {
	cfg := &config.Config{HAProxyConfig: config.HAProxyConfig{Template: "global\n"}}
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, helpers.EngineOptions{})
	require.NoError(t, err)
	wrapped := nonComparableTestEngine{Engine: engine, values: []string{"value"}}
	runner := New(cfg, wrapped, nil, nil)
	assert.Nil(t, runner.coldAnalysisFor(wrapped))
}

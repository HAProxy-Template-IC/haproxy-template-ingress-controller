//go:build e2e

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

// Package e2ecluster resolves the isolated kind environment used by the e2e suite.
package e2ecluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestWithControllerEnvPreservesConfiguredVariables(t *testing.T) {
	input := []byte(`controller:
  extraEnv:
    - {name: GOMAXPROCS, value: "2"}
    - {name: GOGC, value: "300"}
    - {name: GOMEMLIMIT, value: "old"}
    - name: OTHER
      valueFrom: {fieldRef: {fieldPath: metadata.name}}
  resources: {limits: {memory: 2Gi}}
`)
	updated, err := WithControllerEnv(input, "GOMEMLIMIT", "966367641")
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, yaml.Unmarshal(updated, &config))
	controller := config["controller"].(map[string]any)
	environment := controller["extraEnv"].([]any)
	require.Len(t, environment, 4)
	assert.Equal(t, map[string]any{"name": "GOMAXPROCS", "value": "2"}, environment[0])
	assert.Equal(t, map[string]any{"name": "GOGC", "value": "300"}, environment[1])
	assert.Contains(t, environment[2].(map[string]any), "valueFrom")
	assert.Equal(t, map[string]any{"name": "GOMEMLIMIT", "value": "966367641"}, environment[3])
	assert.Equal(t, map[string]any{"limits": map[string]any{"memory": "2Gi"}}, controller["resources"])
}

func TestWithControllerEnvAddsFirstVariable(t *testing.T) {
	updated, err := WithControllerEnv([]byte("controller: {}"), "GOMEMLIMIT", "1000")
	require.NoError(t, err)
	assert.Contains(t, string(updated), "name: GOMEMLIMIT")
	assert.Contains(t, string(updated), `value: "1000"`)
}

func TestWithControllerEnvRejectsMalformedValues(t *testing.T) {
	for _, input := range []string{"[", "controller: []", "controller: {extraEnv: wrong}", "controller: {extraEnv: [wrong]}"} {
		t.Run(input, func(t *testing.T) {
			_, err := WithControllerEnv([]byte(input), "GOMEMLIMIT", "1000")
			require.Error(t, err)
		})
	}
}

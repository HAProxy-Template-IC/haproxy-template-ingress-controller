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
	"fmt"

	"sigs.k8s.io/yaml"
)

// WithControllerEnv updates one environment variable without replacing its neighbours.
func WithControllerEnv(values []byte, name, value string) ([]byte, error) {
	var config map[string]any
	if err := yaml.Unmarshal(values, &config); err != nil {
		return nil, fmt.Errorf("decoding controller values: %w", err)
	}
	controller, ok := config["controller"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("controller values require a controller object")
	}
	var environment []any
	if raw, exists := controller["extraEnv"]; exists && raw != nil {
		environment, ok = raw.([]any)
		if !ok {
			return nil, fmt.Errorf("controller.extraEnv must be an array")
		}
	}
	replacement := map[string]any{"name": name, "value": value}
	result := make([]any, 0, len(environment)+1)
	for _, raw := range environment {
		variable, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("controller.extraEnv entries must be objects")
		}
		configuredName, ok := variable["name"].(string)
		if !ok || configuredName == "" {
			return nil, fmt.Errorf("controller.extraEnv entries require a name")
		}
		if configuredName != name {
			result = append(result, variable)
		}
	}
	controller["extraEnv"] = append(result, replacement)
	updated, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encoding controller values: %w", err)
	}
	return updated, nil
}

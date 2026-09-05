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

package validator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func TestValidateTemplatesRejectsAmbientIncrementalInputs(t *testing.T) {
	tests := []struct {
		name        string
		incremental coreconfig.IncrementalTemplate
		template    string
	}{
		{
			name: "component",
			incremental: coreconfig.IncrementalTemplate{
				Source: "routes",
			},
			template: `{{ now() }}`,
		},
		{
			name: "binding planner",
			incremental: coreconfig.IncrementalTemplate{
				BindingsTemplate: `{{ now() }}`,
			},
			template: `{{ tostring(item) }}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &coreconfig.Config{
				WatchedResources: map[string]coreconfig.WatchedResource{
					"routes": {},
				},
				TemplateSnippets: map[string]coreconfig.TemplateSnippet{
					"component": {
						Requires:    []string{"routes"},
						Incremental: &test.incremental,
						Template:    test.template,
					},
				},
				HAProxyConfig: coreconfig.HAProxyConfig{
					Template: `{{ render "component" }}`,
				},
			}

			errors := validateTemplates(t.Context(), cfg, stubTypeBootstrapper)

			require.Len(t, errors, 1)
			assert.Contains(t, errors[0], "now")
		})
	}
}

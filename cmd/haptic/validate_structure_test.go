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

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

func TestSetupValidationRejectsInvalidIncrementalStructure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*v1alpha1.HAProxyTemplateConfigSpec)
		want      string
	}{
		{
			name: "source must name a watched resource",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["component"] = v1alpha1.TemplateSnippet{
					Template:    "x",
					Requires:    []string{"routes"},
					Incremental: &v1alpha1.IncrementalTemplate{Source: "missing"},
				}
			},
			want: `incremental.source "missing" does not name a watched resource`,
		},
		{
			name: "source must appear in requires",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["component"] = v1alpha1.TemplateSnippet{
					Template:    "x",
					Incremental: &v1alpha1.IncrementalTemplate{Source: "routes"},
				}
			},
			want: `incremental.source "routes" must also appear in requires`,
		},
		{
			name: "unsupported effect",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["component"] = v1alpha1.TemplateSnippet{
					Template: "x",
					Incremental: &v1alpha1.IncrementalTemplate{
						BindingsTemplate: "{}",
						Effects:          []v1alpha1.IncrementalEffect{"unknown"},
					},
				}
			},
			want: `incremental.effects contains unsupported value "unknown"`,
		},
		{
			name: "duplicate effect",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["component"] = v1alpha1.TemplateSnippet{
					Template: "x",
					Incremental: &v1alpha1.IncrementalTemplate{
						BindingsTemplate: "{}",
						Effects: []v1alpha1.IncrementalEffect{
							v1alpha1.IncrementalEffectDeriveResource,
							v1alpha1.IncrementalEffectDeriveResource,
						},
					},
				}
			},
			want: `incremental.effects contains duplicate value "deriveResource"`,
		},
		{
			name: "activation path must not use a filter",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["component"] = v1alpha1.TemplateSnippet{
					Template: "x",
					Incremental: &v1alpha1.IncrementalTemplate{
						BindingsTemplate:  "{}",
						WhenAnyPathExists: []string{"spec.rules[?(@.host)].host"},
					},
				}
			},
			want: "incremental.when_any_path_exists[0]",
		},
		{
			name: "private entry point prefix",
			configure: func(spec *v1alpha1.HAProxyTemplateConfigSpec) {
				spec.TemplateSnippets["__haptic_incremental__collision"] = v1alpha1.TemplateSnippet{Template: "x"}
			},
			want: `names starting with "__haptic_incremental__" are reserved`,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := minimumOfflineValidationSpec()
			test.configure(spec)
			encoded, err := yaml.Marshal(spec)
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, encoded, 0o600))

			_, err = setupValidation(context.Background(), []string{path}, schemaSource{}, nil, logger)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func minimumOfflineValidationSpec() *v1alpha1.HAProxyTemplateConfigSpec {
	return &v1alpha1.HAProxyTemplateConfigSpec{
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"routes": {
				APIVersion: "example.io/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.name"},
			},
		},
		TemplateSnippets: map[string]v1alpha1.TemplateSnippet{},
		HAProxyConfig:    v1alpha1.HAProxyConfig{Template: "global"},
	}
}

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

package conversion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// source builds one HAProxyTemplateConfig with the given name and spec.
func source(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": expectedAPIVersion,
		"kind":       expectedKind,
		"metadata":   map[string]any{"name": name, "namespace": "haptic"},
		"spec":       spec,
	}}
}

// snippet is the shape a templateSnippets entry has on the wire.
func snippet(template string) map[string]any {
	return map[string]any{"template": template}
}

func mergedSpec(t *testing.T, sources ...*unstructured.Unstructured) map[string]any {
	t.Helper()
	result, _, err := MergeSpecs(sources)
	require.NoError(t, err)
	spec, found, err := unstructured.NestedMap(result.Object, "spec")
	require.NoError(t, err)
	require.True(t, found, "merged object must carry a spec")
	return spec
}

func TestMergeSpecs(t *testing.T) {
	tests := []struct {
		name    string
		sources []*unstructured.Unstructured
		want    map[string]any
	}{
		{
			name: "single source is identity",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{
					"haproxyConfig":    map[string]any{"template": "global\n  daemon"},
					"templateSnippets": map[string]any{"a": snippet("A")},
				}),
			},
			want: map[string]any{
				"haproxyConfig":    map[string]any{"template": "global\n  daemon"},
				"templateSnippets": map[string]any{"a": snippet("A")},
			},
		},
		{
			name: "maps deep-merge key-wise so libraries accumulate snippets",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{"a": snippet("A")}}),
				source("ssl", map[string]any{"templateSnippets": map[string]any{"b": snippet("B")}}),
				source("gateway", map[string]any{"templateSnippets": map[string]any{"c": snippet("C")}}),
			},
			want: map[string]any{"templateSnippets": map[string]any{
				"a": snippet("A"), "b": snippet("B"), "c": snippet("C"),
			}},
		},
		{
			name: "five libraries contributing the same watchedResources key merge field-wise",
			sources: []*unstructured.Unstructured{
				source("ssl", map[string]any{"watchedResources": map[string]any{
					"secrets": map[string]any{"apiVersion": "v1", "resources": "secrets"},
				}}),
				source("nginx-ingress", map[string]any{"watchedResources": map[string]any{
					"secrets": map[string]any{"indexBy": []any{"metadata.namespace"}},
				}}),
			},
			want: map[string]any{"watchedResources": map[string]any{
				"secrets": map[string]any{
					"apiVersion": "v1",
					"resources":  "secrets",
					"indexBy":    []any{"metadata.namespace"},
				},
			}},
		},
		{
			name: "later source wins on a colliding leaf",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{"a": snippet("from base")}}),
				source("overrides", map[string]any{"templateSnippets": map[string]any{"a": snippet("from operator")}}),
			},
			want: map[string]any{"templateSnippets": map[string]any{"a": snippet("from operator")}},
		},
		{
			name: "plain lists replace rather than accumulate",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"watchedResourcesIgnoreFields": []any{"metadata.managedFields"}}),
				source("overrides", map[string]any{"watchedResourcesIgnoreFields": []any{"metadata.resourceVersion"}}),
			},
			want: map[string]any{"watchedResourcesIgnoreFields": []any{"metadata.resourceVersion"}},
		},
		{
			name: "validators are not special-cased: the last non-empty list wins",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{}),
				source("overrides", map[string]any{"validators": []any{
					map[string]any{"name": "spoa-hub", "socketPath": "/run/v.sock"},
				}}),
			},
			want: map[string]any{"validators": []any{
				map[string]any{"name": "spoa-hub", "socketPath": "/run/v.sock"},
			}},
		},
		{
			name: "migrationCoverage accumulates across every contributing library, operator last",
			sources: []*unstructured.Unstructured{
				source("haproxytech", map[string]any{"migrationCoverage": []any{map[string]any{"source": "haproxytech"}}}),
				source("haproxy-ingress", map[string]any{"migrationCoverage": []any{map[string]any{"source": "haproxy-ingress"}}}),
				source("nginx-ingress", map[string]any{"migrationCoverage": []any{map[string]any{"source": "ingress-nginx"}}}),
				source("overrides", map[string]any{"migrationCoverage": []any{map[string]any{"source": "custom"}}}),
			},
			want: map[string]any{"migrationCoverage": []any{
				map[string]any{"source": "haproxytech"},
				map[string]any{"source": "haproxy-ingress"},
				map[string]any{"source": "ingress-nginx"},
				map[string]any{"source": "custom"},
			}},
		},
		{
			name: "two libraries contributing validationTests._global fixtures coexist",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"validationTests": map[string]any{
					"_global": map[string]any{"fixtures": map[string]any{
						"services": []any{map[string]any{"kind": "Service"}},
					}},
				}}),
				source("ssl", map[string]any{"validationTests": map[string]any{
					"_global": map[string]any{"fixtures": map[string]any{
						"secrets": []any{map[string]any{"kind": "Secret"}},
					}},
				}}),
			},
			want: map[string]any{"validationTests": map[string]any{
				"_global": map[string]any{"fixtures": map[string]any{
					"services": []any{map[string]any{"kind": "Service"}},
					"secrets":  []any{map[string]any{"kind": "Secret"}},
				}},
			}},
		},
		{
			name: "a source with no spec contributes nothing",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{"a": snippet("A")}}),
				{Object: map[string]any{
					"apiVersion": expectedAPIVersion,
					"kind":       expectedKind,
					"metadata":   map[string]any{"name": "empty"},
				}},
			},
			want: map[string]any{"templateSnippets": map[string]any{"a": snippet("A")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mergedSpec(t, tt.sources...))
		})
	}
}

func TestMergeSpecs_Errors(t *testing.T) {
	tests := []struct {
		name    string
		sources []*unstructured.Unstructured
		wantErr string
	}{
		{
			name:    "no sources",
			sources: nil,
			wantErr: "no HAProxyTemplateConfig sources to merge",
		},
		{
			name: "wrong kind names the offending object",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{}),
				{Object: map[string]any{
					"apiVersion": expectedAPIVersion,
					"kind":       "ConfigMap",
					"metadata":   map[string]any{"name": "not-a-config"},
				}},
			},
			wantErr: "not-a-config: expected HAProxyTemplateConfig, got ConfigMap",
		},
		{
			name: "wrong apiVersion names the offending object",
			sources: []*unstructured.Unstructured{
				{Object: map[string]any{
					"apiVersion": "haproxy-haptic.org/v1beta1",
					"kind":       expectedKind,
					"metadata":   map[string]any{"name": "from-the-future"},
				}},
			},
			wantErr: "from-the-future: expected apiVersion haproxy-haptic.org/v1alpha1, got haproxy-haptic.org/v1beta1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := MergeSpecs(tt.sources)
			require.Error(t, err)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

// The merged object is what status write-back and events are keyed on, so it
// must carry the operator's identity, not the first library's.
func TestMergeSpecs_IdentityComesFromTheLastSource(t *testing.T) {
	result, _, err := MergeSpecs([]*unstructured.Unstructured{
		source("haptic-config-00-base", map[string]any{}),
		source("haptic-config", map[string]any{}),
	})
	require.NoError(t, err)

	assert.Equal(t, "haptic-config", result.GetName())
	assert.Equal(t, "haptic", result.GetNamespace())
	assert.Equal(t, expectedKind, result.GetKind())
	assert.Equal(t, expectedAPIVersion, result.GetAPIVersion())
}

// The sources are objects held by the informer cache; merging must not write
// through to them.
func TestMergeSpecs_DoesNotMutateSources(t *testing.T) {
	base := source("base", map[string]any{
		"templateSnippets":  map[string]any{"a": snippet("A")},
		"migrationCoverage": []any{map[string]any{"source": "haproxytech"}},
	})
	overrides := source("overrides", map[string]any{
		"templateSnippets": map[string]any{"a": snippet("overridden")},
	})

	_, _, err := MergeSpecs([]*unstructured.Unstructured{base, overrides})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"templateSnippets":  map[string]any{"a": snippet("A")},
		"migrationCoverage": []any{map[string]any{"source": "haproxytech"}},
	}, base.Object["spec"], "the first source must be untouched")
}

func TestMergeSpecs_SnippetOverrides(t *testing.T) {
	tests := []struct {
		name    string
		sources []*unstructured.Unstructured
		want    []SnippetOverride
	}{
		{
			name: "distinct snippet names report nothing",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{"a": snippet("A")}}),
				source("ssl", map[string]any{"templateSnippets": map[string]any{"b": snippet("B")}}),
			},
			want: nil,
		},
		{
			name: "an operator override is reported with both source names",
			sources: []*unstructured.Unstructured{
				source("haptic-config-00-base", map[string]any{"templateSnippets": map[string]any{
					"global-settings-100-logging": snippet("log stdout"),
				}}),
				source("haptic-config", map[string]any{"templateSnippets": map[string]any{
					"global-settings-100-logging": snippet("log 127.0.0.1"),
				}}),
			},
			want: []SnippetOverride{{
				Name:           "global-settings-100-logging",
				PreviousSource: "haptic-config-00-base",
				WinningSource:  "haptic-config",
			}},
		},
		{
			name: "a three-way collision reports each hop, sorted by name",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{
					"x": snippet("1"), "y": snippet("1"),
				}}),
				source("ssl", map[string]any{"templateSnippets": map[string]any{
					"x": snippet("2"), "y": snippet("2"),
				}}),
				source("gateway", map[string]any{"templateSnippets": map[string]any{"x": snippet("3")}}),
			},
			want: []SnippetOverride{
				{Name: "x", PreviousSource: "base", WinningSource: "ssl"},
				{Name: "y", PreviousSource: "base", WinningSource: "ssl"},
				{Name: "x", PreviousSource: "ssl", WinningSource: "gateway"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, overrides, err := MergeSpecs(tt.sources)
			require.NoError(t, err)
			assert.Equal(t, tt.want, overrides)
		})
	}
}

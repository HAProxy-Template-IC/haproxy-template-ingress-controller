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

func TestMergeSpecs_SpecOverrides(t *testing.T) {
	tests := []struct {
		name    string
		sources []*unstructured.Unstructured
		want    []SpecOverride
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
			want: []SpecOverride{{
				Section:        "templateSnippets",
				Name:           "global-settings-100-logging",
				PreviousSource: "haptic-config-00-base",
				WinningSource:  "haptic-config",
			}},
		},
		{
			name: "the last source may override entries from several sections at once",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{
					"haproxyConfig": map[string]any{"template": "global"},
					"maps":          map[string]any{"host.map": map[string]any{"template": "A"}},
				}),
				source("haptic-config", map[string]any{
					"haproxyConfig": map[string]any{"template": "global\n  daemon"},
					"maps":          map[string]any{"host.map": map[string]any{"template": "B"}},
				}),
			},
			want: []SpecOverride{
				{Section: "maps", Name: "host.map", PreviousSource: "base", WinningSource: "haptic-config"},
				{Section: "haproxyConfig", Name: "haproxyConfig", PreviousSource: "base", WinningSource: "haptic-config"},
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

// An override replaces the entry. Left to mergo, two same-named entries
// deep-merge: an operator overriding a library file but omitting a sub-field
// the library set would inherit that field silently — a hybrid neither author
// wrote, with nothing logged.
func TestMergeSpecs_AnOverrideReplacesTheEntryOutright(t *testing.T) {
	merged, overrides, err := MergeSpecs([]*unstructured.Unstructured{
		source("base", map[string]any{
			"files": map[string]any{"custom.lua": map[string]any{
				"template":     "-- library body",
				"languageHint": "lua",
			}},
			"haproxyConfig": map[string]any{
				"template":       "global",
				"postProcessing": []any{map[string]any{"type": "regex_replace"}},
			},
		}),
		source("haptic-config", map[string]any{
			"files":         map[string]any{"custom.lua": map[string]any{"template": "-- operator body"}},
			"haproxyConfig": map[string]any{"template": "global\n  daemon"},
		}),
	})
	require.NoError(t, err)
	require.Len(t, overrides, 2)

	file, _, err := unstructured.NestedMap(merged.Object, "spec", "files", "custom.lua")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"template": "-- operator body"}, file,
		"the library's languageHint must not survive into the operator's override")

	cfg, _, err := unstructured.NestedMap(merged.Object, "spec", "haproxyConfig")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"template": "global\n  daemon"}, cfg,
		"the library's postProcessing must not survive into the operator's haproxyConfig")
}

// A collision anywhere before the last source is an error, not a log line:
// with N chart shards, mergo's silent later-wins would let two libraries
// swallow each other's entries with nothing to show for it.
func TestMergeSpecs_DuplicateNamesAmongShardsAreErrors(t *testing.T) {
	tests := []struct {
		name    string
		sources []*unstructured.Unstructured
		wantErr []string
	}{
		{
			name: "two shards defining one snippet, a third source after them",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"templateSnippets": map[string]any{"x": snippet("1")}}),
				source("ssl", map[string]any{"templateSnippets": map[string]any{"x": snippet("2")}}),
				source("haptic-config", map[string]any{}),
			},
			wantErr: []string{"templateSnippets", `"x"`, "base", "ssl"},
		},
		{
			name: "two shards defining the same map file",
			sources: []*unstructured.Unstructured{
				source("ingress", map[string]any{"maps": map[string]any{"host.map": map[string]any{"template": "A"}}}),
				source("gateway", map[string]any{"maps": map[string]any{"host.map": map[string]any{"template": "B"}}}),
				source("haptic-config", map[string]any{}),
			},
			wantErr: []string{"maps", `"host.map"`, "ingress", "gateway"},
		},
		{
			name: "two shards both carrying haproxyConfig",
			sources: []*unstructured.Unstructured{
				source("base", map[string]any{"haproxyConfig": map[string]any{"template": "global"}}),
				source("rogue", map[string]any{"haproxyConfig": map[string]any{"template": "defaults"}}),
				source("haptic-config", map[string]any{}),
			},
			wantErr: []string{"haproxyConfig", "base", "rogue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := MergeSpecs(tt.sources)
			require.Error(t, err)
			for _, fragment := range tt.wantErr {
				assert.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// Regression for the merge this function exists to prevent: under plain mergo,
// two sources sharing a test name produced a hybrid — the later source's
// description and assertions over the earlier source's surviving fixtures —
// with err=nil and nothing reported. Reproduced against the real MergeSpecs
// before this union existed.
func TestMergeSpecs_DuplicateValidationTestIsAnError(t *testing.T) {
	a := source("shard-a", map[string]any{"validationTests": map[string]any{
		"test-dup": map[string]any{
			"description": "A's description",
			"fixtures":    map[string]any{"ingresses": []any{map[string]any{"kind": "Ingress"}}},
			"assertions":  []any{map[string]any{"type": "contains", "pattern": "A"}},
		},
	}})
	b := source("shard-b", map[string]any{"validationTests": map[string]any{
		"test-dup": map[string]any{
			"description": "B's description",
			"assertions":  []any{map[string]any{"type": "contains", "pattern": "B"}},
		},
	}})

	_, _, err := MergeSpecs([]*unstructured.Unstructured{a, b})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"test-dup"`)
	assert.Contains(t, err.Error(), "HAProxyTemplateConfig/shard-a")
	assert.Contains(t, err.Error(), "HAProxyTemplateConfig/shard-b")
}

// The duplicate-test error has no positional exemption: overriding a test
// silently weakens the suite both gates run, which is RULE #2 territory. An
// operator wanting different behaviour writes a differently-named test.
func TestMergeSpecs_LastSourceMayNotOverrideAValidationTest(t *testing.T) {
	_, _, err := MergeSpecs([]*unstructured.Unstructured{
		source("base", map[string]any{"validationTests": map[string]any{
			"test-x": map[string]any{"description": "bundled"},
		}}),
		source("haptic-config", map[string]any{"validationTests": map[string]any{
			"test-x": map[string]any{"description": "operator"},
		}}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"test-x"`)
}

// _global is the shared baseline several libraries each contribute part of:
// fixtures accumulate in source order rather than replacing, and a scalar two
// sources set to different values is a conflict, not a merge.
func TestMergeSpecs_GlobalBaselineAccumulatesAcrossShards(t *testing.T) {
	merged := mergedSpec(t,
		source("shard-a", map[string]any{"validationTests": map[string]any{
			"_global": map[string]any{"fixtures": map[string]any{
				"ingresses": []any{map[string]any{"name": "from-a"}},
			}},
		}}),
		source("shard-b", map[string]any{"validationTests": map[string]any{
			"_global": map[string]any{"fixtures": map[string]any{
				"ingresses": []any{map[string]any{"name": "from-b"}},
			}},
		}}),
	)

	fixtures, _, err := unstructured.NestedSlice(
		map[string]any{"spec": merged}, "spec", "validationTests", "_global", "fixtures", "ingresses")
	require.NoError(t, err)
	require.Len(t, fixtures, 2, "_global fixture lists must accumulate, not replace")
	assert.Equal(t, map[string]any{"name": "from-a"}, fixtures[0])
	assert.Equal(t, map[string]any{"name": "from-b"}, fixtures[1])
}

func TestMergeSpecs_GlobalScalarConflictIsAnError(t *testing.T) {
	_, _, err := MergeSpecs([]*unstructured.Unstructured{
		source("shard-a", map[string]any{"validationTests": map[string]any{
			"_global": map[string]any{"minHAProxyVersion": "3.0"},
		}}),
		source("shard-b", map[string]any{"validationTests": map[string]any{
			"_global": map[string]any{"minHAProxyVersion": "3.1"},
		}}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minHAProxyVersion")
	assert.Contains(t, err.Error(), "HAProxyTemplateConfig/shard-a")
	assert.Contains(t, err.Error(), "HAProxyTemplateConfig/shard-b")
}

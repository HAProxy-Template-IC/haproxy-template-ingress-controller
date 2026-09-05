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

package templating

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bindingPlannerVisibleStruct struct {
	Value  float64
	hidden int
}

func newBindingPlannerEngine(t *testing.T, source string) *ScriggoEngine {
	t.Helper()
	engine, err := New(map[string]string{"planner": source}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
	})
	require.NoError(t, err)
	return engine
}

func TestIncrementalBindingPlannerReturnsCanonicalJSON(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)
	extraContext := map[string]any{
		"z": map[string]any{"second": 2, "first": 1},
		"a": []any{"value"},
	}

	for range 20 {
		result, err := engine.RenderIncrementalBindings(t.Context(), "planner", extraContext)
		require.NoError(t, err)
		assert.Equal(t, `{"a":["value"],"z":{"first":1,"second":2}}`, string(result))
	}
}

func TestIncrementalBindingInputSnapshotIsExactPrivateAndAuthenticated(t *testing.T) {
	engine, err := New(map[string]string{
		"first":  `{{ toJSON(map[string]any{"routes": extraContext["selected"]}) }}`,
		"second": `{{ toJSON(map[string]any{"routes": runtimeEnvironment.GOMAXPROCS}) }}`,
	}, &Options{
		EntryPoints:                   []string{"first", "second"},
		IncrementalBindingEntryPoints: []string{"first", "second"},
	})
	require.NoError(t, err)
	contextValue := map[string]any{
		"extraContext":       map[string]any{"selected": map[string]any{"value": 1}},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
	}
	snapshot, err := engine.SnapshotIncrementalBindingInputs([]string{"second", "first"}, contextValue)
	require.NoError(t, err)

	contextValue["extraContext"].(map[string]any)["selected"].(map[string]any)["value"] = 2
	contextValue["runtimeEnvironment"].(*RuntimeEnvironment).GOMAXPROCS = 8
	result, err := engine.RenderIncrementalBindingsSnapshot(t.Context(), "first", snapshot)
	require.NoError(t, err)
	assert.JSONEq(t, `{"routes":{"value":1}}`, string(result))
	result, err = engine.RenderIncrementalBindingsSnapshot(t.Context(), "second", snapshot)
	require.NoError(t, err)
	assert.JSONEq(t, `{"routes":4}`, string(result))

	equal, err := engine.SnapshotIncrementalBindingInputs([]string{"first", "second"}, map[string]any{
		"extraContext":       map[string]any{"selected": map[string]any{"value": 1}},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
	})
	require.NoError(t, err)
	assert.True(t, snapshot.Equal(equal))
	assert.True(t, engine.MatchIncrementalBindingInputs([]string{"second", "first"}, map[string]any{
		"extraContext":       map[string]any{"selected": map[string]any{"value": 1}},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
		"controller":         make(chan struct{}),
	}, snapshot))
	typeChanged, err := engine.SnapshotIncrementalBindingInputs([]string{"first", "second"}, map[string]any{
		"extraContext":       map[string]any{"selected": map[string]any{"value": float64(1)}},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
	})
	require.NoError(t, err)
	assert.False(t, snapshot.Equal(typeChanged))
	assert.False(t, engine.MatchIncrementalBindingInputs([]string{"first", "second"}, map[string]any{
		"extraContext":       map[string]any{"selected": map[string]any{"value": float64(1)}},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
	}, snapshot))

	other := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)
	_, err = other.RenderIncrementalBindingsSnapshot(t.Context(), "planner", snapshot)
	require.ErrorContains(t, err, "no matching input snapshot")

	missing := newBindingPlannerEngine(t, `{{ toJSON(map[string]any{"routes": capabilities["feature"]}) }}`)
	missingSnapshot, err := missing.SnapshotIncrementalBindingInputs([]string{"planner"}, nil)
	require.NoError(t, err)
	assert.True(t, missing.MatchIncrementalBindingInputs([]string{"planner"}, nil, missingSnapshot))
	assert.False(t, missing.MatchIncrementalBindingInputs([]string{"planner"}, map[string]any{
		"extraContext": map[string]any{},
		"capabilities": map[string]any{"feature": true},
	}, missingSnapshot))
}

func TestIncrementalBindingInputSnapshotMatchesPlannerVisibleStructState(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)
	snapshot, err := engine.SnapshotIncrementalBindingInputs([]string{"planner"}, map[string]any{
		"extraContext": map[string]any{
			"value": bindingPlannerVisibleStruct{Value: 0, hidden: 1},
		},
	})
	require.NoError(t, err)

	assert.True(t, engine.MatchIncrementalBindingInputs([]string{"planner"}, map[string]any{
		"extraContext": map[string]any{
			"value": bindingPlannerVisibleStruct{Value: 0, hidden: 2},
		},
	}, snapshot))
	assert.False(t, engine.MatchIncrementalBindingInputs([]string{"planner"}, map[string]any{
		"extraContext": map[string]any{
			"value": bindingPlannerVisibleStruct{Value: 1, hidden: 1},
		},
	}, snapshot))
	assert.False(t, engine.MatchIncrementalBindingInputs([]string{"planner"}, map[string]any{
		"extraContext": map[string]any{
			"value": bindingPlannerVisibleStruct{Value: math.Copysign(0, -1), hidden: 1},
		},
	}, snapshot))
}

func TestIncrementalBindingPlannerAllowsLocalMutation(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{%%
var bindings = map[string]any{}
bindings["routes"] = map[string]any{"rules": toSlice(extraContext["rules"])}
%%}{{ toJSON(bindings) }}`)

	result, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"rules": []any{map[string]any{"name": "required"}},
	})
	require.NoError(t, err)
	assert.Equal(t, `{"routes":{"rules":[{"name":"required"}]}}`, string(result))
}

func TestIncrementalBindingPlannerRejectsContextMutation(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{% extraContext["value"] = "changed" %}{{ toJSON(extraContext) }}`)
	extraContext := map[string]any{
		"value":  "original",
		"nested": map[string]any{"enabled": true},
	}

	_, err := engine.RenderIncrementalBindings(t.Context(), "planner", extraContext)
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", extraContext["value"])
	assert.Equal(t, map[string]any{"enabled": true}, extraContext["nested"])
}

func TestIncrementalBindingPlannerSelectsStableAmbientInputs(t *testing.T) {
	engine, err := New(map[string]string{
		"planner": `{{ toJSON(map[string]any{
  "capability": capabilities["feature"],
  "file": currentFiles["state"],
  "gomaxprocs": runtimeEnvironment.GOMAXPROCS,
  "path": pathResolver.GetBaseDir(),
  "snippets": templateSnippets,
  "value": extraContext["value"],
}) }}`,
	}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
		Declarations: map[string]any{
			"currentFiles": (*map[string]string)(nil),
		},
	})
	require.NoError(t, err)
	files := map[string]string{"state": "current"}

	result, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"extraContext":       map[string]any{"value": "selected"},
		"capabilities":       map[string]any{"feature": true},
		"currentFiles":       &files,
		"pathResolver":       &PathResolver{BaseDir: "/etc/haproxy"},
		"runtimeEnvironment": &RuntimeEnvironment{GOMAXPROCS: 4},
		"templateSnippets":   []string{"a", "b"},
		"controller":         make(chan struct{}),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
  "capability": true,
  "file": "current",
  "gomaxprocs": 4,
  "path": "/etc/haproxy",
  "snippets": ["a", "b"],
  "value": "selected"
}`, string(result))
}

func TestIncrementalBindingPlannerRejectsStableAmbientMutation(t *testing.T) {
	engine, err := New(map[string]string{
		"planner": `{% currentFiles["state"] = "changed" %}{}`,
	}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
		Declarations: map[string]any{
			"currentFiles": (*map[string]string)(nil),
		},
	})
	require.NoError(t, err)
	files := map[string]string{"state": "current"}

	_, err = engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"extraContext": map[string]any{},
		"currentFiles": &files,
	})
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "current", files["state"])
}

func TestIncrementalBindingPlannerProjectsUsedAmbientInputs(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)
	calls := 0
	largeCurrentConfig := make(map[string]any, 10_000)
	for index := range 10_000 {
		largeCurrentConfig[fmt.Sprintf("entry-%d", index)] = index
	}
	largeCurrentConfig["undetachable"] = make(chan struct{})
	largeCurrentConfig["marshaler"] = incrementalNativeCustomMarshaler{calls: &calls}

	result, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"extraContext":  map[string]any{"selected": "value"},
		"capabilities":  make(chan struct{}),
		"currentConfig": &largeCurrentConfig,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"selected":"value"}`, string(result))
	assert.Zero(t, calls)
}

func TestIncrementalBindingPlannerIncludesDependencyInputs(t *testing.T) {
	engine, err := New(map[string]string{
		"planner": `{%- import "imported" for Imported -%}` +
			`{"imported":"{{- Imported() -}}","rendered":{{- render "rendered" -}}}`,
		"imported": `{% macro Imported() string %}{{ templateSnippets[0] }}{% end %}`,
		"rendered": `{{ toJSON(currentConfig["value"]) }}`,
	}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
		Declarations: map[string]any{
			"currentConfig": (*map[string]any)(nil),
		},
	})
	require.NoError(t, err)
	currentConfig := map[string]any{"value": "from-render"}

	result, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"extraContext":     map[string]any{},
		"currentConfig":    &currentConfig,
		"templateSnippets": []string{"from-import"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"imported":"from-import","rendered":"from-render"}`, string(result))
	assert.ElementsMatch(t,
		[]string{"currentConfig", "templateSnippets"},
		engine.incrementalBindingInputs["planner"],
	)
}

func TestIncrementalBindingPlannerPreservesMissingInputDefaults(t *testing.T) {
	engine, err := New(map[string]string{
		"planner": `{{ toJSON(map[string]any{
  "capabilities": len(capabilities),
  "currentConfig": isNil(currentConfig),
  "currentFiles": len(currentFiles),
  "extraContext": len(extraContext),
  "pathResolver": isNil(pathResolver),
  "runtimeEnvironment": isNil(runtimeEnvironment),
  "templateSnippets": len(templateSnippets),
}) }}`,
	}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
		Declarations: map[string]any{
			"currentConfig": (*map[string]any)(nil),
			"currentFiles":  (*map[string]string)(nil),
		},
	})
	require.NoError(t, err)

	result, err := engine.RenderIncrementalBindings(t.Context(), "planner", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{
  "capabilities": 0,
  "currentConfig": true,
  "currentFiles": 0,
  "extraContext": 0,
  "pathResolver": false,
  "runtimeEnvironment": false,
  "templateSnippets": 0
}`, string(result))
}

func TestIncrementalBindingPlannerClonesEveryUsedAmbientInput(t *testing.T) {
	extraContext := map[string]any{"nested": map[string]any{"value": "original"}}
	capabilities := map[string]any{"feature": "original"}
	currentConfigValue := map[string]any{"value": "original"}
	currentConfig := &currentConfigValue
	currentFilesValue := map[string]string{"state": "original"}
	currentFiles := &currentFilesValue
	pathResolver := &PathResolver{BaseDir: "original"}
	runtimeEnvironment := &RuntimeEnvironment{GOMAXPROCS: 4}
	templateSnippets := []string{"original"}

	detached, err := detachIncrementalBindingContext(map[string]any{
		"extraContext":       extraContext,
		"capabilities":       capabilities,
		"currentConfig":      currentConfig,
		"currentFiles":       currentFiles,
		"pathResolver":       pathResolver,
		"runtimeEnvironment": runtimeEnvironment,
		"templateSnippets":   templateSnippets,
	}, incrementalBindingContextNames[:])
	require.NoError(t, err)

	detached["extraContext"].(map[string]any)["nested"].(map[string]any)["value"] = "changed"
	detached["capabilities"].(map[string]any)["feature"] = "changed"
	(*detached["currentConfig"].(*map[string]any))["value"] = "changed"
	(*detached["currentFiles"].(*map[string]string))["state"] = "changed"
	detached["pathResolver"].(*PathResolver).BaseDir = "changed"
	detached["runtimeEnvironment"].(*RuntimeEnvironment).GOMAXPROCS = 8
	detached["templateSnippets"].([]string)[0] = "changed"

	assert.Equal(t, "original", extraContext["nested"].(map[string]any)["value"])
	assert.Equal(t, "original", capabilities["feature"])
	assert.Equal(t, "original", currentConfigValue["value"])
	assert.Equal(t, "original", currentFilesValue["state"])
	assert.Equal(t, "original", pathResolver.BaseDir)
	assert.Equal(t, 4, runtimeEnvironment.GOMAXPROCS)
	assert.Equal(t, "original", templateSnippets[0])
}

func TestIncrementalBindingPlannerGuardsEveryUsedAmbientInput(t *testing.T) {
	tests := map[string]struct {
		source string
		setup  func() (map[string]any, func(*testing.T))
	}{
		"extra context": {
			source: `{% extraContext["value"] = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := map[string]any{"value": "original"}
				return map[string]any{"extraContext": value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value["value"])
				}
			},
		},
		"capabilities": {
			source: `{% capabilities["feature"] = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := map[string]any{"feature": "original"}
				return map[string]any{"extraContext": map[string]any{}, "capabilities": value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value["feature"])
				}
			},
		},
		"current config": {
			source: `{% currentConfig["value"] = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := map[string]any{"value": "original"}
				return map[string]any{"extraContext": map[string]any{}, "currentConfig": &value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value["value"])
				}
			},
		},
		"current files": {
			source: `{% currentFiles["state"] = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := map[string]string{"state": "original"}
				return map[string]any{"extraContext": map[string]any{}, "currentFiles": &value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value["state"])
				}
			},
		},
		"path resolver": {
			source: `{% pathResolver.BaseDir = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := &PathResolver{BaseDir: "original"}
				return map[string]any{"extraContext": map[string]any{}, "pathResolver": value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value.BaseDir)
				}
			},
		},
		"runtime environment": {
			source: `{% runtimeEnvironment.GOMAXPROCS = 8 %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := &RuntimeEnvironment{GOMAXPROCS: 4}
				return map[string]any{"extraContext": map[string]any{}, "runtimeEnvironment": value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, 4, value.GOMAXPROCS)
				}
			},
		},
		"template snippets": {
			source: `{% templateSnippets[0] = "changed" %}{}`,
			setup: func() (map[string]any, func(*testing.T)) {
				value := []string{"original"}
				return map[string]any{"extraContext": map[string]any{}, "templateSnippets": value}, func(t *testing.T) {
					t.Helper()
					assert.Equal(t, "original", value[0])
				}
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"planner": test.source}, &Options{
				EntryPoints:                   []string{"planner"},
				IncrementalBindingEntryPoints: []string{"planner"},
				Declarations: map[string]any{
					"currentConfig": (*map[string]any)(nil),
					"currentFiles":  (*map[string]string)(nil),
				},
			})
			require.NoError(t, err)
			bindingContext, verify := test.setup()

			_, err = engine.RenderIncrementalBindings(t.Context(), "planner", bindingContext)
			require.ErrorContains(t, err, "mutates an immutable input")
			verify(t)
		})
	}
}

func TestIncrementalBindingPlannerRejectsAmbientDeclarations(t *testing.T) {
	tests := map[string]string{
		"item":              `{{ toJSON(item) }}`,
		"shared":            `{{ toJSON(shared.Get("value")) }}`,
		"http":              `{{ toJSON(http.Fetch("https://example.test")) }}`,
		"controller":        `{{ toJSON(controller) }}`,
		"resources":         `{{ toJSON(resources) }}`,
		"source":            `{{ toJSON(source) }}`,
		"props":             `{{ toJSON(props) }}`,
		"render subject":    `{{ toJSON(renderSubject) }}`,
		"render mode":       `{{ toJSON(renderMode) }}`,
		"admission subject": `{{ toJSON(admissionSubject) }}`,
		"derive resource":   `{{ deriveResource("routes", map[string]any{}, "metadata.name", "derived") }}`,
		"first seen":        `{{ toJSON(first_seen("value")) }}`,
		"record event":      `{{ recordEvent(map[string]any{}, "Rejected", "message") }}`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(map[string]string{"planner": source}, &Options{
				EntryPoints:                   []string{"planner"},
				IncrementalBindingEntryPoints: []string{"planner"},
			})
			require.Error(t, err)
		})
	}
}

func TestIncrementalBindingPlannerRejectsInvalidOutput(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"array":         "[]",
		"null":          "null",
		"two objects":   "{} {}",
		"trailing text": "{} invalid",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			engine := newBindingPlannerEngine(t, source)
			_, err := engine.RenderIncrementalBindings(t.Context(), "planner", nil)
			require.ErrorContains(t, err, "incremental binding planner")
		})
	}
}

func TestIncrementalBindingPlannerRejectsUndetachableContext(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)

	_, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"channel": make(chan struct{}),
	})
	require.ErrorContains(t, err, "detaching incremental binding planner")
}

func TestIncrementalBindingPlannerRejectsCustomMarshalerWithoutCallingIt(t *testing.T) {
	engine := newBindingPlannerEngine(t, `{{ toJSON(extraContext) }}`)
	calls := 0

	_, err := engine.RenderIncrementalBindings(t.Context(), "planner", map[string]any{
		"custom": incrementalNativeCustomMarshaler{calls: &calls},
	})
	require.ErrorContains(t, err, "uses a custom marshaler")
	assert.Zero(t, calls)
}

func TestIncrementalBindingPlannerMustBeExplicitAndDistinct(t *testing.T) {
	_, err := New(map[string]string{"planner": "{}"}, &Options{
		EntryPoints:                   []string{"ordinary"},
		IncrementalBindingEntryPoints: []string{"planner"},
	})
	require.EqualError(t, err, `incremental binding entry point "planner" is not in EntryPoints`)

	_, err = New(map[string]string{"planner": "{}"}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalEntryPoints:        []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
	})
	require.EqualError(t, err, `entry point "planner" cannot be both an incremental component and binding planner`)
}

func TestRenderIncrementalBindingsRejectsOrdinaryTemplate(t *testing.T) {
	engine, err := New(map[string]string{"ordinary": "{}"}, &Options{
		EntryPoints: []string{"ordinary"},
	})
	require.NoError(t, err)

	_, err = engine.RenderIncrementalBindings(t.Context(), "ordinary", nil)
	require.EqualError(t, err, `template "ordinary" is not an incremental binding planner`)
}

func TestOrdinaryRenderRejectsPrivateIncrementalEntryPoints(t *testing.T) {
	engine, err := New(map[string]string{
		"component": "component",
		"planner":   "{}",
	}, &Options{
		EntryPoints:                   []string{"component", "planner"},
		IncrementalEntryPoints:        []string{"component"},
		IncrementalBindingEntryPoints: []string{"planner"},
	})
	require.NoError(t, err)

	for _, name := range []string{"component", "planner"} {
		t.Run(name, func(t *testing.T) {
			_, err := engine.Render(t.Context(), name, nil)
			require.EqualError(t, err, `template "`+name+`" is a private incremental entry point`)

			_, _, err = engine.RenderWithSourceMap(t.Context(), name, nil)
			require.EqualError(t, err, `template "`+name+`" is a private incremental entry point`)
		})
	}
}

func BenchmarkIncrementalBindingPlannerUnusedCurrentConfig(b *testing.B) {
	engine, err := New(map[string]string{
		"planner": `{{ toJSON(extraContext) }}`,
	}, &Options{
		EntryPoints:                   []string{"planner"},
		IncrementalBindingEntryPoints: []string{"planner"},
		Declarations: map[string]any{
			"currentConfig": (*map[string]any)(nil),
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{0, 100_000} {
		currentConfig := make(map[string]any, size)
		for index := range size {
			currentConfig[fmt.Sprintf("entry-%d", index)] = index
		}
		bindingContext := map[string]any{
			"extraContext":  map[string]any{"value": "stable"},
			"currentConfig": &currentConfig,
		}
		b.Run(fmt.Sprintf("unused-entries-%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				result, err := engine.RenderIncrementalBindings(b.Context(), "planner", bindingContext)
				if err != nil {
					b.Fatal(err)
				}
				if len(result) == 0 {
					b.Fatal("planner returned empty output")
				}
			}
		})
	}
}

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
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type exactCycleHiddenStringer struct {
	value string
}

type exactCycleShadowDeclaration struct{}

type exactCycleHiddenComparable struct {
	value int
}

func (s exactCycleHiddenStringer) String() string {
	return s.value
}

func TestExactCycleReplayRequiresEveryPublicEntryPoint(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `main`, "aux": `aux`},
		&Options{EntryPoints: []string{"main", "aux"}},
	)
	require.NoError(t, err)

	_, err = engine.PrepareExactCycleReplay([]string{"main"})
	require.ErrorContains(t, err, "entry points are incomplete")

	program, err := engine.PrepareExactCycleReplay([]string{"aux", "main", "main"})
	require.NoError(t, err)
	require.NoError(t, program.validate())
}

func TestExactCycleReplayImportedNativeUsageIsCovered(t *testing.T) {
	tests := []struct {
		name     string
		library  string
		want     string
		function string
	}{
		{
			name:    "time",
			library: `{% macro Value() string %}{{ now().UTC().Format("20060102") }}{% end %}`,
			want:    "uses unproved native \"now\"",
		},
		{
			name:    "random",
			library: `{% macro Value() string %}{{ randBytes(8) }}{% end %}`,
			want:    "uses unproved native \"randBytes\"",
		},
		{
			name:     "custom function",
			library:  `{% macro Value() string %}{{ external() }}{% end %}`,
			want:     "uses custom native \"external\"",
			function: "external",
		},
		{
			name:    "imported generic formatting",
			library: `{% macro Value() string %}{{ sprintf("%p", extraContext) }}{% end %}`,
			want:    "uses unproved native \"sprintf\"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			functions := map[string]GlobalFunc(nil)
			if test.function != "" {
				functions = map[string]GlobalFunc{test.function: func(...any) (any, error) { return "x", nil }}
			}
			engine, err := New(
				map[string]string{
					"main":    `{% import "library" for Value %}{{ Value() }}`,
					"library": test.library,
				},
				&Options{EntryPoints: []string{"main"}, Functions: functions},
			)
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestExactCycleReplayRejectsReservedDeclarationShadowing(t *testing.T) {
	for _, name := range []string{
		"http", "controller", "shared", "fileRegistry", "planRegistry",
		ResourceDeriverContextName, "pathResolver", "item",
	} {
		t.Run("variable/"+name, func(t *testing.T) {
			engine, err := New(
				map[string]string{"main": "static"},
				&Options{
					EntryPoints:  []string{"main"},
					Declarations: map[string]any{name: (*exactCycleShadowDeclaration)(nil)},
				},
			)
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, "shadows an engine declaration")
		})
	}
	for _, name := range []string{"tostring", FuncCycleRandomBytes, FuncStatusPatch} {
		t.Run("function/"+name, func(t *testing.T) {
			engine, err := New(
				map[string]string{"main": "static"},
				&Options{
					EntryPoints: []string{"main"},
					Functions: map[string]GlobalFunc{
						name: func(...any) (any, error) { return nil, errors.New("shadowing stub must not run") },
					},
				},
			)
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, "shadows an engine declaration")
		})
	}
	for _, name := range []string{"currentConfig", "currentFiles", "resources"} {
		t.Run("protocol/"+name, func(t *testing.T) {
			engine, err := New(
				map[string]string{"main": "static"},
				&Options{
					EntryPoints:  []string{"main"},
					Declarations: map[string]any{name: (*exactCycleShadowDeclaration)(nil)},
				},
			)
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.Error(t, err)
		})
	}
}

func TestExactCycleReplayRejectsGenericFormattingAndHiddenStringers(t *testing.T) {
	for name, source := range map[string]string{
		"sprint":  `{{ sprint(extraContext) }}`,
		"sprintf": `{{ sprintf("%p", extraContext) }}`,
	} {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"main": source}, &Options{EntryPoints: []string{"main"}})
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, fmt.Sprintf("uses unproved native %q", name))
		})
	}

	engine, err := New(
		map[string]string{"main": `{{ tostring(extraContext["value"]) }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	_, _, err = program.Begin(t.Context(), 1, map[string]any{
		"extraContext": map[string]any{"value": exactCycleHiddenStringer{value: "hidden"}},
	})
	require.ErrorContains(t, err, "custom marshaler")
}

func TestExactCycleReplayRejectsProcessTimezoneFunctions(t *testing.T) {
	for name, source := range map[string]string{
		"date":     `{{ date(2026, 8, 27, 0, 0, 0, 0, "Local") }}`,
		"unixTime": `{{ unixTime(0, 0) }}`,
	} {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"main": source}, &Options{EntryPoints: []string{"main"}})
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, fmt.Sprintf("uses unproved native %q", name))
		})
	}
}

func TestExactCycleReplayRejectsFmtBackedCollectionIndexes(t *testing.T) {
	for name, source := range map[string]string{
		"count_by": `{{ count_by(extraContext["values"], "key") | toJSON() }}`,
		"index_by": `{{ index_by(extraContext["values"], "key") | toJSON() }}`,
	} {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"main": source}, &Options{EntryPoints: []string{"main"}})
			require.NoError(t, err)
			_, err = engine.PrepareExactCycleReplay([]string{"main"})
			require.ErrorContains(t, err, fmt.Sprintf("uses unproved native %q", name))
		})
	}
}

func TestExactCycleReplayExecutionIsDeterministic(t *testing.T) {
	engine, err := New(
		map[string]string{
			"main": `{% for key := range extraContext %}{{ key }}{% end %}`,
		},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	ctx, err := program.ExecutionContext(t.Context())
	require.NoError(t, err)
	input := map[string]any{
		"extraContext": map[string]any{"z": 1, "a": 2, "m": 3},
	}
	for range 100 {
		output, renderErr := engine.Render(ctx, "main", input)
		require.NoError(t, renderErr)
		require.Equal(t, "amz\n", output)
	}
}

func TestExactCycleReplayRejectsParallelRootExecution(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{% macro Value() string %}value{% end %}{{ go Value() }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	_, err = engine.PrepareExactCycleReplay([]string{"main"})
	require.ErrorContains(t, err, "not deterministic")
}

func TestExactCycleReplayExecutionRejectsAmbientAliasMutation(t *testing.T) {
	engine, err := New(
		map[string]string{
			"main": `{% var nested = fallback(extraContext["nested"], map[string]any{}).(map[string]any) %}` +
				`{% nested["value"] = "mutated" %}{{ nested["value"] }}`,
		},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	ctx, err := program.ExecutionContext(t.Context())
	require.NoError(t, err)
	nested := map[string]any{"value": "original"}
	_, err = engine.Render(ctx, "main", map[string]any{
		"extraContext": map[string]any{"nested": nested},
	})
	require.ErrorContains(t, err, "mutates an immutable input")
	require.Equal(t, "original", nested["value"])
}

func TestExactCycleReplayExecutionRejectsPreviousOutputMutation(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{% currentFiles["state"] = "mutated" %}`},
		&Options{
			EntryPoints:  []string{"main"},
			Declarations: map[string]any{"currentFiles": (*map[string]string)(nil)},
		},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	ctx, err := program.ExecutionContext(t.Context())
	require.NoError(t, err)
	files := map[string]string{"state": "original"}
	_, err = engine.Render(ctx, "main", map[string]any{"currentFiles": &files})
	require.ErrorContains(t, err, "mutates an immutable input")
	require.Equal(t, "original", files["state"])
}

func TestExactCycleReplayAmbientSnapshotIsExact(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ tostring(extraContext["value"]) }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	contextValue := map[string]any{"extraContext": map[string]any{"value": "first"}}
	ctx, inputs, err := program.Begin(t.Context(), 1, contextValue)
	require.NoError(t, err)
	_, err = engine.Render(WithIncrementalScope(ctx, "main"), "main", contextValue)
	require.NoError(t, err)
	err = inputs.Finalize()
	require.NoError(t, err)

	matched, err := program.Matches(inputs, map[string]any{
		"extraContext": map[string]any{"value": "first"},
		"unrelated":    "changed",
	})
	require.NoError(t, err)
	assert.True(t, matched)

	matched, err = program.Matches(inputs, map[string]any{
		"extraContext": map[string]any{"value": "second"},
	})
	require.NoError(t, err)
	assert.False(t, matched)

	inputs.values[0].value = map[string]any{"value": "poisoned"}
	_, err = program.Matches(inputs, contextValue)
	require.ErrorContains(t, err, "failed authentication")
}

func TestExactCycleReplayAmbientPointerAliasTopologyIsExact(t *testing.T) {
	engine, err := New(
		map[string]string{
			"main": `{% var a = extraContext["a"].(*int) %}` +
				`{% var b = extraContext["b"].(*int) %}{{ a == b }}`,
		},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)

	first, second := 1, 1
	previous := map[string]any{
		"extraContext": map[string]any{"a": &first, "b": &second},
	}
	ctx, inputs, err := program.Begin(t.Context(), 1, previous)
	require.NoError(t, err)
	output, err := engine.Render(WithIncrementalScope(ctx, "main"), "main", previous)
	require.NoError(t, err)
	require.Equal(t, "false\n", output)
	require.NoError(t, inputs.Finalize())

	left, right := 1, 1
	matched, err := program.Matches(inputs, map[string]any{
		"extraContext": map[string]any{"a": &left, "b": &right},
	})
	require.NoError(t, err)
	require.True(t, matched)

	shared := 1
	matched, err = program.Matches(inputs, map[string]any{
		"extraContext": map[string]any{"a": &shared, "b": &shared},
	})
	require.NoError(t, err)
	require.False(t, matched)
}

func TestExactCycleReplayRejectsAmbientStructWithUnexportedState(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ extraContext["value"] == extraContext["expected"] }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)

	_, _, err = program.Begin(t.Context(), 1, map[string]any{
		"extraContext": map[string]any{
			"value":    exactCycleHiddenComparable{value: 1},
			"expected": exactCycleHiddenComparable{value: 2},
		},
	})
	require.ErrorContains(t, err, "field value is unexported")
}

func TestExactCycleReplayCaptureCannotBypassOwnedExecution(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ extraContext["value"] }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	_, err = program.Capture(map[string]any{"extraContext": map[string]any{"value": "original"}})
	require.ErrorContains(t, err, "owned execution attempt")

	_, inputs, err := program.Begin(
		t.Context(), 1, map[string]any{"extraContext": map[string]any{"value": "original"}},
	)
	require.NoError(t, err)
	require.ErrorContains(t, inputs.Finalize(), "invalid provenance")
}

func TestExactCycleReplayTracksOnlyUsedPreviousOutputs(t *testing.T) {
	engine, err := New(
		map[string]string{
			"main":    `{% import "library" for Value %}{{ Value() }}`,
			"library": `{% macro Value() string %}{{ tostring(currentFiles["visible"]) }}{% end %}`,
		},
		&Options{
			EntryPoints:  []string{"main"},
			Declarations: map[string]any{"currentFiles": (*map[string]string)(nil)},
		},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	used, err := program.UsesPreviousOutput("currentFiles")
	require.NoError(t, err)
	require.True(t, used)
	used, err = program.UsesPreviousOutput("currentConfig")
	require.NoError(t, err)
	require.False(t, used)

	program.usesCurrentFiles = false
	_, err = program.UsesPreviousOutput("currentFiles")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestExactCycleReplayAllowsObservedSharedCalls(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ tostring(shared.Get("value")) }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	_, err = engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)

	unsafe, err := New(
		map[string]string{"main": `{{ tostring(shared.Get("value")) }}{{ tostring(shared) }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	_, err = unsafe.PrepareExactCycleReplay([]string{"main"})
	require.ErrorContains(t, err, "outside an observed call")
}

func TestExactCycleReplayTracksLegacySharedInitialState(t *testing.T) {
	tests := []struct {
		name      string
		templates map[string]string
		want      []string
	}{
		{
			name: "direct shared",
			templates: map[string]string{
				"main": `{{ tostring(shared.Get("value")) }}`,
			},
			want: []string{"shared"},
		},
		{
			name: "imported shared",
			templates: map[string]string{
				"main":    `{% import "library" for Value %}{{ Value() }}`,
				"library": `{% macro Value() string %}{{ tostring(shared.Get("value")) }}{% end %}`,
			},
			want: []string{"shared"},
		},
		{
			name: "direct first seen",
			templates: map[string]string{
				"main": `{{ first_seen("value") }}`,
			},
			want: []string{"shared"},
		},
		{
			name: "imported first seen",
			templates: map[string]string{
				"main":    `{% import "library" for Value %}{{ Value() }}`,
				"library": `{% macro Value() string %}{{ first_seen("value") }}{% end %}`,
			},
			want: []string{"shared"},
		},
		{
			name: "pure",
			templates: map[string]string{
				"main": `{{ toUpper("value") }}`,
			},
			want: []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, err := New(test.templates, &Options{EntryPoints: []string{"main"}})
			require.NoError(t, err)
			program, err := engine.PrepareExactCycleReplay([]string{"main"})
			require.NoError(t, err)
			requiresAllRoots, err := program.RequiresUnchangedInputRoots()
			require.NoError(t, err)
			assert.False(t, requiresAllRoots)
			assert.Equal(t, test.want, program.protocolNames)
		})
	}
}

func TestExactCycleReplaySharedProtocolIsAuthenticated(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ first_seen("value") }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	program.protocolNames = nil
	_, err = program.RequiresUnchangedInputRoots()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestExactCycleReplayFirstSeenUsesFreshSharedAndOrderedRoots(t *testing.T) {
	engine, err := New(
		map[string]string{
			"first":  `{{ first_seen("duplicate") }}`,
			"second": `{{ first_seen("duplicate") }}`,
		},
		&Options{EntryPoints: []string{"first", "second"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"first", "second"})
	require.NoError(t, err)
	requiresAllRoots, err := program.RequiresUnchangedInputRoots()
	require.NoError(t, err)
	require.False(t, requiresAllRoots)

	shared := NewSharedContext()
	templateContext := map[string]any{"shared": shared}
	ctx, inputs, err := program.Begin(t.Context(), 1, templateContext)
	require.NoError(t, err)
	outputs := make([]string, 0, 2)
	for _, root := range []string{"first", "second"} {
		output, renderErr := engine.Render(WithIncrementalScope(ctx, root), root, templateContext)
		require.NoError(t, renderErr)
		outputs = append(outputs, strings.TrimSpace(output))
	}
	require.NoError(t, inputs.Finalize())
	assert.Equal(t, []string{"true", "false"}, outputs)

	matched, err := program.Matches(inputs, map[string]any{"shared": NewSharedContext()})
	require.NoError(t, err)
	require.True(t, matched)

	preseeded := NewSharedContext()
	preseeded.ComputeIfAbsent("duplicate", func() any { return true })
	matched, err = program.Matches(inputs, map[string]any{"shared": preseeded})
	require.NoError(t, err)
	require.False(t, matched)
	_, _, err = program.Begin(t.Context(), 2, map[string]any{"shared": preseeded})
	require.ErrorContains(t, err, "not fresh and empty")

	inputs.protocols[0].state = newExactCycleEmptyProtocolState("shared")
	_, err = program.Matches(inputs, map[string]any{"shared": NewSharedContext()})
	require.ErrorContains(t, err, "failed authentication")
}

func TestExactCycleReplayEffectsAuthenticateOrderArgumentsAndLease(t *testing.T) {
	engine, err := New(
		map[string]string{
			"main": `{{ cycleTimeBucket(60, "200601021504") }}:{{ len(cycleRandomBytes(16)) }}`,
		},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	ctx, inputs, err := program.Begin(context.Background(), 7, map[string]any{})
	require.NoError(t, err)
	ctx = WithIncrementalScope(ctx, "main")
	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Contains(t, output, ":16")
	require.NoError(t, inputs.Finalize())
	require.Len(t, inputs.effects, 2)
	assert.Equal(t, uint64(1), inputs.effects[0].ordinal)
	assert.Equal(t, int64(60), inputs.effects[0].integerArg)
	assert.Equal(t, uint64(2), inputs.effects[1].ordinal)
	assert.Equal(t, int64(16), inputs.effects[1].integerArg)
	generation, err := inputs.Generation()
	require.NoError(t, err)
	assert.Equal(t, uint64(7), generation)

	matched, err := program.matchesAt(inputs, map[string]any{}, inputs.effects[0].expiresAt.Add(-1))
	require.NoError(t, err)
	assert.True(t, matched)
	matched, err = program.matchesAt(inputs, map[string]any{}, inputs.effects[0].expiresAt)
	require.NoError(t, err)
	assert.False(t, matched)
	backward := inputs.effects[0].expiresAt.Add(-2 * time.Duration(inputs.effects[0].integerArg) * time.Second)
	matched, err = program.matchesAt(inputs, map[string]any{}, backward)
	require.NoError(t, err)
	assert.False(t, matched)

	inputs.effects[1].integerArg++
	_, err = program.Matches(inputs, map[string]any{})
	require.ErrorContains(t, err, "effects failed authentication")

	_, err = engine.Render(ctx, "main", map[string]any{})
	require.ErrorContains(t, err, "no longer active")
}

func TestExactCycleReplayEffectAttemptPreservesGlobalRootOrder(t *testing.T) {
	engine, err := New(
		map[string]string{
			"first":  `{{ len(cycleRandomBytes(3)) }}{{ len(cycleRandomBytes(4)) }}`,
			"second": `{{ len(cycleRandomBytes(5)) }}{{ len(cycleRandomBytes(6)) }}`,
		},
		&Options{EntryPoints: []string{"first", "second"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"first", "second"})
	require.NoError(t, err)
	ctx, inputs, err := program.Begin(context.Background(), 11, map[string]any{})
	require.NoError(t, err)

	for _, root := range []string{"first", "second"} {
		_, renderErr := engine.Render(WithIncrementalScope(ctx, root), root, map[string]any{})
		require.NoError(t, renderErr)
	}
	require.NoError(t, inputs.Finalize())
	require.Len(t, inputs.effects, 4)
	for index := range inputs.effects {
		assert.Equal(t, uint64(index+1), inputs.effects[index].globalOrdinal)
	}
	assert.Equal(t, []string{"first", "first", "second", "second"}, []string{
		inputs.effects[0].scope, inputs.effects[1].scope, inputs.effects[2].scope, inputs.effects[3].scope,
	})
}

func TestExactCycleReplayRejectsOutOfOrderAndConcurrentRoots(t *testing.T) {
	engine, err := New(
		map[string]string{"first": `first`, "second": `second`},
		&Options{EntryPoints: []string{"first", "second"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"first", "second"})
	require.NoError(t, err)
	ctx, _, err := program.Begin(context.Background(), 12, map[string]any{})
	require.NoError(t, err)
	_, err = engine.Render(WithIncrementalScope(ctx, "second"), "second", map[string]any{})
	require.ErrorContains(t, err, "executed at occurrence")
}

func TestExactCycleReplayUncommittedAttemptCannotMatch(t *testing.T) {
	engine, err := New(
		map[string]string{"main": `{{ cycleRandomBytes(4) }}{{ fail("failed") }}`},
		&Options{EntryPoints: []string{"main"}},
	)
	require.NoError(t, err)
	program, err := engine.PrepareExactCycleReplay([]string{"main"})
	require.NoError(t, err)
	ctx, inputs, err := program.Begin(context.Background(), 13, map[string]any{})
	require.NoError(t, err)
	_, err = engine.Render(WithIncrementalScope(ctx, "main"), "main", map[string]any{})
	require.Error(t, err)
	_, err = program.Matches(inputs, map[string]any{})
	require.ErrorContains(t, err, "invalid provenance")
	_, err = inputs.Generation()
	require.ErrorContains(t, err, "invalid provenance")
}

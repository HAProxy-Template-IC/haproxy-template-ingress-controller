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

package templating

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
)

func TestIncrementalEntryPointCompilesWithTrackedInputsAndPureHelpers(t *testing.T) {
	templates := map[string]string{
		"component": `{%- import "safe-library" for Label -%}
{%- var route = resources.routes.GetSingle("default", "route") -%}
{%- var pods = controller.haproxy_pods.List() -%}
{%- var fetched, fetchErr = http.Fetch("https://example.test/routes") -%}
{{- Label(item) -}}
{{- tostring(route) -}}
{{- tostring(pods) -}}
{{- tostring(fetched) -}}
{{- tostring(fetchErr) -}}
{{- source -}}
{{- tostring(props["label"]) -}}
{{- tostring(renderSubject["mode"]) -}}
{{- renderMode -}}
{{- statusPatch(item, map[string]any{"deployed": map[string]any{"ok": true}}) -}}
{{- shared.Unique("routes", "key", "line") -}}`,
		"safe-library": `{% macro Label(value any) string %}{{ toUpper(dig_string(value, "", "metadata", "name")) }}{% end %}`,
	}

	_, err := New(templates, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations: map[string]any{
			"resources":    incrementalBatchResourcesDeclaration(),
			"currentFiles": (*map[string]string)(nil),
		},
	})
	require.NoError(t, err)
}

func TestIncrementalEntryPointBatchCertificateIsExact(t *testing.T) {
	tests := map[string]string{
		"resource":   `{{ len(resources.routes.List()) }}`,
		"controller": `{{ len(controller.haproxy_pods.List()) }}`,
		"shared":     `{{ shared.Publish("values", "key", "value") }}`,
		"http":       `{% var value, err = http.Fetch("https://example.test/value") %}{{ tostring(value) }}{{ tostring(err) }}`,
		"plan":       `{% var value, err = planRegistry.Profile(map[string]any{"mode": "http"}) %}{% if err != nil %}{{ err.Error() }}{% end %}{{ value }}`,
		"helper":     `{{ toUpper("value") }}`,
		"duration":   `{% var value, err = parseDuration("1ms") %}{{ value.Milliseconds() }}{{ tostring(err) }}`,
		"time":       `{{ parseTime("2006-01-02", "2026-08-25").UnixNano() }}`,
	}
	declarations := map[string]any{"resources": incrementalBatchResourcesDeclaration()}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"component": source}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
				Declarations:           declarations,
			})
			require.NoError(t, err)
			compiled := engine.compiledTemplates["component"]
			require.True(t, compiled.BatchSafe(), "callables: %#v", compiled.UsedNativeCallables())
			require.NoError(t, compiled.DeterministicSafe())
			for _, callable := range compiled.UsedNativeCallables() {
				require.True(t, callable.Synchronous, "uncertified callable: %#v", callable)
			}
		})
	}
}

func TestIncrementalEntryPointBatchCertificateAcceptsRenderedTemplate(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{%- var fragment = render "body" -%}{{ fragment }}`,
		"body":      `{{ tostring(resources.routes.GetSingle("default", "route")) }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations: map[string]any{
			"resources": incrementalBatchResourcesDeclaration(),
		},
	})
	require.NoError(t, err)
	require.True(t, engine.compiledTemplates["component"].BatchSafe())
}

func TestIncrementalEntryPointConstructionRejectsIncompleteBatchCertificate(t *testing.T) {
	_, err := New(map[string]string{
		"certified-component":   `{{ toUpper("value") }}`,
		"uncertified-component": `{% var value, _ = parseDuration("1ms") %}{{ value.String() }}`,
	}, &Options{
		EntryPoints:            []string{"certified-component", "uncertified-component"},
		IncrementalEntryPoints: []string{"certified-component", "uncertified-component"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uncertified-component")
	assert.Contains(t, err.Error(), "not certified for batch execution")
	assert.Contains(t, err.Error(), "parseDuration.[0].String")
}

func TestIncrementalEntryPointBatchCertificateRejectsUnlistedResultMembers(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"duration error": {
			source: `{% var _, err = parseDuration("1ms") %}{% if err != nil %}{{ err.Error() }}{% end %}`,
			want:   "parseDuration.[1].Error",
		},
		"time string": {
			source: `{{ parseTime("2006-01-02", "2026-08-25").String() }}`,
			want:   "parseTime.String",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(map[string]string{"component": test.source}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, scriggo.ErrBatchUncertifiedNative)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestIncrementalEntryPointConstructionRejectsNondeterministicProgram(t *testing.T) {
	_, err := New(map[string]string{
		"component": `{% var values = map[*int]string{} %}{% for key := range values %}{{ key }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component")
	assert.Contains(t, err.Error(), "not deterministic")
	assert.Contains(t, err.Error(), "map key cannot be ordered deterministically")
}

func TestIncrementalEntryPointBatchCertificateRejectsUnboundResourceSurface(t *testing.T) {
	_, err := New(map[string]string{
		"component": `{{ len(resources.routes.List()) }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations: map[string]any{
			"resources": (*map[string]ResourceStore)(nil),
		},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, scriggo.ErrBatchUncertifiedNative)
	assert.Contains(t, err.Error(), "resources.routes.List")
}

func incrementalBatchResourcesDeclaration() any {
	anySlice := reflect.TypeFor[[]any]()
	anyType := reflect.TypeFor[any]()
	keysType := reflect.TypeFor[[]any]()
	storeType := reflect.StructOf([]reflect.StructField{
		{Name: "List", Type: reflect.FuncOf(nil, []reflect.Type{anySlice}, false)},
		{Name: "Fetch", Type: reflect.FuncOf([]reflect.Type{keysType}, []reflect.Type{anySlice}, true)},
		{Name: "GetSingle", Type: reflect.FuncOf([]reflect.Type{keysType}, []reflect.Type{anyType}, true)},
		{Name: "APIVersion", Type: reflect.TypeFor[func() string]()},
	})
	resourcesType := reflect.StructOf([]reflect.StructField{{
		Name: "Routes", Type: reflect.PointerTo(storeType), Tag: `json:"routes"`,
	}})
	declaration := reflect.Zero(reflect.PointerTo(resourcesType)).Interface()
	RegisterIncrementalResourceDeclaration(declaration)
	return declaration
}

func TestIncrementalEntryPointRejectsAmbientAndEffectfulDeclarations(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"current config":       {`{{ currentConfig }}`, "currentConfig"},
		"current files":        {`{{ currentFiles["file"] }}`, "currentFiles"},
		"path resolver":        {`{{ pathResolver.GetBaseDir() }}`, "pathResolver"},
		"dataplane":            {`{{ len(dataplane) }}`, "dataplane"},
		"capabilities":         {`{{ len(capabilities) }}`, "capabilities"},
		"extra context":        {`{{ len(extraContext) }}`, "extraContext"},
		"runtime environment":  {`{{ runtimeEnvironment.GOMAXPROCS }}`, "runtimeEnvironment"},
		"render mode override": {`{% var renderMode = "admission" %}{{ renderMode }}`, "renderMode"},
		"admission subject":    {`{{ len(admissionSubject) }}`, "admissionSubject"},
		"file registry":        {`{{ fileRegistry.Register("map", "test.map", "line") }}`, "fileRegistry"},
		"plan registry":        {`{{ planRegistry.ProfileGroup() }}`, "planRegistry"},
		"resource":             {`{{ len(resource("routes")) }}`, "resource"},
		"jsonpath mutation":    {`{{ jsonpathSet(item, "$.metadata.annotations.test", "value") }}`, "jsonpathSet"},
		"shared get":           {`{{ shared.Get("legacy") }}`, "Get"},
		"shared compute":       {`{{ shared.ComputeIfAbsent("key", func() any { return "value" }) }}`, "ComputeIfAbsent"},
		"first seen":           {`{{ first_seen("scope", "key") }}`, "first_seen"},
		"clock":                {`{{ now() }}`, "now"},
		"random":               {`{{ randBytes(8) }}`, "randBytes"},
		"regexp object":        {`{{ regexp("x") }}`, "regexp"},
		"pointer formatting":   {`{{ sprintf("%p", item) }}`, "sprintf"},
		"debug formatting":     {`{{ debug(item) }}`, "debug"},
		"custom function":      {`{{ custom_func() }}`, "custom_func"},
	}
	declarations := map[string]any{
		"resources":     (*map[string]ResourceStore)(nil),
		"currentConfig": (*map[string]any)(nil),
		"currentFiles":  (*map[string]string)(nil),
	}
	functions := map[string]GlobalFunc{
		"custom_func": func(...any) (any, error) { return "custom", nil },
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(map[string]string{"component": test.source}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
				Functions:              functions,
				Declarations:           declarations,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "component")
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestIncrementalEntryPointRejectsRenderModeOverrides(t *testing.T) {
	tests := map[string]struct {
		templates map[string]string
		want      string
	}{
		"assignment": {
			templates: map[string]string{"component": `{% renderMode = "admission" %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"short declaration": {
			templates: map[string]string{"component": `{% renderMode := "admission" %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"var declaration": {
			templates: map[string]string{"component": `{% var renderMode = "admission" %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"const declaration": {
			templates: map[string]string{"component": `{% const renderMode = "admission" %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"range variable": {
			templates: map[string]string{"component": `{% for _, renderMode := range []string{"admission"} %}{% end %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"for-in variable": {
			templates: map[string]string{"component": `{% for renderMode in []string{"admission"} %}{% end %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"macro name": {
			templates: map[string]string{"component": `{% macro renderMode() string %}admission{% end %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"macro parameter": {
			templates: map[string]string{"component": `{% macro Mode(renderMode string) string %}{{ renderMode }}{% end %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"function parameter": {
			templates: map[string]string{"component": `{% var mode = func(renderMode string) string { return renderMode } %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"function result": {
			templates: map[string]string{"component": `{% var mode = func() (renderMode string) { return "admission" } %}`},
			want:      "derived renderMode cannot be declared or assigned",
		},
		"import alias": {
			templates: map[string]string{
				"component": `{% import renderMode "library" %}`,
				"library":   `{% macro Mode() string %}admission{% end %}`,
			},
			want: "derived renderMode cannot be declared or assigned",
		},
		"import-for": {
			templates: map[string]string{
				"component": `{% import "library" for renderMode %}`,
				"library":   `{% macro renderMode() string %}admission{% end %}`,
			},
			want: "cannot refer to unexported name renderMode",
		},
		"transitive imported declaration": {
			templates: map[string]string{
				"component": `{% import "middle" for Mode %}{{ Mode() }}`,
				"middle":    `{% import "library" for Mode %}`,
				"library":   `{% macro Mode() string %}{% var renderMode = "admission" %}{{ renderMode }}{% end %}`,
			},
			want: "derived renderMode cannot be declared or assigned",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(test.templates, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
			})
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestIncrementalEntryPointAcceptsControllerResourceStore(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{{ len(controller.haproxy_pods.List()) }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	output, err := engine.RenderIncrementalComponent(t.Context(), "component", incrementalComponentContext(map[string]any{
		"controller": map[string]ResourceStore{
			"haproxy_pods": &mockResourceStore{listResult: []any{map[string]any{"name": "pod"}}},
		},
	}))
	require.NoError(t, err)
	assert.Equal(t, "1", output)
}

func TestIncrementalEntryPointAcceptsNarrowBackendPlanSurface(t *testing.T) {
	_, err := New(map[string]string{
		"component": `{% var profile, profileErr = planRegistry.Profile(map[string]any{"mode": "http"}) %}` +
			`{% if profileErr != nil %}{{ fail(profileErr.Error()) }}{% end %}` +
			`{% var token, backendErr = planRegistry.Backend(map[string]any{"name": "be_app", "profile": profile}, "backend be_app\n") %}` +
			`{% if backendErr != nil %}{{ fail(backendErr.Error()) }}{% end %}` +
			`{% var conditional, conditionalErr = planRegistry.BackendWhenAny(map[string]any{"name": "be_conditional", "profile": profile}, "backend be_conditional\n", "owners", []string{"owner"}) %}` +
			`{% if conditionalErr != nil %}{{ fail(conditionalErr.Error()) }}{% end %}` +
			`{{ token }}{{ conditional }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)
}

func TestIncrementalEntryPointBackendPlanSurfaceExcludesMutableRegistryMethods(t *testing.T) {
	tests := map[string]string{
		"section":       `{{ planRegistry.Section("backend", "be_app", "backend be_app\n") }}`,
		"profile group": `{{ planRegistry.ProfileGroup() }}`,
		"map metadata":  `{{ planRegistry.MapMeta("routes.map", false) }}`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(map[string]string{"component": source}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
			})
			require.Error(t, err)
		})
	}
}

func TestIncrementalEntryPointCannotReachControllerStoreImplementation(t *testing.T) {
	for _, field := range []string{"Store", "SnapshotView", "CloneWithSnapshotView"} {
		t.Run(field, func(t *testing.T) {
			_, err := New(map[string]string{
				"component": `{{ controller.haproxy_pods.` + field + ` }}`,
			}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), field)
		})
	}
}

func TestIncrementalEntryPointRejectsCustomFilter(t *testing.T) {
	_, err := New(map[string]string{"component": `{{ custom_filter("value") }}`}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Filters: map[string]FilterFunc{
			"custom_filter": func(value any, _ ...any) (any, error) { return value, nil },
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom_filter")
}

func TestIncrementalEntryPointIgnoresCustomOverrideOfPureHelper(t *testing.T) {
	engine, err := New(map[string]string{"component": `{{ dig(item, "value") }}`}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Functions: map[string]GlobalFunc{
			FuncDig: func(...any) (any, error) { return "custom", nil },
		},
	})
	require.NoError(t, err)

	output, err := engine.RenderIncrementalComponent(context.Background(), "component", map[string]any{
		"item":          map[string]any{"value": "standard"},
		"source":        "test",
		"props":         map[string]any{},
		"renderSubject": map[string]any{"mode": "reconcile"},
		"shared":        NewSharedContributionContext(&sharedRecorder{}),
	})
	require.NoError(t, err)
	assert.Equal(t, "standard", output)
}

func TestIncrementalEntryPointImportsUseRestrictedDeclarations(t *testing.T) {
	_, err := New(map[string]string{
		"component":      `{% import "unsafe-library" for Ambient %}{{ Ambient() }}`,
		"unsafe-library": `{% macro Ambient() string %}{{ now() }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "now")
}

func TestIncrementalEntryPointTransitiveImportsUseRestrictedDeclarations(t *testing.T) {
	_, err := New(map[string]string{
		"component": `{% import "middle-library" for Forward %}{{ Forward() }}`,
		"middle-library": `{% import "unsafe-library" for Ambient %}` +
			`{% macro Forward() string %}{{ Ambient() }}{% end %}`,
		"unsafe-library": `{% macro Ambient() string %}{{ now() }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe-library")
	assert.Contains(t, err.Error(), "now")
}

func TestNonIncrementalEntryPointTransitiveImportsKeepFullDeclarations(t *testing.T) {
	_, err := New(map[string]string{
		"ordinary": `{% import "middle-library" for Forward %}{{ Forward() }}`,
		"middle-library": `{% import "ambient-library" for Ambient %}` +
			`{% macro Forward() string %}{{ Ambient() }}{% end %}`,
		"ambient-library": `{% macro Ambient() string %}{{ now() }}{% end %}`,
	}, &Options{EntryPoints: []string{"ordinary"}})
	require.NoError(t, err)
}

func TestNonIncrementalEntryPointKeepsFullDeclarations(t *testing.T) {
	_, err := New(map[string]string{
		"ordinary": `{{ custom_func() }}{{ now() }}{{ len(resource("routes")) }}{{ currentFiles["file"] }}`,
	}, &Options{
		EntryPoints: []string{"ordinary"},
		Functions: map[string]GlobalFunc{
			"custom_func": func(...any) (any, error) { return "custom", nil },
		},
		Declarations: map[string]any{
			"currentFiles": (*map[string]string)(nil),
		},
	})
	require.NoError(t, err)
}

func TestIncrementalEntryPointMustBeCompiledExplicitly(t *testing.T) {
	_, err := New(map[string]string{"component": "safe", "ordinary": "safe"}, &Options{
		EntryPoints:            []string{"ordinary"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.EqualError(t, err, `incremental entry point "component" is not in EntryPoints`)
}

func TestIncrementalComponentInputsAreImmutable(t *testing.T) {
	tests := map[string]string{
		"item":           `{% item["value"] = "changed" %}`,
		"props":          `{% props["value"] = "changed" %}`,
		"render subject": `{% renderSubject["mode"] = "changed" %}`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			engine, err := New(map[string]string{"component": source}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
			})
			require.NoError(t, err)
			item := map[string]any{"value": "original"}
			props := map[string]any{"value": "original"}
			renderSubject := map[string]any{"mode": "reconcile"}

			_, err = engine.RenderIncrementalComponent(t.Context(), "component", map[string]any{
				"item":          item,
				"source":        "routes",
				"props":         props,
				"renderSubject": renderSubject,
				"shared":        NewSharedContributionContext(&sharedRecorder{}),
			})
			require.ErrorContains(t, err, "mutates an immutable input")
			assert.Equal(t, "original", item["value"])
			assert.Equal(t, "original", props["value"])
			assert.Equal(t, "reconcile", renderSubject["mode"])
		})
	}
}

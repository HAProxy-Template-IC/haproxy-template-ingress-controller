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

package templating

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalEffectTestDeriver struct{}

func (*incrementalEffectTestDeriver) DeriveResource(_ string, item any, path string, value any) (any, error) {
	return DeriveResourceJSONPath(item, path, value)
}

type incrementalEffectMutatingDeriver struct {
	derived map[string]any
}

func (d *incrementalEffectMutatingDeriver) DeriveResource(
	_ string,
	item any,
	path string,
	value any,
) (any, error) {
	input := item.(map[string]any)
	input["kind"] = "MutatedInput"
	derived, err := DeriveResourceJSONPath(input, path, value)
	if err != nil {
		return nil, err
	}
	d.derived = derived.(map[string]any)
	return d.derived, nil
}

type incrementalEffectTestRecorder struct {
	mu      sync.Mutex
	events  []RenderedEvent
	patches []StatusPatch
}

func (r *incrementalEffectTestRecorder) RecordStatusPatch(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
	sourceTemplate string,
	sourceLine int,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.patches = append(r.patches, StatusPatch{
		Namespace: namespace, Name: name, APIVersion: apiVersion, Kind: kind,
		UID: uid, ResourceVersion: resourceVersion,
		Variants: variants, SourceTemplate: sourceTemplate, SourceLine: sourceLine,
	})
	return nil
}

func (r *incrementalEffectTestRecorder) RecordEvent(
	namespace, name, apiVersion, kind, eventType, reason, message string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RenderedEvent{
		Namespace: namespace, Name: name, APIVersion: apiVersion, Kind: kind,
		Type: eventType, Reason: reason, Message: message,
	})
	return nil
}

func (r *incrementalEffectTestRecorder) snapshot() []RenderedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RenderedEvent(nil), r.events...)
}

func (r *incrementalEffectTestRecorder) patchSnapshot() []StatusPatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StatusPatch(nil), r.patches...)
}

func TestIncrementalEffectsRequireQueryCapabilities(t *testing.T) {
	tests := map[string]string{
		FuncDeriveResource: `{{ deriveResource("routes", item, "metadata.annotations['test']", "value") }}`,
		FuncRecordEvent:    `{{ recordEvent(item, "Rejected", "message") }}`,
		FuncStatusPatch: `{{ statusPatch(item, ` +
			`map[string]any{"deployed": map[string]any{"ok": true}}) }}`,
	}
	item := incrementalEffectTestItem("route")

	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			engine := newIncrementalEffectTestEngine(t, map[string]string{"component": source})
			_, err := engine.RenderIncrementalComponent(t.Context(), "component", incrementalEffectTestVars(item))
			require.ErrorContains(t, err, "incremental effect capability is unavailable")
		})
	}
}

func TestIncrementalEffectsPropagateThroughImportedMacroAndNativeChild(t *testing.T) {
	engine := newIncrementalEffectTestEngine(t, map[string]string{
		"component": `{%- import "library" for Emit -%}` +
			`{%- for _, emitted := range map([]any{item}, func(resource any) string { return Emit(resource) }) -%}` +
			`{{- emitted -}}{%- end -%}` +
			`{%- var derived = deriveResource("routes", item, "metadata.annotations['test']", "value") -%}` +
			`{{- jsonpathGet(derived, "metadata.annotations['test']") -}}`,
		"library": `{%- macro Emit(resource any) string -%}` +
			`{{- recordEvent(resource, "Rejected", "message") -}}` +
			`{%- end -%}`,
	})
	recorder := &incrementalEffectTestRecorder{}
	ctx := WithIncrementalResourceDeriver(t.Context(), &incrementalEffectTestDeriver{})
	ctx = WithIncrementalEventRecorder(ctx, recorder)
	ctx = WithIncrementalStatusPatchRecorder(ctx, recorder)

	output, err := engine.RenderIncrementalComponent(
		ctx,
		"component",
		incrementalEffectTestVars(incrementalEffectTestItem("route")),
	)
	require.NoError(t, err)
	assert.Equal(t, "value", output)
	require.Len(t, recorder.snapshot(), 1)
	assert.Equal(t, "route", recorder.snapshot()[0].Name)
}

func TestIncrementalStatusPatchDetachesTemplateAliases(t *testing.T) {
	engine := newIncrementalEffectTestEngine(t, map[string]string{
		"component": `{%%
var deployed = map[string]any{"value": "original"}
var variants = map[string]any{"deployed": deployed}
statusPatch(item, variants)
deployed["value"] = "mutated"
%%}`,
	})
	recorder := &incrementalEffectTestRecorder{}
	ctx := WithIncrementalStatusPatchRecorder(t.Context(), recorder)

	_, err := engine.RenderIncrementalComponent(
		ctx, "component", incrementalEffectTestVars(incrementalEffectTestItem("route")),
	)
	require.NoError(t, err)
	patches := recorder.patchSnapshot()
	require.Len(t, patches, 1)
	assert.Equal(t, "original", patches[0].Variants["deployed"]["value"])
	assert.Equal(t, "uid-route", patches[0].UID)
	assert.Equal(t, "rv-route", patches[0].ResourceVersion)
	assert.NotEmpty(t, patches[0].SourceTemplate)
	assert.Positive(t, patches[0].SourceLine)
}

func TestIncrementalEffectContextsAreIsolatedAcrossConcurrentRuns(t *testing.T) {
	engine := newIncrementalEffectTestEngine(t, map[string]string{
		"component": `{{ recordEvent(item, "Rejected", "message") }}`,
	})
	const runs = 16
	recorders := make([]*incrementalEffectTestRecorder, runs)
	errorsByRun := make([]error, runs)
	var wait sync.WaitGroup
	wait.Add(runs)
	for run := range runs {
		go func() {
			defer wait.Done()
			recorders[run] = &incrementalEffectTestRecorder{}
			ctx := WithIncrementalEventRecorder(context.Background(), recorders[run])
			_, errorsByRun[run] = engine.RenderIncrementalComponent(
				ctx,
				"component",
				incrementalEffectTestVars(incrementalEffectTestItem(fmt.Sprintf("route-%d", run))),
			)
		}()
	}
	wait.Wait()

	for run := range runs {
		require.NoError(t, errorsByRun[run])
		events := recorders[run].snapshot()
		require.Len(t, events, 1)
		assert.Equal(t, fmt.Sprintf("route-%d", run), events[0].Name)
	}
}

func TestIncrementalDeriverCannotMutateInputsOrAliasItsResult(t *testing.T) {
	engine := newIncrementalEffectTestEngine(t, map[string]string{
		"component": `{%- var derived = deriveResource("routes", item, "metadata.annotations['test']", "value") -%}` +
			`{%- derived.(map[string]any)["kind"] = "MutatedResult" -%}` +
			`{{- derived | dig_string("", "kind") -}}`,
	})
	item := incrementalEffectTestItem("route")
	deriver := &incrementalEffectMutatingDeriver{}
	ctx := WithIncrementalResourceDeriver(t.Context(), deriver)

	output, err := engine.RenderIncrementalComponent(ctx, "component", incrementalEffectTestVars(item))
	require.NoError(t, err)
	assert.Equal(t, "MutatedResult", output)
	assert.Equal(t, "Route", item["kind"])
	assert.Equal(t, "MutatedInput", deriver.derived["kind"])
}

func newIncrementalEffectTestEngine(t *testing.T, templates map[string]string) *ScriggoEngine {
	t.Helper()
	engine, err := New(templates, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)
	return engine
}

func incrementalEffectTestVars(item map[string]any) map[string]any {
	return map[string]any{
		"item":          item,
		"source":        "routes",
		"props":         map[string]any{},
		"renderSubject": map[string]any{"mode": "reconcile"},
		"shared":        NewSharedContributionContext(&sharedRecorder{}),
	}
}

func incrementalEffectTestItem(name string) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"namespace":       "default",
			"name":            name,
			"uid":             "uid-" + name,
			"resourceVersion": "rv-" + name,
			"annotations":     map[string]any{},
		},
	}
}

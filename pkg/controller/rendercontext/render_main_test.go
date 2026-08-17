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

package rendercontext_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// mainTemplateWithBackends declares two backends through planRegistry and emits
// their tokens where the sections belong. The declarations run before any
// output so the expected config is exactly the text plus the spliced sections.
const mainTemplateWithBackends = `{% var tokenA, errA = planRegistry.Backend(map[string]any{"name": "be_a", "body": []any{"    stick-table type ip size 1m"}}, "backend be_a\n    stick-table type ip size 1m\n") %}{% var tokenB, errB = planRegistry.Backend(map[string]any{"name": "be_b", "servers": []any{map[string]any{"name": "SRV_1", "address": "10.0.0.2", "port": 8080}}}, "backend be_b\n    server SRV_1 10.0.0.2:8080\n") %}{% if errA != nil || errB != nil %}{{ fail("backend declaration failed") }}{% end %}global
    daemon
{{ tokenA }}{{ tokenB }}`

func newTestEngine(t *testing.T, source string, post []templating.PostProcessorConfig) templating.Engine {
	t.Helper()
	engine, err := templating.New(
		map[string]string{names.MainTemplateName: source},
		&templating.Options{PostProcessors: map[string][]templating.PostProcessorConfig{
			names.MainTemplateName: post,
		}},
	)
	require.NoError(t, err)
	return engine
}

func TestRenderMainAssemblesDeclaredBackends(t *testing.T) {
	engine := newTestEngine(t, mainTemplateWithBackends, nil)
	registry := rendercontext.NewPlanRegistry()

	main, err := rendercontext.RenderMain(context.Background(), engine,
		map[string]any{"planRegistry": registry}, registry, false)

	require.NoError(t, err)
	assert.Equal(t,
		"global\n    daemon\n"+
			"backend be_a\n    stick-table type ip size 1m\n"+
			"backend be_b\n    server SRV_1 10.0.0.2:8080\n", main.Config)
	assert.Equal(t, []string{"core", "backend", "backend"}, kindsOf(main.Sections))
	assert.Equal(t, []string{"core#0", "be_a", "be_b"}, namesOf(main.Sections))
	assertConfigPartitioned(t, main.Config, main.Sections)

	plan := registry.Plan(nil, nil)
	require.Len(t, plan.Backends, 2)
	assert.Equal(t, renderplan.ShapeStructural, plan.Backends["be_a"].Shape)
	assert.Equal(t, renderplan.DigestString("backend be_b\n    server SRV_1 10.0.0.2:8080\n"),
		plan.Backends["be_b"].TextDigest)
	assert.Equal(t, "10.0.0.2", plan.CurrentConfig().ServerIndex["be_b"]["SRV_1"].Address)
}

func TestRenderMainAppliesPostProcessorsPerSection(t *testing.T) {
	// The bundled chart normalises indentation this way; a spliced section must
	// come out indented like the rest of the file.
	post := []templating.PostProcessorConfig{{
		Type:   templating.PostProcessorTypeRegexReplace,
		Params: map[string]string{"pattern": "^[ ]+", "replace": "  "},
	}}
	source := `{% var token, err = planRegistry.Section("backend", "be_a", "backend be_a\n        server s1 10.0.0.1:80\n") %}{% if err != nil %}{{ fail("section failed") }}{% end %}global
        daemon
{{ token }}`
	engine := newTestEngine(t, source, post)
	registry := rendercontext.NewPlanRegistry()

	main, err := rendercontext.RenderMain(context.Background(), engine,
		map[string]any{"planRegistry": registry}, registry, false)

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\nbackend be_a\n  server s1 10.0.0.1:80\n", main.Config)
	assertConfigPartitioned(t, main.Config, main.Sections)
}

func TestRenderMainReportsRenderErrors(t *testing.T) {
	engine := newTestEngine(t, `{{ fail("boom") }}`, nil)
	registry := rendercontext.NewPlanRegistry()

	_, err := rendercontext.RenderMain(context.Background(), engine,
		map[string]any{"planRegistry": registry}, registry, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRenderMainWithoutDeclarationsIsUnchanged(t *testing.T) {
	source := "global\n    daemon\n\ndefaults\n    mode http\n"
	engine := newTestEngine(t, source, nil)
	registry := rendercontext.NewPlanRegistry()

	main, err := rendercontext.RenderMain(context.Background(), engine, map[string]any{}, registry, false)

	require.NoError(t, err)
	assert.Equal(t, source, main.Config, "a chart that declares nothing renders byte-identically")
	assert.Equal(t, []string{"core#0"}, namesOf(main.Sections))
	assert.Len(t, registry.Plan(nil, nil).Sections, 1)
}

func assertConfigPartitioned(t *testing.T, config string, sections []renderplan.Section) {
	t.Helper()
	offset := 0
	for _, section := range sections {
		require.LessOrEqual(t, offset+section.Length, len(config))
		assert.Equal(t, section.TextDigest, renderplan.DigestString(config[offset:offset+section.Length]))
		offset += section.Length
	}
	assert.Equal(t, len(config), offset)
}

func kindsOf(sections []renderplan.Section) []string {
	kinds := make([]string, 0, len(sections))
	for _, section := range sections {
		kinds = append(kinds, section.Kind)
	}
	return kinds
}

func namesOf(sections []renderplan.Section) []string {
	values := make([]string, 0, len(sections))
	for _, section := range sections {
		values = append(values, section.Name)
	}
	return values
}

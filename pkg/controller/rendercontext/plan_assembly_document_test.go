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

package rendercontext

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestAssembleDocumentMatchesStringAssembly(t *testing.T) {
	legacy := NewPlanRegistry(nil)
	documentRegistry, err := NewPlanRegistryWithAuthority(nil, legacy.authority)
	require.NoError(t, err)
	register := func(registry *PlanRegistry) string {
		tokenA, registerErr := registry.Section(
			renderplan.SectionKindBackend,
			"be_a",
			"backend be_a\n\tserver a 127.0.0.1:80",
		)
		require.NoError(t, registerErr)
		tokenB, registerErr := registry.Section(
			renderplan.SectionKindBackend,
			"be_b",
			"backend be_b\n",
		)
		require.NoError(t, registerErr)
		return "global\n" + tokenA + "# between\n" + tokenB + "frontend fe\n"
	}
	rendered := register(legacy)
	require.Equal(t, rendered, register(documentRegistry))
	post := func(_ context.Context, text string) (string, error) {
		return strings.ReplaceAll(text, "\t", "  "), nil
	}

	wantConfig, wantSections, err := legacy.Assemble(t.Context(), rendered, post)
	require.NoError(t, err)
	source, err := renderDocumentFromString(rendered)
	require.NoError(t, err)
	document, sections, err := documentRegistry.AssembleDocument(t.Context(), source, post)
	require.NoError(t, err)
	require.NoError(t, document.ValidateAuthentication())
	config, err := document.String()
	require.NoError(t, err)

	assert.Equal(t, wantConfig, config)
	assert.Equal(t, wantSections, sections)
}

func TestAssembleDocumentAlignsFragmentedCoreWithPlanSection(t *testing.T) {
	first := documentWithTextFragments(t, "global\n", "    daemon\n")
	registry := NewPlanRegistry(nil)
	document, sections, err := registry.AssembleDocument(t.Context(), first, nil)
	require.NoError(t, err)
	require.Len(t, sections, 1)
	require.Equal(t, 1, mustDocumentLeaves(t, document))
	assert.Equal(t, "global\n    daemon\n", mustDocumentString(t, document))

	second := documentWithTextFragments(t, "global\n", "    log stdout\n")
	nextRegistry, err := NewPlanRegistryWithAuthority(nil, registry.authority)
	require.NoError(t, err)
	next, nextSections, err := nextRegistry.AssembleDocument(t.Context(), second, nil)
	require.NoError(t, err)
	require.Len(t, nextSections, 1)
	require.Equal(t, 1, mustDocumentLeaves(t, next))
	assert.Equal(t, "global\n    log stdout\n", mustDocumentString(t, next))
}

func TestAssembleDocumentReusesOnlyExactUnchangedParts(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	proof, err := engine.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(t, err)
	require.NotNil(t, proof)

	render := func(beB string) (rendercontent.Document, *renderAssemblyGeneration) {
		registry, registryErr := NewPlanRegistryWithAuthority(nil, authority)
		require.NoError(t, registryErr)
		tokenA, registryErr := registry.Section(renderplan.SectionKindBackend, "be_a", "backend be_a\n")
		require.NoError(t, registryErr)
		tokenB, registryErr := registry.Section(renderplan.SectionKindBackend, "be_b", beB)
		require.NoError(t, registryErr)
		raw, registryErr := renderDocumentFromString("global\n" + tokenA + tokenB)
		require.NoError(t, registryErr)
		session := state.begin(t)
		generation, registryErr := session.prepareIdentityDocument(raw, proof)
		require.NoError(t, registryErr)
		document, _, _, registryErr := registry.assembleDocument(
			t.Context(), raw, nil, nil, raw, true, session, generation,
		)
		require.NoError(t, registryErr)
		state.retain(t, t.Context(), session)
		return document, state.publication.candidate.assembly
	}

	firstDocument, first := render("backend be_b\n")
	secondDocument, second := render("backend be_b\n    mode http\n")
	require.Len(t, first.parts.values, 3)
	require.Len(t, second.parts.values, 3)

	for _, index := range []int{0, 1} {
		same, sameErr := first.parts.values[index].SameRoot(second.parts.values[index])
		require.NoError(t, sameErr)
		assert.True(t, same)
	}
	changed, err := first.parts.values[2].SameRoot(second.parts.values[2])
	require.NoError(t, err)
	assert.False(t, changed)
	sameDocument, err := firstDocument.SameRoot(secondDocument)
	require.NoError(t, err)
	assert.False(t, sameDocument)
}

func TestAssemblyDocumentCacheRejectsPoisonedRootsAndParts(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	registry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	token, err := registry.Section(renderplan.SectionKindBackend, "be_a", "backend be_a\n")
	require.NoError(t, err)
	raw, err := renderDocumentFromString("global\n" + token)
	require.NoError(t, err)
	proof, err := engine.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(t, err)
	session := state.begin(t)
	render, err := session.prepareIdentityDocument(raw, proof)
	require.NoError(t, err)
	_, _, err = assembleCachedDocument(t.Context(), registry, raw, session, render)
	require.NoError(t, err)
	state.retain(t, t.Context(), session)
	valid := state.publication

	tests := []struct {
		name   string
		poison func(*renderAssemblyGeneration)
	}{
		{name: "assembled root", poison: func(assembly *renderAssemblyGeneration) {
			assembly.assembled = rendercontent.Document{}
		}},
		{name: "part root", poison: func(assembly *renderAssemblyGeneration) {
			parts := &renderAssemblyParts{values: append([]rendercontent.Document(nil), assembly.parts.values...)}
			parts.values[0] = rendercontent.Document{}
			parts.seal = parts
			assembly.parts = parts
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assembly := *valid.candidate.assembly
			test.poison(&assembly)
			assembly.seal = &assembly
			candidate := *valid.candidate
			candidate.assembly = &assembly
			candidate.seal = &candidate
			publication := *valid
			publication.candidate = &candidate
			publication.seal = &publication

			_, beginErr := state.cache.Begin(engine, valid.occurrence+1, &publication)
			require.Error(t, beginErr)
		})
	}
}

func TestAssemblyDocumentCacheConcurrentExactReuse(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	registry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	token, err := registry.Section(renderplan.SectionKindBackend, "be_a", "backend be_a\n")
	require.NoError(t, err)
	raw, err := renderDocumentFromString("global\n" + token)
	require.NoError(t, err)
	proof, err := engine.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(t, err)
	session := state.begin(t)
	render, err := session.prepareIdentityDocument(raw, proof)
	require.NoError(t, err)
	want, _, _, err := registry.assembleDocument(t.Context(), raw, nil, nil, raw, true, session, render)
	require.NoError(t, err)
	state.retain(t, t.Context(), session)

	var group sync.WaitGroup
	errorsFound := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsFound <- runAssemblyDocumentReuseWorker(
				t.Context(), state, engine, authority, raw, proof, want,
			)
		}()
	}
	group.Wait()
	close(errorsFound)
	for workerErr := range errorsFound {
		require.NoError(t, workerErr)
	}
}

func runAssemblyDocumentReuseWorker(
	ctx context.Context,
	state *renderCacheTestState,
	engine templating.Engine,
	authority *PlanTokenAuthority,
	raw rendercontent.Document,
	proof *templating.PostProcessReuseProof,
	want rendercontent.Document,
) error {
	candidate, err := state.cache.Begin(engine, state.occurrence+1, state.publication)
	if err != nil {
		return err
	}
	current, err := NewPlanRegistryWithAuthority(nil, authority)
	if err != nil {
		return err
	}
	if _, err := current.Section(renderplan.SectionKindBackend, "be_a", "backend be_a\n"); err != nil {
		return err
	}
	generation, err := candidate.prepareIdentityDocument(raw, proof)
	if err != nil {
		return err
	}
	got, _, _, err := current.assembleDocument(ctx, raw, nil, nil, raw, true, candidate, generation)
	if err != nil {
		return err
	}
	same, err := want.SameRoot(got)
	if err != nil {
		return err
	}
	if !same {
		return assert.AnError
	}
	return nil
}

func documentWithTextFragments(tb testing.TB, values ...string) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	for index, value := range values {
		fragment, err := rendercontent.TextFragmentFromSorted([]rendercontent.TextPart{{
			Key: fmt.Sprintf("part-%d", index), Text: value,
		}})
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendTextFragment(fragment))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func assembleCachedDocument(
	ctx context.Context,
	registry *PlanRegistry,
	raw rendercontent.Document,
	session *RenderCacheSession,
	render *renderDocumentGeneration,
) (rendercontent.Document, []renderplan.Section, error) {
	document, sections, _, err := registry.assembleDocument(ctx, raw, nil, nil, raw, true, session, render)
	return document, sections, err
}

func mustDocumentLeaves(tb testing.TB, document rendercontent.Document) int {
	tb.Helper()
	leaves, err := document.Leaves()
	require.NoError(tb, err)
	return leaves
}

func mustDocumentString(tb testing.TB, document rendercontent.Document) string {
	tb.Helper()
	text, err := document.String()
	require.NoError(tb, err)
	return text
}

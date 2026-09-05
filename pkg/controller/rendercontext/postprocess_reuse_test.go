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
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderMainReusesProcessedDocumentOnlyWithExactProof(t *testing.T) {
	engine := newProofEngine(t, "beta")
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)
	authority := NewPlanTokenAuthority()
	firstRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)

	first, err := state.renderMain(t, ctx, map[string]any{}, firstRegistry)
	require.NoError(t, err)
	assert.Equal(t, "beta\n", first.Config)
	firstRender := state.publication.candidate.document
	firstAssembly := state.publication.candidate.assembly
	require.NotNil(t, firstRender)
	require.NotNil(t, firstAssembly)

	secondRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	second, err := state.renderMain(t, ctx, map[string]any{}, secondRegistry)
	require.NoError(t, err)
	assert.Equal(t, first.Config, second.Config)
	assert.Same(t, firstRender, state.publication.candidate.document)
	assert.Same(t, firstAssembly, state.publication.candidate.assembly)
}

func TestRenderMainRerunsUncacheablePostProcessorAndAssembly(t *testing.T) {
	var calls atomic.Int32
	engine := newAmbientPostProcessEngine(t, func(...any) (any, error) {
		return calls.Add(1), nil
	})
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	second, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)

	assert.Equal(t, "alpha\n1", first.Config)
	assert.Equal(t, "alpha\n2", second.Config)
	assert.EqualValues(t, 2, calls.Load())
	assert.Nil(t, state.publication.candidate.document.proof)
	assert.Nil(t, state.publication.candidate.assembly)
}

func TestRenderMainRejectsCacheAcrossPostProcessorConfigurations(t *testing.T) {
	firstEngine := newProofEngine(t, "beta")
	secondEngine := newProofEngine(t, "gamma")
	state := newRenderCacheTestState(t, firstEngine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "beta\n", first.Config)
	published := state.publication

	_, err = state.cache.Begin(secondEngine, state.occurrence+1, published)
	require.ErrorContains(t, err, "authority does not match")
	assert.Same(t, published, state.publication)

	secondState := newRenderCacheTestState(t, secondEngine)
	second, err := secondState.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "gamma\n", second.Config)
}

func TestRenderMainRejectsForeignPostProcessProofSubstitution(t *testing.T) {
	engine := newProofEngine(t, "beta")
	foreign := newProofEngine(t, "beta")
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	validPublication := state.publication
	valid := validPublication.candidate.document
	require.NotNil(t, valid)
	foreignProof, err := foreign.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(t, err)
	require.NotNil(t, foreignProof)
	poisoned := *valid
	poisoned.proof = foreignProof
	poisoned.seal = &poisoned
	poisonedAssembly := *validPublication.candidate.assembly
	poisonedAssembly.render = &poisoned
	poisonedAssembly.seal = &poisonedAssembly
	poisonedCandidate := *validPublication.candidate
	poisonedCandidate.document = &poisoned
	poisonedCandidate.assembly = &poisonedAssembly
	poisonedCandidate.seal = &poisonedCandidate
	poisonedPublication := *validPublication
	poisonedPublication.candidate = &poisonedCandidate
	poisonedPublication.seal = &poisonedPublication
	state.publication = &poisonedPublication

	_, err = state.beginNext()
	require.ErrorContains(t, err, "does not match its engine")
	state.publication = validPublication
	second, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, first.Config, second.Config)
	assert.Same(t, valid, state.publication.candidate.document)
}

func TestRenderMainDoesNotTrustPromotedProofForPostProcessOverride(t *testing.T) {
	base, err := templating.New(
		map[string]string{names.MainTemplateName: `{{ incremental_render("lines") }}`},
		&templating.Options{EntryPoints: []string{names.MainTemplateName}},
	)
	require.NoError(t, err)
	engine := &overridingProofEngine{ScriggoEngine: base}
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	second, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)

	assert.Equal(t, "override", first.Config)
	assert.Equal(t, first.Config, second.Config)
	assert.EqualValues(t, 2, engine.calls.Load())
	assert.Nil(t, state.publication.candidate.document.proof)
	assert.Nil(t, state.publication.candidate.assembly)
}

func TestRenderMainPostProcessFailureDoesNotPublish(t *testing.T) {
	sentinel := errors.New("ambient post-processor failed")
	engine := newControlledPostProcessEngine(t, func(_ context.Context, call int32, text string) (string, error) {
		if call == 2 {
			return "", sentinel
		}
		return text + strconv.FormatInt(int64(call), 10), nil
	})
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	ctx := templating.WithIncrementalRenderer(t.Context(), renderer)

	first, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "alpha\n1", first.Config)
	published := state.publication
	publishedAssembly := published.candidate.assembly

	_, err = state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.ErrorIs(t, err, sentinel)
	assert.Same(t, published, state.publication)
	assert.Same(t, publishedAssembly, state.publication.candidate.assembly)

	third, err := state.renderMain(t, ctx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	assert.Equal(t, "alpha\n3", third.Config)
	assert.NotSame(t, published, state.publication)
}

func TestRenderMainPostProcessCancellationDoesNotPublish(t *testing.T) {
	var cancel context.CancelFunc
	engine := newControlledPostProcessEngine(t, func(_ context.Context, call int32, text string) (string, error) {
		if call == 2 {
			cancel()
		}
		return text + strconv.FormatInt(int64(call), 10), nil
	})
	state := newRenderCacheTestState(t, engine)
	renderer := newPostProcessDocumentRenderer(t)
	baseCtx := templating.WithIncrementalRenderer(t.Context(), renderer)

	_, err := state.renderMain(t, baseCtx, map[string]any{}, NewPlanRegistry(nil))
	require.NoError(t, err)
	published := state.publication
	publishedAssembly := published.candidate.assembly

	canceledCtx, stop := context.WithCancel(baseCtx)
	cancel = stop
	_, err = state.renderMain(t, canceledCtx, map[string]any{}, NewPlanRegistry(nil))
	stop()
	require.ErrorIs(t, err, context.Canceled)
	assert.Same(t, published, state.publication)
	assert.Same(t, publishedAssembly, state.publication.candidate.assembly)
}

type controlledPostProcessEngine struct {
	*templating.ScriggoEngine
	calls   atomic.Int32
	process func(context.Context, int32, string) (string, error)
}

type overridingProofEngine struct {
	*templating.ScriggoEngine
	calls atomic.Int32
}

func (e *overridingProofEngine) PostProcess(context.Context, string, string) (string, error) {
	e.calls.Add(1)
	return "override", nil
}

func (e *controlledPostProcessEngine) PostProcess(
	ctx context.Context,
	_ string,
	text string,
) (string, error) {
	return e.process(ctx, e.calls.Add(1), text)
}

func (*controlledPostProcessEngine) PostProcessReuseProof(string) (*templating.PostProcessReuseProof, error) {
	var absent *templating.PostProcessReuseProof
	return absent, nil
}

func newProofEngine(t *testing.T, replace string) *templating.ScriggoEngine {
	t.Helper()
	engine, err := templating.New(
		map[string]string{names.MainTemplateName: `{{ incremental_render("lines") }}`},
		&templating.Options{
			EntryPoints: []string{names.MainTemplateName},
			PostProcessors: map[string][]templating.PostProcessorConfig{names.MainTemplateName: {{
				Type: templating.PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "alpha",
					"replace": replace,
				},
			}}},
		},
	)
	require.NoError(t, err)
	return engine
}

func newAmbientPostProcessEngine(t *testing.T, next templating.GlobalFunc) *templating.ScriggoEngine {
	t.Helper()
	engine, err := templating.New(
		map[string]string{names.MainTemplateName: `{{ incremental_render("lines") }}`},
		&templating.Options{
			EntryPoints: []string{names.MainTemplateName},
			Functions:   map[string]templating.GlobalFunc{"next": next},
			PostProcessors: map[string][]templating.PostProcessorConfig{names.MainTemplateName: {{
				Type:   templating.PostProcessorTypeTemplate,
				Params: map[string]string{"source": `{{ input }}{{ next() }}`},
			}}},
		},
	)
	require.NoError(t, err)
	proof, err := engine.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(t, err)
	require.Nil(t, proof)
	return engine
}

func newControlledPostProcessEngine(
	t *testing.T,
	process func(context.Context, int32, string) (string, error),
) *controlledPostProcessEngine {
	t.Helper()
	engine, err := templating.New(
		map[string]string{names.MainTemplateName: `{{ incremental_render("lines") }}`},
		&templating.Options{EntryPoints: []string{names.MainTemplateName}},
	)
	require.NoError(t, err)
	return &controlledPostProcessEngine{ScriggoEngine: engine, process: process}
}

func newPostProcessDocumentRenderer(t *testing.T) *renderDocumentTestRenderer {
	t.Helper()
	output, err := rendercontent.FromSorted([]rendercontent.Change{{Key: "line", Text: "alpha"}})
	require.NoError(t, err)
	return &renderDocumentTestRenderer{fragment: output}
}

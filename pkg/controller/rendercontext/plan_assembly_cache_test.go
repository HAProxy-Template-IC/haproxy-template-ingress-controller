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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestPlanAssemblyCacheReusesOnlyExactPreparedPlan(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	firstBackend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)
	firstPlan, err := NewPreparedPlanSnapshot().WithBackend(&firstBackend)
	require.NoError(t, err)

	firstRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	require.NoError(t, firstRegistry.AttachPreparedPlan(firstPlan))
	token, err := firstRegistry.PreparedBackendToken("be_app")
	require.NoError(t, err)
	rendered := "global\n" + token
	document := assemblyCacheDocument(t, rendered)
	firstConfig, firstSections, err := renderCachedAssembly(
		context.Background(), state, firstRegistry, rendered, document,
	)
	require.NoError(t, err)
	firstGeneration := state.publication.candidate.assembly
	require.NotNil(t, firstGeneration)

	secondRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	require.NoError(t, secondRegistry.AttachPreparedPlan(firstPlan))
	secondConfig, secondSections, err := renderCachedAssembly(
		context.Background(), state, secondRegistry, rendered, document,
	)
	require.NoError(t, err)
	assert.Same(t, firstGeneration, state.publication.candidate.assembly)
	assert.Equal(t, firstConfig, secondConfig)
	assert.Equal(t, firstSections, secondSections)

	secondSections[0].Name = "poison"
	thirdRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	require.NoError(t, thirdRegistry.AttachPreparedPlan(firstPlan))
	_, thirdSections, err := renderCachedAssembly(
		context.Background(), state, thirdRegistry, rendered, document,
	)
	require.NoError(t, err)
	assert.Equal(t, firstSections, thirdSections)

	changedBackend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n    mode http\n")
	require.NoError(t, err)
	changedPlan, err := NewPreparedPlanSnapshot().WithBackend(&changedBackend)
	require.NoError(t, err)
	changedRegistry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	require.NoError(t, changedRegistry.AttachPreparedPlan(changedPlan))
	changedConfig, _, err := renderCachedAssembly(
		context.Background(), state, changedRegistry, rendered, document,
	)
	require.NoError(t, err)
	assert.NotSame(t, firstGeneration, state.publication.candidate.assembly)
	assert.Equal(t, "global\nbackend be_app\n    mode http\n", changedConfig)
}

func TestPlanAssemblyCacheInvalidatesDirectSectionChanges(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()

	first := func(text string) (*PlanRegistry, string) {
		registry, registryErr := NewPlanRegistryWithAuthority(nil, authority)
		require.NoError(t, registryErr)
		token, sectionErr := registry.Section("backend", "be_app", text)
		require.NoError(t, sectionErr)
		return registry, "global\n" + token
	}
	firstRegistry, rendered := first("backend be_app\n")
	document := assemblyCacheDocument(t, rendered)
	firstConfig, _, err := renderCachedAssembly(
		context.Background(), state, firstRegistry, rendered, document,
	)
	require.NoError(t, err)
	firstGeneration := state.publication.candidate.assembly

	sameRegistry, sameRendered := first("backend be_app\n")
	require.Equal(t, rendered, sameRendered)
	sameConfig, _, err := renderCachedAssembly(
		context.Background(), state, sameRegistry, rendered, document,
	)
	require.NoError(t, err)
	assert.Same(t, firstGeneration, state.publication.candidate.assembly)
	assert.Equal(t, firstConfig, sameConfig)

	changedRegistry, changedRendered := first("backend be_app\n    mode tcp\n")
	require.Equal(t, rendered, changedRendered)
	changedConfig, _, err := renderCachedAssembly(
		context.Background(), state, changedRegistry, rendered, document,
	)
	require.NoError(t, err)
	assert.NotSame(t, firstGeneration, state.publication.candidate.assembly)
	assert.Equal(t, "global\nbackend be_app\n    mode tcp\n", changedConfig)
}

func TestPlanAssemblyCacheRejectsPoisonedGeneration(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	registry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	document := assemblyCacheDocument(t, "global\n")
	_, _, err = renderCachedAssembly(
		context.Background(), state, registry, "global\n", document,
	)
	require.NoError(t, err)
	validPublication := state.publication
	valid := validPublication.candidate.assembly
	require.NotNil(t, valid)

	poisoned := *valid
	poisonedCandidate := *validPublication.candidate
	poisonedCandidate.assembly = &poisoned
	poisonedCandidate.seal = &poisonedCandidate
	poisonedPublication := *validPublication
	poisonedPublication.candidate = &poisonedCandidate
	poisonedPublication.seal = &poisonedPublication
	_, err = state.cache.Begin(engine, state.occurrence+1, &poisonedPublication)
	require.ErrorContains(t, err, "invalid assembly")

	poisonedDocument := assemblyCacheDocument(t, "global\n")
	poisoned = *valid
	poisoned.document = poisonedDocument
	poisoned.seal = &poisoned
	poisonedCandidate = *validPublication.candidate
	poisonedCandidate.assembly = &poisoned
	poisonedCandidate.seal = &poisonedCandidate
	poisonedPublication = *validPublication
	poisonedPublication.candidate = &poisonedCandidate
	poisonedPublication.seal = &poisonedPublication
	_, err = state.cache.Begin(engine, state.occurrence+1, &poisonedPublication)
	require.ErrorContains(t, err, "invalid assembly")
}

func TestPlanAssemblyCacheConcurrentReuse(t *testing.T) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(t, err)
	state := newRenderCacheTestState(t, engine)
	authority := NewPlanTokenAuthority()
	backend, err := PreparePlanBackend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)
	plan, err := NewPreparedPlanSnapshot().WithBackend(&backend)
	require.NoError(t, err)
	seed, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(t, err)
	require.NoError(t, seed.AttachPreparedPlan(plan))
	token, err := seed.PreparedBackendToken("be_app")
	require.NoError(t, err)
	rendered := "global\n" + token
	document := assemblyCacheDocument(t, rendered)
	want, _, err := renderCachedAssembly(
		context.Background(), state, seed, rendered, document,
	)
	require.NoError(t, err)

	var group sync.WaitGroup
	errorsFound := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			registry, registryErr := NewPlanRegistryWithAuthority(nil, authority)
			if registryErr == nil {
				registryErr = registry.AttachPreparedPlan(plan)
			}
			var session *RenderCacheSession
			if registryErr == nil {
				session, registryErr = state.cache.Begin(
					state.engine, state.occurrence+1, state.publication,
				)
			}
			var got string
			if registryErr == nil {
				got, _, registryErr = assembleCachedCandidate(
					context.Background(), session, registry, rendered, document,
				)
			}
			if registryErr == nil && got != want {
				registryErr = assert.AnError
			}
			errorsFound <- registryErr
		}()
	}
	group.Wait()
	close(errorsFound)
	for workerErr := range errorsFound {
		require.NoError(t, workerErr)
	}
}

func assemblyCacheDocument(t *testing.T, text string) rendercontent.Document {
	t.Helper()
	var builder rendercontent.DocumentBuilder
	_, err := builder.WriteString(text)
	require.NoError(t, err)
	document, err := builder.Build(nil)
	require.NoError(t, err)
	return document
}

func renderCachedAssembly(
	ctx context.Context,
	state *renderCacheTestState,
	registry *PlanRegistry,
	rendered string,
	document rendercontent.Document,
) (string, []renderplan.Section, error) {
	session, err := state.beginNext()
	if err != nil {
		return "", nil, err
	}
	config, sections, err := assembleCachedCandidate(ctx, session, registry, rendered, document)
	if err != nil {
		return "", nil, err
	}
	if _, err := state.retainCandidate(ctx, session); err != nil {
		return "", nil, err
	}
	return config, sections, nil
}

func assembleCachedCandidate(
	ctx context.Context,
	session *RenderCacheSession,
	registry *PlanRegistry,
	rendered string,
	document rendercontent.Document,
) (string, []renderplan.Section, error) {
	processed, reused, hit, err := session.processed(
		ctx,
		names.MainTemplateName,
		document,
	)
	if err != nil {
		return "", nil, err
	}
	if !hit {
		processed = rendered
	}
	generation, err := session.prepareDocument(
		names.MainTemplateName,
		document,
		processed,
		reused,
	)
	if err != nil {
		return "", nil, err
	}
	config, sections, err := registry.assemble(
		ctx,
		processed,
		nil,
		nil,
		document,
		true,
		session,
		generation,
	)
	if err != nil {
		return "", nil, err
	}
	return config, sections, nil
}

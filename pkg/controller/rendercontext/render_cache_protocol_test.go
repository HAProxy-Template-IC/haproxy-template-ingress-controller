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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderCacheDiscardedCandidateCannotPoisonNextABARender(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	publishedA := fixture.state.publication

	changedAuxiliary := dataplane.CloneAuxiliaryFiles(fixture.aux)
	changedAuxiliary.MapFiles[0].Content = "a changed\n"
	registryB, configB, sessionB := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err := registryB.PlanWithCache(configB, changedAuxiliary, sessionB)
	require.NoError(t, err)
	require.NotSame(t, publishedA.candidate.plan, sessionB.plan)
	abortedB, err := sessionB.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, abortedB.ValidateAuthentication())
	assert.Same(t, publishedA, fixture.state.publication)

	registryA, configA, sessionA := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err = registryA.PlanWithCache(configA, fixture.aux, sessionA)
	require.NoError(t, err)
	require.Same(t, publishedA.candidate.plan, sessionA.plan)
	repeatedA, err := sessionA.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, repeatedA.ValidateAuthentication())
	assert.Same(t, publishedA.candidate.document, repeatedA.candidate.document)
	assert.Same(t, publishedA.candidate.assembly, repeatedA.candidate.assembly)
	assert.Same(t, publishedA.candidate.plan, repeatedA.candidate.plan)
}

func TestRenderCachePublicationRejectsCopiedForeignAndStaleRoots(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	publication := fixture.state.publication
	occurrence, err := publication.Occurrence()
	require.NoError(t, err)

	copied := *publication
	require.ErrorContains(t, copied.ValidateAuthentication(), "publication is invalid")
	_, err = fixture.state.cache.Begin(fixture.state.engine, occurrence+1, &copied)
	require.ErrorContains(t, err, "publication is invalid")

	copiedCandidate := *publication.candidate
	substituted := *publication
	substituted.candidate = &copiedCandidate
	substituted.seal = &substituted
	require.ErrorContains(t, substituted.ValidateAuthentication(), "generation is invalid")

	foreign := newRenderCacheTestState(t, fixture.state.engine)
	_, err = foreign.cache.Begin(foreign.engine, occurrence+1, publication)
	require.ErrorContains(t, err, "publication is invalid")

	_, err = fixture.state.cache.Begin(fixture.state.engine, occurrence, publication)
	require.ErrorContains(t, err, "does not follow its base")
	require.ErrorContains(
		t,
		fixture.state.cache.ValidatePublication(publication, occurrence+1),
		"another occurrence",
	)

	registry2, config2, session2 := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err = registry2.PlanWithCache(config2, fixture.aux, session2)
	require.NoError(t, err)
	publication2, err := session2.Prepare(t.Context())
	require.NoError(t, err)
	registry3, config3, session3 := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err = registry3.PlanWithCache(config3, fixture.aux, session3)
	require.NoError(t, err)
	publication3, err := session3.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, fixture.state.cache.ValidatePublication(publication3, publication3.occurrence))
	require.ErrorContains(
		t,
		fixture.state.cache.ValidatePublication(publication2, publication3.occurrence),
		"another occurrence",
	)
}

func TestRenderCachePublicationAuthenticatesAtomicDocumentAssemblyPlanRoot(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	publicationA := fixture.state.publication

	documentB := planBuildCacheDocument(t, fixture.rendered)
	registryB, configB, sessionB := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, documentB,
	)
	_, err := registryB.PlanWithCache(configB, fixture.aux, sessionB)
	require.NoError(t, err)
	publicationB, err := sessionB.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, publicationB.ValidateAuthentication())
	require.NotSame(t, publicationA.candidate.document, publicationB.candidate.document)
	require.NotSame(t, publicationA.candidate.assembly, publicationB.candidate.assembly)
	require.NotSame(t, publicationA.candidate.plan, publicationB.candidate.plan)

	tests := []struct {
		name   string
		poison func(*renderCacheGeneration)
	}{
		{name: "document", poison: func(candidate *renderCacheGeneration) {
			candidate.document = publicationA.candidate.document
		}},
		{name: "assembly", poison: func(candidate *renderCacheGeneration) {
			candidate.assembly = publicationA.candidate.assembly
		}},
		{name: "plan", poison: func(candidate *renderCacheGeneration) {
			candidate.plan = publicationA.candidate.plan
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *publicationB.candidate
			test.poison(&candidate)
			candidate.seal = &candidate
			hybrid := *publicationB
			hybrid.candidate = &candidate
			hybrid.seal = &hybrid
			require.Error(t, hybrid.ValidateAuthentication())
			_, beginErr := fixture.state.cache.Begin(
				fixture.state.engine, publicationB.occurrence+1, &hybrid,
			)
			require.Error(t, beginErr)
		})
	}
}

func TestRenderCachePlanHitRemainsInTheNextCompositeRoot(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	initialPlan := fixture.state.publication.candidate.plan

	registry, config, session := fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err := registry.PlanWithCache(config, fixture.aux, session)
	require.NoError(t, err)
	publication, err := session.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, publication.ValidateAuthentication())
	assert.Same(t, initialPlan, publication.candidate.plan)

	fixture.state.publication = publication
	registry, config, session = fixture.newRegistry(
		t, planBuildCacheBackendRecord(""), nil, fixture.document,
	)
	_, err = registry.PlanWithCache(config, fixture.aux, session)
	require.NoError(t, err)
	assert.Same(t, initialPlan, session.plan)
}

type renderCacheTestState struct {
	cache       *RenderDocumentCache
	engine      templating.Engine
	publication *PreparedRenderCachePublication
	occurrence  uint64
}

func newRenderCacheTestState(tb testing.TB, engine templating.Engine) *renderCacheTestState {
	tb.Helper()
	cache, err := NewRenderDocumentCache(engine)
	require.NoError(tb, err)
	return &renderCacheTestState{cache: cache, engine: engine}
}

func (s *renderCacheTestState) begin(tb testing.TB) *RenderCacheSession {
	tb.Helper()
	session, err := s.beginNext()
	require.NoError(tb, err)
	return session
}

func (s *renderCacheTestState) beginNext() (*RenderCacheSession, error) {
	s.occurrence++
	return s.cache.Begin(s.engine, s.occurrence, s.publication)
}

func (s *renderCacheTestState) retain(tb testing.TB, ctx context.Context, session *RenderCacheSession) {
	tb.Helper()
	publication, err := s.retainCandidate(ctx, session)
	require.NoError(tb, err)
	require.NoError(tb, publication.ValidateAuthentication())
}

func (s *renderCacheTestState) retainCandidate(
	ctx context.Context,
	session *RenderCacheSession,
) (*PreparedRenderCachePublication, error) {
	publication, err := session.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	s.publication = publication
	return publication, nil
}

func (s *renderCacheTestState) renderMain(
	tb testing.TB,
	ctx context.Context,
	renderCtx map[string]any,
	registry *PlanRegistry,
) (MainRender, error) {
	tb.Helper()
	session := s.begin(tb)
	result, err := RenderMain(ctx, s.engine, renderCtx, registry, false, session)
	if err != nil {
		return MainRender{}, err
	}
	publication, err := s.retainCandidate(ctx, session)
	if err != nil {
		return MainRender{}, err
	}
	if err := publication.ValidateAuthentication(); err != nil {
		return MainRender{}, err
	}
	return result, nil
}

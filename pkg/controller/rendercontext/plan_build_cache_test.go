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
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderPlanCacheExactHitAndReturnedPlanIsolation(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	want := fixture.plan.Clone()
	generation := fixture.generation

	poisonPlan(fixture.plan)
	registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
	got, err := registry.PlanWithCache(config, dataplane.CloneAuxiliaryFiles(fixture.aux), session)
	require.NoError(t, err)
	assert.Same(t, generation, session.plan)
	assert.True(t, renderplan.ExactlyEqual(want, got))

	poisonPlan(got)
	again, err := registry.PlanWithCache(config, fixture.aux, session)
	require.NoError(t, err)
	assert.Same(t, generation, session.plan)
	assert.True(t, renderplan.ExactlyEqual(want, again))
}

func TestRenderPlanCacheIdentityAuthenticatesOnlyOneExactGeneration(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
	_, initial, err := registry.PlanWithCacheIdentity(config, fixture.aux, session)
	require.NoError(t, err)
	require.NoError(t, initial.ValidateAuthentication())

	_, unchanged, err := registry.PlanWithCacheIdentity(config, fixture.aux, session)
	require.NoError(t, err)
	assert.Same(t, initial, unchanged)
	same, err := initial.SameRoot(unchanged)
	require.NoError(t, err)
	assert.True(t, same)

	changedAuxiliary := dataplane.CloneAuxiliaryFiles(fixture.aux)
	changedAuxiliary.MapFiles[0].Content = "a changed\n"
	_, changed, err := registry.PlanWithCacheIdentity(config, changedAuxiliary, session)
	require.NoError(t, err)
	require.NoError(t, changed.ValidateAuthentication())
	assert.NotSame(t, initial, changed)
	same, err = initial.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, same)

	copied := *initial
	require.ErrorContains(t, copied.ValidateAuthentication(), "identity is invalid")
	_, err = initial.SameRoot(&copied)
	require.ErrorContains(t, err, "identity is invalid")
}

func TestRenderPlanCacheInvalidatesEveryPlanInput(t *testing.T) {
	t.Run("auxiliary content", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		changed := dataplane.CloneAuxiliaryFiles(fixture.aux)
		changed.MapFiles[0].Content = "a changed\n"
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

		plan, err := registry.PlanWithCache(config, changed, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
		assert.Equal(t, "changed", plan.Maps["host.map"].Entries[0].Value)
	})

	t.Run("auxiliary metadata", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		changed := dataplane.CloneAuxiliaryFiles(fixture.aux)
		reload := true
		changed.GeneralFiles[0].ReloadOnPush = &reload
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

		plan, err := registry.PlanWithCache(config, changed, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
		assert.True(t, planFileByPath(t, plan, "files/errors.http").ReloadOnChange)
	})

	t.Run("direct backend metadata", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		registry, config, session := fixture.newRegistry(
			t, planBuildCacheBackendRecord("leastconn"), nil, fixture.document,
		)
		assert.Same(t, fixture.registry.assembly, registry.assembly)

		plan, err := registry.PlanWithCache(config, fixture.aux, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
		assert.Equal(t, "leastconn", plan.Backends["be_app"].Balance)
	})

	t.Run("map ordering", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		ordered := false
		registry, config, session := fixture.newRegistry(
			t, planBuildCacheBackendRecord(""), &ordered, fixture.document,
		)
		assert.Same(t, fixture.registry.assembly, registry.assembly)

		plan, err := registry.PlanWithCache(config, fixture.aux, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
		assert.False(t, plan.Maps["host.map"].Ordered)
	})

	t.Run("path resolver", func(t *testing.T) {
		paths := &templating.PathResolver{
			BaseDir: "/etc/haproxy", MapsDir: "maps", SSLDir: "ssl", CRTListDir: "ssl", GeneralDir: "files",
		}
		fixture := newPlanBuildCacheFixture(t, paths, planBuildCacheAuxiliaryFiles())
		paths.MapsDir = "changed-maps"
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

		plan, err := registry.PlanWithCache(config, fixture.aux, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
		assert.Contains(t, plan.Maps, "changed-maps/host.map")
	})

	t.Run("assembly generation", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		document := planBuildCacheDocument(t, fixture.rendered)
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, document)
		assert.NotSame(t, fixture.registry.assembly, registry.assembly)

		_, err := registry.PlanWithCache(config, fixture.aux, session)
		require.NoError(t, err)
		assert.NotSame(t, fixture.generation, session.plan)
	})

	t.Run("config mismatch", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

		_, err := registry.PlanWithCache(config+"# changed\n", fixture.aux, session)
		require.ErrorContains(t, err, "assembly does not match its config")
		assert.Nil(t, session.plan)

		_, err = registry.PlanWithCache(config, fixture.aux, session)
		require.NoError(t, err)
		assert.Same(t, fixture.generation, session.plan)
	})
}

func TestRenderPlanCacheRejectsStaleRegistryAssembly(t *testing.T) {
	t.Run("direct section attached after assembly", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
		_, err := registry.Section(renderplan.SectionKindProfile, "late", "defaults late\n")
		require.NoError(t, err)

		_, err = registry.PlanWithCache(config, fixture.aux, session)
		require.ErrorContains(t, err, "assembly does not match its registry")
		assert.Nil(t, session.plan)
	})

	t.Run("prepared plan attached after assembly", func(t *testing.T) {
		fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
		backend, err := PreparePlanBackend(map[string]any{"name": "be_late"}, "backend be_late\n")
		require.NoError(t, err)
		prepared, err := NewPreparedPlanSnapshot().WithBackend(&backend)
		require.NoError(t, err)
		require.NoError(t, registry.AttachPreparedPlan(prepared))

		_, err = registry.PlanWithCache(config, fixture.aux, session)
		require.ErrorContains(t, err, "assembly does not match its registry")
		assert.Nil(t, session.plan)
	})
}

func TestRenderPlanCacheRejectsCopiedOrSubstitutedState(t *testing.T) {
	tests := []struct {
		name   string
		poison func(*renderPlanGeneration) *renderPlanGeneration
	}{
		{
			name: "generation copy",
			poison: func(valid *renderPlanGeneration) *renderPlanGeneration {
				copied := *valid
				return &copied
			},
		},
		{
			name: "plan copy",
			poison: func(valid *renderPlanGeneration) *renderPlanGeneration {
				copied := *valid
				copied.plan = valid.plan.Clone()
				copied.seal = &copied
				return &copied
			},
		},
		{
			name: "auxiliary files copy",
			poison: func(valid *renderPlanGeneration) *renderPlanGeneration {
				copiedAux := *valid.aux
				copied := *valid
				copied.aux = &copiedAux
				copied.seal = &copied
				return &copied
			},
		},
		{
			name: "input copy",
			poison: func(valid *renderPlanGeneration) *renderPlanGeneration {
				copiedInputs := *valid.inputs
				copied := *valid
				copied.inputs = &copiedInputs
				copied.seal = &copied
				return &copied
			},
		},
		{
			name: "assembly copy",
			poison: func(valid *renderPlanGeneration) *renderPlanGeneration {
				copiedAssembly := *valid.assembly
				copied := *valid
				copied.assembly = &copiedAssembly
				copied.seal = &copied
				return &copied
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
			valid := fixture.state.publication
			poisonedCandidate := *valid.candidate
			poisonedCandidate.plan = test.poison(fixture.generation)
			poisonedCandidate.seal = &poisonedCandidate
			poisonedPublication := *valid
			poisonedPublication.candidate = &poisonedCandidate
			poisonedPublication.seal = &poisonedPublication

			require.Error(t, poisonedPublication.ValidateAuthentication())
			_, err := fixture.state.cache.Begin(
				fixture.state.engine, fixture.state.occurrence+1, &poisonedPublication,
			)
			require.Error(t, err)

			registry, config, session := fixture.newRegistry(
				t, planBuildCacheBackendRecord(""), nil, fixture.document,
			)
			_, err = registry.PlanWithCache(config, fixture.aux, session)
			require.NoError(t, err)
			assert.Same(t, fixture.generation, session.plan)
		})
	}
}

func TestRenderPlanCacheTreatsNilAndEmptyAuxiliaryFilesEqually(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, nil)
	generation := fixture.generation
	registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

	fromNil, err := registry.PlanWithCache(config, nil, session)
	require.NoError(t, err)
	assert.Same(t, generation, session.plan)
	assert.Len(t, fromNil.Files, 1)

	fromEmpty, err := fixture.registry.PlanWithCache(
		config, &dataplane.AuxiliaryFiles{}, session,
	)
	require.NoError(t, err)
	assert.Same(t, generation, session.plan)
	assert.True(t, renderplan.ExactlyEqual(fromNil, fromEmpty))
}

func TestRenderPlanCacheConcurrentHitsReturnIndependentPlans(t *testing.T) {
	fixture := newPlanBuildCacheFixture(t, nil, planBuildCacheAuxiliaryFiles())
	want := fixture.plan.Clone()
	type attempt struct {
		registry *PlanRegistry
		session  *RenderCacheSession
	}
	attempts := make([]attempt, 32)
	for index := range attempts {
		registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
		require.Equal(t, fixture.config, config)
		require.Same(t, fixture.registry.assembly, registry.assembly)
		attempts[index] = attempt{registry: registry, session: session}
	}

	errorsFound := make(chan error, len(attempts))
	var group sync.WaitGroup
	for _, current := range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			plan, err := current.registry.PlanWithCache(fixture.config, fixture.aux, current.session)
			if err == nil && !renderplan.ExactlyEqual(want, plan) {
				err = fmt.Errorf("cache hit returned a different plan")
			}
			if err == nil {
				poisonPlan(plan)
			}
			errorsFound <- err
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	for _, current := range attempts {
		assert.Same(t, fixture.generation, current.session.plan)
	}

	registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)
	after, err := registry.PlanWithCache(config, fixture.aux, session)
	require.NoError(t, err)
	assert.True(t, renderplan.ExactlyEqual(want, after))
}

func TestRenderPlanCacheFailedBuildDoesNotPublish(t *testing.T) {
	paths := &templating.PathResolver{
		BaseDir: "/etc/haproxy", MapsDir: "maps", SSLDir: "ssl", CRTListDir: "ssl", GeneralDir: "files",
	}
	fixture := newPlanBuildCacheFixture(t, paths, planBuildCacheAuxiliaryFiles())
	bad := dataplane.CloneAuxiliaryFiles(fixture.aux)
	bad.MapFiles[0].Path = "../escape.map"
	registry, config, session := fixture.newRegistry(t, planBuildCacheBackendRecord(""), nil, fixture.document)

	_, err := registry.PlanWithCache(config, bad, session)
	require.ErrorContains(t, err, "escape.map")
	assert.Nil(t, session.plan)

	_, err = registry.PlanWithCache(config, fixture.aux, session)
	require.NoError(t, err)
	assert.Same(t, fixture.generation, session.plan)
}

func BenchmarkRenderPlanCache3000BackendsAndMapEntries(b *testing.B) {
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(b, err)
	state := newRenderCacheTestState(b, engine)
	authority := NewPlanTokenAuthority()
	registry, err := NewPlanRegistryWithAuthority(nil, authority)
	require.NoError(b, err)

	var rendered strings.Builder
	rendered.WriteString("global\n")
	var mapContent strings.Builder
	for index := range 3000 {
		name := fmt.Sprintf("be_%04d", index)
		text := fmt.Sprintf("backend %s\n    server SRV_1 10.0.%d.%d:8080\n", name, index/256, index%256)
		token, registerErr := registry.Backend(map[string]any{
			"name": name,
			"mode": "http",
			"servers": []any{map[string]any{
				"name": "SRV_1", "address": fmt.Sprintf("10.0.%d.%d", index/256, index%256), "port": 8080,
			}},
		}, text)
		require.NoError(b, registerErr)
		rendered.WriteString(token)
		fmt.Fprintf(&mapContent, "route-%04d.example.com %s\n", index, name)
	}
	aux := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{
		Path: "host.map", Content: mapContent.String(),
	}}}
	document := planBuildCacheDocument(b, rendered.String())
	seedSession := state.begin(b)
	config, _, err := assembleCachedCandidate(
		b.Context(), seedSession, registry, rendered.String(), document,
	)
	require.NoError(b, err)
	_, err = registry.PlanWithCache(config, aux, seedSession)
	require.NoError(b, err)
	state.retain(b, b.Context(), seedSession)
	hitSession := state.begin(b)
	_, _, err = assembleCachedCandidate(b.Context(), hitSession, registry, rendered.String(), document)
	require.NoError(b, err)

	b.Run("build", func(b *testing.B) {
		b.ReportAllocs()
		var plan *renderplan.Plan
		for b.Loop() {
			plan, err = registry.Plan(config, aux)
			if err != nil {
				b.Fatal(err)
			}
		}
		runtime.KeepAlive(plan)
	})

	b.Run("cache_hit", func(b *testing.B) {
		b.ReportAllocs()
		var plan *renderplan.Plan
		for b.Loop() {
			plan, err = registry.PlanWithCache(config, aux, hitSession)
			if err != nil {
				b.Fatal(err)
			}
		}
		runtime.KeepAlive(plan)
	})
}

type planBuildCacheFixture struct {
	state      *renderCacheTestState
	authority  *PlanTokenAuthority
	paths      *templating.PathResolver
	registry   *PlanRegistry
	document   rendercontent.Document
	rendered   string
	config     string
	aux        *dataplane.AuxiliaryFiles
	plan       *renderplan.Plan
	generation *renderPlanGeneration
}

func newPlanBuildCacheFixture(
	tb testing.TB,
	paths *templating.PathResolver,
	aux *dataplane.AuxiliaryFiles,
) *planBuildCacheFixture {
	tb.Helper()
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(tb, err)
	state := newRenderCacheTestState(tb, engine)
	authority := NewPlanTokenAuthority()
	registry, err := NewPlanRegistryWithAuthority(paths, authority)
	require.NoError(tb, err)
	token, err := registry.Backend(planBuildCacheBackendRecord(""), planBuildCacheBackendText)
	require.NoError(tb, err)
	rendered := "global\n" + token
	document := planBuildCacheDocument(tb, rendered)
	session := state.begin(tb)
	config, _, err := assembleCachedCandidate(tb.Context(), session, registry, rendered, document)
	require.NoError(tb, err)
	plan, err := registry.PlanWithCache(config, aux, session)
	require.NoError(tb, err)
	state.retain(tb, tb.Context(), session)
	generation := state.publication.candidate.plan
	require.NotNil(tb, generation)
	return &planBuildCacheFixture{
		state: state, authority: authority, paths: paths, registry: registry,
		document: document, rendered: rendered, config: config, aux: aux, plan: plan, generation: generation,
	}
}

func (f *planBuildCacheFixture) newRegistry(
	tb testing.TB,
	record map[string]any,
	ordered *bool,
	document rendercontent.Document,
) (*PlanRegistry, string, *RenderCacheSession) {
	tb.Helper()
	registry, err := NewPlanRegistryWithAuthority(f.paths, f.authority)
	require.NoError(tb, err)
	token, err := registry.Backend(record, planBuildCacheBackendText)
	require.NoError(tb, err)
	if ordered != nil {
		require.NoError(tb, registry.MapMeta("host.map", *ordered))
	}
	rendered := "global\n" + token
	require.Equal(tb, f.rendered, rendered)
	session := f.state.begin(tb)
	config, _, err := assembleCachedCandidate(tb.Context(), session, registry, rendered, document)
	require.NoError(tb, err)
	return registry, config, session
}

func planBuildCacheBackendRecord(balance string) map[string]any {
	record := map[string]any{
		"name":     "be_app",
		"mode":     "http",
		"shape":    renderplan.ShapeDynamic,
		"body":     []any{"http-reuse safe"},
		"comments": []any{"generated"},
		"servers": []any{map[string]any{
			"name": "SRV_1", "address": "10.0.0.1", "port": 8080, "weight": 10,
			"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
		}},
		"defaultServer": []any{map[string]any{"name": "check", "args": []any{"fall", "3"}}},
	}
	if balance != "" {
		record["balance"] = balance
	}
	return record
}

func planBuildCacheAuxiliaryFiles() *dataplane.AuxiliaryFiles {
	reload := false
	return &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "host.map", Content: "a one\nb two\n"}},
		GeneralFiles: []auxiliaryfiles.GeneralFile{{
			Filename: "errors.http", Path: "files/errors.http", Content: "error\n", ReloadOnPush: &reload,
		}},
	}
}

func planBuildCacheDocument(tb testing.TB, text string) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	_, err := builder.WriteString(text)
	require.NoError(tb, err)
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func planFileByPath(tb testing.TB, plan *renderplan.Plan, path string) renderplan.File {
	tb.Helper()
	for _, file := range plan.Files {
		if file.Path == path {
			return file
		}
	}
	require.Failf(tb, "missing plan file", "path %q", path)
	return renderplan.File{}
}

func poisonPlan(plan *renderplan.Plan) {
	plan.SchemaVersion++
	plan.ID = "poisoned"
	plan.Sections[0].Text = "poisoned"
	backend := plan.Backends["be_app"]
	backend.Balance = "poisoned"
	*backend.Servers[0].Weight = 999
	backend.Servers[0].Extra[0].Args[0] = "poisoned"
	backend.DefaultServer[0].Args[0] = "poisoned"
	backend.Body[0] = "poisoned"
	backend.Comments[0] = "poisoned"
	plan.Backends["be_app"] = backend
	mapFile := plan.Maps["host.map"]
	mapFile.Entries[0].Value = "poisoned"
	plan.Maps["host.map"] = mapFile
	plan.Files[0].Content = "poisoned"
}

const planBuildCacheBackendText = "backend be_app\n    server SRV_1 10.0.0.1:8080\n"

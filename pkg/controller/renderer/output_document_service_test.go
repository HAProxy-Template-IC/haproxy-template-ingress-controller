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

package renderer

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const outputDocumentServiceTemplate = `{%%
var section = "backend be_app\n    # " + tostring(extraContext["value"]) + "\n"
var token, sectionErr = planRegistry.Backend(map[string]any{
    "name": "be_app",
    "servers": []any{map[string]any{"name": "srv", "address": "192.0.2.1", "port": 8080}},
}, section)
%%}{% if sectionErr != nil %}{{ fail(tostring(sectionErr)) }}{% end %}global
    daemon
{{ token }}listen stable
    bind :8404
`

func TestRenderServiceDocumentOutputColdMatchesFullOracle(t *testing.T) {
	fixture := newOutputDocumentServiceFixture(t)

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.Plan)
	assertOutputDocumentFullOracle(t, fixture.service, result.OutputSnapshot, nil)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
}

func TestRenderServiceDocumentOutputNoOpReusesExactRoot(t *testing.T) {
	fixture := newOutputDocumentServiceFixture(t)
	first := fixture.renderAndCommit(t)

	unchanged, err := fixture.service.Render(
		t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
	)
	require.NoError(t, err)
	assert.Same(t, first.OutputSnapshot, unchanged.OutputSnapshot)
	unchanged.InputTransaction.Abort()
}

func TestRenderServiceDocumentOutputChangedLeafMatchesFullOracle(t *testing.T) {
	fixture := newOutputDocumentServiceFixture(t)
	first := fixture.renderAndCommit(t)
	fixture.config.TemplatingSettings.ExtraContext["value"] = "changed"

	changed, err := fixture.service.Render(
		t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
	)
	require.NoError(t, err)
	require.NotSame(t, first.OutputSnapshot, changed.OutputSnapshot)
	assert.Contains(t, changed.HAProxyConfig, "# changed")
	assertOutputDocumentFullOracle(t, fixture.service, changed.OutputSnapshot, first.OutputSnapshot)
	assertOnlyOneOutputSectionChanged(t, first.OutputSnapshot, changed.OutputSnapshot)
	assertMaterializedOutputPlan(t, changed)
	firstArtifacts, err := first.OutputSnapshot.ArtifactSnapshot()
	require.NoError(t, err)
	changedArtifacts, err := changed.OutputSnapshot.ArtifactSnapshot()
	require.NoError(t, err)
	sameArtifacts, err := firstArtifacts.SameRoot(changedArtifacts)
	require.NoError(t, err)
	assert.True(t, sameArtifacts)
	require.NoError(t, changed.InputTransaction.Commit(t.Context()))
	changedPlanSnapshot, err := changed.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	fixture.service.planMu.Lock()
	storedLegacyPlan := fixture.service.lastPlan
	storedCurrentConfigRoot := fixture.service.lastCurrentConfigRoot
	fixture.service.planMu.Unlock()
	assert.Nil(t, storedLegacyPlan)
	assert.Nil(t, storedCurrentConfigRoot)
	assert.True(t, fixture.service.skipCurrentConfigProjection)
	assert.NotNil(t, changedPlanSnapshot)
}

func TestRenderServiceCurrentConfigAdvancesAcrossChangesBeforeACK(t *testing.T) {
	fixture := newCurrentConfigDocumentServiceFixture(t)
	firstRoot, firstInputs := requireCurrentConfigFirstRender(t, fixture)
	secondRoot, secondInputs := requireCurrentConfigServerAddressChange(
		t, fixture, firstRoot, firstInputs,
	)
	thirdRoot, thirdInputs, thirdPlan := requireCurrentConfigABAReturn(
		t, fixture, firstRoot, secondRoot, firstInputs, secondInputs,
	)
	requireCurrentConfigPlanOnlyChange(t, fixture, thirdRoot, thirdInputs, thirdPlan)

	fixture.service.planMu.Lock()
	storedLegacyPlan := fixture.service.lastPlan
	fixture.service.planMu.Unlock()
	assert.Nil(t, storedLegacyPlan)
}

func requireCurrentConfigFirstRender(
	t *testing.T,
	fixture *currentConfigDocumentServiceFixture,
) (*exactCycleCurrentConfigRoot, *renderAttemptInputs) {
	t.Helper()
	first := fixture.renderAndCommit(t)
	assert.Contains(t, first.HAProxyConfig, "# previous=none")
	firstRoot := fixture.currentConfigRoot(t)
	firstPlan, err := first.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	require.Same(t, firstPlan, firstRoot.plan)
	firstInputs, err := fixture.service.captureRenderAttemptInputs(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, firstInputs.currentConfigSource.ValidateAuthentication())
	assert.Equal(t, "192.0.2.1", serverAddress(t, fixture.service.currentConfig()))
	return firstRoot, firstInputs
}

func requireCurrentConfigServerAddressChange(
	t *testing.T,
	fixture *currentConfigDocumentServiceFixture,
	firstRoot *exactCycleCurrentConfigRoot,
	firstInputs *renderAttemptInputs,
) (*exactCycleCurrentConfigRoot, *renderAttemptInputs) {
	t.Helper()
	fixture.config.TemplatingSettings.ExtraContext["address"] = "192.0.2.2"
	second := fixture.renderAndCommit(t)
	assert.Nil(t, second.Plan)
	assert.Contains(t, second.HAProxyConfig, "# previous=192.0.2.1")
	secondRoot := fixture.currentConfigRoot(t)
	require.NotSame(t, firstRoot, secondRoot)
	require.NotSame(t, firstRoot.projection, secondRoot.projection)
	secondPlan, err := second.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	require.Same(t, secondPlan, secondRoot.plan)
	secondInputs, err := fixture.service.captureRenderAttemptInputs(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, secondInputs.currentConfigSource.ValidateAuthentication())
	same, err := firstInputs.currentConfigSource.SameRoot(secondInputs.currentConfigSource)
	require.NoError(t, err)
	assert.False(t, same)
	matches, err := newExactCyclePreviousOutputs(
		firstInputs.currentConfigSource, nil, true, false,
	).matches(newExactCyclePreviousOutputs(
		secondInputs.currentConfigSource, nil, true, false,
	))
	require.NoError(t, err)
	assert.False(t, matches, "a ServerIndex change must invalidate currentConfig consumers")
	assert.Equal(t, "192.0.2.2", serverAddress(t, fixture.service.currentConfig()))
	return secondRoot, secondInputs
}

func requireCurrentConfigABAReturn(
	t *testing.T,
	fixture *currentConfigDocumentServiceFixture,
	firstRoot, secondRoot *exactCycleCurrentConfigRoot,
	firstInputs, secondInputs *renderAttemptInputs,
) (*exactCycleCurrentConfigRoot, *renderAttemptInputs, *renderplan.Snapshot) {
	t.Helper()
	fixture.config.TemplatingSettings.ExtraContext["address"] = "192.0.2.1"
	third := fixture.renderAndCommit(t)
	assert.Nil(t, third.Plan)
	assert.Contains(t, third.HAProxyConfig, "# previous=192.0.2.2")
	thirdRoot := fixture.currentConfigRoot(t)
	require.NotSame(t, secondRoot, thirdRoot)
	require.NotSame(t, firstRoot.projection, thirdRoot.projection)
	require.NotSame(t, secondRoot.projection, thirdRoot.projection)
	thirdPlan, err := third.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	require.Same(t, thirdPlan, thirdRoot.plan)
	thirdInputs, err := fixture.service.captureRenderAttemptInputs(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, thirdInputs.currentConfigSource.ValidateAuthentication())
	same, err := secondInputs.currentConfigSource.SameRoot(thirdInputs.currentConfigSource)
	require.NoError(t, err)
	assert.False(t, same, "an ABA return must invalidate the current projection")
	same, err = firstInputs.currentConfigSource.SameRoot(thirdInputs.currentConfigSource)
	require.NoError(t, err)
	assert.False(t, same, "ABA provenance must not reuse a stale historic projection")
	assert.Equal(t, "192.0.2.1", serverAddress(t, fixture.service.currentConfig()))
	return thirdRoot, thirdInputs, thirdPlan
}

func requireCurrentConfigPlanOnlyChange(
	t *testing.T,
	fixture *currentConfigDocumentServiceFixture,
	thirdRoot *exactCycleCurrentConfigRoot,
	thirdInputs *renderAttemptInputs,
	thirdPlan *renderplan.Snapshot,
) {
	t.Helper()
	fourth := fixture.renderAndCommit(t)
	assert.Nil(t, fourth.Plan)
	assert.Contains(t, fourth.HAProxyConfig, "# previous=192.0.2.1")
	fourthRoot := fixture.currentConfigRoot(t)
	require.NotSame(t, thirdRoot, fourthRoot)
	fourthPlan, err := fourth.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	require.Same(t, fourthPlan, fourthRoot.plan)
	require.Same(t, thirdRoot.projection, fourthRoot.projection)
	fourthInputs, err := fixture.service.captureRenderAttemptInputs(rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, fourthInputs.currentConfigSource.ValidateAuthentication())
	same, err := thirdInputs.currentConfigSource.SameRoot(fourthInputs.currentConfigSource)
	require.NoError(t, err)
	assert.True(t, same)
	matches, err := newExactCyclePreviousOutputs(
		thirdInputs.currentConfigSource, nil, true, false,
	).matches(newExactCyclePreviousOutputs(
		fourthInputs.currentConfigSource, nil, true, false,
	))
	require.NoError(t, err)
	assert.True(t, matches, "a plan-only change must not invalidate currentConfig consumers")
	assert.Equal(t, "192.0.2.1", serverAddress(t, fixture.service.currentConfig()))
	tamperedRoot := *fourthRoot
	tamperedRoot.plan = thirdPlan
	tamperedRoot.seal = &tamperedRoot
	require.ErrorContains(t,
		newExactCycleCurrentConfigSource(&tamperedRoot).ValidateAuthentication(),
		"invalid provenance",
	)
}

func assertMaterializedOutputPlan(tb testing.TB, result *RenderResult) {
	tb.Helper()
	materialized, err := result.MaterializePlan()
	require.NoError(tb, err)
	require.NotNil(tb, materialized)
	snapshot, err := result.OutputSnapshot.PlanSnapshot()
	require.NoError(tb, err)
	want, err := snapshot.LegacyCopy()
	require.NoError(tb, err)
	require.True(tb, renderplan.ExactlyEqual(want, materialized))
	require.Equal(tb, result.PlanID, materialized.ID)
}

type outputDocumentServiceFixture struct {
	config   *config.Config
	service  *RenderService
	provider stores.StoreProvider
}

type currentConfigDocumentServiceFixture struct {
	config   *config.Config
	service  *RenderService
	provider stores.StoreProvider
}

const currentConfigDocumentServiceTemplate = `{%%
var previous = "none"
if len(currentConfig.ServerIndex) > 0 {
    previous = currentConfig.ServerIndex["be_app"]["srv1"].Address
}
var address = tostring(extraContext["address"])
var section = "backend be_app\n    # previous=" + previous + "\n"
var token, sectionErr = planRegistry.Backend(map[string]any{
    "name": "be_app",
    "servers": []any{map[string]any{"name": "srv1", "address": address, "port": 8080}},
}, section)
%%}{% if sectionErr != nil %}{{ fail(tostring(sectionErr)) }}{% end %}global
    daemon
{{ token }}`

func newCurrentConfigDocumentServiceFixture(tb testing.TB) *currentConfigDocumentServiceFixture {
	tb.Helper()
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: currentConfigDocumentServiceTemplate},
		Dataplane:     testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"address": "192.0.2.1"},
		},
	}
	engine, err := templating.New(
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		&templating.Options{Declarations: map[string]any{
			"currentConfig": (*renderplan.CurrentConfig)(nil),
		}},
	)
	require.NoError(tb, err)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
	})
	service.exactCycleProgram = nil
	return &currentConfigDocumentServiceFixture{
		config: cfg, service: service,
		provider: &mockStoreProvider{storeMap: map[string]stores.Store{}},
	}
}

func (f *currentConfigDocumentServiceFixture) renderAndCommit(tb testing.TB) *RenderResult {
	tb.Helper()
	result, err := f.service.Render(tb.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(tb, err)
	require.NoError(tb, result.InputTransaction.Commit(tb.Context()))
	return result
}

func (f *currentConfigDocumentServiceFixture) currentConfigRoot(tb testing.TB) *exactCycleCurrentConfigRoot {
	tb.Helper()
	f.service.planMu.Lock()
	defer f.service.planMu.Unlock()
	require.Nil(tb, f.service.lastPlan)
	require.NotNil(tb, f.service.lastCurrentConfigRoot)
	return f.service.lastCurrentConfigRoot
}

func newOutputDocumentServiceFixture(tb testing.TB) *outputDocumentServiceFixture {
	tb.Helper()
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: outputDocumentServiceTemplate},
		Dataplane:     testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]any{"value": "base"},
		},
	}
	engine, err := templating.New(
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template}, nil,
	)
	require.NoError(tb, err)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(), Capabilities: defaultCapabilities(),
	})
	service.exactCycleProgram = nil
	return &outputDocumentServiceFixture{
		config: cfg, service: service,
		provider: &mockStoreProvider{storeMap: map[string]stores.Store{}},
	}
}

func (f *outputDocumentServiceFixture) renderAndCommit(tb testing.TB) *RenderResult {
	tb.Helper()
	result, err := f.service.Render(tb.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(tb, err)
	require.NoError(tb, result.InputTransaction.Commit(tb.Context()))
	return result
}

func assertOutputDocumentFullOracle(
	tb testing.TB,
	service *RenderService,
	got *renderoutput.Snapshot,
	previous *renderoutput.Snapshot,
) {
	tb.Helper()
	document, err := got.ConfigDocument()
	require.NoError(tb, err)
	planSnapshot, err := got.PlanSnapshot()
	require.NoError(tb, err)
	plan, err := planSnapshot.LegacyCopy()
	require.NoError(tb, err)
	artifacts, err := got.ArtifactSnapshot()
	require.NoError(tb, err)
	want, err := renderoutput.NewSnapshotFromDocument(
		service.outputAuthority, document, plan, artifacts, previous,
	)
	require.NoError(tb, err)
	equal, err := want.ExactEqual(got)
	require.NoError(tb, err)
	require.True(tb, equal)
}

func assertOnlyOneOutputSectionChanged(
	tb testing.TB,
	before *renderoutput.Snapshot,
	after *renderoutput.Snapshot,
) {
	tb.Helper()
	beforePlan, err := before.PlanSnapshot()
	require.NoError(tb, err)
	afterPlan, err := after.PlanSnapshot()
	require.NoError(tb, err)
	beforeSections, err := beforePlan.SectionsCopy()
	require.NoError(tb, err)
	afterSections, err := afterPlan.SectionsCopy()
	require.NoError(tb, err)
	require.Len(tb, afterSections, len(beforeSections))
	changed := 0
	for index := range beforeSections {
		if beforeSections[index] != afterSections[index] {
			changed++
		}
	}
	require.Equal(tb, 1, changed)
}

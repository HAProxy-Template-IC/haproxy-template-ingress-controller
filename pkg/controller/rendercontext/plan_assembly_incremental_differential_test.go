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
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type assemblySection struct {
	name string
	text string
}

// assemblyStep is one render: the declarations, the token order the main
// template emits, and the auxiliary files the plan is built over.
type assemblyStep struct {
	name         string
	backends     []assemblySection
	profiles     []assemblySection
	preparedName string
	aux          *dataplane.AuxiliaryFiles
	wantFallback string
	wantRebuilt  int
	anyReuse     bool
}

func TestIncrementalAssemblyMatchesFullAssembly(t *testing.T) {
	base := []assemblySection{
		{name: "be_alpha", text: "backend be_alpha\n    server s1 10.0.0.1:80\n"},
		{name: "be_beta", text: "backend be_beta\n    server s1 10.0.0.2:80\n"},
		{name: "be_gamma", text: "backend be_gamma\n    server s1 10.0.0.3:80\n"},
		{name: "be_delta", text: "backend be_delta\n    server s1 10.0.0.4:80\n"},
	}
	profiles := []assemblySection{
		{name: "p_fast", text: "defaults p_fast\n    timeout connect 1s\n"},
		{name: "p_slow", text: "defaults p_slow\n    timeout connect 9s\n"},
	}
	steps := []assemblyStep{
		{name: "cold", backends: base, profiles: profiles, aux: auxFixture("a"), wantFallback: assemblyFallbackNoPrevious, wantRebuilt: 11},
		{name: "no change", backends: base, profiles: profiles, aux: auxFixture("a")},
		{
			name:        "update one middle section",
			backends:    replaceSection(base, 1, "backend be_beta\n    server s1 10.0.0.22:80\n"),
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:        "update the first section",
			backends:    replaceSection(replaceSection(base, 1, "backend be_beta\n    server s1 10.0.0.22:80\n"), 0, "backend be_alpha\n    server s1 10.0.0.11:80\n"),
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:        "update the last section",
			backends:    replaceSection(replaceSection(replaceSection(base, 1, "backend be_beta\n    server s1 10.0.0.22:80\n"), 0, "backend be_alpha\n    server s1 10.0.0.11:80\n"), 3, "backend be_delta\n    server s1 10.0.0.44:80\n"),
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:        "update many sections",
			backends:    base,
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 3,
		},
		{
			name:        "section without a trailing newline",
			backends:    replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"),
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:        "empty section text",
			backends:    replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""),
			profiles:    profiles,
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:        "profile body change",
			backends:    replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""),
			profiles:    replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:         auxFixture("a"),
			wantRebuilt: 1,
		},
		{
			name:         "add a profile",
			backends:     replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""),
			profiles:     append(replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"), assemblySection{name: "p_bulk", text: "defaults p_bulk\n"}),
			aux:          auxFixture("a"),
			wantFallback: assemblyFallbackSectionCount,
			wantRebuilt:  8,
		},
		{
			name:         "remove a profile",
			backends:     replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:          auxFixture("a"),
			wantFallback: assemblyFallbackUnregistered,
			wantRebuilt:  7,
		},
		{
			name:         "add a section",
			backends:     append(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), assemblySection{name: "be_epsilon", text: "backend be_epsilon\n"}),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:          auxFixture("a"),
			wantFallback: assemblyFallbackSourceChanged,
			wantRebuilt:  3,
		},
		{
			name:         "delete a section",
			backends:     removeSection(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), 1),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:          auxFixture("a"),
			wantFallback: assemblyFallbackSourceChanged,
			wantRebuilt:  5,
		},
		{
			name:         "reorder sections",
			backends:     reorderSections(removeSection(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), 1)),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:          auxFixture("a"),
			wantFallback: assemblyFallbackSourceChanged,
			wantRebuilt:  4,
		},
		{
			name:         "auxiliary files change only",
			backends:     reorderSections(removeSection(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), 1)),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			aux:          auxFixture("b"),
			wantFallback: "",
			wantRebuilt:  0,
		},
		{
			name:         "attach a prepared plan",
			backends:     reorderSections(removeSection(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), 1)),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			preparedName: "be_alpha",
			aux:          auxFixture("b"),
			wantFallback: assemblyFallbackPreparedChanged,
			wantRebuilt:  0,
		},
		{
			name:         "prepared plan retained",
			backends:     reorderSections(removeSection(replaceSection(replaceSection(base, 2, "backend be_gamma\n    server s1 10.0.0.33:80"), 3, ""), 1)),
			profiles:     replaceSection(profiles, 0, "defaults p_fast\n    timeout connect 2s\n"),
			preparedName: "be_alpha",
			aux:          auxFixture("b"),
			wantRebuilt:  0,
		},
	}

	harness := newAssemblyDifferentialHarness(t)
	for index := range steps {
		t.Run(steps[index].name, func(t *testing.T) {
			harness.run(t, &steps[index])
		})
	}
}

// TestIncrementalAssemblyMatchesFullAssemblyUnderRandomMutations drives the
// mutations a hand-written table cannot enumerate: the orderings that shift
// every part index at once.
func TestIncrementalAssemblyMatchesFullAssemblyUnderRandomMutations(t *testing.T) {
	for seed := range uint64(8) {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, seed+1))
			harness := newAssemblyDifferentialHarness(t)
			step := assemblyStep{
				backends: []assemblySection{
					{name: "be_0", text: "backend be_0\n"},
					{name: "be_1", text: "backend be_1\n"},
					{name: "be_2", text: "backend be_2\n"},
				},
				profiles: []assemblySection{{name: "p_0", text: "defaults p_0\n"}},
				aux:      auxFixture("a"),
				anyReuse: true,
			}
			nextName, carried := 3, 0
			for round := range 24 {
				step.aux = auxFixture(fmt.Sprintf("v%d", round%3))
				if reuse := harness.run(t, &step); reuse.FallbackReason == "" && reuse.Rebuilt > 0 {
					carried++
				}
				step.backends, step.profiles, nextName = mutateSections(
					random, step.backends, step.profiles, nextName, round,
				)
			}
			require.Positive(t, carried, "the incremental assembly never engaged")
		})
	}
}

func mutateSections(
	random *rand.Rand,
	backends, profiles []assemblySection,
	nextName, round int,
) (mutatedBackends, mutatedProfiles []assemblySection, freeName int) {
	backends = append([]assemblySection(nil), backends...)
	profiles = append([]assemblySection(nil), profiles...)
	switch random.IntN(7) {
	case 0:
		if len(backends) > 0 {
			index := random.IntN(len(backends))
			backends[index].text = fmt.Sprintf("backend %s\n    server s%d 10.0.0.%d:80\n",
				backends[index].name, round, round%250+1)
		}
	case 1:
		backends = append(backends, assemblySection{
			name: fmt.Sprintf("be_%d", nextName),
			text: fmt.Sprintf("backend be_%d\n", nextName),
		})
		nextName++
	case 2:
		if len(backends) > 1 {
			backends = removeSection(backends, random.IntN(len(backends)))
		}
	case 3:
		if len(backends) > 1 {
			left, right := random.IntN(len(backends)), random.IntN(len(backends))
			backends[left], backends[right] = backends[right], backends[left]
		}
	case 4:
		profiles = append(profiles, assemblySection{
			name: fmt.Sprintf("p_%d", nextName),
			text: fmt.Sprintf("defaults p_%d\n", nextName),
		})
		nextName++
	case 5:
		if len(profiles) > 0 {
			profiles = removeSection(profiles, random.IntN(len(profiles)))
		}
	case 6:
		if len(profiles) > 0 {
			index := random.IntN(len(profiles))
			profiles[index].text = fmt.Sprintf("defaults %s\n    timeout connect %ds\n",
				profiles[index].name, round%9+1)
		}
	}
	return backends, profiles, nextName
}

// TestIncrementalAssemblyDistinguishesIdenticalSectionTexts renames one of two
// sections that share their bytes. The config is unchanged, so only the section
// names can tell the two emissions apart.
func TestIncrementalAssemblyDistinguishesIdenticalSectionTexts(t *testing.T) {
	harness := newAssemblyDifferentialHarness(t)
	backends := []assemblySection{
		{name: "be_alpha", text: "backend be_alpha\n"},
		{name: "be_beta", text: "backend be_beta\n"},
	}
	twin := "defaults twin\n    timeout connect 1s\n"
	harness.run(t, &assemblyStep{
		backends: backends,
		profiles: []assemblySection{{name: "p_twin_a", text: twin}, {name: "p_twin_b", text: twin}},
		aux:      auxFixture("a"),
		anyReuse: true,
	})
	reuse := harness.run(t, &assemblyStep{
		backends: backends,
		profiles: []assemblySection{{name: "p_twin_a", text: twin}, {name: "p_twin_c", text: twin}},
		aux:      auxFixture("a"),
		anyReuse: true,
	})
	require.Equal(t, assemblyFallbackUnregistered, reuse.FallbackReason)
}

func TestIncrementalAssemblyRejectsATokenInSectionText(t *testing.T) {
	harness := newAssemblyDifferentialHarness(t)
	base := []assemblySection{{name: "be_alpha", text: "backend be_alpha\n"}}
	harness.run(t, &assemblyStep{
		name: "cold", backends: base, wantFallback: assemblyFallbackNoPrevious, wantRebuilt: 3,
	})

	registry := harness.registry(t)
	poisoned := harness.declare(t, registry, &assemblyStep{
		backends: []assemblySection{
			{name: "be_alpha", text: "backend be_alpha\n" + registry.sectionToken(renderplan.SectionKindBackend, "be_alpha")},
		},
	})
	source := harness.sourceDocument(t, poisoned)
	session := harness.state.begin(t)
	generation, err := session.prepareIdentityDocument(source, harness.proof)
	require.NoError(t, err)
	_, _, err = assembleCachedDocument(t.Context(), registry, source, session, generation)
	require.ErrorContains(t, err, "a token survived assembly")
}

type assemblyDifferentialHarness struct {
	engine            templating.Engine
	proof             *templating.PostProcessReuseProof
	authority         *PlanTokenAuthority
	state             *renderCacheTestState
	planAuthority     *renderplan.Authority
	artifactAuthority *renderartifact.Authority
	outputAuthority   *renderoutput.Authority
	source            *rendercontent.Document
	prepared          *PreparedPlanSnapshot
}

func newAssemblyDifferentialHarness(tb testing.TB) *assemblyDifferentialHarness {
	tb.Helper()
	engine, err := templating.New(map[string]string{names.MainTemplateName: "global\n"}, nil)
	require.NoError(tb, err)
	proof, err := engine.PostProcessReuseProof(names.MainTemplateName)
	require.NoError(tb, err)
	require.NotNil(tb, proof)
	planAuthority := renderplan.NewAuthority()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(tb, err)
	return &assemblyDifferentialHarness{
		engine:            engine,
		proof:             proof,
		authority:         NewPlanTokenAuthority(),
		state:             newRenderCacheTestState(tb, engine),
		planAuthority:     planAuthority,
		artifactAuthority: artifactAuthority,
		outputAuthority:   outputAuthority,
	}
}

func (h *assemblyDifferentialHarness) registry(tb testing.TB) *PlanRegistry {
	tb.Helper()
	registry, err := NewPlanRegistryWithAuthority(nil, h.authority)
	require.NoError(tb, err)
	return registry
}

// declare registers a step's sections and returns the config the main template
// would have rendered for them.
func (h *assemblyDifferentialHarness) declare(
	tb testing.TB,
	registry *PlanRegistry,
	step *assemblyStep,
) string {
	tb.Helper()
	if step.preparedName != "" {
		require.NoError(tb, registry.AttachPreparedPlan(h.preparedSnapshot(tb, step)))
	}
	var rendered strings.Builder
	rendered.WriteString("global\n    daemon\n")
	for index, backend := range step.backends {
		if step.preparedName == backend.name {
			token, err := registry.PreparedBackendToken(backend.name)
			require.NoError(tb, err)
			rendered.WriteString("# core before " + backend.name + "\n")
			rendered.WriteString(token)
			continue
		}
		token, err := registry.Backend(map[string]any{"name": backend.name}, backend.text)
		require.NoError(tb, err)
		rendered.WriteString("# core before " + backend.name + "\n")
		rendered.WriteString(token)
		if index == 1 && len(step.profiles) > 0 {
			rendered.WriteString(registry.ProfileGroup())
		}
	}
	for _, profile := range step.profiles {
		_, err := registry.Section(renderplan.SectionKindProfile, profile.name, profile.text)
		require.NoError(tb, err)
	}
	if len(step.backends) < 2 && len(step.profiles) > 0 {
		rendered.WriteString(registry.ProfileGroup())
	}
	rendered.WriteString("frontend fe\n    bind :80\n")
	return rendered.String()
}

func (h *assemblyDifferentialHarness) preparedSnapshot(
	tb testing.TB,
	step *assemblyStep,
) *PreparedPlanSnapshot {
	tb.Helper()
	if h.prepared != nil {
		return h.prepared
	}
	var text string
	for _, backend := range step.backends {
		if backend.name == step.preparedName {
			text = backend.text
		}
	}
	prepared, err := PreparePlanBackend(map[string]any{"name": step.preparedName}, text)
	require.NoError(tb, err)
	snapshot, err := NewPreparedPlanSnapshot().WithBackend(&prepared)
	require.NoError(tb, err)
	h.prepared = snapshot
	return snapshot
}

// sourceDocument mirrors the production render path: an unchanged config keeps
// the previous authenticated root, which is what the incremental assembly needs.
func (h *assemblyDifferentialHarness) sourceDocument(
	tb testing.TB,
	rendered string,
) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	_, err := builder.WriteString(rendered)
	require.NoError(tb, err)
	document, err := builder.Build(h.source)
	require.NoError(tb, err)
	h.source = &document
	return document
}

func (h *assemblyDifferentialHarness) run(tb testing.TB, step *assemblyStep) AssemblyReuse {
	tb.Helper()
	oracleRegistry := h.registry(tb)
	oracleRendered := h.declare(tb, oracleRegistry, step)
	incrementalRegistry := h.registry(tb)
	require.Equal(tb, oracleRendered, h.declare(tb, incrementalRegistry, step))

	source := h.sourceDocument(tb, oracleRendered)
	oracleDocument, oracleSections, oracleReuse, err := oracleRegistry.assembleDocument(
		tb.Context(), source, nil, nil, source, false, nil, nil,
	)
	require.NoError(tb, err)
	require.Equal(tb, assemblyFallbackNoPrevious, oracleReuse.FallbackReason)

	session := h.state.begin(tb)
	generation, err := session.prepareIdentityDocument(source, h.proof)
	require.NoError(tb, err)
	document, sections, reuse, err := incrementalRegistry.assembleDocument(
		tb.Context(), source, nil, nil, source, true, session, generation,
	)
	require.NoError(tb, err)
	h.state.retain(tb, tb.Context(), session)

	if !step.anyReuse {
		assert.Equal(tb, step.wantFallback, reuse.FallbackReason, "fallback reason")
		assert.Equal(tb, step.wantRebuilt, reuse.Rebuilt, "rebuilt parts")
	}
	assert.Equal(tb, len(sections), reuse.Reused+reuse.Rebuilt, "every part is accounted for")
	assertSameAssembly(tb, oracleDocument, oracleSections, document, sections)
	h.assertSamePlan(tb, oracleRegistry, oracleDocument, oracleSections,
		incrementalRegistry, document, sections, step.aux)
	return reuse
}

func assertSameAssembly(
	tb testing.TB,
	wantDocument rendercontent.Document,
	wantSections []renderplan.Section,
	gotDocument rendercontent.Document,
	gotSections []renderplan.Section,
) {
	tb.Helper()
	require.Equal(tb, mustDocumentString(tb, wantDocument), mustDocumentString(tb, gotDocument))
	leaves := mustDocumentLeaves(tb, wantDocument)
	require.Equal(tb, leaves, mustDocumentLeaves(tb, gotDocument))
	for index := range leaves {
		wantBytes, err := wantDocument.LeafBytes(index)
		require.NoError(tb, err)
		gotBytes, err := gotDocument.LeafBytes(index)
		require.NoError(tb, err)
		require.Equal(tb, wantBytes, gotBytes, "leaf %d", index)
	}
	require.Equal(tb, wantSections, gotSections)
}

func (h *assemblyDifferentialHarness) assertSamePlan(
	tb testing.TB,
	oracleRegistry *PlanRegistry,
	oracleDocument rendercontent.Document,
	oracleSections []renderplan.Section,
	incrementalRegistry *PlanRegistry,
	document rendercontent.Document,
	sections []renderplan.Section,
	aux *dataplane.AuxiliaryFiles,
) {
	tb.Helper()
	oraclePlan, err := oracleRegistry.Plan(mustDocumentString(tb, oracleDocument), aux)
	require.NoError(tb, err)
	plan, err := incrementalRegistry.Plan(mustDocumentString(tb, document), aux)
	require.NoError(tb, err)
	require.Equal(tb, oraclePlan, plan)
	require.True(tb, renderplan.ExactlyEqual(oraclePlan, plan))
	require.Equal(tb, oracleSections, oraclePlan.Sections)
	require.Equal(tb, sections, plan.Sections)

	artifacts, _, err := dataplane.BuildAuxiliaryFileTransition(h.artifactAuthority, nil, aux)
	require.NoError(tb, err)
	oracleSnapshot, err := renderoutput.NewSnapshotFromDocument(
		h.outputAuthority, oracleDocument, oraclePlan, artifacts, nil,
	)
	require.NoError(tb, err)
	snapshot, err := renderoutput.NewSnapshotFromDocument(
		h.outputAuthority, document, plan, artifacts, nil,
	)
	require.NoError(tb, err)
	assertSameOutputIdentity(tb, oracleSnapshot, snapshot)
}

func assertSameOutputIdentity(tb testing.TB, want, got *renderoutput.Snapshot) {
	tb.Helper()
	wantChecksum, err := want.ContentChecksum()
	require.NoError(tb, err)
	gotChecksum, err := got.ContentChecksum()
	require.NoError(tb, err)
	require.Equal(tb, wantChecksum, gotChecksum)
	wantPlanID, err := want.PlanID()
	require.NoError(tb, err)
	gotPlanID, err := got.PlanID()
	require.NoError(tb, err)
	require.Equal(tb, wantPlanID, gotPlanID)
}

func replaceSection(sections []assemblySection, index int, text string) []assemblySection {
	updated := append([]assemblySection(nil), sections...)
	updated[index].text = text
	return updated
}

func removeSection(sections []assemblySection, index int) []assemblySection {
	updated := append([]assemblySection(nil), sections[:index]...)
	return append(updated, sections[index+1:]...)
}

func reorderSections(sections []assemblySection) []assemblySection {
	updated := append([]assemblySection(nil), sections...)
	updated[0], updated[len(updated)-1] = updated[len(updated)-1], updated[0]
	return updated
}

func auxFixture(variant string) *dataplane.AuxiliaryFiles {
	return &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "maps/host.map", Content: "example.com be_alpha\n" + variant + ".example.com be_beta\n"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "certs/tls.pem", Content: "-----BEGIN CERTIFICATE-----\n" + variant + "\n"},
		},
		SSLCaFiles: []auxiliaryfiles.SSLCaFile{
			{Path: "ca/bundle.pem", Content: "-----BEGIN CERTIFICATE-----\nca" + variant + "\n"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "crt-lists/frontend.list", Content: "certs/tls.pem " + variant + ".example.com\n"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "503.http", Path: "general/503.http", Content: "HTTP/1.1 503\n" + variant + "\n"},
		},
	}
}

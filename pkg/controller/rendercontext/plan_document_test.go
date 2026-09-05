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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestPlanDocumentMatchesLegacyPlanAndOutput(t *testing.T) {
	fixture := newDocumentPlanFixture(t, []string{"one", "two", "three"})
	planAuthority := renderplan.NewAuthority()
	transition, err := fixture.registry.PlanDocument(
		fixture.document, nil, planAuthority, nil,
	)
	require.NoError(t, err)
	require.Nil(t, transition.DocumentDelta)
	require.Nil(t, transition.PlanDelta)
	require.NoError(t, transition.Plan.ValidateAuthentication())

	legacy, err := transition.Plan.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, renderplan.ExactlyEqual(fixture.plan, legacy))
	assert.Equal(t, fixture.plan.ID, legacy.ID)

	artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(t, err)
	documentOutput, err := renderoutput.NewSnapshotFromDocument(
		outputAuthority, transition.Document, legacy, artifacts, nil,
	)
	require.NoError(t, err)
	legacyOutput, err := renderoutput.NewSnapshot(
		outputAuthority, fixture.config, fixture.plan, artifacts, nil,
	)
	require.NoError(t, err)
	equal, err := documentOutput.ExactEqual(legacyOutput)
	require.NoError(t, err)
	assert.True(t, equal)
}

func TestPlanDocumentWarmTransitionsMatchFullOracle(t *testing.T) {
	tests := []struct {
		name string
		base []string
		next []string
	}{
		{name: "replace", base: []string{"one", "two", "three"}, next: []string{"one", "changed", "three"}},
		{name: "insert", base: []string{"one", "three"}, next: []string{"one", "two", "three"}},
		{name: "delete", base: []string{"one", "two", "three"}, next: []string{"one", "three"}},
		{name: "reorder", base: []string{"one", "two", "three"}, next: []string{"three", "one", "two"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planAuthority := renderplan.NewAuthority()
			artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
			outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
			require.NoError(t, err)
			base := newDocumentPlanFixture(t, test.base)
			previous, err := renderoutput.NewSnapshotFromDocument(
				outputAuthority, base.document, base.plan, artifacts, nil,
			)
			require.NoError(t, err)

			next := newDocumentPlanFixture(t, test.next)
			transition, err := next.registry.PlanDocument(
				next.document, nil, planAuthority, previous,
			)
			require.NoError(t, err)
			require.NoError(t, transition.DocumentDelta.ValidateAuthentication())
			require.NoError(t, transition.PlanDelta.ValidateAuthentication())

			artifactTransaction, err := renderartifact.BeginTransaction(artifactAuthority, artifacts)
			require.NoError(t, err)
			_, artifactDelta, err := artifactTransaction.Commit()
			require.NoError(t, err)
			outputTransaction, err := renderoutput.BeginTransaction(
				outputAuthority, previous, transition.DocumentDelta,
				transition.PlanDelta, artifactDelta,
			)
			require.NoError(t, err)
			committed, _, err := outputTransaction.Commit()
			require.NoError(t, err)
			oracle, err := renderoutput.NewSnapshotFromDocument(
				outputAuthority, next.document, next.plan, artifacts, previous,
			)
			require.NoError(t, err)
			equal, err := committed.ExactEqual(oracle)
			require.NoError(t, err)
			assert.True(t, equal)
			assertDocumentPlanOutputFieldsEqual(t, oracle, committed)
		})
	}
}

func TestPlanDocumentExactNoOpReusesPreviousRoots(t *testing.T) {
	planAuthority := renderplan.NewAuthority()
	artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(t, err)
	base := newDocumentPlanFixture(t, []string{"one", "two", "three"})
	previous, err := renderoutput.NewSnapshotFromDocument(
		outputAuthority, base.document, base.plan, artifacts, nil,
	)
	require.NoError(t, err)
	previousDocument, err := previous.ConfigDocument()
	require.NoError(t, err)
	previousPlan, err := previous.PlanSnapshot()
	require.NoError(t, err)

	next := newDocumentPlanFixture(t, []string{"one", "two", "three"})
	transition, err := next.registry.PlanDocument(next.document, nil, planAuthority, previous)
	require.NoError(t, err)
	same, err := previousDocument.SameRoot(transition.Document)
	require.NoError(t, err)
	assert.True(t, same)
	assert.Same(t, previousPlan, transition.Plan)
	documentSame, err := transition.DocumentDelta.SameRoot()
	require.NoError(t, err)
	planSame, err := transition.PlanDelta.SameRoot()
	require.NoError(t, err)
	assert.True(t, documentSame)
	assert.True(t, planSame)
}

func TestPlanDocumentTransitionsFragmentedCoreAssembly(t *testing.T) {
	planAuthority := renderplan.NewAuthority()
	artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(t, err)

	registry := NewPlanRegistry(nil)
	fragmented := documentWithTextFragments(t, "global\n", "    daemon\n")
	document, _, err := registry.AssembleDocument(t.Context(), fragmented, nil)
	require.NoError(t, err)
	initial, err := registry.PlanDocument(document, nil, planAuthority, nil)
	require.NoError(t, err)
	legacy, err := initial.Plan.LegacyCopy()
	require.NoError(t, err)
	previous, err := renderoutput.NewSnapshotFromDocument(
		outputAuthority, initial.Document, legacy, artifacts, nil,
	)
	require.NoError(t, err)

	nextRegistry := NewPlanRegistry(nil)
	nextFragmented := documentWithTextFragments(t, "global\n", "    log stdout\n")
	nextDocument, _, err := nextRegistry.AssembleDocument(t.Context(), nextFragmented, nil)
	require.NoError(t, err)
	next, err := nextRegistry.PlanDocument(nextDocument, nil, planAuthority, previous)
	require.NoError(t, err)
	require.NoError(t, next.DocumentDelta.ValidateAuthentication())
	require.NoError(t, next.PlanDelta.ValidateAuthentication())
	require.Equal(t, 1, mustDocumentLeaves(t, next.Document))
	assert.Equal(t, "global\n    log stdout\n", mustDocumentString(t, next.Document))
}

func TestPlanDocumentFailsClosedOnUnprovenAndStaleInputs(t *testing.T) {
	fixture := newDocumentPlanFixture(t, []string{"one", "two"})
	planAuthority := renderplan.NewAuthority()

	foreign := documentPlanDocument(t, fixture.config)
	_, err := fixture.registry.PlanDocument(foreign, nil, planAuthority, nil)
	require.ErrorContains(t, err, "does not match the authenticated assembly")

	_, err = fixture.registry.Section(
		renderplan.SectionKindProfile, "late", "defaults late\n    mode http\n",
	)
	require.NoError(t, err)
	_, err = fixture.registry.PlanDocument(fixture.document, nil, planAuthority, nil)
	require.ErrorContains(t, err, "proof is stale")

	fixture = newDocumentPlanFixture(t, []string{"one", "two"})
	poisoned := *fixture.registry.documentAssembly
	poisoned.document = foreign
	poisoned.seal = &poisoned
	fixture.registry.documentAssembly = &poisoned
	_, err = fixture.registry.PlanDocument(fixture.document, nil, planAuthority, nil)
	require.ErrorContains(t, err, "proof is stale")

	paths := &templating.PathResolver{BaseDir: "/etc/haproxy", MapsDir: "maps"}
	registry := NewPlanRegistry(paths)
	source := documentPlanDocument(t, "global\n")
	document, _, err := registry.AssembleDocument(t.Context(), source, nil)
	require.NoError(t, err)
	paths.MapsDir = "poisoned"
	_, err = registry.PlanDocument(document, nil, planAuthority, nil)
	require.ErrorContains(t, err, "proof is stale")
}

func TestPlanDocumentRejectsForeignPreviousAndUnrepresentableBatch(t *testing.T) {
	base := newDocumentPlanFixture(t, []string{"one"})
	foreignPlanAuthority := renderplan.NewAuthority()
	artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
	foreignOutputAuthority, err := renderoutput.NewAuthority(foreignPlanAuthority, artifactAuthority)
	require.NoError(t, err)
	previous, err := renderoutput.NewSnapshotFromDocument(
		foreignOutputAuthority, base.document, base.plan, artifacts, nil,
	)
	require.NoError(t, err)

	next := newDocumentPlanFixture(t, []string{"one", "two"})
	_, err = next.registry.PlanDocument(next.document, nil, renderplan.NewAuthority(), previous)
	require.Error(t, err)

	next = newDocumentPlanFixture(t, []string{"one", "two", "three"})
	_, err = next.registry.PlanDocument(next.document, nil, foreignPlanAuthority, previous)
	require.ErrorIs(t, err, renderplan.ErrDocumentTransitionRequiresRebuild)
}

func TestPlanDocumentConcurrentNoOpIsRaceSafe(t *testing.T) {
	planAuthority := renderplan.NewAuthority()
	artifacts, artifactAuthority := emptyDocumentPlanArtifacts(t)
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	require.NoError(t, err)
	base := newDocumentPlanFixture(t, []string{"one", "two", "three"})
	previous, err := renderoutput.NewSnapshotFromDocument(
		outputAuthority, base.document, base.plan, artifacts, nil,
	)
	require.NoError(t, err)
	next := newDocumentPlanFixture(t, []string{"one", "two", "three"})

	const readers = 32
	start := make(chan struct{})
	errorsFound := make(chan error, readers)
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			<-start
			transition, transitionErr := next.registry.PlanDocument(
				next.document, nil, planAuthority, previous,
			)
			if transitionErr == nil {
				var same bool
				same, transitionErr = transition.PlanDelta.SameRoot()
				if transitionErr == nil && !same {
					transitionErr = assert.AnError
				}
			}
			errorsFound <- transitionErr
		}()
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for workerErr := range errorsFound {
		require.NoError(t, workerErr)
	}
}

type documentPlanFixture struct {
	registry *PlanRegistry
	document rendercontent.Document
	config   string
	plan     *renderplan.Plan
}

func newDocumentPlanFixture(tb testing.TB, order []string) documentPlanFixture {
	tb.Helper()
	registry := NewPlanRegistry(nil)
	rendered := "global\n"
	for _, name := range order {
		text := fmt.Sprintf("defaults %s\n    mode http\n", name)
		token, err := registry.Section(renderplan.SectionKindProfile, name, text)
		require.NoError(tb, err)
		rendered += token
	}
	source := documentPlanDocument(tb, rendered)
	document, _, err := registry.AssembleDocument(tb.Context(), source, nil)
	require.NoError(tb, err)
	config, err := document.String()
	require.NoError(tb, err)
	plan, err := registry.Plan(config, nil)
	require.NoError(tb, err)
	return documentPlanFixture{
		registry: registry, document: document, config: config, plan: plan,
	}
}

func documentPlanDocument(tb testing.TB, text string) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	_, err := builder.WriteString(text)
	require.NoError(tb, err)
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func emptyDocumentPlanArtifacts(
	tb testing.TB,
) (*renderartifact.Snapshot, *renderartifact.Authority) {
	tb.Helper()
	authority := renderartifact.NewAuthority()
	builder, err := renderartifact.NewBuilder(authority, nil)
	require.NoError(tb, err)
	snapshot, err := builder.Build()
	require.NoError(tb, err)
	return snapshot, authority
}

func assertDocumentPlanOutputFieldsEqual(
	tb testing.TB,
	want *renderoutput.Snapshot,
	got *renderoutput.Snapshot,
) {
	tb.Helper()
	wantConfig, err := want.Config()
	require.NoError(tb, err)
	gotConfig, err := got.Config()
	require.NoError(tb, err)
	assert.Equal(tb, wantConfig, gotConfig)
	wantID, err := want.PlanID()
	require.NoError(tb, err)
	gotID, err := got.PlanID()
	require.NoError(tb, err)
	assert.Equal(tb, wantID, gotID)
	wantChecksum, err := want.ContentChecksum()
	require.NoError(tb, err)
	gotChecksum, err := got.ContentChecksum()
	require.NoError(tb, err)
	assert.Equal(tb, wantChecksum, gotChecksum)
	wantCounts, err := want.Counts()
	require.NoError(tb, err)
	gotCounts, err := got.Counts()
	require.NoError(tb, err)
	assert.Equal(tb, wantCounts, gotCounts)
}

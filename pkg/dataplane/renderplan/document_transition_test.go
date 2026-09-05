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

package renderplan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestDocumentPlanTransitionPreservesLegacyIdentityAndOwnsSource(t *testing.T) {
	authority := NewAuthority()
	basePlan, baseDocument, baseOracle := documentTransitionFixture(t, []string{"one\n", "two\n"})
	base, delta, err := ReconcileSnapshotWithConfigDocument(
		authority, nil, basePlan, baseDocument,
	)
	require.NoError(t, err)
	require.Nil(t, delta)
	baseLegacy, err := base.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, ExactlyEqual(baseOracle, baseLegacy))
	assert.Equal(t, baseOracle.ID, baseLegacy.ID)

	nextPlan, nextDocument, nextOracle := documentTransitionFixture(t, []string{"one\n", "changed\n"})
	next, delta, err := ReconcileSnapshotWithConfigDocument(
		authority, base, nextPlan, nextDocument,
	)
	require.NoError(t, err)
	require.NoError(t, delta.ValidateAuthentication())
	applied, err := delta.Apply(base)
	require.NoError(t, err)
	assert.Same(t, next, applied)
	changes, err := delta.Changes()
	require.NoError(t, err)
	assert.Len(t, changes.Sections, 1)
	assert.Len(t, changes.Files, 1)

	nextPlan.Sections[1].Text = "caller poison\n"
	nextPlan.Files[0].Size = 1
	legacy, err := next.LegacyCopy()
	require.NoError(t, err)
	assert.True(t, ExactlyEqual(nextOracle, legacy))
	assert.Equal(t, nextOracle.ID, legacy.ID)

	noOpPlan, _, _ := documentTransitionFixture(t, []string{"one\n", "changed\n"})
	noOp, noOpDelta, err := ReconcileSnapshotWithConfigDocument(
		authority, next, noOpPlan, nextDocument,
	)
	require.NoError(t, err)
	assert.Same(t, next, noOp)
	same, err := noOpDelta.SameRoot()
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentPlanTransitionRejectsInexactAndForeignInputs(t *testing.T) {
	validPlan, document, _ := documentTransitionFixture(t, []string{"one\n"})
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{name: "trusted ID", mutate: func(plan *Plan) { plan.ID = "forged" }},
		{name: "unknown section text", mutate: func(plan *Plan) { plan.Sections[0].TextKnown = false }},
		{name: "section length", mutate: func(plan *Plan) { plan.Sections[0].Length++ }},
		{name: "section digest", mutate: func(plan *Plan) { plan.Sections[0].TextDigest = "forged" }},
		{name: "config content", mutate: func(plan *Plan) { plan.Files[0].ContentKnown = true }},
		{name: "config digest", mutate: func(plan *Plan) { plan.Files[0].Digest = "forged" }},
		{name: "config size", mutate: func(plan *Plan) { plan.Files[0].Size++ }},
		{name: "config path", mutate: func(plan *Plan) { plan.Files[0].Path = "other.cfg" }},
		{name: "missing config", mutate: func(plan *Plan) { plan.Files = nil }},
		{name: "inexact auxiliary", mutate: func(plan *Plan) {
			plan.Files = append(plan.Files, File{Path: "map", Kind: FileKindMap})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPlan.Clone()
			test.mutate(plan)
			_, _, err := ReconcileSnapshotWithConfigDocument(
				NewAuthority(), nil, plan, document,
			)
			require.ErrorIs(t, err, errInexactSnapshotPlan)
		})
	}

	baseAuthority := NewAuthority()
	base, _, err := ReconcileSnapshotWithConfigDocument(
		baseAuthority, nil, validPlan, document,
	)
	require.NoError(t, err)
	_, _, err = ReconcileSnapshotWithConfigDocument(
		NewAuthority(), base, validPlan.Clone(), document,
	)
	require.ErrorIs(t, err, errForeignSnapshot)

	batchPlan, batchDocument, _ := documentTransitionFixture(
		t, []string{"one\n", "two\n", "three\n"},
	)
	_, _, err = ReconcileSnapshotWithConfigDocument(
		baseAuthority, base, batchPlan, batchDocument,
	)
	require.ErrorIs(t, err, ErrDocumentTransitionRequiresRebuild)
}

func documentTransitionFixture(
	tb testing.TB,
	texts []string,
) (plan *Plan, document rendercontent.Document, oracle *Plan) {
	tb.Helper()
	plan = &Plan{
		SchemaVersion: SchemaVersion,
		Sections:      make([]Section, len(texts)),
		Backends:      map[string]Backend{},
		Profiles:      map[string]Profile{},
		Maps:          map[string]Map{},
		Files: []File{{
			Path: ConfigFilePath, Kind: FileKindConfig, ReloadOnChange: true,
		}},
	}
	var documentBuilder rendercontent.DocumentBuilder
	config := ""
	for index, text := range texts {
		section := Section{
			Kind: SectionKindCore, Name: "core", Text: text, TextKnown: true,
			TextDigest: DigestString(text), Length: len(text),
		}
		plan.Sections[index] = section
		var partBuilder rendercontent.DocumentBuilder
		_, err := partBuilder.WriteString(text)
		require.NoError(tb, err)
		part, err := partBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, documentBuilder.AppendDocument(part))
		config += text
	}
	document, err := documentBuilder.Build(nil)
	require.NoError(tb, err)
	plan.Files[0].Size = int64(len(config))
	oracle = plan.Clone()
	oracle.Files[0].Content = config
	oracle.Files[0].ContentKnown = true
	oracle.Files[0].Digest = DigestString(config)
	oracle.ComputeID()
	return plan, document, oracle
}

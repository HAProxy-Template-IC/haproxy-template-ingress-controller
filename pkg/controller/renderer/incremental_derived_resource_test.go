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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

func TestIncrementalDerivedResourceSealsCallerBytes(t *testing.T) {
	entry := testIncrementalDerivedResource(t, "original")
	wantSource := string(entry.Source)
	wantValue := string(entry.Value)
	result := &incrementalComponentResult{Derivations: []rendercontext.DerivedResource{entry}}
	txn := newIncrementalStateSnapshot().derived.Txn()

	require.NoError(t, validateDerivedResource(&result.Derivations[0]))
	require.NoError(t, stageValidatedIncrementalColdDerivations(txn, result))
	for index := range result.Derivations[0].Source {
		result.Derivations[0].Source[index] = 'x'
	}
	for index := range result.Derivations[0].Value {
		result.Derivations[0].Value[index] = 'y'
	}

	sealed, found := txn.Get(derivedKey(entry.Identity))
	require.True(t, found)
	assert.Equal(t, wantSource, sealed.Source)
	assert.Equal(t, wantValue, sealed.Value)
}

func TestIncrementalDerivedResourceResolverMaterializesDetachedBytes(t *testing.T) {
	entry := testIncrementalDerivedResource(t, "original")
	require.NoError(t, validateDerivedResource(&entry))
	sealed := ownValidatedIncrementalDerivedResource(&entry)
	txn := newIncrementalStateSnapshot().derived.Txn()
	txn.Insert(derivedKey(entry.Identity), sealed)
	plan := newIncrementalBindingPlan()
	plan.owners[entry.Identity.Resource] = incrementalComponent{name: "governance"}
	session := &incrementalRenderSession{derived: txn, bindingPlan: plan}

	first, found, err := session.resolveDerivedResource(entry.Identity)
	require.NoError(t, err)
	require.True(t, found)
	for index := range first.Source {
		first.Source[index] = 'x'
	}
	for index := range first.Value {
		first.Value[index] = 'y'
	}
	second, found, err := session.resolveDerivedResource(entry.Identity)
	require.NoError(t, err)
	require.True(t, found)

	assert.Equal(t, entry.Source, second.Source)
	assert.Equal(t, entry.Value, second.Value)
	stored, found := txn.Get(derivedKey(entry.Identity))
	require.True(t, found)
	assert.Equal(t, string(entry.Source), stored.Source)
	assert.Equal(t, string(entry.Value), stored.Value)
}

func TestPreparedIncrementalStateCommitRejectsDerivedLeafSubstitution(t *testing.T) {
	entry := testIncrementalDerivedResource(t, "original")
	require.NoError(t, validateDerivedResource(&entry))
	sealed := ownValidatedIncrementalDerivedResource(&entry)
	base := newIncrementalStateSnapshot()
	candidate := newIncrementalStateSnapshot()
	txn := candidate.derived.Txn()
	txn.Insert(derivedKey(entry.Identity), sealed)
	candidate.derived = txn.Commit()
	authenticateIncrementalStateSnapshot(candidate)
	state := &incrementalRenderState{snapshot: base}
	session := &incrementalRenderSession{state: state}
	prepared := &preparedIncrementalStateCommit{
		runtime:  session,
		base:     base,
		snapshot: candidate,
		detached: true,
	}
	require.NoError(t, prepared.validateDetachedPublication())

	poisoned := sealed
	poisoned.Value = string(testIncrementalDerivedResource(t, "poison").Value)
	forged := candidate.derived.Txn()
	forged.Insert(derivedKey(entry.Identity), poisoned)
	candidate.derived = forged.Commit()

	require.Equal(t, 1, candidate.derived.Len())
	require.ErrorContains(t, prepared.validateDetachedPublication(), "persistent root changed")
	assert.Panics(t, prepared.Publish)
	assert.Same(t, base, state.snapshot)
	assert.False(t, prepared.published)
	assert.False(t, session.exactCycleCacheCommitted)
}

func testIncrementalDerivedResource(
	t *testing.T,
	annotation string,
) rendercontext.DerivedResource {
	t.Helper()
	identity := rendercontext.DerivedResourceIdentity{
		Resource: "widgets", Namespace: "default", Name: "route",
	}
	source, err := encodeResourceValue(map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name": "route", "namespace": "default",
		},
	})
	require.NoError(t, err)
	value, err := encodeResourceValue(map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"annotations": map[string]any{"governed": annotation},
			"name":        "route",
			"namespace":   "default",
		},
	})
	require.NoError(t, err)
	return rendercontext.DerivedResource{Identity: identity, Source: source, Value: value}
}

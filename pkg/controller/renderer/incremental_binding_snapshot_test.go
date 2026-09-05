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

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalBindingSnapshotOwnsCanonicalProps(t *testing.T) {
	snapshot := newIncrementalStateSnapshot()
	component := incrementalComponent{name: "component"}
	props := []byte(`{"label":"original"}`)
	plan := newIncrementalBindingPlan()
	plan.bindings = []incrementalBinding{{
		component: component.name,
		source:    "arbitrary-crd",
		props:     props,
	}}
	runtime := &incrementalRenderSession{
		state: &incrementalRenderState{
			components: map[string]incrementalComponent{component.name: component},
		},
		base:         snapshot,
		bindings:     snapshot.bindings.Txn(),
		members:      snapshot.members.Txn(),
		retired:      snapshot.retired.Txn(),
		results:      snapshot.results.Txn(),
		bindingPlan:  plan,
		inputChanges: map[incremental.InputKey]incremental.Input{},
		newQueries:   map[incremental.QueryKey]struct{}{},
	}
	require.NoError(t, runtime.applyBindingPlan())

	props[0] = '['
	key := bindingKey(component.name, "arbitrary-crd")
	stored, found := runtime.bindings.Root().Get(key)
	require.True(t, found)
	assert.Equal(t, `{"label":"original"}`, stored)

	bindings, err := runtime.currentBindings()
	require.NoError(t, err)
	materialized := bindings[string(key)]
	require.NotEmpty(t, materialized.props)
	materialized.props[0] = '['
	stored, found = runtime.bindings.Root().Get(key)
	require.True(t, found)
	assert.Equal(t, `{"label":"original"}`, stored)

	snapshot.bindings = runtime.bindings.Commit()
	authenticateIncrementalStateSnapshot(snapshot)
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))
}

func TestIncrementalBindingSnapshotRejectsLeafSubstitution(t *testing.T) {
	snapshot := newIncrementalStateSnapshot()
	key := bindingKey("component", "arbitrary-crd")
	bindings, _, _ := snapshot.bindings.Insert(key, `{"label":"original"}`)
	snapshot.bindings = bindings
	authenticateIncrementalStateSnapshot(snapshot)
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))

	poisoned, _, _ := snapshot.bindings.Insert(key, `{"label":"poisoned"}`)
	snapshot.bindings = poisoned
	require.ErrorContains(t, validateIncrementalStateSnapshotAuthentication(snapshot), "persistent root changed")
}

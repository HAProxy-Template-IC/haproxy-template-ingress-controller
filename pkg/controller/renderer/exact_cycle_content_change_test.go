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

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// A watched object can change without changing the keys it is indexed by: the
// same object, same name, same index keys, different body. Nothing about that
// is resource-specific — it is any watched kind whose interesting state lives
// outside its index — but it is the shape a rolling restart produces, and a
// render that reuses its previous output across it serves the state before the
// change.
func TestExactCycleObservationDetectsContentChangeUnderSameKeys(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		spec resourceInputSpec
	}{
		{name: "list", spec: resourceInputSpec{resourceType: "resources", scope: resourceInputList}},
		{name: "get", spec: resourceInputSpec{
			resourceType: "resources", scope: resourceInputGet, keys: []string{"blue"},
		}},
		{name: "identity", spec: resourceInputSpec{
			resourceType: "resources", scope: resourceInputIdentity,
			namespace: "default", name: "first",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := test.spec
			store := k8sstore.NewMemoryStore(2)
			for _, name := range []string{"first", "second"} {
				require.NoError(t, store.Add(
					lateResourceProofValue(name, "stable"), []string{"blue", name},
				))
			}
			original, err := store.Pin()
			require.NoError(t, err)
			input, err := readResourceSnapshotInput(t.Context(), original, &spec)
			require.NoError(t, err)

			session := &incrementalRenderSession{
				baseStores:      map[string]stores.Store{"resources": store},
				renderSnapshots: map[string]stores.ReadSnapshot{"resources": original},
				rootResourceProofs: map[incremental.InputKey]incremental.InputRevision{
					input.Key: {Key: input.Key, Revision: input.Revision, Found: input.Found},
				},
				cachePublicationEnabled: true,
			}
			observations, err := session.captureExactCycleResourceObservations()
			require.NoError(t, err)
			require.NotNil(t, observations)

			// Same object, same index keys, different body.
			require.NoError(t, store.Update(
				lateResourceProofValue("first", "changed"), []string{"blue", "first"},
			))
			updated, err := store.Pin()
			require.NoError(t, err)
			require.NotEqual(t, original.Sequence(), updated.Sequence(),
				"a real content change must advance the store sequence, or nothing downstream can see it")
			session.renderSnapshots["resources"] = updated

			matched, err := observations.matches(t.Context(), session)
			require.NoError(t, err)
			require.False(t, matched,
				"the observation must not match after the body changed under unchanged keys — "+
					"matching here lets a render replay the output it produced before the change")
		})
	}
}

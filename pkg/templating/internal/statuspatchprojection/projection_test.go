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

package statuspatchprojection_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	projection "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

func TestRootDetachesInputsAndMaterializations(t *testing.T) {
	owner := &struct{ generation int }{generation: 1}
	nested := map[string]any{"owners": []any{"stable"}}
	root, err := projection.New(owner, []projection.InputPatch{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-route", ResourceVersion: "rv-1",
		Variants: map[string]map[string]any{"rendered": {"nested": nested}},
	}})
	require.NoError(t, err)

	nested["owners"].([]any)[0] = "input-poison"
	first := materializeSinglePhase(t, root, owner)
	assert.Equal(t, "stable", first["nested"].(map[string]any)["owners"].([]any)[0])

	first["nested"].(map[string]any)["owners"].([]any)[0] = "output-poison"
	second := materializeSinglePhase(t, root, owner)
	assert.Equal(t, "stable", second["nested"].(map[string]any)["owners"].([]any)[0])
}

func TestRootRejectsCopiedAndForeignOwnership(t *testing.T) {
	owner := &struct{ name string }{name: "owner"}
	root, err := projection.New(owner, []projection.InputPatch{projectionInput("stable")})
	require.NoError(t, err)
	require.NoError(t, root.Validate(owner))
	require.ErrorContains(t, root.Validate(&struct{ name string }{name: "owner"}), "invalid provenance")

	copied := *root
	require.ErrorContains(t, copied.Validate(owner), "invalid provenance")
}

func TestGroupPreservesExactLeafOwners(t *testing.T) {
	firstOwner := &struct{ name string }{name: "first"}
	first, err := projection.New(firstOwner, []projection.InputPatch{projectionInput("first")})
	require.NoError(t, err)
	secondOwner := &struct{ name string }{name: "second"}
	second, err := projection.New(secondOwner, []projection.InputPatch{projectionInput("second")})
	require.NoError(t, err)
	groupOwner := &struct{ name string }{name: "group"}
	group, err := projection.NewGroup(groupOwner, []projection.Part{
		{Root: first, Owner: firstOwner},
		{Root: second, Owner: secondOwner},
	})
	require.NoError(t, err)

	var owners []any
	require.NoError(t, group.Visit(groupOwner, func(patch projection.PatchView) error {
		owner, ownerErr := patch.Owner()
		if ownerErr != nil {
			return ownerErr
		}
		owners = append(owners, owner)
		return nil
	}))
	assert.Equal(t, []any{firstOwner, secondOwner}, owners)

	_, err = projection.NewGroup(groupOwner, []projection.Part{{Root: first, Owner: secondOwner}})
	require.ErrorContains(t, err, "invalid provenance")
}

func TestRecurringContentGetsFreshExactRoots(t *testing.T) {
	ownerA1 := &struct{ generation int }{generation: 1}
	firstA, err := projection.New(ownerA1, []projection.InputPatch{projectionInput("a")})
	require.NoError(t, err)
	ownerB := &struct{ generation int }{generation: 2}
	b, err := projection.New(ownerB, []projection.InputPatch{projectionInput("b")})
	require.NoError(t, err)
	ownerA2 := &struct{ generation int }{generation: 3}
	secondA, err := projection.New(ownerA2, []projection.InputPatch{projectionInput("a")})
	require.NoError(t, err)

	assert.NotSame(t, firstA, b)
	assert.NotSame(t, firstA, secondA)
	assert.NotSame(t, b, secondA)
}

func TestRootConcurrentMaterializationIsDetached(t *testing.T) {
	owner := &struct{ generation int }{generation: 1}
	root, err := projection.New(owner, []projection.InputPatch{projectionInput("stable")})
	require.NoError(t, err)

	const workers = 32
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var materialized map[string]any
			visitErr := root.Visit(owner, func(patch projection.PatchView) error {
				return patch.VisitPhases(func(phase projection.PhaseView) error {
					var phaseErr error
					materialized, phaseErr = phase.Materialize()
					return phaseErr
				})
			})
			if visitErr == nil {
				materialized["owner"] = "caller-local"
			}
			errorsByWorker <- visitErr
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for workerErr := range errorsByWorker {
		require.NoError(t, workerErr)
	}
	assert.Equal(t, "stable", materializeSinglePhase(t, root, owner)["owner"])
}

func projectionInput(owner string) projection.InputPatch {
	return projection.InputPatch{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-route", ResourceVersion: "rv-1",
		Variants: map[string]map[string]any{"rendered": {"owner": owner}},
	}
}

func materializeSinglePhase(tb testing.TB, root *projection.Root, owner any) map[string]any {
	tb.Helper()
	var result map[string]any
	require.NoError(tb, root.Visit(owner, func(patch projection.PatchView) error {
		return patch.VisitPhases(func(phase projection.PhaseView) error {
			var err error
			result, err = phase.Materialize()
			return err
		})
	}))
	return result
}

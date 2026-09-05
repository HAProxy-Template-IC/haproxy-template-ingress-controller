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

package templating

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusPatchProjectionDetachesInputsAndOutputs(t *testing.T) {
	nested := map[string]any{"owners": []any{"stable"}}
	projection, err := NewStatusPatchProjection([]StatusPatch{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-route", ResourceVersion: "rv-1",
		Variants:       map[string]map[string]any{"rendered": {"nested": nested}},
		SourceTemplate: "component", SourceLine: 17,
	}})
	require.NoError(t, err)
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)

	nested["owners"].([]any)[0] = "input-poison"
	nested["added"] = true
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
	first, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, "uid-route", first[0].UID)
	assert.Equal(t, "rv-1", first[0].ResourceVersion)
	assert.Equal(t, "stable", first[0].Variants["rendered"]["nested"].(map[string]any)["owners"].([]any)[0])
	assert.NotContains(t, first[0].Variants["rendered"]["nested"].(map[string]any), "added")

	first[0].Variants["rendered"]["nested"].(map[string]any)["owners"].([]any)[0] = "output-poison"
	second, err := collector.Patches()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Variants["rendered"]["nested"].(map[string]any)["owners"].([]any)[0])
}

func TestStatusPatchProjectionRejectsConflictingSourceLineage(t *testing.T) {
	_, err := NewStatusPatchProjection([]StatusPatch{
		{
			Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
			UID: "uid-route", ResourceVersion: "rv-1",
			Variants: map[string]map[string]any{"rendered": {"owner": "first"}},
		},
		{
			Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
			UID: "uid-route", ResourceVersion: "rv-2",
			Variants: map[string]map[string]any{"deployed": {"owner": "second"}},
		},
	})
	require.ErrorContains(t, err, "conflicting source lineage")
}

func TestStatusPatchProjectionReplayRejectsCompositeLineageConflictAtomically(t *testing.T) {
	newProjection := func(t *testing.T, phase, uid, resourceVersion string) *StatusPatchProjection {
		t.Helper()
		projection, err := NewStatusPatchProjection([]StatusPatch{{
			Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
			UID: uid, ResourceVersion: resourceVersion,
			Variants: map[string]map[string]any{phase: {"owner": phase}},
		}})
		require.NoError(t, err)
		return projection
	}
	composite, err := NewStatusPatchProjectionGroup([]*StatusPatchProjection{
		newProjection(t, "rendered", "uid-route", "rv-1"),
		newProjection(t, "deployed", "uid-route", "rv-2"),
	})
	require.NoError(t, err)
	replay, err := composite.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "baseline", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "baseline"}},
	))

	err = collector.ReplayProjections([]*StatusPatchProjectionReplay{replay})
	require.ErrorContains(t, err, "conflicting source lineage")
	patches, snapshotErr := collector.Patches()
	require.NoError(t, snapshotErr)
	require.Len(t, patches, 1)
	assert.Equal(t, "baseline", patches[0].Name)
}

func TestStatusPatchProjectionPreservesMergeAndBaselineSemantics(t *testing.T) {
	projection, err := NewStatusPatchProjection([]StatusPatch{
		{
			Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
			Variants: map[string]map[string]any{
				"rendered": {"owner": "first"}, "deployed": {"owner": "deployed"},
			},
			SourceTemplate: "first", SourceLine: 1,
		},
		{
			Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
			Variants:       map[string]map[string]any{"rendered": {"owner": "last"}},
			SourceTemplate: "last", SourceLine: 2,
		},
	})
	require.NoError(t, err)
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	var claims []string
	require.NoError(t, replay.VisitClaims(func(claim StatusPatchProjectionClaim) error {
		claims = append(claims, claim.Phase)
		return nil
	}))
	assert.Equal(t, []string{"deployed", "rendered"}, claims)

	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "baseline"}},
	))
	collector.SetSource("default", "route", "example.test/v1", "Route", "direct", 9)
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "last", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, "deployed", patches[0].Variants["deployed"]["owner"])
	assert.Equal(t, "direct", patches[0].SourceTemplate)
	assert.Equal(t, 9, patches[0].SourceLine)
}

func TestStatusPatchProjectionRejectsAuthenticationMismatchAtomically(t *testing.T) {
	projection := mustStatusPatchProjection(t, "cached")
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	copied := *replay

	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "baseline", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "baseline"}},
	))
	err = collector.ReplayProjections([]*StatusPatchProjectionReplay{replay, &copied})
	require.ErrorContains(t, err, "invalid provenance")
	patches, snapshotErr := collector.Patches()
	require.NoError(t, snapshotErr)
	require.Len(t, patches, 1)
	assert.Equal(t, "baseline", patches[0].Name)
}

func TestStatusPatchProjectionFailsClosedAfterReplayAuthenticationPoison(t *testing.T) {
	projection := mustStatusPatchProjection(t, "cached")
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))

	projection.integrity.root = nil
	_, err = collector.Patches()
	require.ErrorContains(t, err, "invalid provenance")
}

func TestStatusPatchProjectionConcurrentReplayIsDetached(t *testing.T) {
	projection := mustStatusPatchProjection(t, "cached")
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	const workers = 32
	errs := make([]error, workers)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs[worker] = collector.ReplayProjections([]*StatusPatchProjectionReplay{replay})
		}()
	}
	wait.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "cached", patches[0].Variants["rendered"]["owner"])
}

func TestStatusPatchCollectorPreservesNilVariant(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": nil},
	))
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Nil(t, patches[0].Variants["rendered"])
}

func TestStatusPatchProjectionPreservesNumericTypes(t *testing.T) {
	numbers := map[string]any{
		"int": int(-1), "int8": int8(-2), "int16": int16(-3), "int32": int32(-4), "int64": int64(-5),
		"uint": uint(1), "uint8": uint8(2), "uint16": uint16(3), "uint32": uint32(4), "uint64": uint64(5),
		"float32": float32(1.25), "float64": float64(2.5),
	}
	projection, err := NewStatusPatchProjection([]StatusPatch{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		Variants: map[string]map[string]any{"rendered": numbers},
	}})
	require.NoError(t, err)
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}))
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, numbers, patches[0].Variants["rendered"])
	for name, value := range numbers {
		assert.IsType(t, value, patches[0].Variants["rendered"][name])
	}
}

func TestStatusPatchProjectionClaimsAllPhasesDeterministically(t *testing.T) {
	projection, err := NewStatusPatchProjection([]StatusPatch{{
		Namespace: "", Name: "cluster-route", APIVersion: "example.test/v1", Kind: "ClusterRoute",
		UID: "uid-cluster-route", ResourceVersion: "rv-3",
		Variants: map[string]map[string]any{
			"rendered": {}, "deployed": {}, "renderFailed": {}, "deployFailed": {},
		},
	}})
	require.NoError(t, err)
	replay, err := projection.PrepareReplay()
	require.NoError(t, err)
	var phases []string
	require.NoError(t, replay.VisitClaims(func(claim StatusPatchProjectionClaim) error {
		assert.Empty(t, claim.Namespace)
		assert.Equal(t, "uid-cluster-route", claim.UID)
		assert.Equal(t, "rv-3", claim.ResourceVersion)
		phases = append(phases, claim.Phase)
		return nil
	}))
	assert.Equal(t, []string{"deployFailed", "deployed", "renderFailed", "rendered"}, phases)
}

func TestStatusPatchCollectorUsesExactTupleIdentity(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.Equal(t,
		statusPatchKey("a", "b/c", "d", "e"),
		statusPatchKey("a/b", "c", "d", "e"),
	)
	require.NoError(t, collector.Register(
		"a", "b/c", "d", "e", map[string]map[string]any{"rendered": {"owner": "first"}},
	))
	require.NoError(t, collector.Register(
		"a/b", "c", "d", "e", map[string]map[string]any{"rendered": {"owner": "second"}},
	))
	patches, err := collector.Patches()
	require.NoError(t, err)
	assert.Len(t, patches, 2)
}

func mustStatusPatchProjection(tb testing.TB, owner string) *StatusPatchProjection {
	tb.Helper()
	projection, err := NewStatusPatchProjection([]StatusPatch{{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-route", ResourceVersion: "rv-1",
		Variants: map[string]map[string]any{"rendered": {"owner": owner}},
	}})
	require.NoError(tb, err)
	return projection
}

func BenchmarkStatusPatchProjectionReplay(b *testing.B) {
	const patchCount = 3000
	parts := make([]*StatusPatchProjection, patchCount)
	for index := range parts {
		projection, err := NewStatusPatchProjection([]StatusPatch{{
			Namespace: "default", Name: fmt.Sprintf("route-%06d", index),
			APIVersion: "example.test/v1", Kind: "Route",
			UID: fmt.Sprintf("uid-%06d", index), ResourceVersion: "rv-1",
			Variants: map[string]map[string]any{
				"rendered": {"conditions": []any{map[string]any{"type": "Accepted", "generation": index}}},
				"deployed": {"conditions": []any{map[string]any{"type": "Programmed", "generation": index}}},
			},
		}})
		if err != nil {
			b.Fatal(err)
		}
		parts[index] = projection
	}
	group, err := NewStatusPatchProjectionGroup(parts)
	if err != nil {
		b.Fatal(err)
	}
	replay, err := group.PrepareReplay()
	if err != nil {
		b.Fatal(err)
	}

	b.Run("authenticate", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := group.ValidateAuthentication(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("replay", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			collector := NewStatusPatchCollector()
			if err := collector.ReplayProjections([]*StatusPatchProjectionReplay{replay}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

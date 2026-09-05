// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusPatchCollector_Register(t *testing.T) {
	t.Run("basic registration", func(t *testing.T) {
		c := NewStatusPatchCollector()

		err := c.Register("default", "my-ingress", "networking.k8s.io/v1", "Ingress",
			map[string]map[string]any{
				"deployed": {"loadBalancer": map[string]any{"ingress": []any{}}},
			})
		require.NoError(t, err)

		patches, err := c.Patches()
		require.NoError(t, err)
		require.Len(t, patches, 1)
		assert.Equal(t, "default", patches[0].Namespace)
		assert.Equal(t, "my-ingress", patches[0].Name)
		assert.Equal(t, "networking.k8s.io/v1", patches[0].APIVersion)
		assert.Equal(t, "Ingress", patches[0].Kind)
		assert.Contains(t, patches[0].Variants, "deployed")
	})

	t.Run("multiple resources", func(t *testing.T) {
		c := NewStatusPatchCollector()

		require.NoError(t, c.Register("default", "ing-1", "networking.k8s.io/v1", "Ingress",
			map[string]map[string]any{"deployed": {"a": 1}}))
		require.NoError(t, c.Register("default", "ing-2", "networking.k8s.io/v1", "Ingress",
			map[string]map[string]any{"deployed": {"b": 2}}))
		require.NoError(t, c.Register("other", "gw-1", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]any{"deployed": {"c": 3}}))

		patches, err := c.Patches()
		require.NoError(t, err)
		assert.Len(t, patches, 3)
	})

	t.Run("merge variants for same resource", func(t *testing.T) {
		c := NewStatusPatchCollector()

		require.NoError(t, c.Register("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute",
			map[string]map[string]any{
				"rendered": {"conditions": []any{"Accepted"}},
			}))
		require.NoError(t, c.Register("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute",
			map[string]map[string]any{
				"deployed": {"conditions": []any{"Accepted", "Programmed"}},
			}))

		patches, err := c.Patches()
		require.NoError(t, err)
		require.Len(t, patches, 1)
		assert.Contains(t, patches[0].Variants, "rendered")
		assert.Contains(t, patches[0].Variants, "deployed")
	})

	t.Run("later call overrides same variant key", func(t *testing.T) {
		c := NewStatusPatchCollector()

		require.NoError(t, c.Register("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]any{
				"deployed": {"version": "old"},
			}))
		require.NoError(t, c.Register("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]any{
				"deployed": {"version": "new"},
			}))

		patches, err := c.Patches()
		require.NoError(t, err)
		require.Len(t, patches, 1)
		assert.Equal(t, "new", patches[0].Variants["deployed"]["version"])
	})
}

func TestStatusPatchCollectorPreservesSourceLineage(t *testing.T) {
	collector := NewStatusPatchCollector()
	require.NoError(t, collector.RegisterWithLineage(
		"default", "route", "example.test/v1", "Route", "uid-route", "rv-17",
		map[string]map[string]any{"rendered": {"owner": "first"}},
	))
	require.NoError(t, collector.RegisterWithLineage(
		"default", "route", "example.test/v1", "Route", "uid-route", "rv-17",
		map[string]map[string]any{"deployed": {"owner": "second"}},
	))

	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, "uid-route", patches[0].UID)
	assert.Equal(t, "rv-17", patches[0].ResourceVersion)
	assert.Equal(t, "first", patches[0].Variants["rendered"]["owner"])
	assert.Equal(t, "second", patches[0].Variants["deployed"]["owner"])
}

func TestStatusPatchCollectorRejectsConflictingSourceLineageAtomically(t *testing.T) {
	tests := map[string]struct {
		firstUID             string
		firstResourceVersion string
		nextUID              string
		nextResourceVersion  string
	}{
		"uid changed":              {"uid-a", "rv-1", "uid-b", "rv-1"},
		"resource version changed": {"uid-a", "rv-1", "uid-a", "rv-2"},
		"lineage removed":          {"uid-a", "rv-1", "", ""},
		"lineage appeared":         {"", "", "uid-a", "rv-1"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			collector := NewStatusPatchCollector()
			require.NoError(t, collector.RegisterWithLineage(
				"default", "route", "example.test/v1", "Route",
				test.firstUID, test.firstResourceVersion,
				map[string]map[string]any{"rendered": {"owner": "stable"}},
			))

			err := collector.RegisterWithLineage(
				"default", "route", "example.test/v1", "Route",
				test.nextUID, test.nextResourceVersion,
				map[string]map[string]any{"deployed": {"owner": "poison"}},
			)
			require.ErrorContains(t, err, "conflicting source lineage")

			patches, snapshotErr := collector.Patches()
			require.NoError(t, snapshotErr)
			require.Len(t, patches, 1)
			assert.Equal(t, test.firstUID, patches[0].UID)
			assert.Equal(t, test.firstResourceVersion, patches[0].ResourceVersion)
			assert.Equal(t, "stable", patches[0].Variants["rendered"]["owner"])
			assert.NotContains(t, patches[0].Variants, "deployed")
		})
	}
}

func TestScriggoStatusPatchExtractsGenericResourceLineage(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{%%
var resource = map[string]any{
  "apiVersion": "example.test/v1", "kind": "Route",
  "metadata": map[string]any{
    "namespace": "default", "name": "route", "uid": "uid-route", "resourceVersion": "rv-29",
  },
}
statusPatch(resource, map[string]any{"rendered": map[string]any{"accepted": true}})
%%}`,
	}, nil)
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	output, err := engine.Render(t.Context(), "main", map[string]any{"statusPatchCollector": collector})
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(output))

	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	assert.Equal(t, StatusPatch{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		UID: "uid-route", ResourceVersion: "rv-29",
		Variants:       map[string]map[string]any{"rendered": {"accepted": true}},
		SourceTemplate: "main", SourceLine: 8,
	}, patches[0])
}

func TestStatusPatchCollector_Register_Validation(t *testing.T) {
	c := NewStatusPatchCollector()

	t.Run("empty namespace is allowed for cluster-scoped resources", func(t *testing.T) {
		// Cluster-scoped resources (GatewayClass, ClusterRole, …)
		// have no namespace; the Register validation explicitly
		// accepts an empty namespace. The applier passes
		// Namespace("") to the dynamic client, which client-go
		// treats as cluster-scoped.
		err := c.Register("", "haptic", "gateway.networking.k8s.io/v1", "GatewayClass",
			map[string]map[string]any{"deployed": {"conditions": []any{}}})
		assert.NoError(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		err := c.Register("ns", "", "v1", "Pod", map[string]map[string]any{"deployed": {"a": 1}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("empty apiVersion", func(t *testing.T) {
		err := c.Register("ns", "name", "", "Pod", map[string]map[string]any{"deployed": {"a": 1}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("empty kind", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "", map[string]map[string]any{"deployed": {"a": 1}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("empty variants", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]any{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one variant")
	})

	t.Run("invalid phase key", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]any{
			"invalidPhase": {"a": 1},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid phase")
	})

	t.Run("all valid phases accepted", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]any{
			"rendered":     {"a": 1},
			"deployed":     {"b": 2},
			"renderFailed": {"c": 3},
			"deployFailed": {"d": 4},
		})
		assert.NoError(t, err)
	})
}

func TestStatusPatchCollector_ConcurrentWrites(t *testing.T) {
	c := NewStatusPatchCollector()

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(idx int) {
			defer wg.Done()
			ns := "default"
			name := "resource-" + string(rune('a'+idx%26))
			err := c.Register(ns, name, "v1", "ConfigMap",
				map[string]map[string]any{
					"deployed": {"idx": idx},
				})
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	patches, err := c.Patches()
	require.NoError(t, err)
	// At least 1 patch (up to 26 unique names), all should be present
	assert.NotEmpty(t, patches)
}

func TestStatusPatchCollector_ConcurrentWritesSameResource(t *testing.T) {
	c := NewStatusPatchCollector()

	var wg sync.WaitGroup
	wg.Add(4)

	phases := []string{"rendered", "deployed", "renderFailed", "deployFailed"}
	for i, phase := range phases {
		go func(p string, idx int) {
			defer wg.Done()
			err := c.Register("default", "shared-resource", "v1", "Service",
				map[string]map[string]any{
					p: {"idx": idx},
				})
			assert.NoError(t, err)
		}(phase, i)
	}

	wg.Wait()

	patches, err := c.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	// All four phases should be present after concurrent merging
	assert.Len(t, patches[0].Variants, 4)
}

func TestStatusPatchCollector_PatchesReturnsSnapshot(t *testing.T) {
	c := NewStatusPatchCollector()

	require.NoError(t, c.Register("ns", "a", "v1", "Pod",
		map[string]map[string]any{"deployed": {"x": 1}}))

	snapshot, err := c.Patches()
	require.NoError(t, err)
	require.Len(t, snapshot, 1)

	// Adding more patches after snapshot shouldn't affect the snapshot
	require.NoError(t, c.Register("ns", "b", "v1", "Pod",
		map[string]map[string]any{"deployed": {"y": 2}}))

	assert.Len(t, snapshot, 1, "snapshot should not be affected by later Register calls")
	current, err := c.Patches()
	require.NoError(t, err)
	assert.Len(t, current, 2, "new Patches() call should include new registrations")
}

func TestStatusPatchCollector_DetachesRegisteredAndReturnedVariants(t *testing.T) {
	c := NewStatusPatchCollector()
	nested := map[string]any{"value": "stable"}
	variants := map[string]map[string]any{
		"rendered": {"nested": nested},
	}
	require.NoError(t, c.Register("ns", "name", "v1", "Route", variants))

	nested["value"] = "input-poison"
	variants["rendered"]["added"] = true
	first, err := c.Patches()
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, "stable", first[0].Variants["rendered"]["nested"].(map[string]any)["value"])
	assert.NotContains(t, first[0].Variants["rendered"], "added")

	first[0].Variants["rendered"]["nested"].(map[string]any)["value"] = "output-poison"
	first[0].Variants["rendered"]["added"] = true
	second, err := c.Patches()
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "stable", second[0].Variants["rendered"]["nested"].(map[string]any)["value"])
	assert.NotContains(t, second[0].Variants["rendered"], "added")
}

func TestStatusPatchCollector_RejectsUndetachableVariantsAtomically(t *testing.T) {
	c := NewStatusPatchCollector()
	require.NoError(t, c.Register("ns", "name", "v1", "Route", map[string]map[string]any{
		"rendered": {"value": "stable"},
	}))

	err := c.Register("ns", "name", "v1", "Route", map[string]map[string]any{
		"deployed": {"value": func() {}},
	})
	require.ErrorContains(t, err, "cannot be detached")
	patches, snapshotErr := c.Patches()
	require.NoError(t, snapshotErr)
	require.Len(t, patches, 1)
	assert.Equal(t, map[string]map[string]any{"rendered": {"value": "stable"}}, patches[0].Variants)
}

func TestStatusPatchCollector_PatchesFailsClosedOnCorruptStoredVariant(t *testing.T) {
	c := NewStatusPatchCollector()
	c.patches[newStatusPatchIdentity("ns", "name", "v1", "Route")] = &collectedStatusPatch{
		Namespace: "ns", Name: "name", APIVersion: "v1", Kind: "Route",
		Variants: map[string]collectedStatusPatchVariant{
			"rendered": {detached: map[string]any{"value": func() {}}, hasDetached: true},
		},
	}

	_, err := c.Patches()
	require.ErrorContains(t, err, "snapshotting ns/name")
}

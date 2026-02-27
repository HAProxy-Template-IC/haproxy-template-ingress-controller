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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusPatchCollector_Register(t *testing.T) {
	t.Run("basic registration", func(t *testing.T) {
		c := NewStatusPatchCollector()

		err := c.Register("default", "my-ingress", "networking.k8s.io/v1", "Ingress",
			map[string]map[string]interface{}{
				"deployed": {"loadBalancer": map[string]interface{}{"ingress": []interface{}{}}},
			})
		require.NoError(t, err)

		patches := c.Patches()
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
			map[string]map[string]interface{}{"deployed": {"a": 1}}))
		require.NoError(t, c.Register("default", "ing-2", "networking.k8s.io/v1", "Ingress",
			map[string]map[string]interface{}{"deployed": {"b": 2}}))
		require.NoError(t, c.Register("other", "gw-1", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]interface{}{"deployed": {"c": 3}}))

		patches := c.Patches()
		assert.Len(t, patches, 3)
	})

	t.Run("merge variants for same resource", func(t *testing.T) {
		c := NewStatusPatchCollector()

		require.NoError(t, c.Register("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute",
			map[string]map[string]interface{}{
				"rendered": {"conditions": []interface{}{"Accepted"}},
			}))
		require.NoError(t, c.Register("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute",
			map[string]map[string]interface{}{
				"deployed": {"conditions": []interface{}{"Accepted", "Programmed"}},
			}))

		patches := c.Patches()
		require.Len(t, patches, 1)
		assert.Contains(t, patches[0].Variants, "rendered")
		assert.Contains(t, patches[0].Variants, "deployed")
	})

	t.Run("later call overrides same variant key", func(t *testing.T) {
		c := NewStatusPatchCollector()

		require.NoError(t, c.Register("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]interface{}{
				"deployed": {"version": "old"},
			}))
		require.NoError(t, c.Register("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway",
			map[string]map[string]interface{}{
				"deployed": {"version": "new"},
			}))

		patches := c.Patches()
		require.Len(t, patches, 1)
		assert.Equal(t, "new", patches[0].Variants["deployed"]["version"])
	})
}

func TestStatusPatchCollector_Register_Validation(t *testing.T) {
	c := NewStatusPatchCollector()

	t.Run("empty namespace", func(t *testing.T) {
		err := c.Register("", "name", "v1", "Pod", map[string]map[string]interface{}{"deployed": {"a": 1}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("empty name", func(t *testing.T) {
		err := c.Register("ns", "", "v1", "Pod", map[string]map[string]interface{}{"deployed": {"a": 1}})
		assert.Error(t, err)
	})

	t.Run("empty variants", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]interface{}{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one variant")
	})

	t.Run("invalid phase key", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]interface{}{
			"invalidPhase": {"a": 1},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid phase")
	})

	t.Run("all valid phases accepted", func(t *testing.T) {
		err := c.Register("ns", "name", "v1", "Pod", map[string]map[string]interface{}{
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

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ns := "default"
			name := "resource-" + string(rune('a'+idx%26))
			err := c.Register(ns, name, "v1", "ConfigMap",
				map[string]map[string]interface{}{
					"deployed": {"idx": idx},
				})
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	patches := c.Patches()
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
				map[string]map[string]interface{}{
					p: {"idx": idx},
				})
			assert.NoError(t, err)
		}(phase, i)
	}

	wg.Wait()

	patches := c.Patches()
	require.Len(t, patches, 1)
	// All four phases should be present after concurrent merging
	assert.Len(t, patches[0].Variants, 4)
}

func TestStatusPatchCollector_PatchesReturnsSnapshot(t *testing.T) {
	c := NewStatusPatchCollector()

	require.NoError(t, c.Register("ns", "a", "v1", "Pod",
		map[string]map[string]interface{}{"deployed": {"x": 1}}))

	snapshot := c.Patches()
	require.Len(t, snapshot, 1)

	// Adding more patches after snapshot shouldn't affect the snapshot
	require.NoError(t, c.Register("ns", "b", "v1", "Pod",
		map[string]map[string]interface{}{"deployed": {"y": 2}}))

	assert.Len(t, snapshot, 1, "snapshot should not be affected by later Register calls")
	assert.Len(t, c.Patches(), 2, "new Patches() call should include new registrations")
}

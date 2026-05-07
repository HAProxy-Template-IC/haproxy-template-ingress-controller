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

func TestRenderedResourceCollector_Register_InjectsIdentity(t *testing.T) {
	c := NewRenderedResourceCollector()

	// Template author supplies only spec; collector must inject apiVersion,
	// kind, metadata.name, metadata.namespace.
	err := c.Register("v1", "Service", "default", "my-svc", map[string]any{
		"spec": map[string]any{"type": "LoadBalancer"},
	})
	require.NoError(t, err)

	resources := c.Resources()
	require.Len(t, resources, 1)

	r := resources[0]
	assert.Equal(t, "v1", r.APIVersion)
	assert.Equal(t, "Service", r.Kind)
	assert.Equal(t, "default", r.Namespace)
	assert.Equal(t, "my-svc", r.Name)
	assert.Equal(t, "v1", r.Object["apiVersion"])
	assert.Equal(t, "Service", r.Object["kind"])
	metadata := r.Object["metadata"].(map[string]any)
	assert.Equal(t, "my-svc", metadata["name"])
	assert.Equal(t, "default", metadata["namespace"])
}

func TestRenderedResourceCollector_Register_ClusterScoped(t *testing.T) {
	c := NewRenderedResourceCollector()

	// Empty namespace → metadata.namespace must be omitted (not set to "").
	err := c.Register("rbac.authorization.k8s.io/v1", "ClusterRole", "", "my-role", map[string]any{
		"rules": []any{},
	})
	require.NoError(t, err)

	r := c.Resources()[0]
	metadata := r.Object["metadata"].(map[string]any)
	assert.Equal(t, "my-role", metadata["name"])
	_, hasNs := metadata["namespace"]
	assert.False(t, hasNs, "cluster-scoped resource must omit metadata.namespace")
}

func TestRenderedResourceCollector_Register_LastWriteWins(t *testing.T) {
	c := NewRenderedResourceCollector()

	require.NoError(t, c.Register("v1", "Service", "default", "my-svc", map[string]any{
		"spec": map[string]any{"type": "ClusterIP"},
	}))

	// Re-register with different spec — last write wins.
	require.NoError(t, c.Register("v1", "Service", "default", "my-svc", map[string]any{
		"spec": map[string]any{"type": "LoadBalancer"},
	}))

	resources := c.Resources()
	require.Len(t, resources, 1, "duplicate registrations must not produce duplicate resources")

	spec := resources[0].Object["spec"].(map[string]any)
	assert.Equal(t, "LoadBalancer", spec["type"])
}

func TestRenderedResourceCollector_Register_DoesNotMutateInputMetadata(t *testing.T) {
	c := NewRenderedResourceCollector()

	// Author passes their own metadata map. Collector must not mutate it
	// (Scriggo treats maps by reference; downstream renders could observe
	// our injection if we mutated in place).
	authorMetadata := map[string]any{"labels": map[string]any{"app": "x"}}
	require.NoError(t, c.Register("v1", "Service", "default", "my-svc", map[string]any{
		"metadata": authorMetadata,
		"spec":     map[string]any{"type": "ClusterIP"},
	}))

	// Author's map should still have only "labels" — we should NOT have
	// injected "name" / "namespace" into it.
	_, hasName := authorMetadata["name"]
	_, hasNs := authorMetadata["namespace"]
	assert.False(t, hasName, "collector must not mutate caller's metadata map")
	assert.False(t, hasNs, "collector must not mutate caller's metadata map")

	// But the collected object DOES have name+namespace via the copy.
	stored := c.Resources()[0].Object["metadata"].(map[string]any)
	assert.Equal(t, "my-svc", stored["name"])
	assert.Equal(t, "default", stored["namespace"])
	// Author's labels are preserved in the copy.
	labels := stored["labels"].(map[string]any)
	assert.Equal(t, "x", labels["app"])
}

func TestRenderedResourceCollector_Register_RejectsInvalid(t *testing.T) {
	c := NewRenderedResourceCollector()

	cases := []struct {
		name string
		fn   func() error
	}{
		{"empty apiVersion", func() error {
			return c.Register("", "Service", "default", "my-svc", map[string]any{})
		}},
		{"empty kind", func() error {
			return c.Register("v1", "", "default", "my-svc", map[string]any{})
		}},
		{"empty name", func() error {
			return c.Register("v1", "Service", "default", "", map[string]any{})
		}},
		{"nil object", func() error {
			return c.Register("v1", "Service", "default", "my-svc", nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.fn())
		})
	}
}

func TestRenderedResourceCollector_ConcurrentRegister(t *testing.T) {
	c := NewRenderedResourceCollector()
	const goroutines = 50

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ns := "ns"
			name := "svc"
			// Same key: must end with a single entry (no duplicate keys, no
			// data race). Different objects ensure last-write-wins behaviour
			// is exercised.
			err := c.Register("v1", "Service", ns, name, map[string]any{
				"spec": map[string]any{"port": i},
			})
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	require.Len(t, c.Resources(), 1, "concurrent same-key registers must collapse to one resource")
}

func TestRenderedResourceCollector_Validate(t *testing.T) {
	c := NewRenderedResourceCollector()
	require.NoError(t, c.Validate(), "empty collector must validate")

	require.NoError(t, c.Register("v1", "Service", "default", "ok", map[string]any{
		"spec": map[string]any{},
	}))
	require.NoError(t, c.Validate())

	// Inject a bad entry directly to exercise Validate's safety net (the
	// public Register would have rejected this, but Validate must catch it
	// regardless of how the entry got into the map).
	c.mu.Lock()
	c.resources["bad"] = &RenderedResource{Object: nil}
	c.mu.Unlock()
	require.Error(t, c.Validate())
}

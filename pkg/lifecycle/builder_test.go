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

package lifecycle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builderTestComponent is a minimal Component used in builder tests. It only
// needs to satisfy the Component interface; nothing actually starts.
type builderTestComponent struct{ name string }

func (c *builderTestComponent) Name() string                  { return c.name }
func (c *builderTestComponent) Start(_ context.Context) error { return nil }

func TestRegistryBuilder_AllReplica(t *testing.T) {
	registry := NewRegistry()
	count := registry.Build().
		AllReplica(
			&builderTestComponent{name: "a"},
			&builderTestComponent{name: "b"},
		).
		Done()

	assert.Equal(t, 2, count)

	status := registry.Status()
	require.Len(t, status, 2)

	for name, info := range status {
		assert.Contains(t, []string{"a", "b"}, name)
		assert.False(t, info.LeaderOnly, "AllReplica components must not be leader-only")
	}
}

func TestRegistryBuilder_LeaderOnly(t *testing.T) {
	registry := NewRegistry()
	count := registry.Build().
		LeaderOnly(
			&builderTestComponent{name: "leader-1"},
			&builderTestComponent{name: "leader-2"},
		).
		Done()

	assert.Equal(t, 2, count)

	status := registry.Status()
	require.Len(t, status, 2)
	for name, info := range status {
		assert.Contains(t, []string{"leader-1", "leader-2"}, name)
		assert.True(t, info.LeaderOnly, "LeaderOnly components must be flagged as leader-only")
	}
}

func TestRegistryBuilder_Mixed(t *testing.T) {
	registry := NewRegistry()
	count := registry.Build().
		AllReplica(&builderTestComponent{name: "all-1"}).
		LeaderOnly(&builderTestComponent{name: "leader-1"}).
		AllReplica(&builderTestComponent{name: "all-2"}).
		Done()

	assert.Equal(t, 3, count)

	status := registry.Status()
	require.Len(t, status, 3)

	assert.False(t, status["all-1"].LeaderOnly)
	assert.False(t, status["all-2"].LeaderOnly)
	assert.True(t, status["leader-1"].LeaderOnly)
}

func TestRegistryBuilder_Empty(t *testing.T) {
	registry := NewRegistry()
	count := registry.Build().Done()

	assert.Equal(t, 0, count)
	assert.Empty(t, registry.Status())
}

func TestRegistryBuilder_Chaining(t *testing.T) {
	// Each fluent method must return the builder so chaining works.
	registry := NewRegistry()

	b := registry.Build()
	assert.Same(t, b, b.AllReplica(&builderTestComponent{name: "a"}))
	assert.Same(t, b, b.LeaderOnly(&builderTestComponent{name: "b"}))
}

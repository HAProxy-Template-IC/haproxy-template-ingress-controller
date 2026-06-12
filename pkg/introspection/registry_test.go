// Copyright 2025 Philipp Hossner.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at.
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software.
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and.
// limitations under the License.

package introspection

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockVar is a simple test implementation of Var.
type mockVar struct {
	value any
	err   error
}

func (m *mockVar) Get() (any, error) {
	return m.value, m.err
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	assert.Equal(t, []string{}, reg.Paths())
}

func TestPublish(t *testing.T) {
	reg := NewRegistry()

	v1 := &mockVar{value: "test1"}
	reg.Publish("var1", v1)

	assert.Equal(t, []string{"var1"}, reg.Paths())

	// Publish another
	v2 := &mockVar{value: "test2"}
	reg.Publish("var2", v2)

	paths := reg.Paths()
	assert.Len(t, paths, 2)
	assert.Contains(t, paths, "var1")
	assert.Contains(t, paths, "var2")

	// Replace existing
	v3 := &mockVar{value: "test3"}
	reg.Publish("var1", v3)

	assert.Len(t, reg.Paths(), 2, "replacing should not increase count")
}

func TestPublishPanics(t *testing.T) {
	reg := NewRegistry()

	// Empty path
	assert.Panics(t, func() {
		reg.Publish("", &mockVar{value: "test"})
	})

	// Nil var
	assert.Panics(t, func() {
		reg.Publish("test", nil)
	})
}

func TestGet(t *testing.T) {
	reg := NewRegistry()

	v := &mockVar{value: map[string]string{"key": "value"}}
	reg.Publish("config", v)

	// Get existing var
	value, err := reg.Get("config")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key": "value"}, value)

	// Get non-existent var
	_, err = reg.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetWithError(t *testing.T) {
	reg := NewRegistry()

	v := &mockVar{err: errors.New("test error")}
	reg.Publish("faulty", v)

	_, err := reg.Get("faulty")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test error")
}

func TestAll(t *testing.T) {
	reg := NewRegistry()

	v1 := &mockVar{value: "value1"}
	v2 := &mockVar{value: "value2"}
	reg.Publish("var1", v1)
	reg.Publish("var2", v2)

	all, err := reg.All()
	require.NoError(t, err)
	assert.Equal(t, 2, len(all))
	assert.Equal(t, "value1", all["var1"])
	assert.Equal(t, "value2", all["var2"])
}

func TestAllWithError(t *testing.T) {
	reg := NewRegistry()

	v1 := &mockVar{value: "value1"}
	v2 := &mockVar{err: errors.New("faulty var")}
	reg.Publish("var1", v1)
	reg.Publish("faulty", v2)

	_, err := reg.All()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "faulty")
}

func TestPaths(t *testing.T) {
	reg := NewRegistry()

	reg.Publish("zebra", &mockVar{value: 1})
	reg.Publish("alpha", &mockVar{value: 2})
	reg.Publish("beta", &mockVar{value: 3})

	paths := reg.Paths()
	assert.Equal(t, []string{"alpha", "beta", "zebra"}, paths, "paths should be sorted")
}

func TestHierarchicalPaths(t *testing.T) {
	reg := NewRegistry()

	reg.Publish("config", &mockVar{value: "config"})
	reg.Publish("resources/ingresses", &mockVar{value: "ingresses"})
	reg.Publish("resources/services", &mockVar{value: "services"})

	paths := reg.Paths()
	assert.Equal(t, 3, len(paths))
	assert.Contains(t, paths, "config")
	assert.Contains(t, paths, "resources/ingresses")
	assert.Contains(t, paths, "resources/services")
}

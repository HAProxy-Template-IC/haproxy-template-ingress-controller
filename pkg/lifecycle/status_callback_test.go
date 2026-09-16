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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registryCallbackComponent struct {
	*mockComponent
	registry      *Registry
	nameCalls     int
	nameWasLocked bool
}

func (c *registryCallbackComponent) Name() string {
	c.nameCalls++
	if c.registry.mu.TryLock() {
		c.registry.mu.Unlock()
		c.registry.Count()
	} else {
		c.nameWasLocked = true
	}
	return c.mockComponent.Name()
}

func (c *registryCallbackComponent) HealthCheck() error {
	if !c.registry.mu.TryLock() {
		return errors.New("health callback called while registry is locked")
	}
	c.registry.mu.Unlock()
	c.registry.Register(newMockComponent("from-health"), false)
	return nil
}

type registryCallbackError func() string

func (f registryCallbackError) Error() string { return f() }

func TestRegistryStatusRunsCallbacksOutsideLock(t *testing.T) {
	registry := NewRegistry()
	component := &registryCallbackComponent{
		mockComponent: newMockComponent("healthy"), registry: registry,
	}
	registry.Register(component, false)
	registry.updateStatus("healthy", StatusRunning, nil)
	registry.Register(newMockComponent("failed"), false)
	registry.updateStatus("failed", StatusFailed, registryCallbackError(func() string {
		if !registry.mu.TryLock() {
			return "error callback called while registry is locked"
		}
		registry.mu.Unlock()
		registry.Count()
		return "component failed"
	}))

	status := registry.Status()
	require.Len(t, status, 2)
	require.NotNil(t, status["healthy"].Healthy)
	assert.True(t, *status["healthy"].Healthy)
	assert.Equal(t, "component failed", status["failed"].Error)
	assert.False(t, component.nameWasLocked)
	assert.Equal(t, 1, component.nameCalls)
	assert.Equal(t, 3, registry.Count())
}

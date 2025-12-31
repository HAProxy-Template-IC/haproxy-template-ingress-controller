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

package currentconfigstore

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// validHAProxyConfig is a minimal valid HAProxy config for testing.
const validHAProxyConfig = `global
    daemon

defaults
    mode http

backend test-backend
    server srv1 127.0.0.1:8080
`

// invalidHAProxyConfig is malformed HAProxy config that will fail parsing.
// Using unclosed brace in 'backend' to trigger a parser error.
const invalidHAProxyConfig = `global

backend incomplete {
    # missing closing brace - this should trigger a parse error
`

// newTestLogger creates a test logger that discards output.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newHAProxyCfgResource creates an unstructured HAProxyCfg resource for testing.
func newHAProxyCfgResource(content string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "haproxy-haptic.org/v1alpha1",
			"kind":       "HAProxyCfg",
			"metadata": map[string]interface{}{
				"name":      "test-haproxycfg",
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"content": content,
			},
		},
	}
}

func TestNew(t *testing.T) {
	logger := newTestLogger()

	store, err := New(logger)

	require.NoError(t, err)
	require.NotNil(t, store)
	assert.NotNil(t, store.parser)
	assert.NotNil(t, store.logger)
	assert.Nil(t, store.currentConfig, "initial config should be nil")
}

func TestStore_GetReturnsNilInitially(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	config := store.Get()

	assert.Nil(t, config, "Get() should return nil before any Update()")
}

func TestStore_UpdateWithValidConfig(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// Update with valid HAProxyCfg
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)

	// Get should return parsed config
	config := store.Get()
	require.NotNil(t, config, "Get() should return parsed config after Update()")
	assert.NotEmpty(t, config.Backends, "parsed config should have backends")
}

func TestStore_UpdateWithNilClearsConfig(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	require.NotNil(t, store.Get(), "config should be set")

	// Update with nil should clear
	store.Update(nil)

	config := store.Get()
	assert.Nil(t, config, "Get() should return nil after Update(nil)")
}

func TestStore_UpdateWithTypedNilClearsConfig(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	require.NotNil(t, store.Get(), "config should be set")

	// Update with typed nil (interface with type but nil value)
	// This can happen when watcher callbacks pass (*unstructured.Unstructured)(nil)
	var typedNil *unstructured.Unstructured = nil
	store.Update(typedNil)

	config := store.Get()
	assert.Nil(t, config, "Get() should return nil after Update() with typed nil")
}

func TestStore_UpdateWithInvalidResourceType(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	require.NotNil(t, store.Get(), "config should be set")

	// Update with wrong type should not change config (logs warning)
	store.Update("not an unstructured resource")

	config := store.Get()
	assert.NotNil(t, config, "config should remain unchanged after invalid resource type")
}

func TestStore_UpdateWithEmptyContent(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	require.NotNil(t, store.Get(), "config should be set")

	// Update with empty content should clear config
	emptyResource := newHAProxyCfgResource("")
	store.Update(emptyResource)

	config := store.Get()
	assert.Nil(t, config, "Get() should return nil after Update() with empty content")
}

func TestStore_UpdateWithMissingContent(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	require.NotNil(t, store.Get(), "config should be set")

	// Update with resource missing spec.content should clear config
	resourceWithoutContent := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "haproxy-haptic.org/v1alpha1",
			"kind":       "HAProxyCfg",
			"metadata": map[string]interface{}{
				"name": "test-haproxycfg",
			},
			"spec": map[string]interface{}{
				// No content field
			},
		},
	}
	store.Update(resourceWithoutContent)

	config := store.Get()
	assert.Nil(t, config, "Get() should return nil after Update() with missing content")
}

func TestStore_UpdateWithMalformedConfig(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// First populate with valid config
	validResource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(validResource)
	originalConfig := store.Get()
	require.NotNil(t, originalConfig, "config should be set")
	originalBackendsCount := len(originalConfig.Backends)
	require.Greater(t, originalBackendsCount, 0, "original config should have backends")

	// Update with malformed config should not change stored config (logs warning)
	malformedResource := newHAProxyCfgResource(invalidHAProxyConfig)
	store.Update(malformedResource)

	// Config should remain unchanged - verify by checking backends count
	config := store.Get()
	assert.NotNil(t, config, "config should remain unchanged after malformed content")
	assert.Equal(t, originalBackendsCount, len(config.Backends),
		"config backends count should remain unchanged after malformed content")
}

func TestStore_ConcurrentAccess(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	const numGoroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Start writers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			resource := newHAProxyCfgResource(validHAProxyConfig)
			for j := 0; j < iterations; j++ {
				store.Update(resource)
			}
		}()
	}

	// Start readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = store.Get()
			}
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Final state should be valid
	config := store.Get()
	assert.NotNil(t, config, "config should be set after concurrent updates")
}

func TestStore_MultipleUpdates(t *testing.T) {
	logger := newTestLogger()
	store, err := New(logger)
	require.NoError(t, err)

	// Multiple sequential updates
	for i := 0; i < 5; i++ {
		resource := newHAProxyCfgResource(validHAProxyConfig)
		store.Update(resource)

		config := store.Get()
		require.NotNil(t, config, "config should be set after update %d", i)
	}

	// Clear and re-populate
	store.Update(nil)
	assert.Nil(t, store.Get())

	resource := newHAProxyCfgResource(validHAProxyConfig)
	store.Update(resource)
	assert.NotNil(t, store.Get())
}

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

package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/core/config"
	"haptic/pkg/dataplane"
	busevents "haptic/pkg/events"
	"haptic/pkg/introspection"
)

func TestRegisterVariables(t *testing.T) {
	// Create test dependencies
	registry := introspection.NewRegistry()
	provider := &mockStateProvider{
		config:        &config.Config{},
		configVersion: "test-v1",
		resourceCounts: map[string]int{
			"services": 5,
		},
		renderedConfig:  "global\n  maxconn 1000",
		auxFiles:        &dataplane.AuxiliaryFiles{},
		pipelineStatus:  &PipelineStatus{},
		validatedConfig: &ValidatedConfigInfo{},
		errorSummary:    &ErrorSummary{},
	}

	bus := busevents.NewEventBus(100)
	eventBuffer := NewEventBuffer(100, bus)

	// Register all variables
	RegisterVariables(registry, provider, eventBuffer)

	// Verify all expected variables are registered by trying to get them
	expectedVars := []string{
		"config",
		"credentials",
		"rendered",
		"auxfiles",
		"resources",
		"events",
		"state",
		"pipeline",
		"validated",
		"errors",
		"uptime",
	}

	for _, varName := range expectedVars {
		t.Run(varName, func(t *testing.T) {
			value, err := registry.Get(varName)
			// Some vars may return error if state is nil, which is fine
			// as long as the variable is registered (no "not found" error)
			if err != nil {
				assert.NotContains(t, err.Error(), "not found", "variable %s should be registered", varName)
			} else {
				assert.NotNil(t, value)
			}
		})
	}
}

func TestRegisterVariables_UptimeIncreases(t *testing.T) {
	registry := introspection.NewRegistry()
	provider := &mockStateProvider{}
	bus := busevents.NewEventBus(100)
	eventBuffer := NewEventBuffer(100, bus)

	RegisterVariables(registry, provider, eventBuffer)

	// Get initial uptime
	value1, err := registry.Get("uptime")
	require.NoError(t, err)
	data1 := value1.(map[string]interface{})
	uptime1 := data1["uptime_seconds"].(float64)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Get uptime again
	value2, err := registry.Get("uptime")
	require.NoError(t, err)
	data2 := value2.(map[string]interface{})
	uptime2 := data2["uptime_seconds"].(float64)

	// Verify uptime increased
	assert.Greater(t, uptime2, uptime1, "uptime should increase over time")
	assert.NotEmpty(t, data2["uptime_string"])
	assert.NotNil(t, data2["started"])
}

// TestRegisterEventsHandler tests that RegisterEventsHandler registers the handler correctly
// and the handler responds to HTTP requests properly.
func TestRegisterEventsHandler(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventBuffer := NewEventBuffer(100, bus)

	// Start the event buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go eventBuffer.Start(ctx)
	bus.Start()

	// Find a free port dynamically to avoid port conflicts in CI
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close() // Close so the server can bind to it

	// Create introspection server with the dynamic port
	registry := introspection.NewRegistry()
	server := introspection.NewServer(fmt.Sprintf("localhost:%d", port), registry)

	// This is the function we're testing - it should register the handler
	RegisterEventsHandler(server, eventBuffer)

	// Start the server
	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond) // Wait for server to start

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	// Test the handler through the introspection server
	t.Run("GET events via introspection server", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/debug/events")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)

		assert.Contains(t, response, "events")
		assert.Contains(t, response, "count")
		assert.Contains(t, response, "limit")
	})

	t.Run("GET events with limit", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/debug/events?limit=5")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(5), response["limit"])
	})

	t.Run("GET events with correlation_id", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/debug/events?correlation_id=test-abc")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, "test-abc", response["correlation_id"])
	})

	t.Run("POST not allowed", func(t *testing.T) {
		resp, err := http.Post(baseURL+"/debug/events", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("invalid limit uses default", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/debug/events?limit=invalid")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(100), response["limit"]) // Default
	})

	t.Run("negative limit uses default", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/debug/events?limit=-5")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, float64(100), response["limit"]) // Default
	})
}

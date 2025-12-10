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

package introspection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer(t *testing.T) {
	registry := NewRegistry()
	server := NewServer(":6060", registry)

	assert.NotNil(t, server)
	assert.Equal(t, ":6060", server.Addr())
}

func TestServer_RegisterHandler(t *testing.T) {
	registry := NewRegistry()
	server := NewServer(":0", registry)

	// Register custom handler
	server.RegisterHandler("/custom", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, map[string]string{"custom": "response"})
	})

	// Start server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Note: Since we can't get the actual port with :0, this is just a registration test
	assert.Len(t, server.customHandlers, 1)
}

func TestServer_SetHealthChecker(t *testing.T) {
	registry := NewRegistry()
	server := NewServer(":0", registry)

	checker := func() map[string]ComponentHealth {
		return map[string]ComponentHealth{
			"test": {Healthy: true},
		}
	}

	server.SetHealthChecker(checker)
	assert.NotNil(t, server.healthChecker)
}

func TestServer_StartAndShutdown(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("test", Func(func() (interface{}, error) {
		return "test-value", nil
	}))

	server := NewServer("localhost:16060", registry)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is running
	resp, err := http.Get("http://localhost:16060/debug/vars")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Trigger shutdown
	cancel()

	// Wait for shutdown
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServer_HandleIndex(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("config", Func(func() (interface{}, error) {
		return map[string]string{"version": "1.0"}, nil
	}))
	registry.Publish("status", Func(func() (interface{}, error) {
		return "running", nil
	}))

	server := NewServer("localhost:16061", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:16061/debug/vars")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Contains(t, result, "paths")
	assert.Contains(t, result, "count")
	assert.Equal(t, float64(2), result["count"])
}

func TestServer_HandleAllVars(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("config", Func(func() (interface{}, error) {
		return map[string]string{"version": "1.0"}, nil
	}))

	server := NewServer("localhost:16062", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:16062/debug/vars/all")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Contains(t, result, "config")
}

func TestServer_HandleVar(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("config", Func(func() (interface{}, error) {
		return map[string]interface{}{
			"version": "1.0",
			"name":    "test",
		}, nil
	}))

	server := NewServer("localhost:16063", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	t.Run("get specific var", func(t *testing.T) {
		resp, err := http.Get("http://localhost:16063/debug/vars/config")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "1.0", result["version"])
	})

	t.Run("not found", func(t *testing.T) {
		resp, err := http.Get("http://localhost:16063/debug/vars/nonexistent")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("with field query", func(t *testing.T) {
		resp, err := http.Get("http://localhost:16063/debug/vars/config?field={.name}")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "test")
	})
}

func TestServer_HandleHealth(t *testing.T) {
	t.Run("without health checker", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:16064", registry)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:16064/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "ok", result["status"])
	})

	t.Run("with healthy components", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:16065", registry)
		server.SetHealthChecker(func() map[string]ComponentHealth {
			return map[string]ComponentHealth{
				"component1": {Healthy: true},
				"component2": {Healthy: true},
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:16065/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "ok", result["status"])
	})

	t.Run("with unhealthy component", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:16066", registry)
		server.SetHealthChecker(func() map[string]ComponentHealth {
			return map[string]ComponentHealth{
				"healthy":   {Healthy: true},
				"unhealthy": {Healthy: false, Error: "connection failed"},
			}
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:16066/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "degraded", result["status"])
	})

	t.Run("healthz alias", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:16067", registry)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:16067/healthz")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestServer_HandleNotFound(t *testing.T) {
	registry := NewRegistry()
	server := NewServer("localhost:16068", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:16068/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_PortInUse(t *testing.T) {
	registry := NewRegistry()

	// Start first server
	server1 := NewServer("localhost:16069", registry)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	go server1.Start(ctx1)
	time.Sleep(100 * time.Millisecond)

	// Start second server on same port
	server2 := NewServer("localhost:16069", registry)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server2.Start(ctx2)
	}()

	// Second server should fail
	select {
	case err := <-errChan:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "server error")
	case <-time.After(2 * time.Second):
		t.Fatal("expected server to fail due to port in use")
	}
}

func TestServer_HandleVarEdgeCases(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("test", Func(func() (interface{}, error) {
		return "value", nil
	}))

	server := NewServer("localhost:16070", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	t.Run("empty path redirects to index", func(t *testing.T) {
		resp, err := http.Get("http://localhost:16070/debug/vars/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		// Should return index response
		assert.Contains(t, result, "paths")
	})
}

// TestServer_TwoPhaseInitialization verifies the Setup/Serve pattern works correctly.
func TestServer_TwoPhaseInitialization(t *testing.T) {
	t.Run("setup then serve", func(t *testing.T) {
		registry := NewRegistry()
		registry.Publish("test", Func(func() (interface{}, error) {
			return "test-value", nil
		}))

		server := NewServer("localhost:16071", registry)

		// Register custom handler before Setup
		server.RegisterHandler("/custom", func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, map[string]string{"result": "custom-handler"})
		})

		// Call Setup to finalize routes
		server.Setup()

		// Start serving
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			err := server.Serve(ctx)
			// Serve returns nil on graceful shutdown
			if err != nil && ctx.Err() == nil {
				t.Errorf("Serve returned unexpected error: %v", err)
			}
		}()
		time.Sleep(100 * time.Millisecond)

		// Verify standard endpoint works
		resp, err := http.Get("http://localhost:16071/debug/vars/test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify custom handler works
		resp2, err := http.Get("http://localhost:16071/custom")
		require.NoError(t, err)
		defer resp2.Body.Close()
		assert.Equal(t, http.StatusOK, resp2.StatusCode)

		body, _ := io.ReadAll(resp2.Body)
		assert.Contains(t, string(body), "custom-handler")
	})

	t.Run("serve without setup returns error", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:16072", registry)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Calling Serve without Setup should return error
		err := server.Serve(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Setup() must be called before Serve()")
	})

	t.Run("start calls setup implicitly", func(t *testing.T) {
		registry := NewRegistry()
		registry.Publish("test", Func(func() (interface{}, error) {
			return "implicit-setup", nil
		}))

		server := NewServer("localhost:16073", registry)

		// Register handler before Start
		server.RegisterHandler("/custom2", func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, map[string]string{"result": "implicit"})
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start should call Setup implicitly
		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		// Verify both standard and custom endpoints work
		resp, err := http.Get("http://localhost:16073/debug/vars/test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		resp2, err := http.Get("http://localhost:16073/custom2")
		require.NoError(t, err)
		defer resp2.Body.Close()
		assert.Equal(t, http.StatusOK, resp2.StatusCode)
	})
}

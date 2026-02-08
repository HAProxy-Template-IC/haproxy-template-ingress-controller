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

// startServer creates and starts an introspection server using Start(), returning the server and a cancel function.
// It polls until the server is ready to accept connections.
func startServer(t *testing.T, server *Server) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	go server.Start(ctx)

	// Poll until server is ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		resp, err := http.Get("http://" + server.Addr() + "/health")
		if err == nil {
			resp.Body.Close()
			return cancel
		}
	}
	t.Fatal("server did not start in time")
	return nil
}

// startServerTwoPhase creates and starts an introspection server using Setup()+Serve(), returning a cancel function.
// It polls until the server is ready to accept connections.
func startServerTwoPhase(t *testing.T, server *Server) context.CancelFunc {
	t.Helper()
	server.Setup()
	ctx, cancel := context.WithCancel(context.Background())

	go server.Serve(ctx)

	// Poll until server is ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		resp, err := http.Get("http://" + server.Addr() + "/health")
		if err == nil {
			resp.Body.Close()
			return cancel
		}
	}
	t.Fatal("server did not start in time")
	return nil
}

func TestNewServer(t *testing.T) {
	registry := NewRegistry()
	server := NewServer(":6060", registry)

	assert.NotNil(t, server)
	assert.Equal(t, ":6060", server.Addr())
}

func TestServer_RegisterHandler(t *testing.T) {
	registry := NewRegistry()
	server := NewServer("localhost:0", registry)

	server.RegisterHandler("/custom", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, map[string]string{"custom": "response"})
	})

	assert.Len(t, server.customHandlers, 1)
}

func TestServer_SetHealthChecker(t *testing.T) {
	registry := NewRegistry()
	server := NewServer("localhost:0", registry)

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

	server := NewServer("localhost:0", registry)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// Poll until server is ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		resp, err := http.Get("http://" + server.Addr() + "/debug/vars")
		if err == nil {
			resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			break
		}
	}

	cancel()

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

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/debug/vars")
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

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/debug/vars/all")
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

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	t.Run("get specific var", func(t *testing.T) {
		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/config")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "1.0", result["version"])
	})

	t.Run("not found", func(t *testing.T) {
		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/nonexistent")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("with field query", func(t *testing.T) {
		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/config?field={.name}")
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
		server := NewServer("localhost:0", registry)
		cancel := startServer(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/health")
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
		server := NewServer("localhost:0", registry)
		server.SetHealthChecker(func() map[string]ComponentHealth {
			return map[string]ComponentHealth{
				"component1": {Healthy: true},
				"component2": {Healthy: true},
			}
		})
		cancel := startServer(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/health")
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
		server := NewServer("localhost:0", registry)
		server.SetHealthChecker(func() map[string]ComponentHealth {
			return map[string]ComponentHealth{
				"healthy":   {Healthy: true},
				"unhealthy": {Healthy: false, Error: "connection failed"},
			}
		})
		cancel := startServer(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/health")
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
		server := NewServer("localhost:0", registry)
		cancel := startServer(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/healthz")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestServer_HandleNotFound(t *testing.T) {
	registry := NewRegistry()
	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_PortInUse(t *testing.T) {
	registry := NewRegistry()

	// Start first server on dynamic port
	server1 := NewServer("localhost:0", registry)
	cancel1 := startServer(t, server1)
	defer cancel1()

	// Try to start second server on same port
	server2 := NewServer(server1.Addr(), registry)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server2.Start(ctx2)
	}()

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

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	t.Run("empty path redirects to index", func(t *testing.T) {
		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Contains(t, result, "paths")
	})
}

func TestServer_TwoPhaseInitialization(t *testing.T) {
	t.Run("setup then serve", func(t *testing.T) {
		registry := NewRegistry()
		registry.Publish("test", Func(func() (interface{}, error) {
			return "test-value", nil
		}))

		server := NewServer("localhost:0", registry)

		server.RegisterHandler("/custom", func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, map[string]string{"result": "custom-handler"})
		})

		cancel := startServerTwoPhase(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		resp2, err := http.Get("http://" + server.Addr() + "/custom")
		require.NoError(t, err)
		defer resp2.Body.Close()
		assert.Equal(t, http.StatusOK, resp2.StatusCode)

		body, _ := io.ReadAll(resp2.Body)
		assert.Contains(t, string(body), "custom-handler")
	})

	t.Run("serve without setup returns error", func(t *testing.T) {
		registry := NewRegistry()
		server := NewServer("localhost:0", registry)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := server.Serve(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Setup() must be called before Serve()")
	})

	t.Run("start calls setup implicitly", func(t *testing.T) {
		registry := NewRegistry()
		registry.Publish("test", Func(func() (interface{}, error) {
			return "implicit-setup", nil
		}))

		server := NewServer("localhost:0", registry)

		server.RegisterHandler("/custom2", func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, map[string]string{"result": "implicit"})
		})

		cancel := startServer(t, server)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/debug/vars/test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		resp2, err := http.Get("http://" + server.Addr() + "/custom2")
		require.NoError(t, err)
		defer resp2.Body.Close()
		assert.Equal(t, http.StatusOK, resp2.StatusCode)
	})
}

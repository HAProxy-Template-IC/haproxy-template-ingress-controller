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

package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startServer creates and starts a metrics server, returning the server and a cancel function.
// It polls until the server is ready to accept connections.
func startServer(t *testing.T, registry prometheus.Gatherer) (*Server, context.CancelFunc) {
	t.Helper()
	server := NewServer("localhost:0", registry)
	ctx, cancel := context.WithCancel(context.Background())

	go server.Start(ctx)

	// Poll until server is ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if server.Addr() == "localhost:0" {
			continue
		}
		resp, err := http.Get("http://" + server.Addr() + "/metrics")
		if err == nil {
			resp.Body.Close()
			return server, cancel
		}
	}
	t.Fatal("server did not start in time")
	return nil, nil
}

func TestNewServer(t *testing.T) {
	registry := prometheus.NewRegistry()
	server := NewServer(":9090", registry)

	assert.NotNil(t, server)
	assert.Equal(t, ":9090", server.Addr())
}

func TestServer_Start(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := NewCounter(registry, "test_counter", "Test counter metric")
	counter.Inc()

	server := NewServer("localhost:0", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	cancel()

	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestServer_ServesMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := NewCounter(registry, "test_requests_total", "Total test requests")
	gauge := NewGauge(registry, "test_active_connections", "Active connections")

	counter.Inc()
	counter.Inc()
	gauge.Set(5)

	server, cancel := startServer(t, registry)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	assert.Contains(t, bodyStr, "test_requests_total")
	assert.Contains(t, bodyStr, "test_active_connections")
	assert.Contains(t, bodyStr, "test_requests_total 2")
	assert.Contains(t, bodyStr, "test_active_connections 5")
}

func TestServer_RootHandler(t *testing.T) {
	registry := prometheus.NewRegistry()
	server, cancel := startServer(t, registry)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	assert.Contains(t, bodyStr, "<html>")
	assert.Contains(t, bodyStr, "Metrics")
	assert.Contains(t, bodyStr, "/metrics")
}

func TestServer_NotFound(t *testing.T) {
	registry := prometheus.NewRegistry()
	server, cancel := startServer(t, registry)
	defer cancel()

	resp, err := http.Get("http://" + server.Addr() + "/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_GracefulShutdown(t *testing.T) {
	registry := prometheus.NewRegistry()
	server, cancel := startServer(t, registry)

	// Verify server is running
	resp, err := http.Get("http://" + server.Addr() + "/metrics")
	require.NoError(t, err)
	resp.Body.Close()

	addr := server.Addr()

	// Cancel context to trigger shutdown
	cancel()

	// Wait for shutdown to complete
	time.Sleep(200 * time.Millisecond)

	// Verify server is stopped
	resp, err = http.Get("http://" + addr + "/metrics")
	if resp != nil {
		resp.Body.Close()
	}
	assert.Error(t, err)
}

func TestServer_InstanceBased(t *testing.T) {
	registry1 := prometheus.NewRegistry()
	counter1 := NewCounter(registry1, "instance1_counter", "Counter for instance 1")
	counter1.Add(10)

	server1, cancel1 := startServer(t, registry1)
	defer cancel1()

	registry2 := prometheus.NewRegistry()
	counter2 := NewCounter(registry2, "instance2_counter", "Counter for instance 2")
	counter2.Add(20)

	server2, cancel2 := startServer(t, registry2)
	defer cancel2()

	resp1, err := http.Get("http://" + server1.Addr() + "/metrics")
	require.NoError(t, err)
	defer resp1.Body.Close()

	body1, _ := io.ReadAll(resp1.Body)
	bodyStr1 := string(body1)

	assert.Contains(t, bodyStr1, "instance1_counter")
	assert.NotContains(t, bodyStr1, "instance2_counter")

	resp2, err := http.Get("http://" + server2.Addr() + "/metrics")
	require.NoError(t, err)
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	bodyStr2 := string(body2)

	assert.Contains(t, bodyStr2, "instance2_counter")
	assert.NotContains(t, bodyStr2, "instance1_counter")
}

func TestServer_NoGlobalState(t *testing.T) {
	var metricsContent1, metricsContent2 string

	// Iteration 1
	{
		registry := prometheus.NewRegistry()
		counter := NewCounter(registry, "iteration_counter", "Counter for iteration")
		counter.Add(100)

		server, cancel := startServer(t, registry)

		resp, err := http.Get("http://" + server.Addr() + "/metrics")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metricsContent1 = string(body)

		cancel()
		time.Sleep(200 * time.Millisecond)
	}

	// Iteration 2 - fresh start
	{
		registry := prometheus.NewRegistry()
		counter := NewCounter(registry, "iteration_counter", "Counter for iteration")
		counter.Add(50)

		server, cancel := startServer(t, registry)
		defer cancel()

		resp, err := http.Get("http://" + server.Addr() + "/metrics")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metricsContent2 = string(body)
	}

	assert.Contains(t, metricsContent1, "iteration_counter 100")
	assert.Contains(t, metricsContent2, "iteration_counter 50")
	assert.NotContains(t, metricsContent2, "iteration_counter 100")
	assert.NotContains(t, metricsContent2, "iteration_counter 150")
}

func TestServer_StartFailsWhenPortInUse(t *testing.T) {
	registry := prometheus.NewRegistry()

	// Start first server on dynamic port
	server1, cancel1 := startServer(t, registry)
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

func BenchmarkServer_MetricsEndpoint(b *testing.B) {
	registry := prometheus.NewRegistry()

	for i := 0; i < 10; i++ {
		counter := NewCounter(registry, fmt.Sprintf("bench_counter_%d", i), "Benchmark counter")
		counter.Add(float64(i * 100))
	}

	server := NewServer("localhost:0", registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)

	// Poll until server is ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if server.Addr() == "localhost:0" {
			continue
		}
		resp, err := http.Get("http://" + server.Addr() + "/metrics")
		if err == nil {
			resp.Body.Close()
			break
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := http.Get("http://" + server.Addr() + "/metrics")
		if err != nil {
			b.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

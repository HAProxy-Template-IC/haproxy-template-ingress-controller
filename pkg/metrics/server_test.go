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

func TestNewServer(t *testing.T) {
	registry := prometheus.NewRegistry()
	server := NewServer(":9090", registry)

	assert.NotNil(t, server)
	assert.Equal(t, ":9090", server.Addr())
}

func TestServer_Start(t *testing.T) {
	// Create registry with a test metric
	registry := prometheus.NewRegistry()
	counter := NewCounter(registry, "test_counter", "Test counter metric")
	counter.Inc()

	// Create server with random port
	server := NewServer(":0", registry)

	// Start server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Server should be running - extract actual port
	// Since we used :0, we need to get the actual address from the server
	// For simplicity, we'll just test that we can cancel the context
	cancel()

	// Wait for shutdown
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestServer_ServesMetrics(t *testing.T) {
	// Create registry with test metrics
	registry := prometheus.NewRegistry()
	counter := NewCounter(registry, "test_requests_total", "Total test requests")
	gauge := NewGauge(registry, "test_active_connections", "Active connections")

	counter.Inc()
	counter.Inc()
	gauge.Set(5)

	// Create and start server
	server := NewServer("localhost:0", registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Since we used port 0, we need to find the actual port
	// For testing, let's use a fixed port
	server2 := NewServer("localhost:19090", registry)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go server2.Start(ctx2)
	time.Sleep(100 * time.Millisecond)

	// Query /metrics endpoint
	resp, err := http.Get("http://localhost:19090/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Read response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)

	// Verify metrics are present
	assert.Contains(t, bodyStr, "test_requests_total")
	assert.Contains(t, bodyStr, "test_active_connections")
	assert.Contains(t, bodyStr, "test_requests_total 2")
	assert.Contains(t, bodyStr, "test_active_connections 5")

	// Cleanup
	cancel2()
	time.Sleep(100 * time.Millisecond)
}

func TestServer_RootHandler(t *testing.T) {
	registry := prometheus.NewRegistry()
	server := NewServer("localhost:19091", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Query root endpoint
	resp, err := http.Get("http://localhost:19091/")
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

	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestServer_NotFound(t *testing.T) {
	registry := prometheus.NewRegistry()
	server := NewServer("localhost:19092", registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Query non-existent endpoint
	resp, err := http.Get("http://localhost:19092/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestServer_GracefulShutdown(t *testing.T) {
	registry := prometheus.NewRegistry()
	server := NewServer("localhost:19093", registry)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is running
	resp, err := http.Get("http://localhost:19093/metrics")
	require.NoError(t, err)
	resp.Body.Close()

	// Cancel context to trigger shutdown
	cancel()

	// Wait for shutdown
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("server shutdown timeout exceeded")
	}

	// Verify server is stopped
	resp, err = http.Get("http://localhost:19093/metrics")
	if resp != nil {
		resp.Body.Close()
	}
	assert.Error(t, err)
}

func TestServer_InstanceBased(t *testing.T) {
	// Test that multiple servers can use different registries
	// This validates the instance-based (non-global) design

	// Create first registry and server
	registry1 := prometheus.NewRegistry()
	counter1 := NewCounter(registry1, "instance1_counter", "Counter for instance 1")
	counter1.Add(10)

	server1 := NewServer("localhost:19094", registry1)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	go server1.Start(ctx1)
	time.Sleep(100 * time.Millisecond)

	// Create second registry and server
	registry2 := prometheus.NewRegistry()
	counter2 := NewCounter(registry2, "instance2_counter", "Counter for instance 2")
	counter2.Add(20)

	server2 := NewServer("localhost:19095", registry2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go server2.Start(ctx2)
	time.Sleep(100 * time.Millisecond)

	// Query first server
	resp1, err := http.Get("http://localhost:19094/metrics")
	require.NoError(t, err)
	defer resp1.Body.Close()

	body1, _ := io.ReadAll(resp1.Body)
	bodyStr1 := string(body1)

	// Should have instance1 metrics only
	assert.Contains(t, bodyStr1, "instance1_counter")
	assert.NotContains(t, bodyStr1, "instance2_counter")

	// Query second server
	resp2, err := http.Get("http://localhost:19095/metrics")
	require.NoError(t, err)
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	bodyStr2 := string(body2)

	// Should have instance2 metrics only
	assert.Contains(t, bodyStr2, "instance2_counter")
	assert.NotContains(t, bodyStr2, "instance1_counter")

	// Cleanup
	cancel1()
	cancel2()
	time.Sleep(100 * time.Millisecond)
}

func TestServer_NoGlobalState(t *testing.T) {
	// Test that metrics don't leak between iterations
	// Simulates application reinitialization pattern

	var metricsContent1, metricsContent2 string

	// Iteration 1
	{
		registry := prometheus.NewRegistry()
		counter := NewCounter(registry, "iteration_counter", "Counter for iteration")
		counter.Add(100)

		server := NewServer("localhost:19096", registry)
		ctx, cancel := context.WithCancel(context.Background())

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:19096/metrics")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metricsContent1 = string(body)

		// Shutdown iteration 1
		cancel()
		time.Sleep(200 * time.Millisecond)
	}

	// Iteration 2 - fresh start
	{
		registry := prometheus.NewRegistry()
		counter := NewCounter(registry, "iteration_counter", "Counter for iteration")
		counter.Add(50) // Different value

		server := NewServer("localhost:19096", registry)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go server.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		resp, err := http.Get("http://localhost:19096/metrics")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metricsContent2 = string(body)

		cancel()
		time.Sleep(100 * time.Millisecond)
	}

	// Verify iteration 1 had value 100
	assert.Contains(t, metricsContent1, "iteration_counter 100")

	// Verify iteration 2 has value 50 (not 150 - proves no global state)
	assert.Contains(t, metricsContent2, "iteration_counter 50")
	assert.NotContains(t, metricsContent2, "iteration_counter 100")
	assert.NotContains(t, metricsContent2, "iteration_counter 150")
}

func TestServer_StartFailsWhenPortInUse(t *testing.T) {
	registry := prometheus.NewRegistry()

	// Start first server on a fixed port
	server1 := NewServer("localhost:19098", registry)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	errChan1 := make(chan error, 1)
	go func() {
		errChan1 <- server1.Start(ctx1)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Try to start second server on same port
	server2 := NewServer("localhost:19098", registry)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errChan2 := make(chan error, 1)
	go func() {
		errChan2 <- server2.Start(ctx2)
	}()

	// Second server should fail
	select {
	case err := <-errChan2:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "server error")
	case <-time.After(2 * time.Second):
		t.Fatal("expected server to fail due to port in use")
	}

	// Cleanup first server
	cancel1()
	time.Sleep(100 * time.Millisecond)
}

// BenchmarkServer_MetricsEndpoint benchmarks the /metrics endpoint.
func BenchmarkServer_MetricsEndpoint(b *testing.B) {
	registry := prometheus.NewRegistry()

	// Create some test metrics
	for i := 0; i < 10; i++ {
		counter := NewCounter(registry, fmt.Sprintf("bench_counter_%d", i), "Benchmark counter")
		counter.Add(float64(i * 100))
	}

	server := NewServer("localhost:19097", registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := http.Get("http://localhost:19097/metrics")
		if err != nil {
			b.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	cancel()
}

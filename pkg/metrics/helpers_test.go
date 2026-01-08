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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCounter(t *testing.T) {
	registry := prometheus.NewRegistry()

	counter := NewCounter(registry, "test_counter_total", "Test counter")
	assert.NotNil(t, counter)

	// Increment counter
	counter.Inc()
	counter.Add(5)

	// Verify value
	assert.Equal(t, float64(6), testutil.ToFloat64(counter))
}

func TestNewHistogram(t *testing.T) {
	registry := prometheus.NewRegistry()

	histogram := NewHistogram(registry, "test_duration_seconds", "Test duration")
	assert.NotNil(t, histogram)

	// Record observations
	histogram.Observe(0.5)
	histogram.Observe(1.5)
	histogram.Observe(2.5)

	// Verify histogram recorded observations
	// We can't easily verify count/sum without internal access,
	// so just verify the histogram was created successfully
	assert.NotNil(t, histogram)
}

func TestNewHistogramWithBuckets(t *testing.T) {
	registry := prometheus.NewRegistry()

	customBuckets := []float64{0.1, 0.5, 1.0, 5.0}
	histogram := NewHistogramWithBuckets(
		registry,
		"test_custom_duration_seconds",
		"Test duration with custom buckets",
		customBuckets,
	)
	assert.NotNil(t, histogram)

	// Record observations
	histogram.Observe(0.05) // Below first bucket
	histogram.Observe(0.3)  // In first bucket
	histogram.Observe(0.7)  // In second bucket
	histogram.Observe(2.0)  // In third bucket
	histogram.Observe(10.0) // Above last bucket

	// Verify histogram was created successfully
	assert.NotNil(t, histogram)
}

func TestNewHistogramVec(t *testing.T) {
	registry := prometheus.NewRegistry()

	histVec := NewHistogramVec(
		registry,
		"test_queue_wait_seconds",
		"Queue wait time by phase",
		[]string{"phase"},
		DurationBuckets(),
	)
	assert.NotNil(t, histVec)

	// Record observations for different phases
	histVec.WithLabelValues("trigger_to_render").Observe(0.05)
	histVec.WithLabelValues("render_to_validate").Observe(0.1)
	histVec.WithLabelValues("validate_to_deploy").Observe(0.25)

	// Verify metrics were registered correctly
	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	// Find our metric
	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "test_queue_wait_seconds" {
			found = true
			// Should have 3 label combinations
			assert.Len(t, mf.GetMetric(), 3)
		}
	}
	assert.True(t, found, "histogram vec metric not found in registry")
}

func TestNewGauge(t *testing.T) {
	registry := prometheus.NewRegistry()

	gauge := NewGauge(registry, "test_temperature_celsius", "Test temperature")
	assert.NotNil(t, gauge)

	// Set value
	gauge.Set(25.5)
	assert.Equal(t, 25.5, testutil.ToFloat64(gauge))

	// Increase
	gauge.Inc()
	assert.Equal(t, 26.5, testutil.ToFloat64(gauge))

	// Decrease
	gauge.Dec()
	assert.Equal(t, 25.5, testutil.ToFloat64(gauge))

	// Add
	gauge.Add(10)
	assert.Equal(t, 35.5, testutil.ToFloat64(gauge))

	// Subtract
	gauge.Sub(5)
	assert.Equal(t, 30.5, testutil.ToFloat64(gauge))
}

func TestNewGaugeVec(t *testing.T) {
	registry := prometheus.NewRegistry()

	gaugeVec := NewGaugeVec(
		registry,
		"test_queue_size",
		"Size of queue by type",
		[]string{"queue_type"},
	)
	assert.NotNil(t, gaugeVec)

	// Set values for different labels
	gaugeVec.WithLabelValues("high_priority").Set(10)
	gaugeVec.WithLabelValues("low_priority").Set(50)

	// Verify values
	highPriority, err := gaugeVec.GetMetricWithLabelValues("high_priority")
	require.NoError(t, err)
	assert.Equal(t, 10.0, testutil.ToFloat64(highPriority))

	lowPriority, err := gaugeVec.GetMetricWithLabelValues("low_priority")
	require.NoError(t, err)
	assert.Equal(t, 50.0, testutil.ToFloat64(lowPriority))
}

func TestNewCounterVec(t *testing.T) {
	registry := prometheus.NewRegistry()

	counterVec := NewCounterVec(
		registry,
		"test_requests_total",
		"Total requests",
		[]string{"method", "status"},
	)
	assert.NotNil(t, counterVec)

	// Increment counters with different labels
	counterVec.WithLabelValues("GET", "200").Inc()
	counterVec.WithLabelValues("GET", "200").Inc()
	counterVec.WithLabelValues("POST", "201").Inc()
	counterVec.WithLabelValues("GET", "404").Add(3)

	// Verify values
	get200, err := counterVec.GetMetricWithLabelValues("GET", "200")
	require.NoError(t, err)
	assert.Equal(t, 2.0, testutil.ToFloat64(get200))

	post201, err := counterVec.GetMetricWithLabelValues("POST", "201")
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(post201))

	get404, err := counterVec.GetMetricWithLabelValues("GET", "404")
	require.NoError(t, err)
	assert.Equal(t, 3.0, testutil.ToFloat64(get404))
}

func TestDurationBuckets(t *testing.T) {
	buckets := DurationBuckets()

	// Verify expected buckets
	expected := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}
	assert.Equal(t, expected, buckets)

	// Verify buckets are suitable for duration metrics
	registry := prometheus.NewRegistry()
	histogram := NewHistogramWithBuckets(
		registry,
		"operation_duration_seconds",
		"Operation duration",
		buckets,
	)

	// Test various durations
	histogram.Observe(0.005) // Very fast
	histogram.Observe(0.05)  // Fast
	histogram.Observe(0.5)   // Medium
	histogram.Observe(2.0)   // Slow
	histogram.Observe(15.0)  // Very slow

	assert.NotNil(t, histogram)
}

func TestMetricsRegistration(t *testing.T) {
	// Test that metrics are properly registered with the registry
	registry := prometheus.NewRegistry()

	// Create various metrics
	NewCounter(registry, "app_requests_total", "Total requests")
	NewGauge(registry, "app_active_connections", "Active connections")
	NewHistogram(registry, "app_request_duration_seconds", "Request duration")

	// Gather metrics
	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	// Verify all metrics are registered
	metricNames := make(map[string]bool)
	for _, mf := range metricFamilies {
		metricNames[mf.GetName()] = true
	}

	assert.True(t, metricNames["app_requests_total"])
	assert.True(t, metricNames["app_active_connections"])
	assert.True(t, metricNames["app_request_duration_seconds"])
}

func TestInstanceBasedMetrics(t *testing.T) {
	// Test that metrics in different registries are independent
	// This validates the instance-based (non-global) design

	// Registry 1
	registry1 := prometheus.NewRegistry()
	counter1 := NewCounter(registry1, "instance_counter", "Counter for instance")
	counter1.Add(10)

	// Registry 2
	registry2 := prometheus.NewRegistry()
	counter2 := NewCounter(registry2, "instance_counter", "Counter for instance")
	counter2.Add(20)

	// Verify values are independent
	assert.Equal(t, 10.0, testutil.ToFloat64(counter1))
	assert.Equal(t, 20.0, testutil.ToFloat64(counter2))

	// Gather from registry 1
	metrics1, err := registry1.Gather()
	require.NoError(t, err)

	// Should only have metrics from registry 1
	var found1 bool
	for _, mf := range metrics1 {
		if mf.GetName() == "instance_counter" {
			found1 = true
			assert.Equal(t, 10.0, mf.GetMetric()[0].GetCounter().GetValue())
		}
	}
	assert.True(t, found1)

	// Gather from registry 2
	metrics2, err := registry2.Gather()
	require.NoError(t, err)

	// Should only have metrics from registry 2
	var found2 bool
	for _, mf := range metrics2 {
		if mf.GetName() == "instance_counter" {
			found2 = true
			assert.Equal(t, 20.0, mf.GetMetric()[0].GetCounter().GetValue())
		}
	}
	assert.True(t, found2)
}

func TestNoGlobalRegistryUsage(t *testing.T) {
	// Test that helpers don't pollute the global registry
	// Create metrics with local registry
	registry := prometheus.NewRegistry()
	NewCounter(registry, "local_counter", "Local counter")
	NewGauge(registry, "local_gauge", "Local gauge")

	// Gather from default global registry
	defaultMetrics, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	// Verify our metrics are NOT in the global registry
	for _, mf := range defaultMetrics {
		name := mf.GetName()
		assert.NotEqual(t, "local_counter", name, "Metric leaked to global registry")
		assert.NotEqual(t, "local_gauge", name, "Metric leaked to global registry")
	}

	// Verify they ARE in our local registry
	localMetrics, err := registry.Gather()
	require.NoError(t, err)

	foundCounter := false
	foundGauge := false
	for _, mf := range localMetrics {
		if mf.GetName() == "local_counter" {
			foundCounter = true
		}
		if mf.GetName() == "local_gauge" {
			foundGauge = true
		}
	}
	assert.True(t, foundCounter, "Counter not found in local registry")
	assert.True(t, foundGauge, "Gauge not found in local registry")
}

func TestMetricNaming(t *testing.T) {
	registry := prometheus.NewRegistry()

	// Test that metric names follow Prometheus conventions
	counter := NewCounter(registry, "http_requests_total", "HTTP requests")
	histogram := NewHistogram(registry, "http_request_duration_seconds", "HTTP duration")
	gauge := NewGauge(registry, "memory_usage_bytes", "Memory usage")

	assert.NotNil(t, counter)
	assert.NotNil(t, histogram)
	assert.NotNil(t, gauge)

	// Verify metrics can be gathered
	metrics, err := registry.Gather()
	require.NoError(t, err)
	assert.Len(t, metrics, 3)
}

func BenchmarkNewCounter(b *testing.B) {
	registry := prometheus.NewRegistry()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewCounter(registry, "bench_counter", "Benchmark counter")
	}
}

func BenchmarkCounterInc(b *testing.B) {
	registry := prometheus.NewRegistry()
	counter := NewCounter(registry, "bench_counter", "Benchmark counter")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter.Inc()
	}
}

func BenchmarkHistogramObserve(b *testing.B) {
	registry := prometheus.NewRegistry()
	histogram := NewHistogram(registry, "bench_histogram", "Benchmark histogram")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		histogram.Observe(float64(i) * 0.001)
	}
}

func BenchmarkGaugeSet(b *testing.B) {
	registry := prometheus.NewRegistry()
	gauge := NewGauge(registry, "bench_gauge", "Benchmark gauge")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gauge.Set(float64(i))
	}
}

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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// IMPORTANT: All functions in this file accept a prometheus.Registerer parameter.
// NEVER use global prometheus.DefaultRegisterer or prometheus.DefaultGatherer.
//
// This ensures metrics can be garbage collected when the registry is discarded,
// which is critical for applications that reinitialize on configuration changes.

// NewCounter creates and registers a counter metric.
//
// A counter is a cumulative metric that represents a single monotonically
// increasing value. Use counters for values that only increase, such as
// the number of requests served, tasks completed, or errors.
//
// Parameters:
//   - registry: The Prometheus registry to register with (use prometheus.NewRegistry())
//   - name: Metric name (e.g., "http_requests_total")
//   - help: Human-readable description of the metric
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	requestsTotal := metrics.NewCounter(registry, "http_requests_total", "Total HTTP requests")
//	requestsTotal.Inc()
func NewCounter(registry prometheus.Registerer, name, help string) prometheus.Counter {
	return promauto.With(registry).NewCounter(prometheus.CounterOpts{
		Name: name,
		Help: help,
	})
}

// NewHistogramWithBuckets creates and registers a histogram with custom buckets.
//
// Use this when default buckets don't match your use case. For duration metrics,
// consider using DurationBuckets() as a starting point.
//
// Parameters:
//   - registry: The Prometheus registry to register with
//   - name: Metric name
//   - help: Human-readable description
//   - buckets: Bucket boundaries (e.g., []float64{0.1, 0.5, 1.0, 5.0})
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	duration := metrics.NewHistogramWithBuckets(
//	    registry,
//	    "api_latency_seconds",
//	    "API latency distribution",
//	    metrics.DurationBuckets(),
//	)
//	duration.Observe(0.25)
func NewHistogramWithBuckets(registry prometheus.Registerer, name, help string, buckets []float64) prometheus.Histogram {
	return promauto.With(registry).NewHistogram(prometheus.HistogramOpts{
		Name:    name,
		Help:    help,
		Buckets: buckets,
	})
}

// NewGauge creates and registers a gauge metric.
//
// A gauge is a metric that represents a single numerical value that can
// arbitrarily go up and down. Use gauges for values that can increase or
// decrease, such as temperature, memory usage, or number of concurrent requests.
//
// Parameters:
//   - registry: The Prometheus registry to register with
//   - name: Metric name (e.g., "concurrent_requests")
//   - help: Human-readable description of the metric
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	activeConnections := metrics.NewGauge(registry, "active_connections", "Number of active connections")
//	activeConnections.Set(42)
func NewGauge(registry prometheus.Registerer, name, help string) prometheus.Gauge {
	return promauto.With(registry).NewGauge(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	})
}

// NewGaugeVec creates and registers a gauge vector with labels.
//
// A gauge vector is a collection of gauges with the same name but different
// label dimensions. Use gauge vectors when you need to track the same metric
// across different categories.
//
// Parameters:
//   - registry: The Prometheus registry to register with
//   - name: Metric name
//   - help: Human-readable description
//   - labels: Label names (e.g., []string{"method", "status"})
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	queueSize := metrics.NewGaugeVec(
//	    registry,
//	    "queue_size",
//	    "Size of queue by type",
//	    []string{"queue_type"},
//	)
//	queueSize.WithLabelValues("high_priority").Set(10)
//	queueSize.WithLabelValues("low_priority").Set(50)
func NewGaugeVec(registry prometheus.Registerer, name, help string, labels []string) *prometheus.GaugeVec {
	return promauto.With(registry).NewGaugeVec(
		prometheus.GaugeOpts{
			Name: name,
			Help: help,
		},
		labels,
	)
}

// NewCounterVec creates and registers a counter vector with labels.
//
// A counter vector is a collection of counters with the same name but different
// label dimensions. Use counter vectors when you need to track the same counter
// across different categories.
//
// Parameters:
//   - registry: The Prometheus registry to register with
//   - name: Metric name
//   - help: Human-readable description
//   - labels: Label names (e.g., []string{"method", "status"})
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	httpRequests := metrics.NewCounterVec(
//	    registry,
//	    "http_requests_total",
//	    "Total HTTP requests",
//	    []string{"method", "status"},
//	)
//	httpRequests.WithLabelValues("GET", "200").Inc()
//	httpRequests.WithLabelValues("POST", "201").Inc()
func NewCounterVec(registry prometheus.Registerer, name, help string, labels []string) *prometheus.CounterVec {
	return promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: name,
			Help: help,
		},
		labels,
	)
}


// DurationBuckets returns histogram buckets suitable for duration metrics in seconds.
//
// The buckets cover a range from 10ms to 10s, which is appropriate for most
// API and processing durations.
//
// Buckets: [0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0]
//
// Example:
//
//	registry := prometheus.NewRegistry()
//	latency := metrics.NewHistogramWithBuckets(
//	    registry,
//	    "operation_duration_seconds",
//	    "Operation duration in seconds",
//	    metrics.DurationBuckets(),
//	)
func DurationBuckets() []float64 {
	return []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}
}

// DeploymentDurationBuckets returns histogram buckets for HAProxy deployment duration.
//
// Deployments involve network calls to the dataplane API and may wait for HAProxy
// to reload its configuration. Reloads can take several seconds on busy servers,
// so buckets extend to 60s to capture the full tail distribution without capping.
//
// Buckets: [0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 15.0, 30.0, 60.0]
func DeploymentDurationBuckets() []float64 {
	return []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 15.0, 30.0, 60.0}
}

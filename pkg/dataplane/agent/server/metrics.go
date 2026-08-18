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

package server

import (
	"log/slog"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the agent's own Prometheus surface plus the invariant helper.
// Everything here is instance-based so a test registry stays isolated.
type Metrics struct {
	applies    *prometheus.CounterVec
	rejected   *prometheus.CounterVec
	invariants *prometheus.CounterVec
	reloads    *prometheus.CounterVec
	rollbacks  prometheus.Counter
	deferred   *prometheus.CounterVec
	opErrors   *prometheus.CounterVec
	divergence prometheus.Counter
	generation prometheus.Gauge

	logger    *slog.Logger
	violation atomic.Pointer[string]
}

// NewMetrics registers the agent's metrics on registry.
func NewMetrics(registry prometheus.Registerer, logger *slog.Logger) *Metrics {
	m := &Metrics{
		applies: counterVec(registry, "haptic_agent_apply_total",
			"Applies the agent completed, by outcome mode.", "mode"),
		rejected: counterVec(registry, "haptic_agent_apply_rejected_total",
			"Applies the agent refused or rolled back, by stage.", "stage"),
		invariants: counterVec(registry, "haptic_agent_invariant_violations_total",
			"Invariant violations the agent observed, by name.", "name"),
		reloads: counterVec(registry, "haptic_agent_reloads_total",
			"Reloads the agent asked the master process for, by result.", "result"),
		rollbacks: counter(registry, "haptic_agent_rollbacks_total",
			"Applies whose file set was restored to the last known good."),
		deferred: counterVec(registry, "haptic_agent_deferred_deletes_total",
			"Deferred runtime deletes, by object kind and outcome.", "kind", "outcome"),
		opErrors: counterVec(registry, "haptic_agent_op_errors_total",
			"Runtime ops HAProxy rejected, by op kind.", "kind"),
		divergence: counter(registry, "haptic_runtime_map_divergence_total",
			"Read-backs that found the running state different from the desired one."),
		generation: gauge(registry, "haptic_agent_generation",
			"The agent's apply generation, which increases by one per successful apply."),
		logger: logger,
	}
	return m
}

// invariant records a condition that must hold. It never panics: the agent's
// safety layer refuses or aborts the apply, and its decision layer degrades to
// a reload, but a data plane must not take itself down over an assertion.
func (m *Metrics) invariant(cond bool, name string) {
	if cond {
		return
	}
	m.invariants.WithLabelValues(name).Inc()
	m.logger.Error("agent invariant violated", "invariant", name)
	violated := name
	m.violation.Store(&violated)
}

// LastViolation is the invariant that failed most recently, for /v1/state.
func (m *Metrics) LastViolation() string {
	if name := m.violation.Load(); name != nil {
		return *name
	}
	return ""
}

// DeferredDeleteDone implements cli.Observer.
func (m *Metrics) DeferredDeleteDone(kind string) {
	m.deferred.WithLabelValues(kind, "done").Inc()
}

// DeferredDeleteDeferred implements cli.Observer.
func (m *Metrics) DeferredDeleteDeferred(kind string) {
	m.deferred.WithLabelValues(kind, "deferred").Inc()
}

// DeferredDeleteAbandoned implements cli.Observer.
func (m *Metrics) DeferredDeleteAbandoned(kind string) {
	m.deferred.WithLabelValues(kind, "abandoned").Inc()
}

func counterVec(registry prometheus.Registerer, name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	registry.MustRegister(c)
	return c
}

func counter(registry prometheus.Registerer, name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	registry.MustRegister(c)
	return c
}

func gauge(registry prometheus.Registerer, name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	registry.MustRegister(g)
	return g
}

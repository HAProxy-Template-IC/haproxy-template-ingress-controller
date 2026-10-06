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

package controller

import (
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

const (
	// ReadinessPath and LivenessPath judge the serving iteration; /healthz
	// keeps judging the newest one (ADR-0028).
	ReadinessPath = "/readyz"
	LivenessPath  = "/livez"

	healthKeyAdmission   = "admission"
	healthKeyStartup     = "startup"
	healthKeyRenderGraph = "render-graph"
)

// servingIteration is an iteration that completed startup.
type servingIteration struct {
	id      iterationID
	health  introspection.HealthCheckFunc
	leading func() bool
	// graphPublished reports whether the iteration's first full render has
	// published its graph; nil when it renders nothing to warm.
	graphPublished func() bool
}

func (p *persistentInfra) markServing(s *servingIteration) {
	p.servingMu.Lock()
	defer p.servingMu.Unlock()
	p.serving = s
}

func (p *persistentInfra) clearServing(id iterationID) {
	p.servingMu.Lock()
	defer p.servingMu.Unlock()
	if p.serving != nil && p.serving.id == id {
		p.serving = nil
	}
}

func (p *persistentInfra) registerProbeEndpoints() {
	p.IntrospectionServer.RegisterHandler(ReadinessPath, introspection.HealthHandler(p.readinessHealth))
	p.IntrospectionServer.RegisterHandler(LivenessPath, introspection.HealthHandler(p.livenessHealth))
}

// exemptPredecessor returns the iteration the probes judge in place of the
// newest one: a predecessor still serving while its successor starts or
// fails. Its admission verdicts are correct only while the fleet renders its
// configuration, i.e. while it leads, and the exemption ends as soon as
// another replica runs a converged iteration that should lead instead.
func (p *persistentInfra) exemptPredecessor() *servingIteration {
	p.servingMu.Lock()
	serving := p.serving
	p.servingMu.Unlock()
	if serving == nil || serving.id == p.currentIteration() || serving.health == nil || !serving.leading() {
		return nil
	}
	if p.siblings == nil || !p.siblings.converged.NoneMatch() {
		return nil
	}
	return serving
}

func (p *persistentInfra) servingHealth() map[string]introspection.ComponentHealth {
	if predecessor := p.exemptPredecessor(); predecessor != nil {
		return predecessor.health()
	}
	if p.IntrospectionServer == nil {
		return map[string]introspection.ComponentHealth{}
	}
	return p.IntrospectionServer.CheckHealth()
}

func (p *persistentInfra) livenessHealth() map[string]introspection.ComponentHealth {
	entries := p.servingHealth()
	if running, stalled := p.attemptStalled(); stalled {
		entries[healthKeyStartup] = introspection.ComponentHealth{
			Healthy: false,
			Error:   fmt.Sprintf("controller iteration startup has not returned after %s", running.Round(time.Second)),
		}
	}
	return entries
}

// readinessHealth adds the admission listener's state to the liveness
// verdict: a pod without an installed validator generation denies every
// request, so it must not receive any while a sibling can validate.
func (p *persistentInfra) readinessHealth() map[string]introspection.ComponentHealth {
	entries := p.livenessHealth()
	p.webhookMu.Lock()
	server := p.WebhookServer
	p.webhookMu.Unlock()
	if server == nil {
		return entries
	}
	entry := introspection.ComponentHealth{Healthy: server.Validating()}
	if !entry.Healthy {
		entry.Error = "no admission validators installed; this replica denies admission requests"
	}
	entries[healthKeyAdmission] = entry
	if graph, ok := p.renderGraphHealth(); ok {
		entries[healthKeyRenderGraph] = graph
	}
	return entries
}

// renderGraphHealth keeps a replica whose first full render is still running
// out of the webhook Service while another replica can validate: it would
// deny every request until that render commits (#285). Alone, it stays Ready
// and its denials say why; the note marks it for the other replicas.
func (p *persistentInfra) renderGraphHealth() (introspection.ComponentHealth, bool) {
	p.servingMu.Lock()
	serving := p.serving
	p.servingMu.Unlock()
	if serving == nil || serving.graphPublished == nil {
		return introspection.ComponentHealth{}, false
	}
	if serving.graphPublished() {
		return introspection.ComponentHealth{Healthy: true}, true
	}
	if p.siblings != nil && p.siblings.renderGraph.NoneMatch() {
		return introspection.ComponentHealth{
			Healthy: true,
			Error:   "first full render still running; no other replica can validate, so this one answers admission requests",
		}, true
	}
	return introspection.ComponentHealth{
		Healthy: false,
		Error:   "first full render still running; another replica validates admission requests until it finishes",
	}, true
}

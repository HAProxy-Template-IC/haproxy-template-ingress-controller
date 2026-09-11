// Copyright 2026 Philipp Hossner
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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestComponent_DeploymentOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		result     events.DeploymentResult
		wantErrors float64
		failedPod  bool
	}{
		{name: "converged", result: events.DeploymentResult{Total: 2, Succeeded: 2}},
		{name: "all reloads scheduled", result: events.DeploymentResult{Total: 2, PendingReloads: 2}},
		{name: "one converged one scheduled", result: events.DeploymentResult{Total: 2, Succeeded: 1, PendingReloads: 1}},
		{name: "one scheduled one failed", result: events.DeploymentResult{Total: 2, PendingReloads: 1, Failed: 1},
			failedPod: true, wantErrors: 1},
		{name: "one converged one failed", result: events.DeploymentResult{Total: 2, Succeeded: 1, Failed: 1},
			failedPod: true, wantErrors: 1},
		{name: "all failed", result: events.DeploymentResult{Total: 2, Failed: 2}, wantErrors: 1},
		{name: "no endpoints", result: events.DeploymentResult{}, wantErrors: 1},
		{name: "no accepted result", result: events.DeploymentResult{Total: 2}, wantErrors: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			metrics := NewMetrics(prometheus.NewRegistry())
			component := New(metrics, busevents.NewEventBus(100))
			wantTotal := 1.0
			if tt.failedPod {
				component.handleEvent(events.NewInstanceDeploymentFailedEvent("pod", "connection refused", true))
				wantTotal++
			}
			component.handleEvent(events.NewDeploymentCompletedEvent(&tt.result))
			assert.Equal(t, wantTotal, testutil.ToFloat64(metrics.DeploymentTotal))
			assert.Equal(t, tt.wantErrors, testutil.ToFloat64(metrics.DeploymentErrors))
			assert.Equal(t, float64(tt.result.Succeeded), testutil.ToFloat64(metrics.HAProxyFleetConverged))
		})
	}
}

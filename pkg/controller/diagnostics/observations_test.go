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

package diagnostics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func TestFleetObservationsOmitPayloadsAndKeepMismatchEvidence(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "haptic", Generation: 3,
		Annotations: map[string]string{"private": "private-annotation"}},
		Spec: corev1.PodSpec{InitContainers: []corev1.Container{{Name: "agent", Args: []string{"--base-dir=/custom"},
			Env: []corev1.EnvVar{{Name: "PASSWORD", Value: "private-environment"}}}}}}
	state := &api.State{APIVersion: api.Version, AgentOps: agentclient.ComposableOps(), AgentVersion: "dev", Generation: 7,
		HAProxy:       api.HAProxyInfo{Version: "3.4", WorkerPID: 10, WorkerStartTimeUnixMicros: 20, FullVersion: "private-version-detail"},
		Files:         map[string]api.FileAt{"haproxy.cfg": {Digest: "sha256:abc"}, "private-file-name": {Digest: "sha256:def"}},
		AppliedPlanID: "applied", RunningPlanID: "running", AppliedPlan: []byte("private-plan"),
		LastApply:          &api.ApplyResult{OK: false, Error: &api.ApplyError{Message: "private-error"}},
		InvariantViolation: "private-invariant-detail"}
	observation := agentView(pod, state, "/custom/haproxy.cfg")
	require.Equal(t, "sha256:abc", observation.ConfigFileDigest)
	require.Equal(t, "applied", observation.AppliedPlanID)
	require.Equal(t, "running", observation.RunningPlanID)
	require.True(t, observation.LastApplyFailed)
	require.True(t, observation.InvariantFailed)
	require.False(t, observation.ReloadFallback)
	encoded, err := json.Marshal(observation)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
	require.Contains(t, string(encoded), `"generation":3`)
	require.Contains(t, string(encoded), `"worker_generation":7`)
}

func TestAgentProtocolSkewReportsReloadFallback(t *testing.T) {
	state := &api.State{APIVersion: api.Version + 1, AgentOps: agentclient.ComposableOps()}
	require.True(t, agentView(&corev1.Pod{}, state, "/etc/haproxy/haproxy.cfg").ReloadFallback)
	state.APIVersion = api.Version
	state.AgentOps = nil
	require.True(t, agentView(&corev1.Pod{}, state, "/etc/haproxy/haproxy.cfg").ReloadFallback)
}

func TestConfigurationAndPipelineProjectErrorCountsOnly(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1", "kind": "HAProxyCfg",
		"metadata": map[string]any{"name": "haproxy-config"},
		"spec":     map[string]any{"checksum": "sha256:abc", "content": "private-config"},
		"status": map[string]any{"validationError": "private-validation", "deployedToPods": []any{map[string]any{
			"podName": "agent", "podUID": "pod-1", "checksum": "sha256:old", "lastError": "private-deployment", "consecutiveErrors": int64(2),
		}}},
	}}
	config, err := configurationView(object)
	require.NoError(t, err)
	require.Equal(t, 1, config.ValidationErrors)
	require.Equal(t, 2, config.Deployments[0].ConsecutiveErrors)
	require.True(t, config.Deployments[0].HasError)
	controller := Controller{}
	controllerPipeline(&controller, &debug.PipelineStatus{
		Rendering:  &debug.RenderingStatus{Status: "failed", Error: "private-render", Timestamp: time.Now()},
		Validation: &debug.ValidationStatus{Status: "failed", Errors: []string{"private-test", "private-syntax"}, PlanID: "plan-1"},
		Deployment: &debug.DeploymentStatus{Status: "failed", EndpointsFailed: 1, FailedEndpoints: []debug.FailedEndpoint{{URL: "https://private-endpoint", Error: "private-agent"}}},
	})
	require.Equal(t, 2, controller.Validation.Errors)
	require.Equal(t, "plan-1", controller.ValidatedPlan)
	encoded, err := json.Marshal(Report{Configurations: []Configuration{config}, Controllers: []Controller{controller}})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-")
}

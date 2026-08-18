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

package discovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// buildContainerStatus builds a containerStatus map with the given name, ready
// flag, and optional state.
func buildContainerStatus(name string, ready bool, state string) map[string]any {
	cs := map[string]any{
		"name":  name,
		"ready": ready,
	}
	if state != "" {
		cs["state"] = map[string]any{state: map[string]any{}}
	}
	return cs
}

// buildPod builds an *unstructured.Unstructured pod with optional status
// containerStatuses, status.podIP, and status.phase.
func buildPod(name string, statuses []map[string]any, podIP, phase string) *unstructured.Unstructured {
	statusContainers := make([]any, 0, len(statuses))
	for _, s := range statuses {
		statusContainers = append(statusContainers, s)
	}
	pod := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "default",
			},
			"status": map[string]any{
				"containerStatuses": statusContainers,
			},
		},
	}
	if podIP != "" {
		_ = unstructured.SetNestedField(pod.Object, podIP, "status", "podIP")
	}
	if phase != "" {
		_ = unstructured.SetNestedField(pod.Object, phase, "status", "phase")
	}
	return pod
}

// agentContainerRunning gates admission on the container state alone. The ready
// flag is deliberately not consulted: HAProxy's /ready only turns 200 after the
// first apply, so a fresh pod whose agent is up must still be admitted.
func TestAgentContainerRunning(t *testing.T) {
	tests := []struct {
		name     string
		statuses []map[string]any
		want     bool
	}{
		{
			name:     "agent running",
			statuses: []map[string]any{buildContainerStatus(agentContainerName, true, "running")},
			want:     true,
		},
		{
			name:     "agent running but not ready",
			statuses: []map[string]any{buildContainerStatus(agentContainerName, false, "running")},
			want:     true,
		},
		{
			name:     "agent waiting",
			statuses: []map[string]any{buildContainerStatus(agentContainerName, false, "waiting")},
			want:     false,
		},
		{
			name:     "agent terminated",
			statuses: []map[string]any{buildContainerStatus(agentContainerName, false, "terminated")},
			want:     false,
		},
		{
			name:     "another container is running",
			statuses: []map[string]any{buildContainerStatus("haproxy", true, "running")},
			want:     false,
		},
		{
			name:     "no container statuses",
			statuses: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := buildPod("test-pod", tt.statuses, "10.0.0.1", "Running")
			assert.Equal(t, tt.want, agentContainerRunning(pod, nil))
		})
	}
}

func TestExtractPodIP(t *testing.T) {
	tests := []struct {
		name   string
		podIP  string
		wantIP string
	}{
		{name: "valid IP", podIP: "10.1.2.3", wantIP: "10.1.2.3"},
		{name: "missing IP", podIP: "", wantIP: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := buildPod("test-pod", nil, tt.podIP, "")

			got, err := extractPodIP(pod, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantIP, got)
		})
	}
}

func TestExtractPodPhase(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		wantPhase string
	}{
		{name: "running phase", phase: "Running", wantPhase: "Running"},
		{name: "pending phase", phase: "Pending", wantPhase: "Pending"},
		{name: "missing phase", phase: "", wantPhase: ""},
		{name: "failed phase", phase: "Failed", wantPhase: "Failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := buildPod("test-pod", nil, "", tt.phase)

			got, err := extractPodPhase(pod, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPhase, got)
		})
	}
}

func TestResourceToPod(t *testing.T) {
	unstructuredPod := &unstructured.Unstructured{
		Object: map[string]any{"apiVersion": "v1", "kind": "Pod"},
	}
	mapPod := map[string]any{"apiVersion": "v1", "kind": "Pod"}

	tests := []struct {
		name     string
		resource any
		wantNil  bool
	}{
		{name: "pointer to unstructured", resource: unstructuredPod},
		{name: "map[string]any", resource: mapPod},
		{name: "string is not a pod", resource: "not a pod", wantNil: true},
		{name: "nil resource", resource: nil, wantNil: true},
		{name: "int is not a pod", resource: 42, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceToPod(tt.resource)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, "Pod", got.GetKind())
		})
	}
}

func TestHasKey(t *testing.T) {
	m := map[string]any{"running": true, "started": false}
	assert.True(t, hasKey(m, "running"))
	assert.True(t, hasKey(m, "started"))
	assert.False(t, hasKey(m, "missing"))
	assert.False(t, hasKey(nil, "anything"))
}

func TestDiscovery_EvaluatePod(t *testing.T) {
	creds := coreconfig.Credentials{
		DataplaneUsername: "user",
		DataplanePassword: "pass",
	}
	deletionTime := metav1.NewTime(time.Now())
	running := []map[string]any{buildContainerStatus(agentContainerName, false, "running")}

	tests := []struct {
		name       string
		pod        *unstructured.Unstructured
		resource   any
		wantOK     bool
		wantReason string
		wantURL    string
	}{
		{
			name:    "running pod with a running agent is a candidate",
			pod:     buildPod("happy-pod", running, "10.0.0.1", "Running"),
			wantOK:  true,
			wantURL: "http://10.0.0.1:5555",
		},
		{
			name:   "no IP assigned yet is skipped like a pending pod",
			pod:    buildPod("no-ip", running, "", "Running"),
			wantOK: false,
		},
		{
			name:   "not in running phase",
			pod:    buildPod("pending", running, "10.0.0.2", "Pending"),
			wantOK: false,
		},
		{
			name:       "agent container not running",
			pod:        buildPod("starting", []map[string]any{buildContainerStatus(agentContainerName, false, "waiting")}, "10.0.0.3", "Running"),
			wantOK:     true,
			wantReason: RejectionAgentNotRunning,
		},
		{
			name:       "no agent container at all",
			pod:        buildPod("foreign", []map[string]any{buildContainerStatus("haproxy", true, "running")}, "10.0.0.4", "Running"),
			wantOK:     true,
			wantReason: RejectionAgentNotRunning,
		},
		{
			name:     "non-pod resource",
			resource: "not a pod",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := createTestDiscovery(5555)

			resource := tt.resource
			if resource == nil {
				resource = tt.pod
			}

			candidate, ok, err := d.evaluatePod(resource, creds, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantReason, candidate.Reason)
			if tt.wantReason != "" {
				return
			}
			assert.Equal(t, tt.wantURL, candidate.Endpoint.URL)
			assert.Equal(t, "happy-pod", candidate.Endpoint.PodName)
			assert.Equal(t, "default", candidate.Endpoint.PodNamespace)
			assert.Equal(t, creds.DataplaneUsername, candidate.Endpoint.Username)
			assert.Equal(t, creds.DataplanePassword, candidate.Endpoint.Password)
		})
	}

	t.Run("terminating pod is skipped", func(t *testing.T) {
		d := createTestDiscovery(5555)
		pod := buildPod("terminating", running, "10.0.0.5", "Running")
		pod.SetDeletionTimestamp(&deletionTime)

		_, ok, err := d.evaluatePod(pod, creds, nil)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

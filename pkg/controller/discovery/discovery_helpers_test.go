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

// buildContainer builds a container spec map with the given name and ports.
func buildContainer(name string, ports ...int) map[string]any {
	portList := make([]any, 0, len(ports))
	for _, p := range ports {
		portList = append(portList, map[string]any{"containerPort": int64(p)})
	}
	c := map[string]any{"name": name}
	if len(portList) > 0 {
		c["ports"] = portList
	}
	return c
}

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

// buildPod builds an *unstructured.Unstructured pod with optional spec
// containers, status containerStatuses, status.podIP, and status.phase.
func buildPod(name string, spec, statuses []map[string]any, podIP, phase string) *unstructured.Unstructured {
	specContainers := make([]any, 0, len(spec))
	for _, c := range spec {
		specContainers = append(specContainers, c)
	}
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
			"spec": map[string]any{
				"containers": specContainers,
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

func TestContainerHasPort(t *testing.T) {
	tests := []struct {
		name      string
		container map[string]any
		port      int
		want      bool
	}{
		{
			name:      "matches first port",
			container: buildContainer("haproxy", 5555, 8080),
			port:      5555,
			want:      true,
		},
		{
			name:      "matches second port",
			container: buildContainer("haproxy", 8080, 5555),
			port:      5555,
			want:      true,
		},
		{
			name:      "no matching port",
			container: buildContainer("haproxy", 8080, 9090),
			port:      5555,
			want:      false,
		},
		{
			name:      "no ports field",
			container: map[string]any{"name": "sidecar"},
			port:      5555,
			want:      false,
		},
		{
			name: "ports entry is not a map",
			container: map[string]any{
				"name":  "broken",
				"ports": []any{"not-a-map"},
			},
			port: 5555,
			want: false,
		},
		{
			name: "containerPort missing on entry",
			container: map[string]any{
				"name":  "missing-cp",
				"ports": []any{map[string]any{"protocol": "TCP"}},
			},
			port: 5555,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containerHasPort(tt.container, tt.port)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDiscovery_FindDataplaneContainerName(t *testing.T) {
	tests := []struct {
		name      string
		spec      []map[string]any
		port      int
		want      string
		wantError bool
	}{
		{
			name: "single container with matching port",
			spec: []map[string]any{buildContainer("haproxy", 5555)},
			port: 5555,
			want: "haproxy",
		},
		{
			name: "second container has the port",
			spec: []map[string]any{
				buildContainer("sidecar", 9090),
				buildContainer("haproxy", 5555),
			},
			port: 5555,
			want: "haproxy",
		},
		{
			name:      "no container matches",
			spec:      []map[string]any{buildContainer("sidecar", 9090)},
			port:      5555,
			wantError: true,
		},
		{
			name: "container without name is skipped",
			spec: []map[string]any{
				{"ports": []any{map[string]any{"containerPort": int64(5555)}}},
				buildContainer("haproxy", 5555),
			},
			port: 5555,
			want: "haproxy",
		},
		{
			name:      "no containers at all",
			spec:      []map[string]any{},
			port:      5555,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := createTestDiscovery(tt.port)
			pod := buildPod("test-pod", tt.spec, nil, "", "")

			got, err := d.findDataplaneContainerName(pod)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractPodIP(t *testing.T) {
	tests := []struct {
		name    string
		podIP   string
		wantIP  string
		wantErr bool
	}{
		{name: "valid IP", podIP: "10.1.2.3", wantIP: "10.1.2.3"},
		{name: "missing IP", podIP: "", wantIP: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := buildPod("test-pod", nil, nil, tt.podIP, "")

			got, err := extractPodIP(pod, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
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
			pod := buildPod("test-pod", nil, nil, "", tt.phase)

			got, err := extractPodPhase(pod, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPhase, got)
		})
	}
}

func TestCheckContainerReady(t *testing.T) {
	tests := []struct {
		name          string
		statuses      []map[string]any
		containerName string
		wantReady     bool
		wantErr       bool
	}{
		{
			name:          "container ready",
			statuses:      []map[string]any{buildContainerStatus("haproxy", true, "running")},
			containerName: "haproxy",
			wantReady:     true,
		},
		{
			name:          "container not ready",
			statuses:      []map[string]any{buildContainerStatus("haproxy", false, "waiting")},
			containerName: "haproxy",
			wantReady:     false,
		},
		{
			name: "different container is ready",
			statuses: []map[string]any{
				buildContainerStatus("sidecar", true, "running"),
				buildContainerStatus("haproxy", false, "waiting"),
			},
			containerName: "haproxy",
			wantReady:     false,
		},
		{
			name:          "container not found in statuses",
			statuses:      []map[string]any{buildContainerStatus("sidecar", true, "running")},
			containerName: "haproxy",
			wantReady:     false,
		},
		{
			name:          "no container statuses at all",
			statuses:      nil,
			containerName: "haproxy",
			wantReady:     false,
		},
		{
			name: "ready field missing",
			statuses: []map[string]any{
				{"name": "haproxy"},
			},
			containerName: "haproxy",
			wantReady:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := buildPod("test-pod", nil, tt.statuses, "", "")

			ready, err := checkContainerReady(pod, tt.containerName, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, ready)
		})
	}
}

func TestDiscovery_IsDataplaneContainerReady(t *testing.T) {
	tests := []struct {
		name      string
		spec      []map[string]any
		statuses  []map[string]any
		port      int
		wantReady bool
		wantErr   bool
	}{
		{
			name:      "container exists and ready",
			spec:      []map[string]any{buildContainer("haproxy", 5555)},
			statuses:  []map[string]any{buildContainerStatus("haproxy", true, "running")},
			port:      5555,
			wantReady: true,
		},
		{
			name:      "container exists but not ready",
			spec:      []map[string]any{buildContainer("haproxy", 5555)},
			statuses:  []map[string]any{buildContainerStatus("haproxy", false, "waiting")},
			port:      5555,
			wantReady: false,
		},
		{
			name:     "no container with the port",
			spec:     []map[string]any{buildContainer("sidecar", 9090)},
			statuses: []map[string]any{buildContainerStatus("sidecar", true, "running")},
			port:     5555,
			wantErr:  true,
		},
		{
			name:      "container exists in spec but no matching status",
			spec:      []map[string]any{buildContainer("haproxy", 5555)},
			statuses:  []map[string]any{buildContainerStatus("sidecar", true, "running")},
			port:      5555,
			wantReady: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := createTestDiscovery(tt.port)
			pod := buildPod("test-pod", tt.spec, tt.statuses, "", "")

			ready, err := d.isDataplaneContainerReady(pod, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, ready)
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

	tests := []struct {
		name          string
		pod           *unstructured.Unstructured
		resource      any
		wantOK        bool
		wantURL       string
		wantPodName   string
		wantPodNS     string
		wantErrSubstr string
	}{
		{
			name:        "happy path - running pod with ready haproxy container",
			pod:         buildPod("happy-pod", []map[string]any{buildContainer("haproxy", 5555)}, []map[string]any{buildContainerStatus("haproxy", true, "running")}, "10.0.0.1", "Running"),
			wantOK:      true,
			wantURL:     "http://10.0.0.1:5555/v3",
			wantPodName: "happy-pod",
			wantPodNS:   "default",
		},
		{
			name:   "no IP assigned",
			pod:    buildPod("no-ip", []map[string]any{buildContainer("haproxy", 5555)}, []map[string]any{buildContainerStatus("haproxy", true, "running")}, "", "Running"),
			wantOK: false,
		},
		{
			name:   "not in running phase",
			pod:    buildPod("pending", []map[string]any{buildContainer("haproxy", 5555)}, []map[string]any{buildContainerStatus("haproxy", true, "running")}, "10.0.0.2", "Pending"),
			wantOK: false,
		},
		{
			name:   "container not ready",
			pod:    buildPod("not-ready", []map[string]any{buildContainer("haproxy", 5555)}, []map[string]any{buildContainerStatus("haproxy", false, "waiting")}, "10.0.0.3", "Running"),
			wantOK: false,
		},
		{
			name:          "no container with dataplane port",
			pod:           buildPod("wrong-port", []map[string]any{buildContainer("sidecar", 9090)}, []map[string]any{buildContainerStatus("sidecar", true, "running")}, "10.0.0.4", "Running"),
			wantOK:        false,
			wantErrSubstr: "checking dataplane container readiness",
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

			endpoint, ok, err := d.evaluatePod(resource, creds, nil)

			if tt.wantErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantURL, endpoint.URL)
				assert.Equal(t, tt.wantPodName, endpoint.PodName)
				assert.Equal(t, tt.wantPodNS, endpoint.PodNamespace)
				assert.Equal(t, creds.DataplaneUsername, endpoint.Username)
				assert.Equal(t, creds.DataplanePassword, endpoint.Password)
			}
		})
	}

	t.Run("terminating pod is skipped", func(t *testing.T) {
		d := createTestDiscovery(5555)
		pod := buildPod("terminating",
			[]map[string]any{buildContainer("haproxy", 5555)},
			[]map[string]any{buildContainerStatus("haproxy", true, "running")},
			"10.0.0.5", "Running")
		pod.SetDeletionTimestamp(&deletionTime)

		_, ok, err := d.evaluatePod(pod, creds, nil)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

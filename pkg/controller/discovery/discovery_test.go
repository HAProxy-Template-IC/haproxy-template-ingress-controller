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
	k8stypes "k8s.io/apimachinery/pkg/types"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

// createTestDiscovery creates a Discovery instance for testing.
func createTestDiscovery(dataplanePort int) *Discovery {
	return &Discovery{dataplanePort: dataplanePort}
}

// admittable projects the candidates that are worth probing onto their
// endpoints, which is what the discovery assertions are about.
func admittable(candidates []Candidate) []dataplane.Endpoint {
	endpoints := make([]dataplane.Endpoint, 0, len(candidates))
	for i := range candidates {
		if candidates[i].Reason == "" {
			endpoints = append(endpoints, candidates[i].Endpoint)
		}
	}
	return endpoints
}

func TestDiscovery_DiscoverEndpoints_Success(t *testing.T) {
	tests := []struct {
		name              string
		pods              []*unstructured.Unstructured
		dataplanePort     int
		credentials       coreconfig.Credentials
		expectedEndpoints []dataplane.Endpoint
	}{
		{
			name: "single pod with IP",
			pods: []*unstructured.Unstructured{
				createPod("haproxy-0", "10.0.0.1"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "multiple pods with IPs",
			pods: []*unstructured.Unstructured{
				createPod("haproxy-0", "10.0.0.1"),
				createPod("haproxy-1", "10.0.0.2"),
				createPod("haproxy-2", "10.0.0.3"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.2:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-1",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-2",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "custom dataplane port",
			pods: []*unstructured.Unstructured{
				createPodWithPortAndPhase("haproxy-0", "10.0.0.1", "Running", 8080),
			},
			dataplanePort: 8080,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:8080",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
			},
		},
		{
			name:          "no pods",
			pods:          []*unstructured.Unstructured{},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{},
		},
		{
			name: "pod without IP is skipped",
			pods: []*unstructured.Unstructured{
				createPod("haproxy-0", "10.0.0.1"),
				createPodWithoutIP("haproxy-1"),
				createPod("haproxy-2", "10.0.0.3"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-2",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "pods in Pending phase are skipped",
			pods: []*unstructured.Unstructured{
				createPodWithPhase("haproxy-0", "10.0.0.1", "Running"),
				createPodWithPhase("haproxy-1", "10.0.0.2", "Pending"),
				createPodWithPhase("haproxy-2", "10.0.0.3", "Running"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-2",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "pods in Failed phase are skipped",
			pods: []*unstructured.Unstructured{
				createPodWithPhase("haproxy-0", "10.0.0.1", "Running"),
				createPodWithPhase("haproxy-1", "10.0.0.2", "Failed"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "only Running pods included in mixed scenario",
			pods: []*unstructured.Unstructured{
				createPodWithPhase("haproxy-0", "10.0.0.1", "Pending"),
				createPodWithPhase("haproxy-1", "10.0.0.2", "Running"),
				createPodWithPhase("haproxy-2", "10.0.0.3", "Failed"),
				createPodWithPhase("haproxy-3", "10.0.0.4", "Running"),
				createPodWithPhase("haproxy-4", "10.0.0.5", "Succeeded"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.2:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-1",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.4:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-3",
					PodNamespace: "default",
				},
			},
		},
		{
			name: "terminating pods are skipped",
			pods: []*unstructured.Unstructured{
				createPodWithPhase("haproxy-0", "10.0.0.1", "Running"),
				createTerminatingPod("haproxy-1", "10.0.0.2"),
				createPodWithPhase("haproxy-2", "10.0.0.3", "Running"),
			},
			dataplanePort: 5555,
			credentials: coreconfig.Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "secret",
			},
			expectedEndpoints: []dataplane.Endpoint{
				{
					URL:          "http://10.0.0.1:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-2",
					PodNamespace: "default",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create store and populate with pods
			podStore := store.NewMemoryStore(2)
			for _, pod := range tt.pods {
				keys := []string{pod.GetNamespace(), pod.GetName()}
				err := podStore.Add(pod, keys)
				require.NoError(t, err)
			}

			// Create discovery instance (using test helper that doesn't require haproxy)
			discovery := createTestDiscovery(tt.dataplanePort)

			candidates, err := discovery.DiscoverEndpoints(podStore, tt.credentials)
			endpoints := admittable(candidates)

			// Verify
			require.NoError(t, err)
			assert.Len(t, endpoints, len(tt.expectedEndpoints))

			// Convert to maps for easier comparison (order doesn't matter)
			expectedMap := make(map[string]dataplane.Endpoint)
			for _, ep := range tt.expectedEndpoints {
				expectedMap[ep.URL] = ep
			}

			actualMap := make(map[string]dataplane.Endpoint)
			for _, ep := range endpoints {
				actualMap[ep.URL] = ep
			}

			assert.Equal(t, expectedMap, actualMap)
		})
	}
}

func TestDiscovery_DiscoverEndpoints_NilStore(t *testing.T) {
	discovery := createTestDiscovery(5555)
	credentials := coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	candidates, err := discovery.DiscoverEndpoints(nil, credentials)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pod store is nil")
	assert.Nil(t, candidates)
}

func TestDiscovery_DiscoverEndpoints_StoreListError(t *testing.T) {
	// Create a mock store that returns an error
	mockStore := &mockStore{
		listErr: assert.AnError,
	}

	discovery := createTestDiscovery(5555)
	credentials := coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	candidates, err := discovery.DiscoverEndpoints(mockStore, credentials)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing pods")
	assert.Nil(t, candidates)
}

// This is the actual format stored in production after float-to-int conversion.
func TestDiscovery_DiscoverEndpoints_MapResources(t *testing.T) {
	// Create pod as map[string]any (production format after conversion)
	pod := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "haproxy-0",
			"namespace": "default",
		},
		"status": map[string]any{
			"podIP": "10.0.0.1",
			"phase": "Running",
			"containerStatuses": []any{
				map[string]any{
					"name":  agentContainerName,
					"state": map[string]any{"running": map[string]any{}},
				},
			},
		},
	}

	// Create store and add pod as map (not as *unstructured.Unstructured)
	podStore := store.NewMemoryStore(2)
	err := podStore.Add(pod, []string{"default", "haproxy-0"})
	require.NoError(t, err)

	discovery := createTestDiscovery(5555)
	credentials := coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	candidates, err := discovery.DiscoverEndpoints(podStore, credentials)
	endpoints := admittable(candidates)

	// Verify
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, "http://10.0.0.1:5555", endpoints[0].URL)
	assert.Equal(t, "admin", endpoints[0].Username)
	assert.Equal(t, "secret", endpoints[0].Password)
	assert.Equal(t, "haproxy-0", endpoints[0].PodName)
	assert.Equal(t, "default", endpoints[0].PodNamespace)
}

func TestDiscovery_DiscoverEndpoints_IncludesPodUID(t *testing.T) {
	pod := createPod("haproxy-0", "10.0.0.1")
	pod.SetUID(k8stypes.UID("pod-uid-1"))
	podStore := store.NewMemoryStore(2)
	require.NoError(t, podStore.Add(pod, []string{"default", "haproxy-0"}))

	candidates, err := createTestDiscovery(5555).DiscoverEndpoints(podStore, coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	})
	endpoints := admittable(candidates)

	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, "pod-uid-1", endpoints[0].PodUID)
}

func TestDiscovery_DiscoverEndpoints_IdentifiesContainerRuntimeEpoch(t *testing.T) {
	pod := createPod("haproxy-0", "10.0.0.1")
	setStatuses := func(haproxyImage, haproxyContainer string) {
		require.NoError(t, unstructured.SetNestedSlice(pod.Object, []any{
			map[string]any{
				"name": agentContainerName, "state": map[string]any{"running": map[string]any{}},
				"imageID": "sha256:agent", "containerID": "containerd://agent-1",
			},
			map[string]any{
				"name": "haproxy", "ready": true,
				"imageID": haproxyImage, "containerID": "containerd://" + haproxyContainer,
			},
		}, "status", "containerStatuses"))
	}
	setStatuses("sha256:haproxy-old", "haproxy-1")
	discovery := createTestDiscovery(5555)
	credentials := coreconfig.Credentials{}

	old, evaluated, err := discovery.evaluatePod(pod, credentials, nil)
	require.NoError(t, err)
	require.True(t, evaluated)
	require.NotEmpty(t, old.Endpoint.PodRuntimeID)

	setStatuses("sha256:haproxy-old", "haproxy-2")
	restarted, evaluated, err := discovery.evaluatePod(pod, credentials, nil)
	require.NoError(t, err)
	require.True(t, evaluated)
	assert.NotEqual(t, old.Endpoint.PodRuntimeID, restarted.Endpoint.PodRuntimeID)

	setStatuses("sha256:haproxy-new", "haproxy-3")
	updated, evaluated, err := discovery.evaluatePod(pod, credentials, nil)
	require.NoError(t, err)
	require.True(t, evaluated)
	assert.NotEqual(t, restarted.Endpoint.PodRuntimeID, updated.Endpoint.PodRuntimeID)
}

func TestDiscovery_DiscoverEndpoints_FormatsIPv6URL(t *testing.T) {
	podStore := store.NewMemoryStore(2)
	require.NoError(t, podStore.Add(createPod("haproxy-0", "fd00::1"), []string{"default", "haproxy-0"}))

	candidates, err := createTestDiscovery(5555).DiscoverEndpoints(podStore, coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	})
	endpoints := admittable(candidates)

	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, "http://[fd00::1]:5555", endpoints[0].URL)
}

// createPod creates a test pod with the specified name and IP in the default namespace.
// The pod is created with phase "Running" by default.
func createPod(name, podIP string) *unstructured.Unstructured {
	return createPodWithPhase(name, podIP, "Running")
}

// createPodWithPhase creates a test pod with the specified name, IP, and phase in the default namespace.
func createPodWithPhase(name, podIP, phase string) *unstructured.Unstructured {
	return createPodWithPortAndPhase(name, podIP, phase, 5555)
}

// createPodWithPortAndPhase creates a test pod with the specified name, IP and phase in the default namespace.
func createPodWithPortAndPhase(name, podIP, phase string, _ int) *unstructured.Unstructured {
	pod := &unstructured.Unstructured{}
	pod.SetAPIVersion("v1")
	pod.SetKind("Pod")
	pod.SetName(name)
	pod.SetNamespace("default")
	pod.SetLabels(map[string]string{
		"app":       "haproxy",
		"component": "loadbalancer",
	})

	_ = unstructured.SetNestedField(pod.Object, podIP, "status", "podIP")

	_ = unstructured.SetNestedField(pod.Object, phase, "status", "phase")

	// The agent container is running; its ready flag is deliberately unset,
	// because admission must not depend on it.
	containerStatuses := []any{
		map[string]any{
			"name":  "haproxy",
			"ready": false,
		},
		map[string]any{
			"name":  agentContainerName,
			"state": map[string]any{"running": map[string]any{}},
		},
	}
	_ = unstructured.SetNestedSlice(pod.Object, containerStatuses, "status", "containerStatuses")

	return pod
}

// createPodWithoutIP creates a test pod without an IP in the default namespace (e.g., pending pod).
func createPodWithoutIP(name string) *unstructured.Unstructured {
	pod := &unstructured.Unstructured{}
	pod.SetAPIVersion("v1")
	pod.SetKind("Pod")
	pod.SetName(name)
	pod.SetNamespace("default")
	pod.SetLabels(map[string]string{
		"app":       "haproxy",
		"component": "loadbalancer",
	})

	// No pod IP set (simulates pending pod)

	return pod
}

// createTerminatingPod creates a test pod with deletionTimestamp set (terminating pod).
// Terminating pods may still have phase="Running" and ready=true during graceful shutdown.
func createTerminatingPod(name, podIP string) *unstructured.Unstructured {
	pod := createPodWithPhase(name, podIP, "Running")

	// Set deletionTimestamp to indicate pod is terminating
	now := metav1.Time{Time: time.Now()}
	pod.SetDeletionTimestamp(&now)

	return pod
}

type mockStore struct {
	listErr error
}

func (m *mockStore) List() ([]any, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return []any{}, nil
}

func (m *mockStore) Get(keys ...string) ([]any, error) {
	return nil, nil
}

func (m *mockStore) Add(resource any, keys []string) error {
	return nil
}

func (m *mockStore) Update(resource any, keys []string) error {
	return nil
}

func (m *mockStore) Delete(_, _ string, _ []string) error {
	return nil
}

func (m *mockStore) Clear() error {
	return nil
}

func (m *mockStore) GetKeys(resource any, indexBy []string) ([]string, error) {
	return nil, nil
}

func (m *mockStore) Refresh(resource any, oldKeys, newKeys []string) (changed, deleted bool) {
	return false, false
}

func (m *mockStore) Count() int {
	return 0
}

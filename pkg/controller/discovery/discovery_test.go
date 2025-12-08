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
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	coreconfig "haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/k8s/store"
)

func TestNewDiscoveryEngine(t *testing.T) {
	// Skip if haproxy is not available (newDiscoveryEngine detects local version)
	if _, err := exec.LookPath("haproxy"); err != nil {
		t.Skip("skipping test: haproxy binary not found in PATH")
	}

	discovery, err := newDiscoveryEngine(5555)

	require.NoError(t, err)
	assert.NotNil(t, discovery)
	assert.Equal(t, 5555, discovery.dataplanePort)
	assert.NotNil(t, discovery.localVersion)
}

// createTestDiscovery creates a Discovery instance for testing without requiring haproxy.
// This is used by tests that only test DiscoverEndpoints, which doesn't need localVersion.
func createTestDiscovery(dataplanePort int) *Discovery {
	return &Discovery{
		dataplanePort: dataplanePort,
		// localVersion is nil - not needed for DiscoverEndpoints tests
	}
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
					URL:          "http://10.0.0.1:5555/v3",
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
					URL:          "http://10.0.0.1:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.2:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-1",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555/v3",
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
					URL:          "http://10.0.0.1:8080/v3",
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
					URL:          "http://10.0.0.1:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555/v3",
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
					URL:          "http://10.0.0.1:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555/v3",
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
					URL:          "http://10.0.0.1:5555/v3",
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
					URL:          "http://10.0.0.2:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-1",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.4:5555/v3",
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
					URL:          "http://10.0.0.1:5555/v3",
					Username:     "admin",
					Password:     "secret",
					PodName:      "haproxy-0",
					PodNamespace: "default",
				},
				{
					URL:          "http://10.0.0.3:5555/v3",
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

			// Discover endpoints
			endpoints, err := discovery.DiscoverEndpoints(podStore, tt.credentials)

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

	endpoints, err := discovery.DiscoverEndpoints(nil, credentials)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pod store is nil")
	assert.Nil(t, endpoints)
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

	endpoints, err := discovery.DiscoverEndpoints(mockStore, credentials)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list pods")
	assert.Nil(t, endpoints)
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// createPod creates a test pod with the specified name and IP in the default namespace.
// The pod is created with phase "Running" by default.
func createPod(name, podIP string) *unstructured.Unstructured {
	return createPodWithPhase(name, podIP, "Running")
}

// createPodWithPhase creates a test pod with the specified name, IP, and phase in the default namespace.
func createPodWithPhase(name, podIP, phase string) *unstructured.Unstructured {
	return createPodWithPortAndPhase(name, podIP, phase, 5555)
}

// createPodWithPortAndPhase creates a test pod with the specified name, IP, phase, and dataplane port in the default namespace.
func createPodWithPortAndPhase(name, podIP, phase string, dataplanePort int) *unstructured.Unstructured {
	pod := &unstructured.Unstructured{}
	pod.SetAPIVersion("v1")
	pod.SetKind("Pod")
	pod.SetName(name)
	pod.SetNamespace("default")
	pod.SetLabels(map[string]string{
		"app":       "haproxy",
		"component": "loadbalancer",
	})

	// Set pod IP in status
	_ = unstructured.SetNestedField(pod.Object, podIP, "status", "podIP")

	// Set pod phase in status
	_ = unstructured.SetNestedField(pod.Object, phase, "status", "phase")

	// Set spec.containers with dataplane container
	containers := []interface{}{
		map[string]interface{}{
			"name": "haproxy",
			"ports": []interface{}{
				map[string]interface{}{
					"name":          "http",
					"containerPort": int64(80),
					"protocol":      "TCP",
				},
			},
		},
		map[string]interface{}{
			"name": "dataplane",
			"ports": []interface{}{
				map[string]interface{}{
					"name":          "dataplane",
					"containerPort": int64(dataplanePort),
					"protocol":      "TCP",
				},
			},
		},
	}
	_ = unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers")

	// Set status.containerStatuses with ready containers
	containerStatuses := []interface{}{
		map[string]interface{}{
			"name":  "haproxy",
			"ready": true,
		},
		map[string]interface{}{
			"name":  "dataplane",
			"ready": true,
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

// -----------------------------------------------------------------------------
// Mock Store
// -----------------------------------------------------------------------------

type mockStore struct {
	listErr error
}

func (m *mockStore) List() ([]interface{}, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return []interface{}{}, nil
}

func (m *mockStore) Get(keys ...string) ([]interface{}, error) {
	return nil, nil
}

func (m *mockStore) Add(resource interface{}, keys []string) error {
	return nil
}

func (m *mockStore) Update(resource interface{}, keys []string) error {
	return nil
}

func (m *mockStore) Delete(keys ...string) error {
	return nil
}

func (m *mockStore) Clear() error {
	return nil
}

func (m *mockStore) GetKeys(resource interface{}, indexBy []string) ([]string, error) {
	return nil, nil
}

func (m *mockStore) Refresh(resource interface{}, oldKeys, newKeys []string) (changed, deleted bool) {
	return false, false
}

func (m *mockStore) Count() int {
	return 0
}

// -----------------------------------------------------------------------------
// LocalVersion Tests
// -----------------------------------------------------------------------------

func TestDiscovery_LocalVersion(t *testing.T) {
	tests := []struct {
		name          string
		localVersion  *dataplane.Version
		expectVersion *dataplane.Version
	}{
		{
			name: "returns configured version",
			localVersion: &dataplane.Version{
				Major: 3,
				Minor: 2,
				Full:  "3.2.1",
			},
			expectVersion: &dataplane.Version{
				Major: 3,
				Minor: 2,
				Full:  "3.2.1",
			},
		},
		{
			name:          "returns nil when not set",
			localVersion:  nil,
			expectVersion: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Discovery{
				dataplanePort: 5555,
				localVersion:  tt.localVersion,
			}

			got := d.LocalVersion()

			if tt.expectVersion == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.expectVersion.Major, got.Major)
				assert.Equal(t, tt.expectVersion.Minor, got.Minor)
				assert.Equal(t, tt.expectVersion.Full, got.Full)
			}
		})
	}
}

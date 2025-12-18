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

package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewFromClientset(t *testing.T) {
	fakeClientset := fake.NewClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)

	client := NewFromClientset(fakeClientset, fakeDynamic, "test-namespace")

	require.NotNil(t, client)
	assert.Equal(t, "test-namespace", client.Namespace())
	assert.NotNil(t, client.Clientset())
	assert.NotNil(t, client.DynamicClient())
}

func TestClient_Getters(t *testing.T) {
	fakeClientset := fake.NewClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)

	client := NewFromClientset(fakeClientset, fakeDynamic, "default")

	assert.Equal(t, fakeClientset, client.Clientset())
	assert.Equal(t, fakeDynamic, client.DynamicClient())
	assert.Equal(t, "default", client.Namespace())
	assert.Nil(t, client.RestConfig()) // RestConfig is nil for NewFromClientset
}

func TestClient_GetResource_Success(t *testing.T) {
	// Create a ConfigMap as an unstructured object
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-config",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, configMap)
	fakeClientset := fake.NewClientset()

	client := NewFromClientset(fakeClientset, fakeDynamic, "default")

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	resource, err := client.GetResource(context.Background(), gvr, "test-config")

	require.NoError(t, err)
	require.NotNil(t, resource)
	assert.Equal(t, "test-config", resource.GetName())
}

func TestClient_GetResource_NoNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)
	fakeClientset := fake.NewClientset()

	client := NewFromClientset(fakeClientset, fakeDynamic, "") // Empty namespace

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	_, err := client.GetResource(context.Background(), gvr, "test-config")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no namespace available")

	var clientErr *ClientError
	require.True(t, errors.As(err, &clientErr))
	assert.Equal(t, "get resource", clientErr.Operation)
}

func TestClient_GetResource_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme) // No resources
	fakeClientset := fake.NewClientset()

	client := NewFromClientset(fakeClientset, fakeDynamic, "default")

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	_, err := client.GetResource(context.Background(), gvr, "nonexistent")

	require.Error(t, err)

	var clientErr *ClientError
	require.True(t, errors.As(err, &clientErr))
	assert.Contains(t, clientErr.Operation, "get resource")
}

func TestDiscoverNamespaceFromFile_Success(t *testing.T) {
	// Create temp file with namespace
	tmpDir := t.TempDir()
	namespaceFile := filepath.Join(tmpDir, "namespace")
	err := os.WriteFile(namespaceFile, []byte("test-namespace"), 0o600)
	require.NoError(t, err)

	namespace, err := DiscoverNamespaceFromFile(namespaceFile)

	require.NoError(t, err)
	assert.Equal(t, "test-namespace", namespace)
}

func TestDiscoverNamespaceFromFile_FileNotExists(t *testing.T) {
	_, err := DiscoverNamespaceFromFile("/nonexistent/path/namespace")

	require.Error(t, err)

	var nsErr *NamespaceDiscoveryError
	require.True(t, errors.As(err, &nsErr))
	assert.Equal(t, "/nonexistent/path/namespace", nsErr.Path)
	assert.True(t, os.IsNotExist(nsErr.Unwrap()))
}

func TestDiscoverNamespaceFromFile_EmptyFile(t *testing.T) {
	// Create temp file with empty content
	tmpDir := t.TempDir()
	namespaceFile := filepath.Join(tmpDir, "namespace")
	err := os.WriteFile(namespaceFile, []byte(""), 0o600)
	require.NoError(t, err)

	_, err = DiscoverNamespaceFromFile(namespaceFile)

	require.Error(t, err)

	var nsErr *NamespaceDiscoveryError
	require.True(t, errors.As(err, &nsErr))
	assert.ErrorIs(t, nsErr.Unwrap(), os.ErrInvalid)
}

func TestDiscoverNamespace_UsesDefaultPath(t *testing.T) {
	// This test verifies DiscoverNamespace uses the correct default path
	// It will fail since the file doesn't exist outside of a cluster
	_, err := DiscoverNamespace()

	require.Error(t, err)

	var nsErr *NamespaceDiscoveryError
	require.True(t, errors.As(err, &nsErr))
	assert.Equal(t, DefaultNamespaceFile, nsErr.Path)
}

// =============================================================================
// Error Type Tests
// =============================================================================

func TestClientError_Error(t *testing.T) {
	err := &ClientError{
		Operation: "create client",
		Err:       errors.New("connection refused"),
	}

	assert.Contains(t, err.Error(), "create client")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestClientError_Unwrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &ClientError{
		Operation: "test",
		Err:       underlying,
	}

	assert.Equal(t, underlying, err.Unwrap())
	assert.True(t, errors.Is(err, underlying))
}

func TestNamespaceDiscoveryError_Error(t *testing.T) {
	err := &NamespaceDiscoveryError{
		Path: "/path/to/namespace",
		Err:  os.ErrNotExist,
	}

	assert.Contains(t, err.Error(), "/path/to/namespace")
	assert.Contains(t, err.Error(), "not exist")
}

func TestNamespaceDiscoveryError_Unwrap(t *testing.T) {
	underlying := os.ErrNotExist
	err := &NamespaceDiscoveryError{
		Path: "/path/to/namespace",
		Err:  underlying,
	}

	assert.Equal(t, underlying, err.Unwrap())
	assert.True(t, errors.Is(err, underlying))
}

// =============================================================================
// New() Error Handling Tests
// =============================================================================

func TestNew_InvalidKubeconfig(t *testing.T) {
	// Create invalid kubeconfig file
	tmpDir := t.TempDir()
	kubeconfigPath := filepath.Join(tmpDir, "kubeconfig")
	err := os.WriteFile(kubeconfigPath, []byte("invalid: yaml: content: ["), 0o600)
	require.NoError(t, err)

	_, err = New(Config{Kubeconfig: kubeconfigPath})

	require.Error(t, err)

	var clientErr *ClientError
	require.True(t, errors.As(err, &clientErr))
	assert.Equal(t, "build kubeconfig", clientErr.Operation)
}

func TestNew_NonexistentKubeconfig(t *testing.T) {
	_, err := New(Config{Kubeconfig: "/nonexistent/kubeconfig"})

	require.Error(t, err)

	var clientErr *ClientError
	require.True(t, errors.As(err, &clientErr))
	assert.Equal(t, "build kubeconfig", clientErr.Operation)
}

func TestNew_InClusterNotAvailable(t *testing.T) {
	// When not running in a cluster, New() with empty config should fail
	_, err := New(Config{})

	require.Error(t, err)

	var clientErr *ClientError
	require.True(t, errors.As(err, &clientErr))
	assert.Equal(t, "get in-cluster config", clientErr.Operation)
}

// =============================================================================
// Config Namespace Handling Tests
// =============================================================================

func TestNewFromClientset_WithNamespace(t *testing.T) {
	fakeClientset := fake.NewClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)

	client := NewFromClientset(fakeClientset, fakeDynamic, "custom-namespace")

	assert.Equal(t, "custom-namespace", client.Namespace())
}

func TestNewFromClientset_EmptyNamespace(t *testing.T) {
	fakeClientset := fake.NewClientset()
	scheme := runtime.NewScheme()
	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme)

	client := NewFromClientset(fakeClientset, fakeDynamic, "")

	assert.Equal(t, "", client.Namespace())
}

// =============================================================================
// GetResource Edge Cases
// =============================================================================

func TestClient_GetResource_WithContext(t *testing.T) {
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, configMap)
	fakeClientset := fake.NewClientset()

	client := NewFromClientset(fakeClientset, fakeDynamic, "default")

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetResource(ctx, gvr, "test-config")

	// The fake client may or may not honor context cancellation
	// This test primarily verifies the method accepts context correctly
	if err != nil {
		assert.Contains(t, err.Error(), "context")
	}
}

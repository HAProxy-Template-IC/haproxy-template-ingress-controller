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

package configpublisher

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/dataplane/auxiliaryfiles"
	"haptic/pkg/generated/clientset/versioned/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// testLogger creates a slog logger for tests that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPublishConfig_CreateNew tests publishing a new runtime config with auxiliary files.
func TestPublishConfig_CreateNew(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{
					Path:    "/etc/haproxy/maps/host.map",
					Content: "example.com backend1\n",
				},
			},
			SSLCertificates: []auxiliaryfiles.SSLCertificate{
				{
					Path:    "/etc/haproxy/ssl/cert.pem",
					Content: "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n",
				},
			},
		},
	}

	result, err := publisher.PublishConfig(ctx, &req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-config-haproxycfg", result.RuntimeConfigName)
	assert.Equal(t, "default", result.RuntimeConfigNamespace)
	assert.Len(t, result.MapFileNames, 1)
	assert.Len(t, result.SecretNames, 1)

	// Verify HAProxyCfg was created
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", runtimeConfig.Spec.Path)
	assert.Equal(t, "global\n  daemon\n", runtimeConfig.Spec.Content)
	assert.Equal(t, "abc123", runtimeConfig.Spec.Checksum)

	// Verify owner reference
	require.Len(t, runtimeConfig.OwnerReferences, 1)
	assert.Equal(t, "HAProxyTemplateConfig", runtimeConfig.OwnerReferences[0].Kind)
	assert.Equal(t, "test-config", runtimeConfig.OwnerReferences[0].Name)
	assert.Equal(t, types.UID("test-uid-123"), runtimeConfig.OwnerReferences[0].UID)

	// Verify map file was created
	mapFiles, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyMapFiles("default").
		List(ctx, metav1.ListOptions{})

	require.NoError(t, err)
	require.Len(t, mapFiles.Items, 1)
	assert.Equal(t, "/etc/haproxy/maps/host.map", mapFiles.Items[0].Spec.Path)
	assert.Equal(t, "example.com backend1\n", mapFiles.Items[0].Spec.Entries)

	// Verify SSL secret was created
	secrets, err := k8sClient.CoreV1().
		Secrets("default").
		List(ctx, metav1.ListOptions{})

	require.NoError(t, err)
	require.Len(t, secrets.Items, 1)
	assert.Contains(t, secrets.Items[0].Data, "certificate")
	assert.Contains(t, secrets.Items[0].Data, "path")
	assert.Equal(t, []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"),
		secrets.Items[0].Data["certificate"])
	assert.Equal(t, []byte("/etc/haproxy/ssl/cert.pem"),
		secrets.Items[0].Data["path"])
}

// TestPublishConfig_Update tests updating an existing runtime config.
func TestPublishConfig_Update(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create initial runtime config
	initialReq := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &initialReq)
	require.NoError(t, err)

	// Update with new config
	updatedReq := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n  maxconn 1000\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "def456",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	result, err := publisher.PublishConfig(ctx, &updatedReq)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify config was updated
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Equal(t, "global\n  daemon\n  maxconn 1000\n", runtimeConfig.Spec.Content)
	assert.Equal(t, "def456", runtimeConfig.Spec.Checksum)
}

// TestUpdateDeploymentStatus_AddPod tests adding a pod to deployment status.
func TestUpdateDeploymentStatus_AddPod(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config first
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Update deployment status
	deployedAt := time.Now()
	update := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             deployedAt,
		Checksum:               "abc123",
	}

	err = publisher.UpdateDeploymentStatus(ctx, &update)

	require.NoError(t, err)

	// Verify deployment status was updated
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-0", runtimeConfig.Status.DeployedToPods[0].PodName)
	assert.Equal(t, "abc123", runtimeConfig.Status.DeployedToPods[0].Checksum)
}

// TestUpdateDeploymentStatus_UpdateExistingPod tests updating existing pod status.
func TestUpdateDeploymentStatus_UpdateExistingPod(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add pod first time
	firstUpdate := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             time.Now(),
		Checksum:               "abc123",
	}

	err = publisher.UpdateDeploymentStatus(ctx, &firstUpdate)
	require.NoError(t, err)

	// Update same pod with new checksum
	time.Sleep(10 * time.Millisecond) // Ensure different timestamp
	secondUpdate := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             time.Now(),
		Checksum:               "def456",
	}

	err = publisher.UpdateDeploymentStatus(ctx, &secondUpdate)
	require.NoError(t, err)

	// Verify only one pod entry exists with updated checksum
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-0", runtimeConfig.Status.DeployedToPods[0].PodName)
	assert.Equal(t, "def456", runtimeConfig.Status.DeployedToPods[0].Checksum)
}

// TestUpdateDeploymentStatus_MultiplePods tests adding multiple pods.
func TestUpdateDeploymentStatus_MultiplePods(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add multiple pods
	pods := []string{"haproxy-0", "haproxy-1", "haproxy-2"}
	for _, podName := range pods {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
			DeployedAt:             time.Now(),
			Checksum:               "abc123",
		}

		err = publisher.UpdateDeploymentStatus(ctx, &update)
		require.NoError(t, err)
	}

	// Verify all pods were added
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 3)

	podNames := make([]string, 3)
	for i, pod := range runtimeConfig.Status.DeployedToPods {
		podNames[i] = pod.PodName
	}

	assert.Contains(t, podNames, "haproxy-0")
	assert.Contains(t, podNames, "haproxy-1")
	assert.Contains(t, podNames, "haproxy-2")
}

// TestCleanupPodReferences_RemovePod tests removing a pod from deployment status.
func TestCleanupPodReferences_RemovePod(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add two pods
	for _, podName := range []string{"haproxy-0", "haproxy-1"} {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
			DeployedAt:             time.Now(),
			Checksum:               "abc123",
		}

		err = publisher.UpdateDeploymentStatus(ctx, &update)
		require.NoError(t, err)
	}

	// Remove one pod
	cleanup := PodCleanupRequest{
		PodName: "haproxy-0",
	}

	err = publisher.CleanupPodReferences(ctx, &cleanup)
	require.NoError(t, err)

	// Verify only one pod remains
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-1", runtimeConfig.Status.DeployedToPods[0].PodName)
}

// TestCleanupPodReferences_NonexistentPod tests cleaning up a pod that doesn't exist.
func TestCleanupPodReferences_NonexistentPod(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Try to cleanup pod that was never added
	cleanup := PodCleanupRequest{
		PodName: "nonexistent-pod",
	}

	err = publisher.CleanupPodReferences(ctx, &cleanup)

	// Should not error - it's a no-op
	require.NoError(t, err)

	// Verify runtime config status unchanged
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Len(t, runtimeConfig.Status.DeployedToPods, 0)
}

// TestUpdateDeploymentStatus_RuntimeConfigNotFound tests updating when runtime config doesn't exist.
func TestUpdateDeploymentStatus_RuntimeConfigNotFound(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Try to update deployment status without creating runtime config first
	update := DeploymentStatusUpdate{
		RuntimeConfigName:      "nonexistent-runtime",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             time.Now(),
		Checksum:               "abc123",
	}

	err := publisher.UpdateDeploymentStatus(ctx, &update)

	// Should not error - gracefully handles missing runtime config
	require.NoError(t, err)
}

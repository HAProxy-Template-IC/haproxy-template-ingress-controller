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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"

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

// TestPublishConfig_GeneralFiles tests publishing a runtime config with general files.
func TestPublishConfig_GeneralFiles(t *testing.T) {
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
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{
					Filename: "503.http",
					Path:     "/etc/haproxy/general/503.http",
					Content:  "HTTP/1.0 503 Service Unavailable\r\nContent-Type: text/plain\r\n\r\nService Unavailable",
				},
			},
		},
	}

	result, err := publisher.PublishConfig(ctx, &req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	require.Len(t, result.GeneralFileNames, 1)

	// Verify HAProxyGeneralFile was created
	generalFiles, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyGeneralFiles("default").
		List(ctx, metav1.ListOptions{})

	require.NoError(t, err)
	require.Len(t, generalFiles.Items, 1)
	assert.Equal(t, "503.http", generalFiles.Items[0].Spec.FileName)
	assert.Equal(t, "/etc/haproxy/general/503.http", generalFiles.Items[0].Spec.Path)
	assert.Equal(t, "HTTP/1.0 503 Service Unavailable\r\nContent-Type: text/plain\r\n\r\nService Unavailable",
		generalFiles.Items[0].Spec.Content)
}

// TestPublishConfig_CRTListFiles tests publishing a runtime config with crt-list files.
func TestPublishConfig_CRTListFiles(t *testing.T) {
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
			CRTListFiles: []auxiliaryfiles.CRTListFile{
				{
					Path: "/etc/haproxy/ssl/crt-list.txt",
					Content: `/etc/haproxy/ssl/example.pem [verify none alpn h2,http/1.1] example.com
/etc/haproxy/ssl/wildcard.pem *.example.com`,
				},
			},
		},
	}

	result, err := publisher.PublishConfig(ctx, &req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	require.Len(t, result.CRTListFileNames, 1)

	// Verify HAProxyCRTListFile was created
	crtListFiles, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCRTListFiles("default").
		List(ctx, metav1.ListOptions{})

	require.NoError(t, err)
	require.Len(t, crtListFiles.Items, 1)
	assert.Equal(t, "/etc/haproxy/ssl/crt-list.txt", crtListFiles.Items[0].Spec.Path)
	assert.Contains(t, crtListFiles.Items[0].Spec.Entries, "example.com")
	assert.Contains(t, crtListFiles.Items[0].Spec.Entries, "wildcard.pem")
}

// TestPublishConfig_WithCompression tests that large content is compressed.
func TestPublishConfig_WithCompression(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create large content that will benefit from compression
	// Repeating patterns compress well
	largeContent := ""
	for i := 0; i < 1000; i++ {
		largeContent += "backend app_backend_" + string(rune('a'+i%26)) + "\n"
		largeContent += "  server server1 10.0.0.1:8080 check\n"
		largeContent += "  server server2 10.0.0.2:8080 check\n"
		largeContent += "\n"
	}

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  largeContent,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		CompressionThreshold:    1024, // 1KB threshold
	}

	result, err := publisher.PublishConfig(ctx, &req)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify HAProxyCfg was created with compression flag
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	// Content should be compressed (shorter than original)
	assert.True(t, runtimeConfig.Spec.Compressed, "Large config should be compressed")
	assert.Less(t, len(runtimeConfig.Spec.Content), len(largeContent),
		"Compressed content should be smaller than original")
}

// TestPublishConfig_CompressionDisabled tests that compression is disabled when threshold is <= 0.
func TestPublishConfig_CompressionDisabled(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create large content
	largeContent := ""
	for i := 0; i < 1000; i++ {
		largeContent += "backend app_backend_" + string(rune('a'+i%26)) + "\n"
		largeContent += "  server server1 10.0.0.1:8080 check\n"
	}

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  largeContent,
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		CompressionThreshold:    0, // Disabled
	}

	result, err := publisher.PublishConfig(ctx, &req)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify HAProxyCfg was created without compression
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.False(t, runtimeConfig.Spec.Compressed, "Compression should be disabled")
	assert.Equal(t, largeContent, runtimeConfig.Spec.Content, "Content should be unchanged")
}

// TestPublishConfig_SSLSecretCompressionAnnotation tests that SSL secrets use annotations for compression flag.
func TestPublishConfig_SSLSecretCompressionAnnotation(t *testing.T) {
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
		CompressionThreshold:    0, // Disabled - small content won't be compressed anyway
		AuxiliaryFiles: &AuxiliaryFiles{
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
	require.Len(t, result.SecretNames, 1)

	// Verify SSL secret uses annotation for compression flag
	secrets, err := k8sClient.CoreV1().
		Secrets("default").
		List(ctx, metav1.ListOptions{})

	require.NoError(t, err)
	require.Len(t, secrets.Items, 1)

	// Check annotation exists
	compressedAnnotation, ok := secrets.Items[0].Annotations["haproxy-haptic.org/compressed"]
	assert.True(t, ok, "compressed annotation should exist")
	assert.Equal(t, "false", compressedAnnotation, "compressed should be false for small content")

	// Verify 'compressed' is NOT in Data
	_, hasCompressedData := secrets.Items[0].Data["compressed"]
	assert.False(t, hasCompressedData, "compressed should NOT be in Secret data")
}

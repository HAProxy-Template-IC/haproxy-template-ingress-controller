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

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
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

	// Remove one pod (using namespace-scoped cleanup)
	cleanup := PodCleanupRequest{
		PodName:   "haproxy-0",
		Namespace: "default",
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

	// Try to cleanup pod that was never added (using namespace-scoped cleanup)
	cleanup := PodCleanupRequest{
		PodName:   "nonexistent-pod",
		Namespace: "default",
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

func TestReconcileDeployedToPods_RemovesStalePods(t *testing.T) {
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

	// Add three pods
	for _, podName := range []string{"haproxy-0", "haproxy-1", "haproxy-2"} {
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

	// Reconcile with only haproxy-1 as running (namespace-scoped)
	runningPods := []string{"haproxy-1"}
	err = publisher.ReconcileDeployedToPods(ctx, "default", runningPods)
	require.NoError(t, err)

	// Verify only haproxy-1 remains
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-1", runtimeConfig.Status.DeployedToPods[0].PodName)
}

func TestReconcileDeployedToPods_NoRunningPods(t *testing.T) {
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

	// Reconcile with no running pods (namespace-scoped)
	runningPods := []string{}
	err = publisher.ReconcileDeployedToPods(ctx, "default", runningPods)
	require.NoError(t, err)

	// Verify all pods are removed
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Empty(t, runtimeConfig.Status.DeployedToPods)
}

func TestReconcileDeployedToPods_NoStalePods(t *testing.T) {
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

	// Reconcile with all pods running (namespace-scoped)
	runningPods := []string{"haproxy-0", "haproxy-1"}
	err = publisher.ReconcileDeployedToPods(ctx, "default", runningPods)
	require.NoError(t, err)

	// Verify both pods remain
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 2)
}

func TestReconcileDeployedToPods_EmptyStatus(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config without adding any pods
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

	// Reconcile with some running pods (namespace-scoped)
	runningPods := []string{"haproxy-0", "haproxy-1"}
	err = publisher.ReconcileDeployedToPods(ctx, "default", runningPods)
	require.NoError(t, err)

	// Should not error - no-op
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Empty(t, runtimeConfig.Status.DeployedToPods)
}

func TestAddOrUpdatePodStatus_PreservesDeployedAt(t *testing.T) {
	existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))

	existing := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: existingTime,
			Checksum:   "old-checksum",
		},
	}

	// Update with zero DeployedAt (simulates drift check with no operations)
	newStatus := &haproxyv1alpha1.PodDeploymentStatus{
		PodName:    "haproxy-0",
		DeployedAt: metav1.Time{}, // Zero value
		Checksum:   "new-checksum",
	}

	result := addOrUpdatePodStatus(existing, newStatus)

	require.Len(t, result, 1)
	assert.Equal(t, "haproxy-0", result[0].PodName)
	assert.Equal(t, "new-checksum", result[0].Checksum)
	// DeployedAt should be preserved from existing
	assert.Equal(t, existingTime, result[0].DeployedAt, "existing deployedAt should be preserved")
}

func TestAddOrUpdatePodStatus_UpdatesExistingPod(t *testing.T) {
	existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	newTime := metav1.NewTime(time.Now())

	existing := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: existingTime,
			Checksum:   "old-checksum",
		},
	}

	// Update with valid DeployedAt
	newStatus := &haproxyv1alpha1.PodDeploymentStatus{
		PodName:    "haproxy-0",
		DeployedAt: newTime,
		Checksum:   "new-checksum",
	}

	result := addOrUpdatePodStatus(existing, newStatus)

	require.Len(t, result, 1, "should update existing, not append")
	assert.Equal(t, "new-checksum", result[0].Checksum)
	assert.Equal(t, newTime, result[0].DeployedAt)
}

func TestAddOrUpdatePodStatus_AppendsDifferentPod(t *testing.T) {
	existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	newTime := metav1.NewTime(time.Now())

	existing := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: existingTime,
			Checksum:   "checksum-0",
		},
	}

	// Add different pod
	newStatus := &haproxyv1alpha1.PodDeploymentStatus{
		PodName:    "haproxy-1",
		DeployedAt: newTime,
		Checksum:   "checksum-1",
	}

	result := addOrUpdatePodStatus(existing, newStatus)

	require.Len(t, result, 2, "should append new pod")
	assert.Equal(t, "haproxy-0", result[0].PodName)
	assert.Equal(t, "haproxy-1", result[1].PodName)
}

func TestCopyPodStatuses_ReturnsDeepCopy(t *testing.T) {
	original := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: metav1.NewTime(time.Now()),
			Checksum:   "checksum-0",
		},
	}

	copied := copyPodStatuses(original)

	// Modify original
	original[0].Checksum = "modified-checksum"

	// Copy should not be affected
	assert.Equal(t, "checksum-0", copied[0].Checksum, "copy should not be affected by original modification")
}

func TestCopyPodStatuses_NilInput(t *testing.T) {
	copied := copyPodStatuses(nil)
	assert.Nil(t, copied, "nil input should return nil")
}

func TestPodStatusesEqual_IdenticalStatuses(t *testing.T) {
	now := metav1.NewTime(time.Now())
	reloadAt := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			LastReloadAt: &reloadAt,
			LastReloadID: "reload-1",
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			LastReloadAt: &reloadAt,
			LastReloadID: "reload-1",
		},
	}

	assert.True(t, podStatusesEqual(a, b), "identical statuses should be equal")
}

func TestPodStatusesEqual_DifferentChecksum(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: now,
			Checksum:   "abc123",
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: now,
			Checksum:   "different",
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different checksums should not be equal")
}

func TestPodStatusesEqual_DifferentDeployedAt(t *testing.T) {
	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: metav1.NewTime(time.Now()),
			Checksum:   "abc123",
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
			Checksum:   "abc123",
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different deployment times should not be equal")
}

func TestPodStatusesEqual_DifferentLength(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{PodName: "haproxy-0", DeployedAt: now, Checksum: "abc123"},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{PodName: "haproxy-0", DeployedAt: now, Checksum: "abc123"},
		{PodName: "haproxy-1", DeployedAt: now, Checksum: "def456"},
	}

	assert.False(t, podStatusesEqual(a, b), "different lengths should not be equal")
}

func TestPodStatusesEqual_EmptySlices(t *testing.T) {
	a := []haproxyv1alpha1.PodDeploymentStatus{}
	b := []haproxyv1alpha1.PodDeploymentStatus{}

	assert.True(t, podStatusesEqual(a, b), "empty slices should be equal")
}

func TestPodStatusesEqual_LastReloadAtNilVsSet(t *testing.T) {
	now := metav1.NewTime(time.Now())
	reloadAt := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			LastReloadAt: nil, // No reload recorded
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			LastReloadAt: &reloadAt, // Reload recorded
		},
	}

	assert.False(t, podStatusesEqual(a, b), "nil vs set LastReloadAt should not be equal")
}

func TestPodStatusesEqual_DifferentSyncDuration(t *testing.T) {
	now := metav1.NewTime(time.Now())
	duration1 := metav1.Duration{Duration: 5 * time.Second}
	duration2 := metav1.Duration{Duration: 10 * time.Second}

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			SyncDuration: &duration1,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			SyncDuration: &duration2,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different SyncDuration should not be equal")
}

func TestPodStatusesEqual_SyncDurationNilVsSet(t *testing.T) {
	now := metav1.NewTime(time.Now())
	duration := metav1.Duration{Duration: 5 * time.Second}

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			SyncDuration: nil,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			SyncDuration: &duration,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "nil vs set SyncDuration should not be equal")
}

func TestPodStatusesEqual_DifferentVersionConflictRetries(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:                "haproxy-0",
			DeployedAt:             now,
			Checksum:               "abc123",
			VersionConflictRetries: 0,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:                "haproxy-0",
			DeployedAt:             now,
			Checksum:               "abc123",
			VersionConflictRetries: 3,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different VersionConflictRetries should not be equal")
}

func TestPodStatusesEqual_DifferentFallbackUsed(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			FallbackUsed: false,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:      "haproxy-0",
			DeployedAt:   now,
			Checksum:     "abc123",
			FallbackUsed: true,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different FallbackUsed should not be equal")
}

func TestPodStatusesEqual_DifferentConsecutiveErrors(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:           "haproxy-0",
			DeployedAt:        now,
			Checksum:          "abc123",
			ConsecutiveErrors: 0,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:           "haproxy-0",
			DeployedAt:        now,
			Checksum:          "abc123",
			ConsecutiveErrors: 5,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different ConsecutiveErrors should not be equal")
}

func TestPodStatusesEqual_DifferentLastErrorAt(t *testing.T) {
	now := metav1.NewTime(time.Now())
	errorAt1 := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	errorAt2 := metav1.NewTime(time.Now().Add(-2 * time.Hour))

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:     "haproxy-0",
			DeployedAt:  now,
			Checksum:    "abc123",
			LastErrorAt: &errorAt1,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:     "haproxy-0",
			DeployedAt:  now,
			Checksum:    "abc123",
			LastErrorAt: &errorAt2,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different LastErrorAt should not be equal")
}

func TestPodStatusesEqual_LastErrorAtNilVsSet(t *testing.T) {
	now := metav1.NewTime(time.Now())
	errorAt := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:     "haproxy-0",
			DeployedAt:  now,
			Checksum:    "abc123",
			LastErrorAt: nil,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:     "haproxy-0",
			DeployedAt:  now,
			Checksum:    "abc123",
			LastErrorAt: &errorAt,
		},
	}

	assert.False(t, podStatusesEqual(a, b), "nil vs set LastErrorAt should not be equal")
}

func TestPodStatusesEqual_DifferentLastOperationSummary(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: now,
			Checksum:   "abc123",
			LastOperationSummary: &haproxyv1alpha1.OperationSummary{
				TotalAPIOperations: 10,
				BackendsAdded:      2,
			},
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: now,
			Checksum:   "abc123",
			LastOperationSummary: &haproxyv1alpha1.OperationSummary{
				TotalAPIOperations: 15,
				BackendsAdded:      3,
			},
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different LastOperationSummary should not be equal")
}

func TestPodStatusesEqual_LastOperationSummaryNilVsSet(t *testing.T) {
	now := metav1.NewTime(time.Now())

	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:              "haproxy-0",
			DeployedAt:           now,
			Checksum:             "abc123",
			LastOperationSummary: nil,
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:    "haproxy-0",
			DeployedAt: now,
			Checksum:   "abc123",
			LastOperationSummary: &haproxyv1alpha1.OperationSummary{
				TotalAPIOperations: 5,
			},
		},
	}

	assert.False(t, podStatusesEqual(a, b), "nil vs set LastOperationSummary should not be equal")
}

func TestPodStatusesEqual_IdenticalWithAllFields(t *testing.T) {
	now := metav1.NewTime(time.Now())
	reloadAt := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	errorAt := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	duration := metav1.Duration{Duration: 5 * time.Second}

	status := haproxyv1alpha1.PodDeploymentStatus{
		PodName:                "haproxy-0",
		DeployedAt:             now,
		Checksum:               "abc123",
		LastReloadAt:           &reloadAt,
		LastReloadID:           "reload-1",
		SyncDuration:           &duration,
		VersionConflictRetries: 2,
		FallbackUsed:           true,
		LastOperationSummary: &haproxyv1alpha1.OperationSummary{
			TotalAPIOperations: 10,
			BackendsAdded:      2,
			BackendsRemoved:    1,
			ServersAdded:       5,
		},
		LastError:         "some error",
		ConsecutiveErrors: 3,
		LastErrorAt:       &errorAt,
	}

	a := []haproxyv1alpha1.PodDeploymentStatus{status}
	b := []haproxyv1alpha1.PodDeploymentStatus{status}

	assert.True(t, podStatusesEqual(a, b), "identical statuses with all fields should be equal")
}

// TestUpdateDeploymentStatus_AuxiliaryFilesUseOwnChecksum verifies that auxiliary files
// (MapFiles, GeneralFiles, CRTListFiles) use their own spec.checksum instead of the main
// config checksum. This prevents unnecessary status updates when only the main config
// changes but auxiliary file content remains the same.
func TestUpdateDeploymentStatus_AuxiliaryFilesUseOwnChecksum(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles:     []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/lua/script.lua", Content: "-- lua script\n"}},
			CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/ssl/crt-list.txt", Content: "default.pem\n"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.MapFileNames, 1)
	require.Len(t, result.GeneralFileNames, 1)
	require.Len(t, result.CRTListFileNames, 1)

	// Get the checksums stored in the auxiliary file specs
	mapFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	mapFileChecksum := mapFile.Spec.Checksum

	generalFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	generalFileChecksum := generalFile.Spec.Checksum

	crtListFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	crtListFileChecksum := crtListFile.Spec.Checksum

	// Verify the checksums are different from the main config checksum
	assert.NotEqual(t, "main-config-checksum-v1", mapFileChecksum)
	assert.NotEqual(t, "main-config-checksum-v1", generalFileChecksum)
	assert.NotEqual(t, "main-config-checksum-v1", crtListFileChecksum)

	t.Run("initial deployment uses file-specific checksums", func(t *testing.T) {
		err := publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
			RuntimeConfigName: "test-config-haproxycfg", RuntimeConfigNamespace: "default",
			PodName: "haproxy-0", DeployedAt: time.Now(), Checksum: "main-config-checksum-v1",
		})
		require.NoError(t, err)

		// Verify main config status uses the main config checksum
		runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
		assert.Equal(t, "main-config-checksum-v1", runtimeConfig.Status.DeployedToPods[0].Checksum)

		// Verify auxiliary files use their own checksums
		mapFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, mapFile.Status.DeployedToPods, 1)
		assert.Equal(t, mapFileChecksum, mapFile.Status.DeployedToPods[0].Checksum, "map file should use its own checksum")

		generalFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, generalFile.Status.DeployedToPods, 1)
		assert.Equal(t, generalFileChecksum, generalFile.Status.DeployedToPods[0].Checksum, "general file should use its own checksum")

		crtListFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		require.Len(t, crtListFile.Status.DeployedToPods, 1)
		assert.Equal(t, crtListFileChecksum, crtListFile.Status.DeployedToPods[0].Checksum, "crt-list file should use its own checksum")
	})

	t.Run("main config change does not update auxiliary file checksums", func(t *testing.T) {
		// Simulate main config change (new checksum) but auxiliary files unchanged
		err := publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
			RuntimeConfigName: "test-config-haproxycfg", RuntimeConfigNamespace: "default",
			PodName: "haproxy-0", DeployedAt: time.Now(), Checksum: "main-config-checksum-v2",
		})
		require.NoError(t, err)

		// Main config status should have updated checksum
		runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "main-config-checksum-v2", runtimeConfig.Status.DeployedToPods[0].Checksum)

		// Auxiliary files should still have their original checksums
		mapFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, mapFileChecksum, mapFile.Status.DeployedToPods[0].Checksum, "map file checksum should remain unchanged")

		generalFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, generalFileChecksum, generalFile.Status.DeployedToPods[0].Checksum, "general file checksum should remain unchanged")

		crtListFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, crtListFileChecksum, crtListFile.Status.DeployedToPods[0].Checksum, "crt-list file checksum should remain unchanged")
	})
}

// TestAuxiliaryFileStatus_NoUpdateWhenChecksumUnchanged verifies that auxiliary file
// status is not updated when the file's checksum hasn't changed, even when main config
// metrics (SyncDuration, LastOperationSummary, etc.) are different.
func TestAuxiliaryFileStatus_NoUpdateWhenChecksumUnchanged(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with a map file
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.MapFileNames, 1)
	mapFileName := result.MapFileNames[0]

	// Initial deployment
	deployTime := time.Now()
	syncDuration := 100 * time.Millisecond
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             deployTime,
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &syncDuration,
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 10,
			BackendsAdded:      2,
		},
	})
	require.NoError(t, err)

	// Get the map file's resource version after initial deployment
	mapFileV1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapFileName, metav1.GetOptions{})
	require.NoError(t, err)
	initialResourceVersion := mapFileV1.ResourceVersion

	// Update with different main config metrics (but same auxiliary file checksum)
	newSyncDuration := 200 * time.Millisecond
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             time.Now(), // Different time
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &newSyncDuration, // Different duration
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 20, // Different operation count
			BackendsAdded:      5,
		},
	})
	require.NoError(t, err)

	// Verify map file resource version didn't change (no update occurred)
	mapFileV2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapFileName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, initialResourceVersion, mapFileV2.ResourceVersion,
		"map file resource version should not change when checksum is unchanged")
}

// TestAuxiliaryFileStatus_UpdateWhenChecksumChanges verifies that auxiliary file
// status IS updated when the file's checksum changes.
func TestAuxiliaryFileStatus_UpdateWhenChecksumChanges(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with a map file
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.MapFileNames, 1)
	mapFileName := result.MapFileNames[0]

	// Initial deployment
	initialDeployTime := time.Now()
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             initialDeployTime,
		Checksum:               "main-config-checksum-v1",
	})
	require.NoError(t, err)

	// Verify initial status
	mapFileV1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapFileName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, mapFileV1.Status.DeployedToPods, 1)
	initialStatusChecksum := mapFileV1.Status.DeployedToPods[0].Checksum
	initialDeployedAt := mapFileV1.Status.DeployedToPods[0].DeployedAt

	// Update the map file spec with different checksum (simulating content change)
	mapFileV1.Spec.Entries = "example.com backend2\n" // Changed content
	mapFileV1.Spec.Checksum = "new-map-file-checksum"
	_, err = crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Update(ctx, mapFileV1, metav1.UpdateOptions{})
	require.NoError(t, err)

	// Update deployment status - the file's spec checksum changed, so status should update
	newDeployTime := time.Now().Add(time.Second)
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             newDeployTime,
		Checksum:               "main-config-checksum-v1",
	})
	require.NoError(t, err)

	// Verify status was updated with new checksum
	mapFileV2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapFileName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, mapFileV2.Status.DeployedToPods, 1)

	assert.NotEqual(t, initialStatusChecksum, mapFileV2.Status.DeployedToPods[0].Checksum,
		"map file status checksum should be updated when spec checksum changes")
	assert.Equal(t, "new-map-file-checksum", mapFileV2.Status.DeployedToPods[0].Checksum)

	// DeployedAt should also be updated (new deployment)
	assert.NotEqual(t, initialDeployedAt, mapFileV2.Status.DeployedToPods[0].DeployedAt,
		"DeployedAt should be updated when checksum changes")
}

// TestAuxiliaryFileStatus_DoesNotInheritMainConfigMetrics verifies that auxiliary file
// status does not include main config metrics like SyncDuration, LastOperationSummary, etc.
func TestAuxiliaryFileStatus_DoesNotInheritMainConfigMetrics(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with all three auxiliary file types
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles:     []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/lua/script.lua", Content: "-- lua script\n"}},
			CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/ssl/crt-list.txt", Content: "default.pem\n"}},
		},
	})
	require.NoError(t, err)

	// Deploy with all main config metrics populated
	syncDuration := 500 * time.Millisecond
	reloadTime := time.Now()
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             time.Now(),
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &syncDuration,
		LastReloadAt:           &reloadTime,
		LastReloadID:           "reload-123",
		VersionConflictRetries: 3,
		FallbackUsed:           true,
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 100,
			BackendsAdded:      10,
			ServersAdded:       50,
		},
	})
	require.NoError(t, err)

	// Verify main config has all metrics
	mainConfig, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, mainConfig.Status.DeployedToPods, 1)
	mainStatus := mainConfig.Status.DeployedToPods[0]
	assert.NotNil(t, mainStatus.SyncDuration, "main config should have SyncDuration")
	assert.NotNil(t, mainStatus.LastReloadAt, "main config should have LastReloadAt")
	assert.NotNil(t, mainStatus.LastOperationSummary, "main config should have LastOperationSummary")
	assert.Equal(t, 3, mainStatus.VersionConflictRetries, "main config should have VersionConflictRetries")
	assert.True(t, mainStatus.FallbackUsed, "main config should have FallbackUsed")

	// Verify map file does NOT have main config metrics
	mapFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, mapFile.Status.DeployedToPods, 1)
	mapStatus := mapFile.Status.DeployedToPods[0]
	assert.Nil(t, mapStatus.SyncDuration, "auxiliary file should NOT have SyncDuration")
	assert.Nil(t, mapStatus.LastReloadAt, "auxiliary file should NOT have LastReloadAt")
	assert.Nil(t, mapStatus.LastOperationSummary, "auxiliary file should NOT have LastOperationSummary")
	assert.Equal(t, 0, mapStatus.VersionConflictRetries, "auxiliary file should NOT have VersionConflictRetries")
	assert.False(t, mapStatus.FallbackUsed, "auxiliary file should NOT have FallbackUsed")
	// But it should have the essential fields
	assert.NotEmpty(t, mapStatus.PodName)
	assert.NotEmpty(t, mapStatus.Checksum)
	assert.False(t, mapStatus.DeployedAt.IsZero())

	// Verify general file does NOT have main config metrics
	generalFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, generalFile.Status.DeployedToPods, 1)
	generalStatus := generalFile.Status.DeployedToPods[0]
	assert.Nil(t, generalStatus.SyncDuration, "auxiliary file should NOT have SyncDuration")
	assert.Nil(t, generalStatus.LastReloadAt, "auxiliary file should NOT have LastReloadAt")
	assert.Nil(t, generalStatus.LastOperationSummary, "auxiliary file should NOT have LastOperationSummary")
	assert.Equal(t, 0, generalStatus.VersionConflictRetries, "auxiliary file should NOT have VersionConflictRetries")
	assert.False(t, generalStatus.FallbackUsed, "auxiliary file should NOT have FallbackUsed")

	// Verify CRT list file does NOT have main config metrics
	crtListFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, crtListFile.Status.DeployedToPods, 1)
	crtStatus := crtListFile.Status.DeployedToPods[0]
	assert.Nil(t, crtStatus.SyncDuration, "auxiliary file should NOT have SyncDuration")
	assert.Nil(t, crtStatus.LastReloadAt, "auxiliary file should NOT have LastReloadAt")
	assert.Nil(t, crtStatus.LastOperationSummary, "auxiliary file should NOT have LastOperationSummary")
	assert.Equal(t, 0, crtStatus.VersionConflictRetries, "auxiliary file should NOT have VersionConflictRetries")
	assert.False(t, crtStatus.FallbackUsed, "auxiliary file should NOT have FallbackUsed")
}

// TestRuntimeConfigStatus_NoUpdateWhenUnchanged verifies that HAProxyCfg status
// is not updated when pod deployment status hasn't actually changed.
func TestRuntimeConfigStatus_NoUpdateWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	_, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	})
	require.NoError(t, err)

	// Initial deployment
	deployTime := time.Now()
	syncDuration := 100 * time.Millisecond
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             deployTime,
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &syncDuration,
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 10,
			BackendsAdded:      2,
		},
	})
	require.NoError(t, err)

	// Get resource version after initial deployment
	configV1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	initialResourceVersion := configV1.ResourceVersion

	// Update with EXACTLY the same status (same checksum, same deployment time, same metrics)
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             deployTime, // Same time
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &syncDuration, // Same duration
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 10,
			BackendsAdded:      2,
		},
	})
	require.NoError(t, err)

	// Verify resource version didn't change (no update occurred)
	configV2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, initialResourceVersion, configV2.ResourceVersion,
		"HAProxyCfg resource version should not change when status is unchanged")
}

// TestRuntimeConfigStatus_UpdateWhenChanged verifies that HAProxyCfg status
// IS updated when pod deployment status changes.
func TestRuntimeConfigStatus_UpdateWhenChanged(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	_, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
	})
	require.NoError(t, err)

	// Initial deployment
	deployTime := time.Now()
	syncDuration := 100 * time.Millisecond
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             deployTime,
		Checksum:               "main-config-checksum-v1",
		SyncDuration:           &syncDuration,
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 10,
			BackendsAdded:      2,
		},
	})
	require.NoError(t, err)

	// Update with DIFFERENT status (new checksum - simulating config change)
	newDeployTime := time.Now().Add(time.Second)
	newSyncDuration := 200 * time.Millisecond
	err = publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		DeployedAt:             newDeployTime,
		Checksum:               "main-config-checksum-v2", // Changed checksum
		SyncDuration:           &newSyncDuration,
		OperationSummary: &OperationSummary{
			TotalAPIOperations: 20,
			BackendsAdded:      5,
		},
	})
	require.NoError(t, err)

	// Verify the status was updated with new values
	configV2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, configV2.Status.DeployedToPods, 1)

	// Verify checksum was updated
	assert.Equal(t, "main-config-checksum-v2", configV2.Status.DeployedToPods[0].Checksum,
		"HAProxyCfg status checksum should be updated")

	// Verify sync duration was updated
	require.NotNil(t, configV2.Status.DeployedToPods[0].SyncDuration)
	assert.Equal(t, newSyncDuration, configV2.Status.DeployedToPods[0].SyncDuration.Duration,
		"HAProxyCfg status sync duration should be updated")

	// Verify operation summary was updated
	require.NotNil(t, configV2.Status.DeployedToPods[0].LastOperationSummary)
	assert.Equal(t, 20, configV2.Status.DeployedToPods[0].LastOperationSummary.TotalAPIOperations,
		"HAProxyCfg status operation summary should be updated")
}

// TestAuxiliaryFileSpec_NoUpdateWhenChecksumUnchanged verifies that auxiliary file
// specs are not updated when the content checksum hasn't changed.
func TestAuxiliaryFileSpec_NoUpdateWhenChecksumUnchanged(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles:     []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/lua/script.lua", Filename: "script.lua", Content: "-- lua script\n"}},
			CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/ssl/crt-list.txt", Content: "default.pem\n"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.MapFileNames, 1)
	require.Len(t, result.GeneralFileNames, 1)
	require.Len(t, result.CRTListFileNames, 1)

	// Get initial resource versions
	mapFile1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	mapFileRV1 := mapFile1.ResourceVersion

	generalFile1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	generalFileRV1 := generalFile1.ResourceVersion

	crtListFile1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	crtListFileRV1 := crtListFile1.ResourceVersion

	// Publish SAME config again (same auxiliary file content)
	result2, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles:     []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
			GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/lua/script.lua", Filename: "script.lua", Content: "-- lua script\n"}},
			CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/ssl/crt-list.txt", Content: "default.pem\n"}},
		},
	})
	require.NoError(t, err)

	// Verify resource versions didn't change (no update occurred)
	mapFile2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result2.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, mapFileRV1, mapFile2.ResourceVersion,
		"map file resource version should not change when checksum is unchanged")

	generalFile2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles("default").Get(ctx, result2.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, generalFileRV1, generalFile2.ResourceVersion,
		"general file resource version should not change when checksum is unchanged")

	crtListFile2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles("default").Get(ctx, result2.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, crtListFileRV1, crtListFile2.ResourceVersion,
		"crt-list file resource version should not change when checksum is unchanged")
}

// TestAuxiliaryFileSpec_UpdateWhenChecksumChanges verifies that auxiliary file
// specs ARE updated when the content checksum changes.
func TestAuxiliaryFileSpec_UpdateWhenChecksumChanges(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1\n"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.MapFileNames, 1)

	// Get initial checksum
	mapFile1, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	initialChecksum := mapFile1.Spec.Checksum

	// Publish with DIFFERENT auxiliary file content
	result2, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles: &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend2\n"}}, // Different content
		},
	})
	require.NoError(t, err)

	// Verify checksum was updated
	mapFile2, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, result2.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEqual(t, initialChecksum, mapFile2.Spec.Checksum,
		"map file checksum should change when content changes")
	assert.Equal(t, "example.com backend2\n", mapFile2.Spec.Entries,
		"map file entries should be updated with new content")
}

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
	"strings"
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create initial runtime config
	initialReq := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config first
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Update deployment status
	update := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add pod first time
	firstUpdate := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "abc123",
	}

	err = publisher.UpdateDeploymentStatus(ctx, &firstUpdate)
	require.NoError(t, err)

	// Update same pod with an error (error state transition triggers UpdateStatus)
	time.Sleep(10 * time.Millisecond) // Ensure different timestamp
	secondUpdate := DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "def456",
		Error:                  "sync failed",
	}

	err = publisher.UpdateDeploymentStatus(ctx, &secondUpdate)
	require.NoError(t, err)

	// Verify only one pod entry exists with updated checksum and error
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-0", runtimeConfig.Status.DeployedToPods[0].PodName)
	assert.Equal(t, "def456", runtimeConfig.Status.DeployedToPods[0].Checksum)
	assert.Equal(t, "sync failed", runtimeConfig.Status.DeployedToPods[0].LastError)
}

func TestUpdateDeploymentStatus_MultiplePods(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add two pods
	for _, podName := range []string{"haproxy-0", "haproxy-1"} {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Try to update deployment status without creating runtime config first
	update := DeploymentStatusUpdate{
		RuntimeConfigName:      "nonexistent-runtime",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create large content that will benefit from compression
	// Repeating patterns compress well
	var largeContent strings.Builder
	for i := range 1000 {
		largeContent.WriteString("backend app_backend_" + string(rune('a'+i%26)) + "\n")
		largeContent.WriteString("  server server1 10.0.0.1:8080 check\n")
		largeContent.WriteString("  server server2 10.0.0.2:8080 check\n")
		largeContent.WriteString("\n")
	}

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  largeContent.String(),
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	assert.Less(t, len(runtimeConfig.Spec.Content), len(largeContent.String()),
		"Compressed content should be smaller than original")
}

func TestPublishConfig_CompressionDisabled(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create large content
	var largeContent strings.Builder
	for i := range 1000 {
		largeContent.WriteString("backend app_backend_" + string(rune('a'+i%26)) + "\n")
		largeContent.WriteString("  server server1 10.0.0.1:8080 check\n")
	}

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  largeContent.String(),
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	assert.Equal(t, largeContent.String(), runtimeConfig.Spec.Content, "Content should be unchanged")
}

func TestPublishConfig_SSLSecretCompressionAnnotation(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add three pods
	for _, podName := range []string{"haproxy-0", "haproxy-1", "haproxy-2"} {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add two pods
	for _, podName := range []string{"haproxy-0", "haproxy-1"} {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
	}

	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// Add two pods
	for _, podName := range []string{"haproxy-0", "haproxy-1"} {
		update := DeploymentStatusUpdate{
			RuntimeConfigName:      "test-config-haproxycfg",
			RuntimeConfigNamespace: "default",
			PodName:                podName,
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
	installSSAListMapMergeReactor(crdClient)

	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config without adding any pods
	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "abc123",
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

func TestAddOrUpdatePodStatus_UpdatesExistingPod(t *testing.T) {
	existing := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "old-checksum",
		},
	}

	newStatus := &haproxyv1alpha1.PodDeploymentStatus{
		PodName:  "haproxy-0",
		Checksum: "new-checksum",
	}

	result := addOrUpdatePodStatus(existing, newStatus)

	require.Len(t, result, 1, "should update existing, not append")
	assert.Equal(t, "new-checksum", result[0].Checksum)
}

func TestAddOrUpdatePodStatus_AppendsDifferentPod(t *testing.T) {
	existing := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "checksum-0",
		},
	}

	newStatus := &haproxyv1alpha1.PodDeploymentStatus{
		PodName:  "haproxy-1",
		Checksum: "checksum-1",
	}

	result := addOrUpdatePodStatus(existing, newStatus)

	require.Len(t, result, 2, "should append new pod")
	assert.Equal(t, "haproxy-0", result[0].PodName)
	assert.Equal(t, "haproxy-1", result[1].PodName)
}

func TestCopyPodStatuses_ReturnsDeepCopy(t *testing.T) {
	original := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "checksum-0",
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
	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "abc123",
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "abc123",
		},
	}

	assert.True(t, podStatusesEqual(a, b), "identical statuses should be equal")
}

func TestPodStatusesEqual_DifferentChecksum(t *testing.T) {
	a := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "abc123",
		},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{
			PodName:  "haproxy-0",
			Checksum: "different",
		},
	}

	assert.False(t, podStatusesEqual(a, b), "different checksums should not be equal")
}

func TestPodStatusesEqual_DifferentLength(t *testing.T) {
	a := []haproxyv1alpha1.PodDeploymentStatus{
		{PodName: "haproxy-0", Checksum: "abc123"},
	}

	b := []haproxyv1alpha1.PodDeploymentStatus{
		{PodName: "haproxy-0", Checksum: "abc123"},
		{PodName: "haproxy-1", Checksum: "def456"},
	}

	assert.False(t, podStatusesEqual(a, b), "different lengths should not be equal")
}

func TestPodStatusesEqual_EmptySlices(t *testing.T) {
	a := []haproxyv1alpha1.PodDeploymentStatus{}
	b := []haproxyv1alpha1.PodDeploymentStatus{}

	assert.True(t, podStatusesEqual(a, b), "empty slices should be equal")
}

func TestPodStatusesEqual_IdenticalWithAllFields(t *testing.T) {
	status := haproxyv1alpha1.PodDeploymentStatus{
		PodName:           "haproxy-0",
		Checksum:          "abc123",
		LastError:         "some error",
		ConsecutiveErrors: 3,
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
	installSSAListMapMergeReactor(crdClient)
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
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
			PodName: "haproxy-0", Checksum: "main-config-checksum-v1",
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

	t.Run("main config change updates main config checksum but not auxiliary file checksums", func(t *testing.T) {
		// Simulate main config change (new checksum). Checksum is a state field
		// so it triggers an UpdateStatus on the main config.
		err := publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
			RuntimeConfigName: "test-config-haproxycfg", RuntimeConfigNamespace: "default",
			PodName: "haproxy-0", Checksum: "main-config-checksum-v2",
		})
		require.NoError(t, err)

		// Main config status checksum IS updated (checksum is a state field)
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

// TestAuxiliaryFileSpec_NoUpdateWhenChecksumUnchanged verifies that auxiliary file
// specs are not updated when the content checksum hasn't changed.
func TestAuxiliaryFileSpec_NoUpdateWhenChecksumUnchanged(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
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
	installSSAListMapMergeReactor(crdClient)
	publisher := New(k8sClient, crdClient, testLogger())

	// Create runtime config with auxiliary files
	result, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("test-uid-123"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "main-config-checksum-v1",
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

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
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
	crdclientfake "haproxy-template-ic/pkg/generated/clientset/versioned/fake"
	"haproxy-template-ic/pkg/k8s/configpublisher"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// testLogger creates a slog logger for tests that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestComponent_ConfigPublishedEvent tests that ConfigPublishedEvent is properly published after validation.
//
// This test verifies the full event flow:
// 1. Component receives ConfigValidatedEvent and caches template config.
// 2. Component receives TemplateRenderedEvent and caches rendered config.
// 3. Component receives ValidationCompletedEvent (HAProxy validation success).
// 4. Component publishes runtime config CRs via Publisher.
// 5. Component publishes ConfigPublishedEvent with correct metadata.
func TestComponent_ConfigPublishedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewSimpleClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Subscribe to capture ConfigPublishedEvent
	eventChan := eventBus.Subscribe(50)

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to start
	time.Sleep(100 * time.Millisecond)

	// Create template config for ConfigValidatedEvent
	templateConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
			UID:       "test-uid-456",
		},
	}

	// Step 1: Publish ConfigValidatedEvent to cache template config
	eventBus.Publish(events.NewConfigValidatedEvent(
		nil, // Config (not used by component)
		templateConfig,
		"v1",
		"secret-v1",
	))

	// Step 2: Publish TemplateRenderedEvent to cache rendered config
	testHAProxyConfig := "global\n  daemon\n\ndefaults\n  mode http\n"
	eventBus.Publish(events.NewTemplateRenderedEvent(
		testHAProxyConfig,
		testHAProxyConfig, // validation config
		nil,               // validation paths
		nil,               // auxiliary files
		nil,               // validation auxiliary files
		0,                 // aux file count
		100,               // duration ms
	))

	// Step 3: Publish ValidationCompletedEvent to trigger publishing
	eventBus.Publish(events.NewValidationCompletedEvent(nil, 50))

	// Wait for ConfigPublishedEvent
	var receivedEvent *events.ConfigPublishedEvent
	timeout := time.After(2 * time.Second)

eventLoop:
	for {
		select {
		case event := <-eventChan:
			if published, ok := event.(*events.ConfigPublishedEvent); ok {
				receivedEvent = published
				break eventLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for ConfigPublishedEvent")
		}
	}

	// Verify ConfigPublishedEvent metadata
	require.NotNil(t, receivedEvent)
	assert.Equal(t, "test-config-haproxycfg", receivedEvent.RuntimeConfigName)
	assert.Equal(t, "default", receivedEvent.RuntimeConfigNamespace)
	assert.Equal(t, 0, receivedEvent.MapFileCount)
	assert.Equal(t, 0, receivedEvent.SecretCount)

	// Verify runtime config was created in Kubernetes
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Equal(t, "test-config-haproxycfg", runtimeConfig.Name)
	assert.Contains(t, runtimeConfig.Spec.Content, "global")
}

// TestComponent_ConfigAppliedToPodEvent tests the component's response to ConfigAppliedToPodEvent.
func TestComponent_ConfigAppliedToPodEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewSimpleClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// First create a runtime config manually (since we're not testing the full event flow)
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-123",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Now publish ConfigAppliedToPodEvent
	eventBus.Publish(events.NewConfigAppliedToPodEvent(
		"test-config-haproxycfg",
		"default",
		"haproxy-pod-1",
		"haproxy-ns",
		"checksum123",
		false, // isDriftCheck
		nil,   // syncMetadata - not testing metadata in this test
	))

	time.Sleep(500 * time.Millisecond)

	// Verify deployment status was updated
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)

	pod := runtimeConfig.Status.DeployedToPods[0]
	assert.Equal(t, "haproxy-pod-1", pod.PodName)
	assert.Equal(t, "checksum123", pod.Checksum)
	assert.NotNil(t, pod.DeployedAt)
}

// TestComponent_HAProxyPodTerminatedEvent tests the component's response to HAProxyPodTerminatedEvent.
func TestComponent_HAProxyPodTerminatedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewSimpleClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Create a runtime config manually
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-123",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Add a pod to deployment status
	eventBus.Publish(events.NewConfigAppliedToPodEvent(
		"test-config-haproxycfg",
		"default",
		"haproxy-pod-1",
		"haproxy-ns",
		"checksum123",
		false, // isDriftCheck
		nil,   // syncMetadata
	))

	time.Sleep(500 * time.Millisecond)

	// Verify pod was added
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)

	// Publish HAProxyPodTerminatedEvent
	eventBus.Publish(events.NewHAProxyPodTerminatedEvent("haproxy-pod-1", "haproxy-ns"))

	time.Sleep(500 * time.Millisecond)

	// Verify pod was removed from deployment status
	runtimeConfig, err = crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Len(t, runtimeConfig.Status.DeployedToPods, 0, "pod should be removed from deployment status")
}

// TestComponent_MultiplePods tests managing multiple pods in deployment status.
func TestComponent_MultiplePods(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewSimpleClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Create a runtime config manually
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-123",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum123",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Add multiple pods
	for i := 1; i <= 3; i++ {
		eventBus.Publish(events.NewConfigAppliedToPodEvent(
			"test-config-haproxycfg",
			"default",
			fmt.Sprintf("haproxy-pod-%d", i),
			"haproxy-ns",
			"checksum123",
			false, // isDriftCheck
			nil,   // syncMetadata
		))
	}

	time.Sleep(500 * time.Millisecond)

	// Verify all pods were added
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Len(t, runtimeConfig.Status.DeployedToPods, 3)

	// Remove one pod
	eventBus.Publish(events.NewHAProxyPodTerminatedEvent("haproxy-pod-2", "haproxy-ns"))

	time.Sleep(500 * time.Millisecond)

	// Verify only one pod was removed
	runtimeConfig, err = crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Len(t, runtimeConfig.Status.DeployedToPods, 2)

	// Verify correct pods remain
	podNames := make([]string, len(runtimeConfig.Status.DeployedToPods))
	for i, pod := range runtimeConfig.Status.DeployedToPods {
		podNames[i] = pod.PodName
	}
	assert.Contains(t, podNames, "haproxy-pod-1")
	assert.Contains(t, podNames, "haproxy-pod-3")
	assert.NotContains(t, podNames, "haproxy-pod-2")
}

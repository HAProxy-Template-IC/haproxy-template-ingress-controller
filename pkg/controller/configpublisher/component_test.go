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

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	busevents "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/events"
	crdclientfake "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/clientset/versioned/fake"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/configpublisher"

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
	k8sClient := k8sfake.NewClientset()
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
		"",                // trigger reason
	))

	// Step 3: Publish ValidationCompletedEvent to trigger publishing
	eventBus.Publish(events.NewValidationCompletedEvent(nil, 50, ""))

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
	k8sClient := k8sfake.NewClientset()
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
	k8sClient := k8sfake.NewClientset()
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
	k8sClient := k8sfake.NewClientset()
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

// TestComponent_Name tests that Name returns the correct component name.
func TestComponent_Name(t *testing.T) {
	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Verify name
	assert.Equal(t, ComponentName, component.Name())
	assert.Equal(t, "config-publisher", component.Name())
}

// TestComponent_LostLeadership tests that cached state is cleared when leadership is lost.
func TestComponent_LostLeadership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Subscribe to capture events
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
			UID:       "test-uid-123",
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
	testHAProxyConfig := "global\n  daemon\n"
	eventBus.Publish(events.NewTemplateRenderedEvent(
		testHAProxyConfig,
		testHAProxyConfig,
		nil,
		nil,
		nil,
		0,
		100,
		"",
	))

	// Give component time to process events
	time.Sleep(200 * time.Millisecond)

	// Step 3: Publish LostLeadershipEvent to clear cached state
	eventBus.Publish(events.NewLostLeadershipEvent("lost-leader-id", "test_reason"))

	// Give component time to process event
	time.Sleep(200 * time.Millisecond)

	// Step 4: Now publish ValidationCompletedEvent - should NOT publish config
	// because cached state was cleared
	eventBus.Publish(events.NewValidationCompletedEvent(nil, 50, ""))

	// Give time for any potential ConfigPublishedEvent (which shouldn't happen)
	timeout := time.After(500 * time.Millisecond)
	var receivedConfigPublished bool

drainLoop:
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ConfigPublishedEvent); ok {
				receivedConfigPublished = true
			}
		case <-timeout:
			break drainLoop
		}
	}

	// Verify no ConfigPublishedEvent was received (state should have been cleared)
	assert.False(t, receivedConfigPublished, "ConfigPublishedEvent should not be published after leadership loss")
}

// TestComponent_ValidationFailed tests the handling of validation failure events.
func TestComponent_ValidationFailed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

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
			UID:       "test-uid-789",
		},
	}

	// Step 1: Publish ConfigValidatedEvent to cache template config
	eventBus.Publish(events.NewConfigValidatedEvent(
		nil,
		templateConfig,
		"v1",
		"secret-v1",
	))

	// Step 2: Publish TemplateRenderedEvent to cache rendered config
	testHAProxyConfig := "global\n  daemon\n  maxconn invalid\n"
	eventBus.Publish(events.NewTemplateRenderedEvent(
		testHAProxyConfig,
		testHAProxyConfig,
		nil,
		nil,
		nil,
		0,
		100,
		"",
	))

	// Give component time to process events
	time.Sleep(200 * time.Millisecond)

	// Step 3: Publish ValidationFailedEvent
	eventBus.Publish(events.NewValidationFailedEvent(
		[]string{"maxconn must be numeric", "invalid configuration directive"},
		100,
		"",
	))

	// Give component time to process event
	time.Sleep(500 * time.Millisecond)

	// Verify invalid config was published (for observability) - note the -invalid suffix
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg-invalid", metav1.GetOptions{})

	require.NoError(t, err)
	assert.Equal(t, "test-config-haproxycfg-invalid", runtimeConfig.Name)
	assert.Contains(t, runtimeConfig.Spec.Content, "maxconn invalid")
	// Check validation error is recorded in status
	assert.Contains(t, runtimeConfig.Status.ValidationError, "maxconn must be numeric")
}

// TestComponent_ValidationFailed_NoCachedState tests that validation failure is ignored without cached state.
func TestComponent_ValidationFailed_NoCachedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to start
	time.Sleep(100 * time.Millisecond)

	// Publish ValidationFailedEvent without any prior cached state
	eventBus.Publish(events.NewValidationFailedEvent(
		[]string{"some error"},
		100,
		"",
	))

	// Give component time to process event
	time.Sleep(300 * time.Millisecond)

	// Verify no config was created (should have been skipped due to missing cached state)
	_, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.Error(t, err, "Should get error because no config should be created")
}

// TestComponent_ConfigAppliedToPodEvent_WithSyncMetadata tests sync metadata processing.
func TestComponent_ConfigAppliedToPodEvent_WithSyncMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// First create a runtime config manually
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-123",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum-sync",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Publish ConfigAppliedToPodEvent with SyncMetadata including operations and reload
	syncMetadata := &events.SyncMetadata{
		ReloadTriggered:        true,
		ReloadID:               "reload-123",
		SyncDuration:           100 * time.Millisecond,
		VersionConflictRetries: 2,
		FallbackUsed:           false,
		OperationCounts: events.OperationCounts{
			TotalAPIOperations: 5,
			BackendsAdded:      2,
			ServersAdded:       3,
		},
		Error: "",
	}

	eventBus.Publish(events.NewConfigAppliedToPodEvent(
		"test-config-haproxycfg",
		"default",
		"haproxy-pod-sync",
		"haproxy-ns",
		"checksum-sync",
		false, // isDriftCheck
		syncMetadata,
	))

	time.Sleep(500 * time.Millisecond)

	// Verify deployment status was updated with sync metadata
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)

	pod := runtimeConfig.Status.DeployedToPods[0]
	assert.Equal(t, "haproxy-pod-sync", pod.PodName)
	assert.NotNil(t, pod.DeployedAt, "DeployedAt should be set when operations were performed")
	assert.NotNil(t, pod.LastReloadAt, "LastReloadAt should be set when reload was triggered")
	assert.Equal(t, "reload-123", pod.LastReloadID)
	assert.NotNil(t, pod.SyncDuration)
	assert.Equal(t, 2, pod.VersionConflictRetries)
	assert.NotNil(t, pod.LastOperationSummary)
	assert.Equal(t, 5, pod.LastOperationSummary.TotalAPIOperations)
	assert.Equal(t, 2, pod.LastOperationSummary.BackendsAdded)
}

// TestComponent_ConfigAppliedToPodEvent_DriftCheck tests drift check handling.
func TestComponent_ConfigAppliedToPodEvent_DriftCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// First create a runtime config manually
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-drift",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum-drift",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Publish ConfigAppliedToPodEvent as drift check (no operations)
	eventBus.Publish(events.NewConfigAppliedToPodEvent(
		"test-config-haproxycfg",
		"default",
		"haproxy-pod-drift",
		"haproxy-ns",
		"checksum-drift",
		true, // isDriftCheck
		nil,  // no syncMetadata for drift check with no changes
	))

	time.Sleep(500 * time.Millisecond)

	// Verify deployment status was updated
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)

	pod := runtimeConfig.Status.DeployedToPods[0]
	assert.Equal(t, "haproxy-pod-drift", pod.PodName)
	assert.NotNil(t, pod.LastCheckedAt, "LastCheckedAt should always be set")
}

// TestComponent_ConfigAppliedToPodEvent_WithError tests error handling in sync metadata.
func TestComponent_ConfigAppliedToPodEvent_WithError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup
	k8sClient := k8sfake.NewClientset()
	crdClient := crdclientfake.NewSimpleClientset()
	eventBus := busevents.NewEventBus(100)

	publisher := configpublisher.New(k8sClient, crdClient, testLogger())
	component := New(publisher, eventBus, testLogger())

	// Start event bus and component
	eventBus.Start()
	go component.Start(ctx)

	// Give component time to subscribe
	time.Sleep(100 * time.Millisecond)

	// First create a runtime config manually
	_, err := publisher.PublishConfig(ctx, &configpublisher.PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       "test-uid-error",
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "checksum-error",
		RenderedAt:              time.Now(),
		ValidatedAt:             time.Now(),
		AuxiliaryFiles:          nil,
	})
	require.NoError(t, err)

	// Publish ConfigAppliedToPodEvent with error in sync metadata
	syncMetadata := &events.SyncMetadata{
		ReloadTriggered: false,
		SyncDuration:    50 * time.Millisecond,
		FallbackUsed:    true,
		OperationCounts: events.OperationCounts{
			TotalAPIOperations: 0, // No operations due to error
		},
		Error: "connection refused to dataplane API",
	}

	eventBus.Publish(events.NewConfigAppliedToPodEvent(
		"test-config-haproxycfg",
		"default",
		"haproxy-pod-error",
		"haproxy-ns",
		"checksum-error",
		false,
		syncMetadata,
	))

	time.Sleep(500 * time.Millisecond)

	// Verify deployment status was updated with error
	runtimeConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})

	require.NoError(t, err)
	require.Len(t, runtimeConfig.Status.DeployedToPods, 1)

	pod := runtimeConfig.Status.DeployedToPods[0]
	assert.Equal(t, "haproxy-pod-error", pod.PodName)
	assert.Equal(t, "connection refused to dataplane API", pod.LastError)
	assert.True(t, pod.FallbackUsed)
	// Error is recorded, other fields should be set correctly
	assert.NotNil(t, pod.SyncDuration)
}

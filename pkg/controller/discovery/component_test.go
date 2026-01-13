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
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// createTestComponent creates a new Component for testing, skipping if haproxy is not available.
func createTestComponent(t *testing.T, bus *busevents.EventBus) *Component {
	t.Helper()

	_, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	if err != nil {
		// Check if it's because haproxy is not found
		if strings.Contains(err.Error(), "haproxy binary not found") ||
			strings.Contains(err.Error(), "failed to detect local HAProxy version") {
			t.Skipf("skipping test: haproxy not available: %v", err)
		}
		t.Fatalf("unexpected error creating discovery component: %v", err)
	}
	return component
}

// skipIfNoHAProxy skips the test if haproxy binary is not available.
func skipIfNoHAProxy(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("haproxy"); err != nil {
		t.Skip("skipping test: haproxy binary not found in PATH")
	}
}

func TestComponent_ConfigValidatedEvent(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1", "127.0.0.2"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish CredentialsUpdatedEvent first (need credentials)
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Publish ResourceSyncCompleteEvent for haproxy-pods (required for initial discovery)
	bus.Publish(events.NewResourceSyncCompleteEvent("haproxy-pods", 2))

	// Wait briefly for credentials and sync to be processed
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigValidatedEvent
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))

	// Wait for HAProxyPodsDiscoveredEvent - verifies that ConfigValidatedEvent triggers discovery.
	// Note: Pods won't be admitted (Count=0) because version check fails without real HTTP endpoints.
	// Actual pod discovery with endpoint verification is covered by integration tests.
	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, discovered, "discovery event should be published")
}

func TestComponent_CredentialsUpdatedEvent(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store and config
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish ConfigValidatedEvent first (need dataplane port)
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))

	// Publish ResourceSyncCompleteEvent for haproxy-pods (required for initial discovery)
	bus.Publish(events.NewResourceSyncCompleteEvent("haproxy-pods", 1))

	// Wait briefly for config and sync to be processed
	time.Sleep(testutil.StartupDelay)

	// Publish CredentialsUpdatedEvent
	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "newsecret",
	}
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v2"))

	// Wait for HAProxyPodsDiscoveredEvent - verifies that CredentialsUpdatedEvent triggers discovery.
	// Note: Pods won't be admitted (Count=0) because version check fails without real HTTP endpoints.
	// Actual pod discovery with endpoint verification is covered by integration tests.
	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, discovered, "discovery event should be published")
}

func TestComponent_ResourceIndexUpdatedEvent(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store, config, and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))
	bus.Publish(events.NewResourceSyncCompleteEvent("haproxy-pods", 1))

	// Wait briefly for prerequisites to be processed and drain initial discovery event
	time.Sleep(testutil.StartupDelay)
	testutil.DrainChannel(eventChan)

	// Publish ResourceIndexUpdatedEvent for haproxy-pods (real-time change, not initial sync)
	changeStats := types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false, // Real-time change
	}
	bus.Publish(events.NewResourceIndexUpdatedEvent("haproxy-pods", changeStats))

	// Wait for HAProxyPodsDiscoveredEvent - verifies that ResourceIndexUpdatedEvent triggers re-discovery.
	// Note: Pods won't be admitted (Count=0) because version check fails without real HTTP endpoints.
	// Actual pod discovery with endpoint verification is covered by integration tests.
	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, discovered, "discovery event should be published")
}

func TestComponent_ResourceIndexUpdatedEvent_InitialSync_Skipped(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store, config, and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Wait briefly for prerequisites to be processed and drain any events triggered
	time.Sleep(testutil.StartupDelay)
	testutil.DrainChannel(eventChan)

	// Publish ResourceIndexUpdatedEvent with IsInitialSync=true
	changeStats := types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: true, // Initial sync - should be skipped
	}
	bus.Publish(events.NewResourceIndexUpdatedEvent("haproxy-pods", changeStats))

	// Should NOT receive HAProxyPodsDiscoveredEvent for initial sync
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_ResourceIndexUpdatedEvent_WrongResourceType_Ignored(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store, config, and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Wait briefly for prerequisites to be processed and drain any events triggered
	time.Sleep(testutil.StartupDelay)
	testutil.DrainChannel(eventChan)

	// Publish ResourceIndexUpdatedEvent for different resource type
	changeStats := types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}
	bus.Publish(events.NewResourceIndexUpdatedEvent("ingresses", changeStats)) // Different resource

	// Should NOT receive HAProxyPodsDiscoveredEvent for non-haproxy-pods resource
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_ResourceSyncCompleteEvent(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store, config, and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Wait briefly for prerequisites to be processed
	time.Sleep(testutil.StartupDelay)

	// Publish ResourceSyncCompleteEvent for haproxy-pods
	bus.Publish(events.NewResourceSyncCompleteEvent("haproxy-pods", 1))

	// Wait for HAProxyPodsDiscoveredEvent - verifies that ResourceSyncCompleteEvent triggers discovery.
	// Note: Pods won't be admitted (Count=0) because version check fails without real HTTP endpoints.
	// Actual pod discovery with endpoint verification is covered by integration tests.
	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, discovered, "discovery event should be published")
}

func TestComponent_ResourceSyncCompleteEvent_WrongResourceType_Ignored(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store, config, and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Wait briefly for prerequisites to be processed and drain any events triggered
	time.Sleep(testutil.StartupDelay)
	testutil.DrainChannel(eventChan)

	// Publish ResourceSyncCompleteEvent for different resource type
	bus.Publish(events.NewResourceSyncCompleteEvent("ingresses", 0))

	// Should NOT receive HAProxyPodsDiscoveredEvent for non-haproxy-pods resource
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_MissingPrerequisites(t *testing.T) {
	tests := []struct {
		name           string
		hasConfig      bool
		hasCredentials bool
		hasPodStore    bool
		shouldDiscover bool
	}{
		{
			name:           "all prerequisites present",
			hasConfig:      true,
			hasCredentials: true,
			hasPodStore:    true,
			shouldDiscover: true,
		},
		{
			name:           "missing config",
			hasConfig:      false,
			hasCredentials: true,
			hasPodStore:    true,
			shouldDiscover: false,
		},
		{
			name:           "missing credentials",
			hasConfig:      true,
			hasCredentials: false,
			hasPodStore:    true,
			shouldDiscover: false,
		},
		{
			name:           "missing pod store",
			hasConfig:      true,
			hasCredentials: true,
			hasPodStore:    false,
			shouldDiscover: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testMissingPrerequisite(t, tt.hasConfig, tt.hasCredentials, tt.hasPodStore, tt.shouldDiscover)
		})
	}
}

// testMissingPrerequisite is a helper that tests discovery with missing prerequisites.
func testMissingPrerequisite(t *testing.T, hasConfig, hasCredentials, hasPodStore, shouldDiscover bool) {
	t.Helper()
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set prerequisites based on test case
	if hasPodStore {
		podStore := createTestPodStore(t, []string{"127.0.0.1"})
		component.SetPodStore(podStore)
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events
	if hasConfig {
		config := &coreconfig.Config{
			Dataplane: coreconfig.DataplaneConfig{
				Port: 5555,
			},
		}
		bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	}

	if hasCredentials {
		credentials := &coreconfig.Credentials{
			DataplaneUsername: "admin",
			DataplanePassword: "secret",
		}
		bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))
	}

	// Wait briefly for prerequisites to be processed
	time.Sleep(testutil.StartupDelay)

	// Publish ResourceIndexUpdatedEvent
	changeStats := types.ChangeStats{
		Created:       1,
		Modified:      0,
		Deleted:       0,
		IsInitialSync: false,
	}
	bus.Publish(events.NewResourceIndexUpdatedEvent("haproxy-pods", changeStats))

	// Check if discovery occurred
	checkDiscoveryOccurred(t, eventChan, ctx, shouldDiscover)
}

// checkDiscoveryOccurred verifies if discovery occurred as expected.
func checkDiscoveryOccurred(t *testing.T, eventChan <-chan busevents.Event, ctx context.Context, shouldDiscover bool) {
	t.Helper()

	select {
	case event := <-eventChan:
		if _, ok := event.(*events.HAProxyPodsDiscoveredEvent); ok {
			if !shouldDiscover {
				t.Fatal("unexpected HAProxyPodsDiscoveredEvent when prerequisites missing")
			}
			// Expected discovery
		}
	case <-ctx.Done():
		if shouldDiscover {
			t.Fatal("expected HAProxyPodsDiscoveredEvent but none received")
		}
		// Expected - no discovery without prerequisites
	}
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// createTestPodStore creates a test pod store with the specified pod IPs.
// Creates fully configured pods with Running phase, Ready condition, and dataplane container.
//
// IMPORTANT: Use 127.0.0.x addresses (not 10.0.0.x) so HTTP version checks fail
// immediately with "connection refused" rather than waiting for TCP timeout.
func createTestPodStore(t *testing.T, podIPs []string) types.Store {
	t.Helper()

	podStore := store.NewMemoryStore(2)

	for i, ip := range podIPs {
		name := "haproxy-" + string(rune('0'+i))
		addPodToStore(t, podStore, name, "default", ip)
	}

	return podStore
}

func TestComponent_Name(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	assert.Equal(t, "discovery", component.Name())
}

func TestComponent_BecameLeaderEvent(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Create pod store with properly configured pods (Running, with dataplane container)
	// Use 127.0.0.1 addresses so HTTP version checks fail immediately with "connection refused"
	// rather than waiting for TCP timeout to non-existent 10.x.x.x addresses
	podStore := store.NewMemoryStore(2)
	addPodToStore(t, podStore, "haproxy-0", "default", "127.0.0.1")
	addPodToStore(t, podStore, "haproxy-1", "default", "127.0.0.2")
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	// Use a longer context timeout (5s) to ensure component stays alive through all phases
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Publish prerequisite events first
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))
	bus.Publish(events.NewResourceSyncCompleteEvent("haproxy-pods", 2))

	// Wait for prerequisites to be processed and initial discovery to complete
	// The initial discovery will cache lastDiscoveredEvent for BecameLeaderEvent replay
	time.Sleep(testutil.DebounceWait)
	testutil.DrainChannel(eventChan)

	// Now publish BecameLeaderEvent - should re-publish cached lastDiscoveredEvent
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))

	// Wait for HAProxyPodsDiscoveredEvent triggered by BecameLeaderEvent state replay
	// Note: Pods won't be admitted (Count=0) because version check fails without real HTTP endpoints
	// But this tests that BecameLeaderEvent triggers the state replay flow
	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, discovered, "discovery event should be published")
}

func TestComponent_BecameLeaderEvent_MissingPrerequisites(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// DO NOT set prerequisites - should not trigger discovery

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Wait for startup
	time.Sleep(testutil.StartupDelay)

	// Publish BecameLeaderEvent without prerequisites
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))

	// Should NOT receive HAProxyPodsDiscoveredEvent
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

// Note: TestComponent_PodRemoval_TerminationEvent cannot be tested at the unit level
// because it requires pods to be "admitted" first, which requires successful HTTP version checks.
// This functionality is covered by integration tests.

func TestComponent_InvalidConfigType(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store and credentials
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Wait for startup
	time.Sleep(testutil.StartupDelay)

	// Publish credentials first
	bus.Publish(events.NewCredentialsUpdatedEvent(credentials, "v1"))

	// Publish ConfigValidatedEvent with wrong config type (string instead of *Config)
	bus.Publish(events.NewConfigValidatedEvent("invalid-config-type", nil, "v1", "v1"))

	// Should NOT receive HAProxyPodsDiscoveredEvent (invalid config type)
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_InvalidCredentialsType(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set pod store and config
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component
	go func() {
		_ = component.Start(ctx)
	}()

	// Wait for startup
	time.Sleep(testutil.StartupDelay)

	// Publish config first
	config := &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			Port: 5555,
		},
	}
	bus.Publish(events.NewConfigValidatedEvent(config, nil, "v1", "v1"))

	// Publish CredentialsUpdatedEvent with wrong credentials type
	bus.Publish(events.NewCredentialsUpdatedEvent("invalid-credentials-type", "v1"))

	// Should NOT receive HAProxyPodsDiscoveredEvent (invalid credentials type)
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestComponent_PerformInitialDiscovery_NoPodsInStore(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)

	// Set empty pod store and prerequisites
	podStore := store.NewMemoryStore(2)
	component.SetPodStore(podStore)

	credentials := &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}

	// Set credentials and config BEFORE starting component
	// This simulates the case where initial discovery runs but finds no pods
	component.mu.Lock()
	component.credentials = credentials
	component.hasCredentials = true
	component.dataplanePort = 5555
	component.hasDataplanePort = true
	component.mu.Unlock()

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	// Start component - performInitialDiscovery will run and find no pods
	go func() {
		_ = component.Start(ctx)
	}()

	// Should NOT receive HAProxyPodsDiscoveredEvent (no pods in store)
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}

// -----------------------------------------------------------------------------
// Additional Helper Functions
// -----------------------------------------------------------------------------

// addPodToStore adds a pod with the given name, namespace, and IP to the store.
// The pod is created in Running phase with Ready condition true.
// Uses port 5555 for the dataplane container.
func addPodToStore(t *testing.T, podStore types.Store, name, namespace, ip string) {
	t.Helper()
	addPodToStoreWithPort(t, podStore, name, namespace, ip, 5555)
}

// addPodToStoreWithPort adds a pod with the given name, namespace, IP, and dataplane port.
// The pod is created in Running phase with Ready condition true.
func addPodToStoreWithPort(t *testing.T, podStore types.Store, name, namespace, ip string, port int64) {
	t.Helper()

	pod := &unstructured.Unstructured{}
	pod.SetAPIVersion("v1")
	pod.SetKind("Pod")
	pod.SetName(name)
	pod.SetNamespace(namespace)

	// Set spec.containers with dataplane port
	containers := []interface{}{
		map[string]interface{}{
			"name": "dataplane",
			"ports": []interface{}{
				map[string]interface{}{
					"containerPort": port,
					"name":          "dataplane",
				},
			},
		},
	}
	err := unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers")
	require.NoError(t, err)

	// Set pod status to Running
	err = unstructured.SetNestedField(pod.Object, "Running", "status", "phase")
	require.NoError(t, err)

	// Set pod IP
	err = unstructured.SetNestedField(pod.Object, ip, "status", "podIP")
	require.NoError(t, err)

	// Set Ready condition
	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "True",
		},
		map[string]interface{}{
			"type":   "ContainersReady",
			"status": "True",
		},
	}
	err = unstructured.SetNestedSlice(pod.Object, conditions, "status", "conditions")
	require.NoError(t, err)

	// Set container status (dataplane container running)
	containerStatuses := []interface{}{
		map[string]interface{}{
			"name": "dataplane",
			"state": map[string]interface{}{
				"running": map[string]interface{}{
					"startedAt": "2025-01-01T00:00:00Z",
				},
			},
			"ready": true,
		},
	}
	err = unstructured.SetNestedSlice(pod.Object, containerStatuses, "status", "containerStatuses")
	require.NoError(t, err)

	keys := []string{namespace, name}
	err = podStore.Add(pod, keys)
	require.NoError(t, err)
}

func TestComponent_CleanupRemovedPods(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	require.NoError(t, err)
	bus.Start()

	// Pre-populate with admitted pods
	component.mu.Lock()
	component.admittedPods["pod-1"] = &dataplane.Endpoint{PodName: "pod-1"}
	component.admittedPods["pod-2"] = &dataplane.Endpoint{PodName: "pod-2"}
	component.admittedPods["pod-3"] = &dataplane.Endpoint{PodName: "pod-3"}
	component.pendingRetries["pod-2"] = &retryState{retryCount: 1, lastAttempt: time.Now()}
	component.pendingRetries["pod-4"] = &retryState{retryCount: 1, lastAttempt: time.Now()}
	component.mu.Unlock()

	// Current candidates only have pod-1 and pod-3
	currentCandidates := map[string]string{
		"pod-1": "10.0.0.1",
		"pod-3": "10.0.0.3",
	}

	// Call cleanup
	component.cleanupRemovedPods(currentCandidates)

	// Verify removed pods are cleaned up
	component.mu.Lock()
	defer component.mu.Unlock()

	// pod-2 should be removed from admittedPods
	_, exists := component.admittedPods["pod-2"]
	assert.False(t, exists, "pod-2 should be removed from admittedPods")

	// pod-2 and pod-4 should be removed from pendingRetries
	_, exists = component.pendingRetries["pod-2"]
	assert.False(t, exists, "pod-2 should be removed from pendingRetries")
	_, exists = component.pendingRetries["pod-4"]
	assert.False(t, exists, "pod-4 should be removed from pendingRetries")

	// pod-1 and pod-3 should remain
	assert.Len(t, component.admittedPods, 2)
	_, exists = component.admittedPods["pod-1"]
	assert.True(t, exists, "pod-1 should remain in admittedPods")
	_, exists = component.admittedPods["pod-3"]
	assert.True(t, exists, "pod-3 should remain in admittedPods")
}

func TestComponent_HandleRetryTimer_NoPendingPods(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	require.NoError(t, err)
	bus.Start()

	// Start component so context is set
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Wait for component to start
	time.Sleep(testutil.StartupDelay)

	// Ensure no pending retries
	component.mu.Lock()
	component.pendingRetries = make(map[string]*retryState)
	component.mu.Unlock()

	// Call handleRetryTimer - should return early
	component.handleRetryTimer()

	// Just verify it doesn't panic and returns
	cancel()
	<-done
}

func TestComponent_HandleRetryTimer_MissingRequirements(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	require.NoError(t, err)
	bus.Start()

	// Start component so context is set
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error)
	go func() {
		done <- component.Start(ctx)
	}()

	// Wait for component to start
	time.Sleep(testutil.StartupDelay)

	// Add pending retries but don't set credentials/port
	component.mu.Lock()
	component.pendingRetries["pod-1"] = &retryState{retryCount: 1, lastAttempt: time.Now()}
	component.hasCredentials = false
	component.hasDataplanePort = false
	component.podStore = nil
	component.mu.Unlock()

	// Call handleRetryTimer - should log warning and return
	component.handleRetryTimer()

	// Just verify it doesn't panic and returns
	cancel()
	<-done
}

func TestComponent_ScheduleRetryTimerLocked_NoPendingRetries(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	require.NoError(t, err)
	bus.Start()

	// Ensure no pending retries
	component.mu.Lock()
	component.pendingRetries = make(map[string]*retryState)

	// Call scheduleRetryTimerLocked - should return early without scheduling
	component.scheduleRetryTimerLocked()

	// Verify no timer was scheduled
	component.retryTimerMu.Lock()
	assert.Nil(t, component.retryTimer, "no timer should be scheduled when no pending retries")
	component.retryTimerMu.Unlock()

	component.mu.Unlock()
}

func TestComponent_ScheduleRetryTimerLocked_WithPendingRetries(t *testing.T) {
	skipIfNoHAProxy(t)

	bus, logger := testutil.NewTestBusAndLogger()
	component, err := New(bus, logger)
	require.NoError(t, err)
	bus.Start()

	// Add pending retries
	component.mu.Lock()
	component.pendingRetries["pod-1"] = &retryState{
		retryCount:  1,
		lastAttempt: time.Now(),
	}

	// Call scheduleRetryTimerLocked
	component.scheduleRetryTimerLocked()

	// Verify timer was scheduled
	component.retryTimerMu.Lock()
	assert.NotNil(t, component.retryTimer, "timer should be scheduled when pending retries exist")
	component.retryTimer.Stop() // Clean up
	component.retryTimerMu.Unlock()

	component.mu.Unlock()
}

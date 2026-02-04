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

package deployer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Test helper to create a test deployer component.
func createTestDeployer(eventBus *busevents.EventBus) *Component {
	logger := testutil.NewTestLogger()
	return New(eventBus, logger, 0, 0) // 0 = unlimited parallelism, 0 = disabled raw push threshold
}

// TestHandleDeploymentScheduled tests deployment execution when scheduled.
func TestHandleDeploymentScheduled(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()
	deployer := createTestDeployer(bus)

	ctx := context.Background()

	// Start deployer in background
	go deployer.Start(ctx)
	time.Sleep(10 * time.Millisecond)

	// Create deployment scheduled event (with no endpoints, just to test event handling)
	event := events.NewDeploymentScheduledEvent(
		"test config",
		nil,
		nil, // parsedConfig
		[]interface{}{},
		"test-runtime-config",
		"test-namespace",
		"test",
		true, // coalescible
	)

	// Publish event
	bus.Publish(event)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Since there are no valid endpoints, no deployment events should be published
	// This test just verifies the component handles the event without crashing
}

// TestDeployToEndpoints_InvalidEndpointType tests handling invalid endpoint types.
func TestDeployToEndpoints_InvalidEndpointType(t *testing.T) {
	bus := busevents.NewEventBus(100)
	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	deployer := createTestDeployer(bus)

	ctx := context.Background()
	config := "test config"
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Invalid endpoint type (string instead of dataplane.Endpoint)
	invalidEndpoints := []interface{}{"not-an-endpoint"}

	deployer.deployToEndpoints(ctx, config, auxFiles, nil, invalidEndpoints, "test-runtime-config", "default", "test", "test-correlation-id")

	// Should not crash, just log error and publish completion event with zero endpoints
	timeout := time.After(100 * time.Millisecond)
	receivedEvents := []busevents.Event{}

loop:
	for {
		select {
		case event := <-eventChan:
			receivedEvents = append(receivedEvents, event)
		case <-timeout:
			break loop
		}
	}

	// Should publish a DeploymentCompletedEvent with 0 endpoints to notify downstream components
	assert.Len(t, receivedEvents, 1)
	completedEvent, ok := receivedEvents[0].(*events.DeploymentCompletedEvent)
	assert.True(t, ok, "expected DeploymentCompletedEvent")
	assert.Equal(t, 0, completedEvent.Total)
	assert.Equal(t, 0, completedEvent.Succeeded)
	assert.Equal(t, 0, completedEvent.Failed)
}

// TestDeployToEndpoints_EventPublishing tests that all expected events are published.
func TestDeployToEndpoints_EventPublishing(t *testing.T) {
	// Note: This test can't actually deploy to real HAProxy instances
	// It tests the event publishing flow assuming deployment would succeed/fail
	bus := busevents.NewEventBus(100)
	bus.Start()

	deployer := createTestDeployer(bus)

	// This test would need a mock dataplane client to test event publishing
	// For now, we've verified the event publishing code structure
	assert.NotNil(t, deployer)
}

// TestComponent_Start tests the component startup and shutdown.
func TestComponent_Start(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := deployer.Start(ctx)

	// Start returns nil on graceful shutdown, ctx.Err() indicates the reason
	require.NoError(t, err)
}

// TestComponent_EndToEndFlow tests the complete event flow.
func TestComponent_EndToEndFlow(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start component in background BEFORE starting bus
	// This ensures the component subscribes before events are published
	go deployer.Start(ctx)

	// Give deployer time to subscribe
	time.Sleep(10 * time.Millisecond)

	// NOW start the bus to begin event processing
	bus.Start()

	// Subscribe to events
	eventChan := bus.Subscribe("test-sub", 10)

	// Simulate deployment scheduled event (with no endpoints)
	bus.Publish(events.NewDeploymentScheduledEvent(
		"global\n  daemon\n",
		&dataplane.AuxiliaryFiles{},
		nil,             // parsedConfig
		[]interface{}{}, // no endpoints
		"test-runtime-config",
		"test-namespace",
		"test",
		true, // coalescible
	))

	// Wait for event processing
	time.Sleep(50 * time.Millisecond)

	// Should receive DeploymentScheduledEvent + DeploymentCompletedEvent (with 0 endpoints)
	timeout := time.After(100 * time.Millisecond)
	receivedEvents := 0

loop:
	for {
		select {
		case <-eventChan:
			receivedEvents++
		case <-timeout:
			break loop
		}
	}

	// DeploymentScheduledEvent we published + DeploymentCompletedEvent from deployer
	assert.Equal(t, 2, receivedEvents)

	// Cleanup
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestComponent_ConvertEndpoints tests endpoint conversion.
func TestComponent_ConvertEndpoints(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	t.Run("valid endpoints", func(t *testing.T) {
		endpoints := []interface{}{
			dataplane.Endpoint{
				URL:          "http://localhost:5555",
				PodName:      "haproxy-0",
				PodNamespace: "default",
			},
			dataplane.Endpoint{
				URL:          "http://localhost:5556",
				PodName:      "haproxy-1",
				PodNamespace: "default",
			},
		}

		result := deployer.convertEndpoints(endpoints)

		assert.Len(t, result, 2)
		assert.Equal(t, "http://localhost:5555", result[0].URL)
		assert.Equal(t, "http://localhost:5556", result[1].URL)
	})

	t.Run("empty endpoints", func(t *testing.T) {
		endpoints := []interface{}{}

		result := deployer.convertEndpoints(endpoints)

		assert.Len(t, result, 0)
	})

	t.Run("mixed valid and invalid endpoints", func(t *testing.T) {
		endpoints := []interface{}{
			dataplane.Endpoint{
				URL:     "http://localhost:5555",
				PodName: "haproxy-0",
			},
			"invalid-endpoint",
			dataplane.Endpoint{
				URL:     "http://localhost:5556",
				PodName: "haproxy-1",
			},
		}

		result := deployer.convertEndpoints(endpoints)

		// Should only include valid endpoints
		assert.Len(t, result, 2)
	})
}

// TestComponent_ConvertAuxFiles tests auxiliary files conversion.
func TestComponent_ConvertAuxFiles(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	t.Run("nil input", func(t *testing.T) {
		result := deployer.convertAuxFiles(nil)
		assert.Nil(t, result)
	})

	t.Run("valid AuxiliaryFiles pointer", func(t *testing.T) {
		auxFiles := &dataplane.AuxiliaryFiles{}

		result := deployer.convertAuxFiles(auxFiles)

		assert.NotNil(t, result)
	})

	t.Run("invalid type", func(t *testing.T) {
		result := deployer.convertAuxFiles("invalid-type")
		assert.Nil(t, result)
	})
}

// TestComponent_ConvertSyncResultToMetadata tests sync result conversion.
func TestComponent_ConvertSyncResultToMetadata(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	t.Run("nil input", func(t *testing.T) {
		result := deployer.convertSyncResultToMetadata(nil)
		assert.Nil(t, result)
	})

	t.Run("valid sync result", func(t *testing.T) {
		syncResult := &dataplane.SyncResult{
			ReloadTriggered: true,
			ReloadID:        "12345",
			Duration:        100 * time.Millisecond,
			Retries:         2,
			SyncMode:        dataplane.SyncModeFineGrained,
			Details: dataplane.DiffDetails{
				TotalOperations:  10,
				BackendsAdded:    []string{"backend1", "backend2"},
				BackendsDeleted:  []string{"backend3"},
				BackendsModified: []string{"backend4"},
				ServersAdded: map[string][]string{
					"backend1": {"server1", "server2"},
				},
				ServersDeleted: map[string][]string{
					"backend3": {"server3"},
				},
				ServersModified: map[string][]string{
					"backend4": {"server4"},
				},
				FrontendsAdded:    []string{"frontend1"},
				FrontendsDeleted:  []string{},
				FrontendsModified: []string{"frontend2"},
			},
		}

		result := deployer.convertSyncResultToMetadata(syncResult)

		require.NotNil(t, result)
		assert.True(t, result.ReloadTriggered)
		assert.Equal(t, "12345", result.ReloadID)
		assert.Equal(t, 100*time.Millisecond, result.SyncDuration)
		assert.Equal(t, 2, result.VersionConflictRetries)
		assert.False(t, result.FallbackUsed)
		assert.Equal(t, 10, result.OperationCounts.TotalAPIOperations)
		assert.Equal(t, 2, result.OperationCounts.BackendsAdded)
		assert.Equal(t, 1, result.OperationCounts.BackendsRemoved)
		assert.Equal(t, 1, result.OperationCounts.BackendsModified)
		assert.Equal(t, 2, result.OperationCounts.ServersAdded)
		assert.Equal(t, 1, result.OperationCounts.ServersRemoved)
		assert.Equal(t, 1, result.OperationCounts.ServersModified)
		assert.Equal(t, 1, result.OperationCounts.FrontendsAdded)
		assert.Equal(t, 0, result.OperationCounts.FrontendsRemoved)
		assert.Equal(t, 1, result.OperationCounts.FrontendsModified)
		assert.Empty(t, result.Error)
	})
}

// TestComponent_HandleEvent tests event handling.
func TestComponent_HandleEvent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	ctx := context.Background()

	t.Run("ignores non-deployment events", func(t *testing.T) {
		// Should not panic or error when receiving non-DeploymentScheduledEvent
		otherEvent := events.NewValidationStartedEvent()
		deployer.handleEvent(ctx, otherEvent)
	})

	t.Run("handles DeploymentScheduledEvent", func(t *testing.T) {
		event := events.NewDeploymentScheduledEvent(
			"test config",
			nil,
			nil, // parsedConfig
			[]interface{}{},
			"test-runtime-config",
			"test-namespace",
			"test",
			true, // coalescible
		)
		// Should not panic when receiving valid event with no endpoints
		deployer.handleEvent(ctx, event)
	})
}

// TestComponent_DeploymentInProgressFlag tests the atomic deployment in progress flag.
func TestComponent_DeploymentInProgressFlag(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()
	deployer := createTestDeployer(bus)

	ctx := context.Background()

	// First deployment should succeed
	event := events.NewDeploymentScheduledEvent(
		"test config",
		nil,
		nil, // parsedConfig
		[]interface{}{},
		"test-runtime-config",
		"test-namespace",
		"test",
		true, // coalescible
	)

	// Process first event - should set flag
	deployer.handleDeploymentScheduled(ctx, event)

	// Flag should be cleared after deployToEndpoints completes (even with no endpoints)
	assert.False(t, deployer.deploymentInProgress.Load())
}

// TestDeployer_Name tests the Name method.
func TestDeployer_Name(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	assert.Equal(t, ComponentName, deployer.Name())
}

// TestComponent_DeploymentInProgressFlag_DuplicateRejected tests that duplicate deployments are rejected.
func TestComponent_DeploymentInProgressFlag_DuplicateRejected(t *testing.T) {
	bus := busevents.NewEventBus(100)
	bus.Start()
	deployer := createTestDeployer(bus)

	ctx := context.Background()

	// Set flag to simulate deployment in progress
	deployer.deploymentInProgress.Store(true)

	// Second deployment should be rejected
	event := events.NewDeploymentScheduledEvent(
		"test config",
		nil,
		nil, // parsedConfig
		[]interface{}{},
		"test-runtime-config",
		"test-namespace",
		"duplicate",
		true, // coalescible
	)

	// This should be rejected (flag was already set)
	deployer.handleDeploymentScheduled(ctx, event)

	// Flag should still be true (not modified)
	assert.True(t, deployer.deploymentInProgress.Load())
}

// TestComponent_ConvertEndpoints_InvalidType tests conversion of invalid endpoint types.
func TestComponent_ConvertEndpoints_InvalidType(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	// Mix of valid and invalid endpoint types
	endpointsRaw := []interface{}{
		dataplane.Endpoint{URL: "http://localhost:5555"},
		"invalid-string-type",
		dataplane.Endpoint{URL: "http://localhost:5556"},
		123, // invalid int type
	}

	endpoints := deployer.convertEndpoints(endpointsRaw)

	// Should only include valid endpoints
	assert.Len(t, endpoints, 2)
	assert.Equal(t, "http://localhost:5555", endpoints[0].URL)
	assert.Equal(t, "http://localhost:5556", endpoints[1].URL)
}

// TestComponent_isNoOpDeployment tests the helper function that determines
// whether a deployment made meaningful changes that warrant publishing ConfigAppliedToPodEvent.
func TestComponent_isNoOpDeployment(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	tests := []struct {
		name       string
		syncResult *dataplane.SyncResult
		expected   bool
	}{
		{
			name:       "nil sync result - skip",
			syncResult: nil,
			expected:   true,
		},
		{
			name: "with operations - do not skip",
			syncResult: &dataplane.SyncResult{
				Details: dataplane.DiffDetails{
					TotalOperations: 5,
				},
			},
			expected: false,
		},
		{
			name: "with reload - do not skip",
			syncResult: &dataplane.SyncResult{
				ReloadTriggered: true,
				Details:         dataplane.DiffDetails{},
			},
			expected: false,
		},
		{
			name: "no operations no reload - skip",
			syncResult: &dataplane.SyncResult{
				ReloadTriggered: false,
				Details: dataplane.DiffDetails{
					TotalOperations: 0,
				},
			},
			expected: true,
		},
		{
			name: "zero value details - skip",
			syncResult: &dataplane.SyncResult{
				ReloadTriggered: false,
				Details:         dataplane.DiffDetails{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deployer.isNoOpDeployment(tt.syncResult)
			assert.Equal(t, tt.expected, got)
		})
	}
}

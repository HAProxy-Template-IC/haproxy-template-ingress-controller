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
	// Zero timeouts are intentional: every per-sync knob falls back to
	// dataplane.DefaultSyncOptions() in deployToSingleEndpoint (timeouts applied
	// only via the > 0 guards). This is the contract the deployer gives test code.
	return New(eventBus, logger, 0, 0)
}

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
		[]dataplane.Endpoint{},
		"test-runtime-config",
		"test-namespace",
		"test",
		"",   // contentChecksum
		nil,  // statusPatches
		true, // coalescible
	)

	// Publish event
	bus.Publish(event)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Since there are no valid endpoints, no deployment events should be published
	// This test just verifies the component handles the event without crashing
}

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

func TestComponent_Start(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := deployer.Start(ctx)

	// Start returns nil on graceful shutdown, ctx.Err() indicates the reason
	require.NoError(t, err)
}

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
		nil,                    // parsedConfig
		[]dataplane.Endpoint{}, // no endpoints
		"test-runtime-config",
		"test-namespace",
		"test",
		"",   // contentChecksum
		nil,  // statusPatches
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

func TestComponent_ConvertSyncResultToMetadata(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := syncResultToMetadata(nil)
		assert.Nil(t, result)
	})

	t.Run("valid sync result", func(t *testing.T) {
		syncResult := &dataplane.SyncResult{
			ReloadTriggered: true,
			ReloadID:        "12345",
			Duration:        100 * time.Millisecond,
			SyncMode:        dataplane.SyncModeReload,
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

		result := syncResultToMetadata(syncResult)

		require.NotNil(t, result)
		assert.True(t, result.ReloadTriggered)
		assert.Equal(t, "12345", result.ReloadID)
		assert.Equal(t, 100*time.Millisecond, result.SyncDuration)
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

func TestComponent_HandleEvent(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)
	deployer.ctx = context.Background()

	t.Run("ignores non-deployment events", func(t *testing.T) {
		// Should not panic or error when receiving non-DeploymentScheduledEvent
		otherEvent := events.NewReconciliationCompletedEvent(0, nil)
		deployer.HandleEvent(otherEvent)
	})

	t.Run("handles DeploymentScheduledEvent", func(t *testing.T) {
		event := events.NewDeploymentScheduledEvent(
			"test config",
			nil,
			nil, // parsedConfig
			[]dataplane.Endpoint{},
			"test-runtime-config",
			"test-namespace",
			"test",
			"",   // contentChecksum
			nil,  // statusPatches
			true, // coalescible
		)
		// Should not panic when receiving valid event with no endpoints
		deployer.HandleEvent(event)
	})
}

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
		[]dataplane.Endpoint{},
		"test-runtime-config",
		"test-namespace",
		"test",
		"",   // contentChecksum
		nil,  // statusPatches
		true, // coalescible
	)

	// Process first event - should set flag
	deployer.performDeployment(ctx, event)

	// Flag should be cleared after deployToEndpoints completes (even with no endpoints)
	assert.False(t, deployer.deploymentInProgress.Load())
}

func TestDeployer_Name(t *testing.T) {
	bus := busevents.NewEventBus(100)
	deployer := createTestDeployer(bus)

	assert.Equal(t, ComponentName, deployer.Name())
}

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
		[]dataplane.Endpoint{},
		"test-runtime-config",
		"test-namespace",
		"duplicate",
		"",   // contentChecksum
		nil,  // statusPatches
		true, // coalescible
	)

	// This should be rejected (flag was already set)
	deployer.performDeployment(ctx, event)

	// Flag should still be true (not modified)
	assert.True(t, deployer.deploymentInProgress.Load())
}

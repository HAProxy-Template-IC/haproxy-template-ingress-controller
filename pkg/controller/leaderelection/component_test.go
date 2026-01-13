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

package leaderelection

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	k8sleaderelection "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
)

func TestNew_NilConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(nil, clientset, bus, callbacks, logger)

	require.Error(t, err)
	assert.Nil(t, component)
	assert.Contains(t, err.Error(), "config cannot be nil")
}

func TestNew_NilClientset(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, nil, bus, callbacks, logger)

	require.Error(t, err)
	assert.Nil(t, component)
	assert.Contains(t, err.Error(), "failed to create elector")
}

func TestNew_NilEventBus(t *testing.T) {
	clientset := fake.NewClientset()
	logger := testutil.NewTestLogger()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, nil, callbacks, logger)

	require.Error(t, err)
	assert.Nil(t, component)
	assert.Contains(t, err.Error(), "event bus cannot be nil")
}

func TestNew_NilLogger(t *testing.T) {
	bus := testutil.NewTestBus()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, bus, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, component)
	// Should use default logger
	assert.NotNil(t, component.logger)
}

func TestNew_ValidConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, bus, callbacks, logger)

	require.NoError(t, err)
	require.NotNil(t, component)
	assert.Equal(t, bus, component.eventBus)
	assert.Equal(t, logger, component.logger)
	assert.Equal(t, "test-pod", component.identity)
	assert.Equal(t, "test-lease", component.leaseName)
	assert.Equal(t, "test-ns", component.leaseNamespace)
	assert.NotNil(t, component.elector)
}

func TestNew_CallbackWrapping_OnStartedLeading(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}

	callbackCalled := false
	callbacks := k8sleaderelection.Callbacks{
		OnStartedLeading: func(ctx context.Context) {
			callbackCalled = true
		},
	}

	// Subscribe before bus.Start()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	component, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	// Simulate OnStartedLeading callback by accessing the elector's internal callback
	// This is a workaround since we can't easily trigger leader election in unit tests
	// Instead, we verify that the component is correctly configured

	// The component stores the identity correctly for event publishing
	assert.Equal(t, "test-pod", component.identity)

	// Drain events if any
	select {
	case <-eventChan:
	default:
	}

	assert.False(t, callbackCalled) // Not called yet - would be called when elected
}

func TestNew_CallbackWrapping_OnStoppedLeading(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}

	callbackCalled := false
	callbacks := k8sleaderelection.Callbacks{
		OnStoppedLeading: func() {
			callbackCalled = true
		},
	}

	_, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	assert.False(t, callbackCalled) // Not called yet - would be called when losing leadership
}

func TestNew_CallbackWrapping_OnNewLeader(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}

	var observedLeader string
	callbacks := k8sleaderelection.Callbacks{
		OnNewLeader: func(identity string) {
			observedLeader = identity
		},
	}

	_, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	assert.Empty(t, observedLeader) // Not called yet - would be called when new leader observed
}

func TestNew_CallbackWrapping_NilCallbacks(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}

	// All nil callbacks
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, bus, callbacks, logger)

	// Should succeed even with nil callbacks
	require.NoError(t, err)
	require.NotNil(t, component)
}

func TestComponent_Run_PublishesLeaderElectionStartedEvent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	// Subscribe before bus.Start()
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	component, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	// Cancel context immediately to prevent Start() from blocking forever
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Start should publish LeaderElectionStartedEvent before trying to acquire lease
	go component.Start(ctx)

	// Wait for event
	timeout := time.After(1 * time.Second)
	for {
		select {
		case event := <-eventChan:
			if startedEvent, ok := event.(*events.LeaderElectionStartedEvent); ok {
				assert.Equal(t, "test-pod", startedEvent.Identity)
				assert.Equal(t, "test-lease", startedEvent.LeaseName)
				assert.Equal(t, "test-ns", startedEvent.LeaseNamespace)
				return
			}
		case <-timeout:
			t.Fatal("timeout waiting for LeaderElectionStartedEvent")
		}
	}
}

func TestComponent_IsLeader_BeforeElection(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	// Before election starts, should not be leader
	assert.False(t, component.IsLeader())
}

func TestComponent_GetLeader_BeforeElection(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset()

	config := &k8sleaderelection.Config{
		Enabled:        true,
		Identity:       "test-pod",
		LeaseName:      "test-lease",
		LeaseNamespace: "test-ns",
		LeaseDuration:  15 * time.Second,
		RenewDeadline:  10 * time.Second,
		RetryPeriod:    2 * time.Second,
	}
	callbacks := k8sleaderelection.Callbacks{}

	component, err := New(config, clientset, bus, callbacks, logger)
	require.NoError(t, err)

	// Before election starts, no leader
	assert.Empty(t, component.GetLeader())
}

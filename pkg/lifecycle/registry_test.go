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

package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockComponent is a simple test component.
type mockComponent struct {
	name        string
	startDelay  time.Duration
	startErr    error
	started     bool
	stopped     bool
	startedChan chan struct{} // Signaled when component has started
	mu          sync.Mutex
}

// newMockComponent creates a mockComponent with a started channel for synchronization.
func newMockComponent(name string) *mockComponent {
	return &mockComponent{
		name:        name,
		startedChan: make(chan struct{}),
	}
}

func (c *mockComponent) Name() string {
	return c.name
}

func (c *mockComponent) Start(ctx context.Context) error {
	c.mu.Lock()
	c.started = true
	// Signal that we've started (close is idempotent-safe via sync.Once pattern)
	if c.startedChan != nil {
		select {
		case <-c.startedChan:
			// Already closed
		default:
			close(c.startedChan)
		}
	}
	c.mu.Unlock()

	if c.startDelay > 0 {
		select {
		case <-time.After(c.startDelay):
		case <-ctx.Done():
			c.mu.Lock()
			c.stopped = true
			c.mu.Unlock()
			return ctx.Err()
		}
	}

	if c.startErr != nil {
		return c.startErr
	}

	// Block until context cancelled
	<-ctx.Done()

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()

	return nil
}

func (c *mockComponent) IsStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

func (c *mockComponent) IsStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// WaitStarted waits for the component to start, with a timeout.
func (c *mockComponent) WaitStarted(timeout time.Duration) bool {
	if c.startedChan == nil {
		return false
	}
	select {
	case <-c.startedChan:
		return true
	case <-time.After(timeout):
		return false
	}
}

// healthyComponent implements HealthChecker.
type healthyComponent struct {
	mockComponent
	healthy bool
}

func (c *healthyComponent) HealthCheck() error {
	if !c.healthy {
		return errors.New("component unhealthy")
	}
	return nil
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	comp1 := &mockComponent{name: "comp1"}
	comp2 := &mockComponent{name: "comp2"}

	registry.Register(comp1)
	registry.Register(comp2, LeaderOnly())

	assert.Equal(t, 2, registry.Count())

	status := registry.Status()
	assert.Len(t, status, 2)
	assert.Equal(t, StatusPending, status["comp1"].Status)
	assert.Equal(t, StatusPending, status["comp2"].Status)
	assert.False(t, status["comp1"].LeaderOnly)
	assert.True(t, status["comp2"].LeaderOnly)
}

func TestRegistry_StartAll(t *testing.T) {
	registry := NewRegistry()

	comp1 := newMockComponent("comp1")
	comp2 := newMockComponent("comp2")

	registry.Register(comp1)
	registry.Register(comp2)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start all components (non-blocking goroutine for testing)
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for components to start using synchronization
	require.True(t, comp1.WaitStarted(200*time.Millisecond), "comp1 should have started")
	require.True(t, comp2.WaitStarted(200*time.Millisecond), "comp2 should have started")

	// Verify both started
	assert.True(t, comp1.IsStarted())
	assert.True(t, comp2.IsStarted())

	// Cancel and wait for completion
	cancel()
	err := <-errChan
	assert.NoError(t, err)

	// Verify both stopped
	assert.True(t, comp1.IsStopped())
	assert.True(t, comp2.IsStopped())
}

func TestRegistry_StartAll_LeaderOnlySkipped(t *testing.T) {
	registry := NewRegistry()

	comp1 := newMockComponent("comp1")
	comp2 := newMockComponent("leader-comp")

	registry.Register(comp1)
	registry.Register(comp2, LeaderOnly())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start without being leader
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for comp1 to start
	require.True(t, comp1.WaitStarted(200*time.Millisecond), "comp1 should have started")

	// Only non-leader component should start
	assert.True(t, comp1.IsStarted())
	assert.False(t, comp2.IsStarted())

	cancel()
	<-errChan
}

func TestRegistry_StartAll_LeaderOnlyStarted(t *testing.T) {
	registry := NewRegistry()

	comp1 := newMockComponent("comp1")
	comp2 := newMockComponent("leader-comp")

	registry.Register(comp1)
	registry.Register(comp2, LeaderOnly())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start as leader
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, true)
	}()

	// Wait for both to start
	require.True(t, comp1.WaitStarted(200*time.Millisecond), "comp1 should have started")
	require.True(t, comp2.WaitStarted(200*time.Millisecond), "comp2 should have started")

	// Both components should start
	assert.True(t, comp1.IsStarted())
	assert.True(t, comp2.IsStarted())

	cancel()
	<-errChan
}

func TestRegistry_StartAll_ComponentError(t *testing.T) {
	registry := NewRegistry()

	expectedErr := errors.New("start failed")
	comp1 := &mockComponent{name: "failing-comp", startErr: expectedErr}

	registry.Register(comp1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := registry.StartAll(ctx, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failing-comp")

	status := registry.Status()
	assert.Equal(t, StatusFailed, status["failing-comp"].Status)
	assert.Contains(t, status["failing-comp"].Error, "start failed")
}

func TestRegistry_Status(t *testing.T) {
	registry := NewRegistry()

	comp := &mockComponent{name: "test-comp"}
	registry.Register(comp, LeaderOnly())

	status := registry.Status()

	require.Len(t, status, 1)
	info := status["test-comp"]

	assert.Equal(t, "test-comp", info.Name)
	assert.Equal(t, StatusPending, info.Status)
	assert.True(t, info.LeaderOnly)
	assert.Empty(t, info.Error)
	assert.Nil(t, info.Healthy) // No health checker
}

func TestRegistry_Status_WithHealthCheck(t *testing.T) {
	registry := NewRegistry()

	comp := &healthyComponent{
		mockComponent: mockComponent{name: "healthy-comp", startedChan: make(chan struct{})},
		healthy:       true,
	}
	registry.Register(comp)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start the component so it's Running (HealthCheck is only called for Running components)
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for component to start
	require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

	status := registry.Status()

	info := status["healthy-comp"]
	require.NotNil(t, info.Healthy)
	assert.True(t, *info.Healthy)

	// Make unhealthy
	comp.healthy = false
	status = registry.Status()

	info = status["healthy-comp"]
	require.NotNil(t, info.Healthy)
	assert.False(t, *info.Healthy)

	cancel()
	<-errChan
}

func TestRegistry_Options(t *testing.T) {
	t.Run("LeaderOnly", func(t *testing.T) {
		registry := NewRegistry()
		comp := &mockComponent{name: "comp"}

		registry.Register(comp, LeaderOnly())

		status := registry.Status()
		assert.True(t, status["comp"].LeaderOnly)
	})
}

func TestRegistry_StatusRunning(t *testing.T) {
	t.Run("component reaches running status", func(t *testing.T) {
		registry := NewRegistry()
		comp := newMockComponent("test-comp")
		registry.Register(comp)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		errChan := make(chan error, 1)
		go func() {
			errChan <- registry.StartAll(ctx, false)
		}()

		// Wait for component to start using synchronization
		require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

		// Verify component is Running while Start() blocks
		status := registry.Status()
		assert.Equal(t, StatusRunning, status["test-comp"].Status)

		cancel()
		<-errChan

		// After cancellation, status should be Stopped
		status = registry.Status()
		assert.Equal(t, StatusStopped, status["test-comp"].Status)
	})

	t.Run("failed component has failed status", func(t *testing.T) {
		registry := NewRegistry()
		comp := &mockComponent{name: "failing-comp", startErr: errors.New("failed")}
		registry.Register(comp)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_ = registry.StartAll(ctx, false)

		status := registry.Status()
		assert.Equal(t, StatusFailed, status["failing-comp"].Status)
	})
}

// trackingHealthComponent tracks whether HealthCheck was called.
type trackingHealthComponent struct {
	mockComponent
	healthCalled bool
	healthy      bool
	mu           sync.Mutex
}

func (c *trackingHealthComponent) HealthCheck() error {
	c.mu.Lock()
	c.healthCalled = true
	c.mu.Unlock()
	if !c.healthy {
		return errors.New("component unhealthy")
	}
	return nil
}

func (c *trackingHealthComponent) wasHealthCheckCalled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthCalled
}

func (c *trackingHealthComponent) resetHealthCheck() {
	c.mu.Lock()
	c.healthCalled = false
	c.mu.Unlock()
}

func TestRegistry_Status_SkipsHealthCheckForStandbyComponents(t *testing.T) {
	registry := NewRegistry()

	// Create a leader-only component with health check
	comp := &trackingHealthComponent{
		mockComponent: mockComponent{name: "leader-comp"},
		healthy:       false, // Would return error if called
	}
	registry.Register(comp, LeaderOnly())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start without being leader - component should be in Standby
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Give StartAll time to set status to Standby
	time.Sleep(50 * time.Millisecond)

	// Verify component is in Standby
	status := registry.Status()
	require.Equal(t, StatusStandby, status["leader-comp"].Status, "component should be in Standby")

	// HealthCheck should NOT have been called for Standby component
	assert.False(t, comp.wasHealthCheckCalled(), "HealthCheck should not be called for Standby components")

	// info.Healthy should be nil (not set)
	assert.Nil(t, status["leader-comp"].Healthy, "Healthy should be nil for Standby components")

	cancel()
	<-errChan
}

func TestRegistry_Status_CallsHealthCheckForRunningComponents(t *testing.T) {
	registry := NewRegistry()

	// Create an all-replica component with health check
	comp := &trackingHealthComponent{
		mockComponent: mockComponent{name: "running-comp", startedChan: make(chan struct{})},
		healthy:       true,
	}
	registry.Register(comp)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start the component
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for component to start
	require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

	// Reset the health check tracking
	comp.resetHealthCheck()

	// Get status - should call HealthCheck for running component
	status := registry.Status()
	require.Equal(t, StatusRunning, status["running-comp"].Status, "component should be Running")

	// HealthCheck SHOULD have been called for running component
	assert.True(t, comp.wasHealthCheckCalled(), "HealthCheck should be called for Running components")

	// info.Healthy should be set
	require.NotNil(t, status["running-comp"].Healthy, "Healthy should be set for Running components")
	assert.True(t, *status["running-comp"].Healthy, "Healthy should be true")

	cancel()
	<-errChan
}

func TestRegistry_Status_SkipsHealthCheckForPendingComponents(t *testing.T) {
	registry := NewRegistry()

	// Create a component with health check that would fail
	comp := &trackingHealthComponent{
		mockComponent: mockComponent{name: "pending-comp"},
		healthy:       false,
	}
	registry.Register(comp)

	// Don't start - component stays in Pending status

	// Get status - should NOT call HealthCheck for pending component
	status := registry.Status()
	require.Equal(t, StatusPending, status["pending-comp"].Status, "component should be Pending")

	// HealthCheck should NOT have been called for Pending component
	assert.False(t, comp.wasHealthCheckCalled(), "HealthCheck should not be called for Pending components")

	// info.Healthy should be nil (not set)
	assert.Nil(t, status["pending-comp"].Healthy, "Healthy should be nil for Pending components")
}

func TestRegistry_Build(t *testing.T) {
	registry := NewRegistry()

	allReplica1 := &mockComponent{name: "all-replica-1"}
	allReplica2 := &mockComponent{name: "all-replica-2"}
	leaderOnly1 := &mockComponent{name: "leader-only-1"}
	leaderOnly2 := &mockComponent{name: "leader-only-2"}

	count := registry.Build().
		AllReplica(allReplica1, allReplica2).
		LeaderOnly(leaderOnly1, leaderOnly2).
		Done()

	assert.Equal(t, 4, count, "Expected 4 components to be registered")
	assert.Equal(t, 4, registry.Count(), "Registry count should be 4")

	// Verify all-replica components are registered without leader-only flag
	status := registry.Status()
	info1, ok := status["all-replica-1"]
	require.True(t, ok, "all-replica-1 should be registered")
	assert.False(t, info1.LeaderOnly, "all-replica-1 should not be leader-only")

	info2, ok := status["all-replica-2"]
	require.True(t, ok, "all-replica-2 should be registered")
	assert.False(t, info2.LeaderOnly, "all-replica-2 should not be leader-only")

	// Verify leader-only components are registered with leader-only flag
	info3, ok := status["leader-only-1"]
	require.True(t, ok, "leader-only-1 should be registered")
	assert.True(t, info3.LeaderOnly, "leader-only-1 should be leader-only")

	info4, ok := status["leader-only-2"]
	require.True(t, ok, "leader-only-2 should be registered")
	assert.True(t, info4.LeaderOnly, "leader-only-2 should be leader-only")
}

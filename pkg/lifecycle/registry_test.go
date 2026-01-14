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

// -----------------------------------------------------------------------------
// Test Components
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

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

func TestRegistry_StartAll_ErrorHandler(t *testing.T) {
	registry := NewRegistry()

	expectedErr := errors.New("start failed")
	comp1 := &mockComponent{name: "failing-comp", startErr: expectedErr}

	var handlerCalled bool
	var handlerName string
	var handlerErr error

	registry.Register(comp1, OnError(func(name string, err error) {
		handlerCalled = true
		handlerName = name
		handlerErr = err
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = registry.StartAll(ctx, false)

	assert.True(t, handlerCalled)
	assert.Equal(t, "failing-comp", handlerName)
	assert.Equal(t, expectedErr, handlerErr)
}

func TestRegistry_Status(t *testing.T) {
	registry := NewRegistry()

	comp := &mockComponent{name: "test-comp"}
	registry.Register(comp, LeaderOnly(), Criticality(CriticalityDegradable))

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

func TestRegistry_IsHealthy(t *testing.T) {
	t.Run("all healthy", func(t *testing.T) {
		registry := NewRegistry()

		comp := &healthyComponent{
			mockComponent: mockComponent{name: "comp", startedChan: make(chan struct{})},
			healthy:       true,
		}
		registry.Register(comp)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		// Start the component (HealthCheck is only called for Running components)
		errChan := make(chan error, 1)
		go func() {
			errChan <- registry.StartAll(ctx, false)
		}()

		require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

		assert.True(t, registry.IsHealthy())

		cancel()
		<-errChan
	})

	t.Run("critical unhealthy", func(t *testing.T) {
		registry := NewRegistry()

		comp := &healthyComponent{
			mockComponent: mockComponent{name: "comp", startedChan: make(chan struct{})},
			healthy:       false,
		}
		registry.Register(comp, Criticality(CriticalityCritical))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		// Start the component (HealthCheck is only called for Running components)
		errChan := make(chan error, 1)
		go func() {
			errChan <- registry.StartAll(ctx, false)
		}()

		require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

		assert.False(t, registry.IsHealthy())

		cancel()
		<-errChan
	})

	t.Run("optional unhealthy", func(t *testing.T) {
		registry := NewRegistry()

		comp := &healthyComponent{
			mockComponent: mockComponent{name: "comp", startedChan: make(chan struct{})},
			healthy:       false,
		}
		registry.Register(comp, Criticality(CriticalityOptional))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		// Start the component (HealthCheck is only called for Running components)
		errChan := make(chan error, 1)
		go func() {
			errChan <- registry.StartAll(ctx, false)
		}()

		require.True(t, comp.WaitStarted(200*time.Millisecond), "component should have started")

		// Optional component being unhealthy shouldn't affect overall health
		assert.True(t, registry.IsHealthy())

		cancel()
		<-errChan
	})

	t.Run("critical failed", func(t *testing.T) {
		registry := NewRegistry()

		comp := &mockComponent{name: "comp", startErr: errors.New("failed")}
		registry.Register(comp, Criticality(CriticalityCritical))

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_ = registry.StartAll(ctx, false)

		assert.False(t, registry.IsHealthy())
	})
}

func TestRegistry_GetComponent(t *testing.T) {
	registry := NewRegistry()

	comp := &mockComponent{name: "test-comp"}
	registry.Register(comp)

	found := registry.GetComponent("test-comp")
	assert.Equal(t, comp, found)

	notFound := registry.GetComponent("nonexistent")
	assert.Nil(t, notFound)
}

func TestRegistry_StartLeaderOnlyComponents(t *testing.T) {
	registry := NewRegistry()

	comp1 := newMockComponent("all-replica")
	comp2 := newMockComponent("leader-only")

	registry.Register(comp1)
	registry.Register(comp2, LeaderOnly())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start all-replica components first (not as leader)
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for comp1 to start using synchronization
	require.True(t, comp1.WaitStarted(200*time.Millisecond), "comp1 should have started")
	assert.True(t, comp1.IsStarted())
	assert.False(t, comp2.IsStarted())

	// Now start leader-only components
	go func() {
		_ = registry.StartLeaderOnlyComponents(ctx)
	}()

	// Wait for comp2 to start using synchronization
	require.True(t, comp2.WaitStarted(200*time.Millisecond), "comp2 should have started")
	assert.True(t, comp2.IsStarted())

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

	t.Run("DependsOn", func(t *testing.T) {
		registry := NewRegistry()
		comp := &mockComponent{name: "comp"}

		registry.Register(comp, DependsOn("dep1", "dep2"))

		assert.Equal(t, 1, registry.Count())
	})

	t.Run("Criticality", func(t *testing.T) {
		registry := NewRegistry()
		comp := &mockComponent{name: "comp"}

		registry.Register(comp, Criticality(CriticalityOptional))

		// Criticality affects IsHealthy behavior (tested above)
		assert.Equal(t, 1, registry.Count())
	})
}

// -----------------------------------------------------------------------------
// StatusRunning Tests
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// DependsOn Tests
// -----------------------------------------------------------------------------

// startOrderRecorder is a thread-safe recorder for component start order.
type startOrderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *startOrderRecorder) record(name string) {
	r.mu.Lock()
	r.order = append(r.order, name)
	r.mu.Unlock()
}

func (r *startOrderRecorder) getOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.order))
	copy(result, r.order)
	return result
}

// orderedMockComponent tracks start order.
type orderedMockComponent struct {
	mockComponent
	recorder    *startOrderRecorder
	startedChan chan struct{}
}

func newOrderedMock(name string, recorder *startOrderRecorder) *orderedMockComponent {
	return &orderedMockComponent{
		mockComponent: mockComponent{name: name},
		recorder:      recorder,
		startedChan:   make(chan struct{}),
	}
}

func (c *orderedMockComponent) Start(ctx context.Context) error {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()

	c.recorder.record(c.name)
	close(c.startedChan)

	// Block until context cancelled
	<-ctx.Done()

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()

	return nil
}

func TestRegistry_DependsOn_Basic(t *testing.T) {
	registry := NewRegistry()
	recorder := &startOrderRecorder{}

	// A depends on B, so B should start first
	compA := newOrderedMock("compA", recorder)
	compB := newOrderedMock("compB", recorder)

	registry.Register(compB)
	registry.Register(compA, DependsOn("compB"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for both components to start
	select {
	case <-compA.startedChan:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compA did not start in time")
	}

	select {
	case <-compB.startedChan:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compB did not start in time")
	}

	// B must have been added to startOrder before A
	startOrder := recorder.getOrder()
	require.Len(t, startOrder, 2)

	// Find positions
	posA := indexOf(startOrder, "compA")
	posB := indexOf(startOrder, "compB")

	// B must start before A
	assert.True(t, posB < posA, "compB should start before compA, but order was: %v", startOrder)

	cancel()
	<-errChan
}

func TestRegistry_DependsOn_Chain(t *testing.T) {
	registry := NewRegistry()
	recorder := &startOrderRecorder{}

	// C depends on B, B depends on A
	compA := newOrderedMock("compA", recorder)
	compB := newOrderedMock("compB", recorder)
	compC := newOrderedMock("compC", recorder)

	registry.Register(compA)
	registry.Register(compB, DependsOn("compA"))
	registry.Register(compC, DependsOn("compB"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for all components to start
	for _, comp := range []*orderedMockComponent{compA, compB, compC} {
		select {
		case <-comp.startedChan:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("%s did not start in time", comp.name)
		}
	}

	// Verify order: A, B, C
	startOrder := recorder.getOrder()
	require.Len(t, startOrder, 3)

	posA := indexOf(startOrder, "compA")
	posB := indexOf(startOrder, "compB")
	posC := indexOf(startOrder, "compC")

	assert.True(t, posA < posB, "compA should start before compB")
	assert.True(t, posB < posC, "compB should start before compC")

	cancel()
	<-errChan
}

func TestRegistry_DependsOn_MissingDependency(t *testing.T) {
	registry := NewRegistry()

	comp := &mockComponent{name: "comp"}
	registry.Register(comp, DependsOn("nonexistent"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := registry.StartAll(ctx, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends on unknown component")
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_DependsOn_CircularDependency(t *testing.T) {
	registry := NewRegistry()

	// A depends on B, B depends on A
	compA := &mockComponent{name: "compA"}
	compB := &mockComponent{name: "compB"}

	registry.Register(compA, DependsOn("compB"))
	registry.Register(compB, DependsOn("compA"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := registry.StartAll(ctx, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestRegistry_DependsOn_CircularDependencyChain(t *testing.T) {
	registry := NewRegistry()

	// A -> B -> C -> A (cycle)
	compA := &mockComponent{name: "compA"}
	compB := &mockComponent{name: "compB"}
	compC := &mockComponent{name: "compC"}

	registry.Register(compA, DependsOn("compC"))
	registry.Register(compB, DependsOn("compA"))
	registry.Register(compC, DependsOn("compB"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := registry.StartAll(ctx, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestRegistry_DependsOn_MultipleDependencies(t *testing.T) {
	registry := NewRegistry()
	recorder := &startOrderRecorder{}

	// C depends on both A and B
	compA := newOrderedMock("compA", recorder)
	compB := newOrderedMock("compB", recorder)
	compC := newOrderedMock("compC", recorder)

	registry.Register(compA)
	registry.Register(compB)
	registry.Register(compC, DependsOn("compA", "compB"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for all components to start
	for _, comp := range []*orderedMockComponent{compA, compB, compC} {
		select {
		case <-comp.startedChan:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("%s did not start in time", comp.name)
		}
	}

	// Verify C started after both A and B
	startOrder := recorder.getOrder()
	require.Len(t, startOrder, 3)

	posA := indexOf(startOrder, "compA")
	posB := indexOf(startOrder, "compB")
	posC := indexOf(startOrder, "compC")

	assert.True(t, posA < posC, "compA should start before compC")
	assert.True(t, posB < posC, "compB should start before compC")

	cancel()
	<-errChan
}

func TestRegistry_DependsOn_LeaderOnlyDependency(t *testing.T) {
	registry := NewRegistry()

	// A is leader-only, B depends on A
	compA := &mockComponent{name: "compA"}
	compB := &mockComponent{name: "compB"}

	registry.Register(compA, LeaderOnly())
	registry.Register(compB, DependsOn("compA"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Not starting as leader, so A won't be started
	// B depends on A which won't be started, should error
	err := registry.StartAll(ctx, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends on")
	assert.Contains(t, err.Error(), "not being started")
}

func TestRegistry_DependsOn_AlreadyRunningDependency(t *testing.T) {
	registry := NewRegistry()
	recorder := &startOrderRecorder{}

	// A and B are all-replica, C is leader-only and depends on A
	compA := newOrderedMock("compA", recorder)
	compB := newOrderedMock("compB", recorder)
	compC := newOrderedMock("compC", recorder)

	registry.Register(compA)
	registry.Register(compB)
	registry.Register(compC, LeaderOnly(), DependsOn("compA"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start all-replica components (not leader)
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Wait for A and B to start
	select {
	case <-compA.startedChan:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compA did not start in time")
	}

	select {
	case <-compB.startedChan:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compB did not start in time")
	}

	// C should not be started yet
	assert.False(t, compC.IsStarted())

	// Now start leader-only components
	errChan2 := make(chan error, 1)
	go func() {
		errChan2 <- registry.StartLeaderOnlyComponents(ctx)
	}()

	// Wait for C to start (should work since A is already running)
	select {
	case <-compC.startedChan:
		// Success!
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compC did not start in time (should depend on already-running compA)")
	}

	cancel()
	<-errChan
	_ = errChan2 // Drain second error channel
}

// indexOf finds the index of a string in a slice.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
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

func TestRegistry_IsHealthy_SkipsStandbyComponents(t *testing.T) {
	registry := NewRegistry()

	// Create a leader-only component with health check that would return unhealthy
	comp := &trackingHealthComponent{
		mockComponent: mockComponent{name: "leader-comp"},
		healthy:       false, // Would make IsHealthy return false if called
	}
	registry.Register(comp, LeaderOnly(), Criticality(CriticalityCritical))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start without being leader - component should be in Standby
	errChan := make(chan error, 1)
	go func() {
		errChan <- registry.StartAll(ctx, false)
	}()

	// Give StartAll time to set status to Standby
	time.Sleep(50 * time.Millisecond)

	// IsHealthy should return true because Standby components' health is not checked
	assert.True(t, registry.IsHealthy(), "IsHealthy should return true when critical component is in Standby")

	cancel()
	<-errChan
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

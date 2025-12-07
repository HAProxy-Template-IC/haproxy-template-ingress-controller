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
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"
)

// registeredComponent holds a component and its registration configuration.
type registeredComponent struct {
	component Component
	config    registrationConfig
	status    Status
	lastError error
	ready     chan struct{} // Closed when component reaches StatusRunning
}

// Registry manages component lifecycles.
//
// The Registry provides:
//   - Component registration with options (leader-only, dependencies, criticality)
//   - Ordered startup based on dependencies
//   - Status tracking and health checks
//   - Leader-only component management
//
// Example:
//
//	registry := lifecycle.NewRegistry()
//	registry.Register(reconciler.New(bus, logger))
//	registry.Register(deployer.New(bus, logger), lifecycle.LeaderOnly())
//
//	err := registry.StartAll(ctx)
type Registry struct {
	components []*registeredComponent          // Stores pointers to avoid invalidation on slice growth
	byName     map[string]*registeredComponent // Fast lookup by name
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewRegistry creates a new component registry.
func NewRegistry() *Registry {
	return &Registry{
		components: make([]*registeredComponent, 0),
		byName:     make(map[string]*registeredComponent),
		logger:     slog.Default().With("component", "lifecycle-registry"),
	}
}

// WithLogger sets a custom logger for the registry.
func (r *Registry) WithLogger(logger *slog.Logger) *Registry {
	r.logger = logger.With("component", "lifecycle-registry")
	return r
}

// Register adds a component to the registry with optional configuration.
//
// Components are started in the order they are registered, respecting any
// dependency constraints specified via DependsOn().
//
// Example:
//
//	registry.Register(reconciler.New(bus, logger))
//	registry.Register(deployer.New(bus, logger), lifecycle.LeaderOnly())
func (r *Registry) Register(c Component, opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()

	config := registrationConfig{
		criticality: CriticalityCritical, // Default to critical
	}
	for _, opt := range opts {
		opt(&config)
	}

	// Allocate separately to avoid pointer invalidation when slice grows
	comp := &registeredComponent{
		component: c,
		config:    config,
		status:    StatusPending,
		ready:     make(chan struct{}),
	}

	r.components = append(r.components, comp)
	r.byName[c.Name()] = comp

	r.logger.Debug("Component registered",
		"name", c.Name(),
		"leader_only", config.leaderOnly,
		"dependencies", config.dependencies)
}

// StartAll starts all registered components.
//
// Components are started concurrently, respecting dependency ordering.
// Components wait for their dependencies to reach StatusRunning before starting.
// Leader-only components are skipped unless isLeader is true.
//
// This method blocks until all components are running or an error occurs.
// Returns the first error encountered, or nil if all components started successfully.
//
// Parameters:
//   - ctx: Context for cancellation
//   - isLeader: Whether this instance is currently the leader
//
// Example:
//
//	err := registry.StartAll(ctx, isLeader)
//	if err != nil {
//	    return fmt.Errorf("failed to start components: %w", err)
//	}
func (r *Registry) StartAll(ctx context.Context, isLeader bool) error {
	r.mu.Lock()

	// Validate dependencies and get components to start
	componentsToStart, err := r.prepareComponentsToStart(isLeader)
	if err != nil {
		r.mu.Unlock()
		return err
	}

	r.mu.Unlock()

	if len(componentsToStart) == 0 {
		return nil
	}

	// Start components using errgroup for concurrent execution with error handling
	g, gCtx := errgroup.WithContext(ctx)

	for _, comp := range componentsToStart {
		comp := comp // Capture loop variable
		g.Go(func() error {
			// Wait for dependencies to be ready
			if err := r.waitForDependencies(gCtx, comp); err != nil {
				return err
			}
			return r.startComponent(gCtx, comp)
		})
	}

	return g.Wait()
}

// prepareComponentsToStart validates dependencies and returns components to start.
// Must be called with r.mu held.
func (r *Registry) prepareComponentsToStart(isLeader bool) ([]*registeredComponent, error) {
	componentsToStart := make([]*registeredComponent, 0, len(r.components))
	startSet := make(map[string]bool)

	for _, comp := range r.components {
		// Skip leader-only components if not leader, but mark them as standby
		if comp.config.leaderOnly && !isLeader {
			comp.status = StatusStandby
			r.logger.Debug("Setting leader-only component to standby (not leader)",
				"name", comp.component.Name())
			continue
		}

		comp.status = StatusStarting
		componentsToStart = append(componentsToStart, comp)
		startSet[comp.component.Name()] = true
	}

	// Validate dependencies
	if err := r.validateDependencies(componentsToStart, startSet); err != nil {
		return nil, err
	}

	return componentsToStart, nil
}

// validateDependencies checks that all dependencies exist and there are no cycles.
// Must be called with r.mu held.
func (r *Registry) validateDependencies(components []*registeredComponent, startSet map[string]bool) error {
	// Check for missing dependencies
	for _, comp := range components {
		for _, depName := range comp.config.dependencies {
			dep, exists := r.byName[depName]
			if !exists {
				return fmt.Errorf("component %q depends on unknown component %q",
					comp.component.Name(), depName)
			}

			// Dependency must be in the start set or already running
			if !startSet[depName] && dep.status != StatusRunning {
				return fmt.Errorf("component %q depends on %q which is not being started",
					comp.component.Name(), depName)
			}
		}
	}

	// Check for cycles using DFS
	if err := r.detectCycles(components); err != nil {
		return err
	}

	return nil
}

// detectCycles uses DFS to detect circular dependencies.
// Must be called with r.mu held.
func (r *Registry) detectCycles(components []*registeredComponent) error {
	// Build set of components being started
	inSet := make(map[string]bool)
	for _, comp := range components {
		inSet[comp.component.Name()] = true
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(name string) error
	dfs = func(name string) error {
		visited[name] = true
		recStack[name] = true

		comp, exists := r.byName[name]
		if !exists {
			return nil // Unknown component, skip
		}

		for _, depName := range comp.config.dependencies {
			if !inSet[depName] {
				continue // Dependency not in current start set, skip
			}

			if !visited[depName] {
				if err := dfs(depName); err != nil {
					return err
				}
			} else if recStack[depName] {
				return fmt.Errorf("circular dependency detected: %s -> %s", name, depName)
			}
		}

		recStack[name] = false
		return nil
	}

	for _, comp := range components {
		name := comp.component.Name()
		if !visited[name] {
			if err := dfs(name); err != nil {
				return err
			}
		}
	}

	return nil
}

// waitForDependencies waits for all dependencies to reach StatusRunning.
func (r *Registry) waitForDependencies(ctx context.Context, comp *registeredComponent) error {
	for _, depName := range comp.config.dependencies {
		r.mu.RLock()
		dep, exists := r.byName[depName]
		r.mu.RUnlock()

		if !exists {
			// Should not happen after validation, but handle gracefully
			return fmt.Errorf("dependency %q not found", depName)
		}

		r.logger.Debug("Waiting for dependency",
			"component", comp.component.Name(),
			"dependency", depName)

		select {
		case <-dep.ready:
			r.logger.Debug("Dependency ready",
				"component", comp.component.Name(),
				"dependency", depName)
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for dependency %q: %w", depName, ctx.Err())
		}
	}

	return nil
}

// startComponent starts a single component and updates its status.
//
// Design note on timing: Status is set to Running and ready channel is closed
// after Start() has been entered. This ensures:
//  1. Dependent components don't race ahead of their dependencies
//  2. Components subscribe to EventBus in their constructor, not in Start()
//  3. Therefore, components ARE ready to receive events as soon as Start() begins
//
// This design requires that components complete any critical initialization
// in their constructor, not in Start().
func (r *Registry) startComponent(ctx context.Context, comp *registeredComponent) error {
	name := comp.component.Name()

	r.logger.Info("Starting component", "name", name)

	// Set status to Running before calling Start()
	r.updateStatus(name, StatusRunning, nil)

	// Use channels to coordinate Start() entry with ready signal
	startEntered := make(chan struct{})
	errChan := make(chan error, 1)

	go func() {
		// Signal that Start() has been entered
		close(startEntered)
		// Run the component (blocks until context cancelled or error)
		errChan <- comp.component.Start(ctx)
	}()

	// Wait for Start() to be entered before signaling ready to dependents
	<-startEntered

	// Signal that this component is ready for dependents
	close(comp.ready)

	// Wait for Start() to complete
	err := <-errChan

	// Update status after Start() returns
	if err != nil && err != context.Canceled {
		r.updateStatus(name, StatusFailed, err)
		r.logger.Error("Component failed", "name", name, "error", err)

		// Call error handler if configured
		r.mu.RLock()
		onError := comp.config.onError
		r.mu.RUnlock()

		if onError != nil {
			onError(name, err)
		}

		return fmt.Errorf("component %s failed: %w", name, err)
	}

	r.updateStatus(name, StatusStopped, nil)
	r.logger.Info("Component stopped", "name", name)

	return nil
}

// updateStatus updates the status of a component by name.
func (r *Registry) updateStatus(name string, status Status, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if comp, exists := r.byName[name]; exists {
		comp.status = status
		comp.lastError = err
	}
}

// Status returns the current status of all registered components.
func (r *Registry) Status() map[string]ComponentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]ComponentInfo, len(r.components))

	for _, comp := range r.components {
		info := ComponentInfo{
			Name:       comp.component.Name(),
			Status:     comp.status,
			LeaderOnly: comp.config.leaderOnly,
		}

		if comp.lastError != nil {
			info.Error = comp.lastError.Error()
		}

		// Check health if supported (released lock briefly for health check)
		if checker, ok := comp.component.(HealthChecker); ok {
			healthy := checker.HealthCheck() == nil
			info.Healthy = &healthy
		}

		result[info.Name] = info
	}

	return result
}

// IsHealthy returns true if all critical components are running and healthy.
func (r *Registry) IsHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, comp := range r.components {
		// Check if critical component is not running
		if comp.config.criticality == CriticalityCritical {
			if comp.status == StatusFailed {
				return false
			}
		}

		// Check health if supported
		if checker, ok := comp.component.(HealthChecker); ok {
			if err := checker.HealthCheck(); err != nil {
				if comp.config.criticality == CriticalityCritical {
					return false
				}
			}
		}
	}

	return true
}

// GetComponent returns a component by name, or nil if not found.
func (r *Registry) GetComponent(name string) Component {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if comp, exists := r.byName[name]; exists {
		return comp.component
	}

	return nil
}

// StartLeaderOnlyComponents starts components marked as leader-only.
//
// This should be called when leadership is acquired. Returns an error
// if any leader-only component fails to start.
//
// Example:
//
//	// In leadership callback
//	func (c *Controller) onBecameLeader() {
//	    if err := c.registry.StartLeaderOnlyComponents(ctx); err != nil {
//	        log.Error("Failed to start leader components", "error", err)
//	    }
//	}
func (r *Registry) StartLeaderOnlyComponents(ctx context.Context) error {
	r.mu.Lock()
	componentsToStart := make([]*registeredComponent, 0)
	startSet := make(map[string]bool)

	for _, comp := range r.components {
		// Only start leader-only components that are pending or standby
		if comp.config.leaderOnly && (comp.status == StatusPending || comp.status == StatusStandby) {
			comp.status = StatusStarting
			// Re-create ready channel in case this is called multiple times
			comp.ready = make(chan struct{})
			componentsToStart = append(componentsToStart, comp)
			startSet[comp.component.Name()] = true
		}
	}

	// Add already running components to the start set for dependency validation
	for _, comp := range r.components {
		if comp.status == StatusRunning {
			startSet[comp.component.Name()] = true
		}
	}

	// Validate dependencies
	if err := r.validateDependencies(componentsToStart, startSet); err != nil {
		r.mu.Unlock()
		return err
	}

	r.mu.Unlock()

	if len(componentsToStart) == 0 {
		return nil
	}

	g, gCtx := errgroup.WithContext(ctx)

	for _, comp := range componentsToStart {
		comp := comp
		g.Go(func() error {
			// Wait for dependencies to be ready
			if err := r.waitForDependencies(gCtx, comp); err != nil {
				return err
			}
			return r.startComponent(gCtx, comp)
		})
	}

	return g.Wait()
}

// Count returns the number of registered components.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.components)
}

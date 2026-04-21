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

// Package lifecycle manages component lifecycles — registration, ordered
// startup with dependency resolution, leader-only activation, and
// status/health reporting. The entry point is Registry.
//
// Responsibilities are split across files:
//   - registry.go   — the Registry type plus Register / Count
//   - startup.go    — StartAll and the startComponent goroutine logic
//   - dependencies.go — dependency validation, cycle detection, ready-waits
//   - leader.go     — StartLeaderOnlyComponents{,Async}
//   - status.go     — Status, isHealthy, updateStatus, getComponent
package lifecycle

import (
	"log/slog"
	"sync"
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

// Count returns the number of registered components.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.components)
}

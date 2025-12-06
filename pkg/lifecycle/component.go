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

// Package lifecycle provides component lifecycle management for the controller.
//
// This package implements a component registry pattern that allows declarative
// configuration of component startup, dependency ordering, and health tracking.
//
// Example:
//
//	registry := lifecycle.NewRegistry()
//
//	// Register components with options
//	registry.Register(reconciler.New(bus, logger))
//	registry.Register(deployer.New(bus, logger), lifecycle.LeaderOnly())
//
//	// Start all components
//	err := registry.StartAll(ctx)
//
//	// Check status
//	status := registry.Status()
package lifecycle

import (
	"context"
)

// Component is the minimal interface for components managed by the Registry.
//
// Components must provide a unique name and a Start method. The Start method
// should block until the context is cancelled or an error occurs.
type Component interface {
	// Name returns a unique identifier for this component.
	// This is used for logging, status tracking, and dependency resolution.
	Name() string

	// Start begins the component's operation.
	// This method should block until ctx is cancelled or an error occurs.
	// Returning nil indicates graceful shutdown.
	Start(ctx context.Context) error
}

// HealthChecker is an optional interface for components that support health checks.
//
// Components implementing this interface will have their health status
// periodically checked and exposed via the registry's status endpoint.
type HealthChecker interface {
	// HealthCheck returns nil if the component is healthy, or an error describing
	// the health issue.
	HealthCheck() error
}

// Status represents the current lifecycle state of a component.
type Status string

const (
	// StatusPending indicates the component has been registered but not yet started.
	StatusPending Status = "pending"

	// StatusStarting indicates the component is in the process of starting.
	StatusStarting Status = "starting"

	// StatusRunning indicates the component is running normally.
	StatusRunning Status = "running"

	// StatusFailed indicates the component failed to start or encountered a fatal error.
	StatusFailed Status = "failed"

	// StatusStopped indicates the component has been gracefully stopped.
	StatusStopped Status = "stopped"

	// StatusStandby indicates the component is intentionally not active.
	// This is used for leader-only components on non-leader pods that are waiting
	// for potential leadership acquisition. Unlike StatusPending (which implies
	// "about to start"), StatusStandby means "waiting for conditions to be met".
	StatusStandby Status = "standby"
)

// ComponentInfo provides information about a registered component.
type ComponentInfo struct {
	// Name is the component's unique identifier.
	Name string `json:"name"`

	// Status is the current lifecycle status.
	Status Status `json:"status"`

	// LeaderOnly indicates if this component runs only on the leader.
	LeaderOnly bool `json:"leader_only,omitempty"`

	// Error contains the last error message if Status is Failed.
	Error string `json:"error,omitempty"`

	// Healthy indicates the result of the last health check (if supported).
	// nil means health check not supported, true means healthy, false means unhealthy.
	Healthy *bool `json:"healthy,omitempty"`
}

// CriticalityLevel defines how important a component is to the system.
type CriticalityLevel int

const (
	// CriticalityCritical means the system cannot function without this component.
	// If a critical component fails, the entire system should be considered unhealthy.
	CriticalityCritical CriticalityLevel = iota

	// CriticalityDegradable means the system can function with reduced capability
	// if this component fails.
	CriticalityDegradable

	// CriticalityOptional means the system can function normally without this component.
	// Optional components typically provide non-essential features.
	CriticalityOptional
)

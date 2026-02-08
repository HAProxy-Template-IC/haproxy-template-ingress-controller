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

// Package sections provides factory functions for creating HAProxy configuration operations.
//
// These factory functions use generic operation types to eliminate repetitive boilerplate
// while maintaining type safety and compile-time verification.
package sections

import (
	"context"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// Operation defines the interface for all HAProxy configuration operations.
// This interface is implemented by all generic operation types (TopLevelOp, IndexChildOp, etc.)
type Operation interface {
	// Type returns the operation type (Create, Update, Delete)
	Type() OperationType

	// Section returns the configuration section this operation affects
	Section() string

	// Priority returns the execution priority (lower = first for creates, higher = first for deletes)
	Priority() int

	// Execute performs the operation via the Dataplane API
	Execute(ctx context.Context, c *client.DataplaneClient, txID string) error

	// Describe returns a human-readable description of the operation
	Describe() string
}

// RuntimeReloadTracker is an optional interface for operations that can track
// whether they triggered a HAProxy reload during runtime execution.
// Only server update operations implement this interface since they can be
// executed via the runtime API without a transaction.
type RuntimeReloadTracker interface {
	// TriggeredReload returns true if the operation triggered a HAProxy reload
	// during its last execution. This is only meaningful for runtime-eligible
	// operations (server updates with empty transaction ID).
	TriggeredReload() bool
}

// ptrStr safely dereferences a string pointer, returning empty string if nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// unknownIdentifier is the fallback identifier used when a model field is empty.
const unknownIdentifier = "<unknown>"

// NewBackendCreate creates an operation to create a backend.
func NewBackendCreate(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"backend",
		PriorityBackend,
		backend,
		IdentityBackend,
		BackendName,
		executors.BackendCreate(),
		DescribeTopLevel(OperationCreate, "backend", backend.Name),
	)
}

// NewBackendUpdate creates an operation to update a backend.
func NewBackendUpdate(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"backend",
		PriorityBackend,
		backend,
		IdentityBackend,
		BackendName,
		executors.BackendUpdate(),
		DescribeTopLevel(OperationUpdate, "backend", backend.Name),
	)
}

// NewBackendDelete creates an operation to delete a backend.
func NewBackendDelete(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"backend",
		PriorityBackend,
		backend,
		NilBackend,
		BackendName,
		executors.BackendDelete(),
		DescribeTopLevel(OperationDelete, "backend", backend.Name),
	)
}

// NewFrontendCreate creates an operation to create a frontend.
func NewFrontendCreate(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"frontend",
		PriorityFrontend,
		frontend,
		IdentityFrontend,
		FrontendName,
		executors.FrontendCreate(),
		DescribeTopLevel(OperationCreate, "frontend", frontend.Name),
	)
}

// NewFrontendUpdate creates an operation to update a frontend.
func NewFrontendUpdate(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"frontend",
		PriorityFrontend,
		frontend,
		IdentityFrontend,
		FrontendName,
		executors.FrontendUpdate(),
		DescribeTopLevel(OperationUpdate, "frontend", frontend.Name),
	)
}

// NewFrontendDelete creates an operation to delete a frontend.
func NewFrontendDelete(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"frontend",
		PriorityFrontend,
		frontend,
		NilFrontend,
		FrontendName,
		executors.FrontendDelete(),
		DescribeTopLevel(OperationDelete, "frontend", frontend.Name),
	)
}

// NewDefaultsCreate creates an operation to create a defaults section.
func NewDefaultsCreate(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"defaults",
		PriorityDefaults,
		defaults,
		IdentityDefaults,
		DefaultsName,
		executors.DefaultsCreate(),
		DescribeTopLevel(OperationCreate, "defaults section", defaults.Name),
	)
}

// NewDefaultsUpdate creates an operation to update a defaults section.
func NewDefaultsUpdate(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"defaults",
		PriorityDefaults,
		defaults,
		IdentityDefaults,
		DefaultsName,
		executors.DefaultsUpdate(),
		DescribeTopLevel(OperationUpdate, "defaults section", defaults.Name),
	)
}

// NewDefaultsDelete creates an operation to delete a defaults section.
func NewDefaultsDelete(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"defaults",
		PriorityDefaults,
		defaults,
		NilDefaults,
		DefaultsName,
		executors.DefaultsDelete(),
		DescribeTopLevel(OperationDelete, "defaults section", defaults.Name),
	)
}

// NewGlobalUpdate creates an operation to update the global section.
func NewGlobalUpdate(global *models.Global) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"global",
		PriorityGlobal,
		global,
		IdentityGlobal,
		executors.GlobalUpdate(),
		func() string { return "Update global section" },
	)
}

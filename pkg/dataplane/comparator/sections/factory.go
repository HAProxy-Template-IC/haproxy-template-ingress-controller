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

// Top-level CRUD builders for core sections.
var (
	backendOps = NewTopLevelCRUD(
		"backend", "backend", PriorityBackend, BackendName,
		executors.BackendCreate(), executors.BackendUpdate(), executors.BackendDelete(),
	)
	frontendOps = NewTopLevelCRUD(
		"frontend", "frontend", PriorityFrontend, FrontendName,
		executors.FrontendCreate(), executors.FrontendUpdate(), executors.FrontendDelete(),
	)
	defaultsOps = NewTopLevelCRUD(
		"defaults", "defaults section", PriorityDefaults, DefaultsName,
		executors.DefaultsCreate(), executors.DefaultsUpdate(), executors.DefaultsDelete(),
	)
)

// NewBackendCreate creates an operation to create a backend.
func NewBackendCreate(backend *models.Backend) Operation { return backendOps.Create(backend) }

// NewBackendUpdate creates an operation to update a backend.
func NewBackendUpdate(backend *models.Backend) Operation { return backendOps.Update(backend) }

// NewBackendDelete creates an operation to delete a backend.
func NewBackendDelete(backend *models.Backend) Operation { return backendOps.Delete(backend) }

// NewFrontendCreate creates an operation to create a frontend.
func NewFrontendCreate(frontend *models.Frontend) Operation { return frontendOps.Create(frontend) }

// NewFrontendUpdate creates an operation to update a frontend.
func NewFrontendUpdate(frontend *models.Frontend) Operation { return frontendOps.Update(frontend) }

// NewFrontendDelete creates an operation to delete a frontend.
func NewFrontendDelete(frontend *models.Frontend) Operation { return frontendOps.Delete(frontend) }

// NewDefaultsCreate creates an operation to create a defaults section.
func NewDefaultsCreate(defaults *models.Defaults) Operation { return defaultsOps.Create(defaults) }

// NewDefaultsUpdate creates an operation to update a defaults section.
func NewDefaultsUpdate(defaults *models.Defaults) Operation { return defaultsOps.Update(defaults) }

// NewDefaultsDelete creates an operation to delete a defaults section.
func NewDefaultsDelete(defaults *models.Defaults) Operation { return defaultsOps.Delete(defaults) }

// NewGlobalUpdate creates an operation to update the global section.
func NewGlobalUpdate(global *models.Global) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"global",
		PriorityGlobal,
		global,
		Identity[*models.Global],
		executors.GlobalUpdate(),
		func() string { return "Update global section" },
	)
}

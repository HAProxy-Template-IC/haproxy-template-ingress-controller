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
// Operations describe a single diff entry produced by the comparator. They are
// consumed by:
//   - the orchestrator (to decide whether the diff is fully runtime-eligible,
//     and to build the X-Runtime-Actions header for server field updates),
//   - logging / metrics (Section + Describe).
//
// They do NOT execute themselves: the orchestrator pushes the full rendered
// config via the dataplane API's raw endpoint, no per-section API call exists.
package sections

import (
	"github.com/haproxytech/client-native/v6/models"
)

// Operation describes a single diff entry produced by the comparator.
type Operation interface {
	// Type returns the operation type (Create, Update, Delete).
	Type() OperationType

	// Section returns the configuration section this operation affects.
	Section() string

	// Describe returns a human-readable description of the operation.
	Describe() string
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
	backendOps  = NewTopLevelCRUD("backend", "backend", BackendName)
	frontendOps = NewTopLevelCRUD("frontend", "frontend", FrontendName)
	defaultsOps = NewTopLevelCRUD("defaults", "defaults section", DefaultsName)
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
func NewGlobalUpdate(_ *models.Global) Operation {
	return newOp(
		OperationUpdate,
		"global",
		func() string { return "Update global section" },
	)
}

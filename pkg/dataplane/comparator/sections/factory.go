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
	Type() OperationType

	Section() string

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
	BackendOps  = NewTopLevelCRUD("backend", "backend", backendNameFn)
	FrontendOps = NewTopLevelCRUD("frontend", "frontend", frontendNameFn)
	DefaultsOps = NewTopLevelCRUD("defaults", "defaults section", defaultsNameFn)
)

// NewGlobalUpdate creates an operation to update the global section.
func NewGlobalUpdate(_ *models.Global) Operation {
	return newOp(
		OperationUpdate,
		"global",
		func() string { return "Update global section" },
	)
}

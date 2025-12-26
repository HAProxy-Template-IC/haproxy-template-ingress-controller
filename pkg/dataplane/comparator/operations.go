// Package comparator provides fine-grained configuration comparison and operation generation
// for HAProxy Dataplane API synchronization.
//
// The comparator performs attribute-level comparison between current and desired configurations,
// generating the minimal set of operations needed to transform one into the other.
package comparator

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// Operation represents a single configuration change operation.
//
// Operations are executed within transactions and map to specific
// Dataplane API endpoints for atomic configuration updates.
type Operation interface {
	// Type returns the operation type (Create, Update, Delete)
	Type() sections.OperationType

	// Section returns the configuration section this operation affects
	// (e.g., "backend", "server", "frontend", "acl")
	Section() string

	// Priority returns the execution priority for dependency ordering.
	// Lower priority operations are executed first for Creates,
	// higher priority operations are executed first for Deletes.
	Priority() int

	// Execute performs the operation using the Dataplane API client.
	// The transactionID parameter should be included in API calls for
	// atomic transaction management.
	Execute(ctx context.Context, client *client.DataplaneClient, transactionID string) error

	// Describe returns a human-readable description of the operation
	// for logging and debugging.
	Describe() string
}

// Package comparator provides fine-grained configuration comparison and operation generation
// for HAProxy Dataplane API synchronization.
//
// The comparator performs attribute-level comparison between current and desired configurations,
// generating the minimal set of operations needed to transform one into the other.
package comparator

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// Operation represents a single configuration change operation.
//
// Operations are executed within transactions and map to specific
// Dataplane API endpoints for atomic configuration updates.
//
// This is an alias for sections.Operation: factory functions in the
// sections package return the same interface that the comparator surfaces
// to the rest of the dataplane package.
type Operation = sections.Operation

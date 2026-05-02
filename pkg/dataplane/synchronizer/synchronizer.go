// Package synchronizer executes a list of comparator operations against the
// HAProxy Dataplane API inside an open transaction.
//
// The only exported entry point is SyncOperations, which groups operations by
// priority and runs each group in parallel (up to maxParallel) before moving on
// to the next priority. Execution stops at the first error.
package synchronizer

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
)

// SyncOperationsResult contains information about a synchronization operation.
type SyncOperationsResult struct {
	// ReloadTriggered indicates whether a HAProxy reload was triggered.
	// Populated by the caller after the transaction commits.
	ReloadTriggered bool

	// ReloadID is the reload identifier from the Reload-ID response header.
	// Populated by the caller after the transaction commits.
	ReloadID string
}

// SyncOperations executes operations inside the provided transaction.
//
// Operations are grouped by their Priority(): priority groups run sequentially
// (lower first), and operations inside the same group run in parallel up to
// maxParallel (0 = unlimited). Execution stops at the first error.
//
// The caller is expected to commit the transaction after SyncOperations returns
// successfully and to populate ReloadTriggered / ReloadID from the commit
// response.
//
// Example:
//
//	// dpClient avoids shadowing the imported `client` package.
//	adapter := client.NewVersionAdapter(dpClient, 3)
//	err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
//	    _, err := synchronizer.SyncOperations(ctx, dpClient, diff.Operations, tx, 80)
//	    return err
//	})
func SyncOperations(ctx context.Context, dpClient *client.DataplaneClient, operations []comparator.Operation, tx *client.Transaction, maxParallel int) (*SyncOperationsResult, error) {
	if len(operations) == 0 {
		return &SyncOperationsResult{}, nil
	}

	groups := groupByPriority(operations)
	for _, priority := range sortedPriorityKeys(groups) {
		if err := executePriorityGroup(ctx, dpClient, groups[priority], tx.ID, maxParallel); err != nil {
			return nil, err
		}
	}

	return &SyncOperationsResult{}, nil
}

// executePriorityGroup runs every operation in the group in parallel. It stops
// at the first error and returns it (wrapped with the operation description).
func executePriorityGroup(ctx context.Context, dpClient *client.DataplaneClient, ops []comparator.Operation, txID string, maxParallel int) error {
	g, gCtx := errgroup.WithContext(ctx)
	if maxParallel > 0 {
		g.SetLimit(maxParallel)
	}

	for _, op := range ops {
		g.Go(func() error {
			if err := op.Execute(gCtx, dpClient, txID); err != nil {
				return fmt.Errorf("operation %q failed: %w", op.Describe(), err)
			}
			return nil
		})
	}

	return g.Wait()
}

// groupByPriority groups operations by their Priority() level. Operations with
// the same priority have no ordering dependencies and are safe to parallelise.
func groupByPriority(ops []comparator.Operation) map[int][]comparator.Operation {
	groups := make(map[int][]comparator.Operation)
	for _, op := range ops {
		groups[op.Priority()] = append(groups[op.Priority()], op)
	}
	return groups
}

// sortedPriorityKeys returns priority keys in ascending order so lower-priority
// (dependency) operations run first.
func sortedPriorityKeys(groups map[int][]comparator.Operation) []int {
	keys := make([]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

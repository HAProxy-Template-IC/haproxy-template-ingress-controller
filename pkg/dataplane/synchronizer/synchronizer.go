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

// executePriorityGroup runs operations in a priority group, serialising ops
// that share a non-empty Parent() and parallelising ops with different (or
// empty) parents.
//
// Why split on parent: HAProxy 3.0's Dataplane API has been observed
// returning 404 on one of two concurrent DELETE calls against children of
// the same parent (test_integration:[3.0] frontend-remove-binds,
// 2026-05-20: two parallel `DELETE /.../frontends/http-in/binds/*:N`
// requests inside the same transaction — one 202, one 404, even though
// both binds existed when the transaction opened). With per-parent
// serialisation, same-parent ops execute sequentially within a single
// goroutine, while ops on different parents still fan out across
// goroutines up to `maxParallel`.
//
// Ops returning Parent() == "" (top-level resources, singletons, server
// updates that route through the runtime API) have no parent constraint
// and each get their own goroutine.
//
// Stops at the first error (returned wrapped with the operation
// description).
func executePriorityGroup(ctx context.Context, dpClient *client.DataplaneClient, ops []comparator.Operation, txID string, maxParallel int) error {
	g, gCtx := errgroup.WithContext(ctx)
	if maxParallel > 0 {
		g.SetLimit(maxParallel)
	}

	// Group child ops by Parent(). Same-parent ops share one goroutine
	// and run sequentially; everything else (different parents, plus
	// every parent-less op) gets its own goroutine and runs in parallel
	// up to `maxParallel`.
	byParent := make(map[string][]comparator.Operation)
	parentOrder := make([]string, 0)
	var parentless []comparator.Operation
	for _, op := range ops {
		p := op.Parent()
		if p == "" {
			parentless = append(parentless, op)
			continue
		}
		if _, seen := byParent[p]; !seen {
			parentOrder = append(parentOrder, p)
		}
		byParent[p] = append(byParent[p], op)
	}

	for _, op := range parentless {
		g.Go(func() error { return runOps(gCtx, dpClient, txID, []comparator.Operation{op}) })
	}

	for _, parent := range parentOrder {
		parentOps := byParent[parent]
		g.Go(func() error { return runOps(gCtx, dpClient, txID, parentOps) })
	}

	return g.Wait()
}

// runOps executes ops sequentially, checking ctx between each so a
// sibling goroutine's failure (which cancels ctx via
// errgroup.WithContext) short-circuits the rest of this chain instead
// of dispatching doomed HTTP requests against a cancelled ctx. Without
// the ctx.Err() check, an early-op success here followed by a sibling
// failure would still send every remaining same-parent op to the
// dataplane API, racking up cancellation errors across multiple
// in-flight requests against a transaction that's already aborting.
func runOps(ctx context.Context, dpClient *client.DataplaneClient, txID string, ops []comparator.Operation) error {
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := op.Execute(ctx, dpClient, txID); err != nil {
			return fmt.Errorf("operation %q failed: %w", op.Describe(), err)
		}
	}
	return nil
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

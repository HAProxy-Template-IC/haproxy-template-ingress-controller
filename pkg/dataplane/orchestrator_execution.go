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

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/synchronizer"
)

// executeFineGrainedSync runs a fine-grained sync using pre-computed diffs.
// Accepts pre-computed diffs to avoid redundant comparison when they are
// already known. Returns (result, auxFilesSynced, error); the auxFilesSynced
// flag lets the caller's raw-push fallback skip redundant aux file work.
//
// Runs the three phases defined in phases.go in order (PhasePreConfig,
// PhaseConfig, PhasePostConfig) and finally verifies the triggered reload.
func (o *orchestrator) executeFineGrainedSync(
	ctx context.Context,
	diff *comparator.ConfigDiff,
	opts *SyncOptions,
	fileDiff *auxiliaryfiles.FileDiff,
	sslDiff *auxiliaryfiles.SSLCertificateDiff,
	caFileDiff *auxiliaryfiles.SSLCaFileDiff,
	mapDiff *auxiliaryfiles.MapFileDiff,
	crtlistDiff *auxiliaryfiles.CRTListDiff,
	startTime time.Time,
) (*SyncResult, bool, error) {
	// PhasePreConfig: sync auxiliary files and verify any reloads they
	// trigger, so PhaseConfig doesn't race against pending file reloads.
	auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, fileDiff, sslDiff, caFileDiff, mapDiff)
	if err != nil {
		return nil, false, err
	}
	if err := o.verifyAuxiliaryReloads(ctx, auxReloadIDs, opts, "before config sync"); err != nil {
		return nil, false, err
	}
	auxFilesSynced := true

	// PhaseConfig: apply the HAProxy configuration change.
	appliedOps, reloadTriggered, reloadID, retries, err := o.executeConfigOperations(ctx, diff, opts)
	if err != nil {
		return nil, auxFilesSynced, err
	}

	// Verify reload BEFORE PhasePostConfig deletes orphaned files.
	//
	// Why this ordering matters: the transaction commit triggers a reload
	// asynchronously. The Dataplane API writes the new config to disk, then
	// signals HAProxy to spawn a new worker. The new worker parses
	// haproxy.cfg from disk and lazily opens any referenced map / cert files.
	// If we delete an orphaned aux file *while* the new worker is still
	// reading the on-disk config, an unrelated transient state in the
	// running config (or a comparator op the diff missed) can leave the
	// new worker dereferencing a path we just unlinked, and HAProxy aborts
	// the reload with `failed to open pattern file <maps/X.map>`. After
	// verifyReload returns success the new worker has loaded everything
	// it needs into memory and an unlink no longer affects it.
	//
	// On verification failure we deliberately skip the post-config delete:
	// keeping orphans is harmless (a few stale files in DataPlane storage),
	// while deleting them while the running config still references them
	// turns a recoverable failure into a stuck reload loop. The next
	// successful reconcile will clean them up.
	reloadVerified := false
	if reloadTriggered && reloadID == "" {
		// Synchronous forceReload (status 200) — reload already finished.
		reloadVerified = true
	} else if reloadTriggered && opts.VerifyReload {
		if verifyErr := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); verifyErr != nil {
			o.logger.Error("Fine-grained sync completed but reload verification failed; skipping orphan aux-file delete to avoid mid-reload race",
				"operations", len(appliedOps),
				"reload_id", reloadID,
				"error", verifyErr)

			result := &SyncResult{
				Success:                 false,
				AppliedOperations:       appliedOps,
				ReloadTriggered:         reloadTriggered,
				ReloadID:                reloadID,
				ReloadVerified:          false,
				ReloadVerificationError: verifyErr.Error(),
				SyncMode:                SyncModeFineGrained,
				Duration:                time.Since(startTime),
				Retries:                 max(0, retries-1),
				Details:                 convertDiffSummary(&diff.Summary),
			}
			return result, auxFilesSynced, &SyncError{
				Stage:   "reload_verification",
				Message: "reload verification failed",
				Cause:   verifyErr,
				Hints: []string{
					"HAProxy reload failed, config may have been reverted",
					hintCheckHAProxyLogs,
				},
			}
		}
		reloadVerified = true
	}

	// PhasePostConfig: delete auxiliary files the new config no longer
	// references. Safe only after the reload triggered by PhaseConfig has
	// fully verified — otherwise the new worker may still be reading the
	// on-disk config and a delete races against its file open.
	//
	// Gate explicitly on reloadVerified rather than relying on the
	// conditions above: if the caller disables VerifyReload (test
	// fixtures today, but possibly production paths later) we'd
	// otherwise fall through and re-introduce the race the reorder is
	// trying to prevent. When no reload was triggered (config-only
	// change with all runtime-eligible ops), there's nothing to wait
	// for and the delete is also safe.
	if !reloadTriggered || reloadVerified {
		o.deleteUnreferencedFilesPostConfig(ctx, fileDiff, sslDiff, caFileDiff, mapDiff)
	} else {
		o.logger.Info("Skipping orphan aux-file delete: reload not verified (VerifyReload disabled)",
			"reload_id", reloadID)
	}

	// Build result
	auxDiffs := &auxiliaryFileDiffs{
		fileDiff:    fileDiff,
		sslDiff:     sslDiff,
		caFileDiff:  caFileDiff,
		mapDiff:     mapDiff,
		crtlistDiff: crtlistDiff,
	}

	details := convertDiffSummary(&diff.Summary)
	addAuxiliaryFileCounts(&details, auxDiffs)

	// Merge config operations with aux file operations for consistent view
	appliedOps = append(appliedOps, auxDiffsToOperations(auxDiffs)...)

	result := &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   reloadTriggered,
		ReloadID:          reloadID,
		ReloadVerified:    reloadVerified,
		SyncMode:          SyncModeFineGrained,
		Duration:          time.Since(startTime),
		Retries:           max(0, retries-1),
		Details:           details,
		Message:           fmt.Sprintf("Successfully applied %d operations", len(appliedOps)),
	}

	// Capture post-sync version for caller's cache
	postVersion, postVersionErr := o.client.GetVersion(ctx)
	if postVersionErr != nil {
		o.logger.Debug("Failed to get post-sync version for caching", "error", postVersionErr)
	} else {
		result.PostSyncVersion = postVersion
	}

	o.logger.Debug("fine-grained sync completed",
		"operations", len(appliedOps),
		"reload_triggered", reloadTriggered,
		"reload_verified", result.ReloadVerified,
		"retries", max(0, retries-1),
		"duration", time.Since(startTime))

	return result, auxFilesSynced, nil
}

// areAllOperationsRuntimeEligible checks if all operations can be executed via the optimized
// runtime path (skip_reload + X-Runtime-Actions) without triggering a HAProxy reload.
//
// Both conditions must hold:
//  1. All operations are server UPDATE operations (no creates/deletes/other sections)
//  2. All changed fields within each server update are in the runtime-supported set
//     (weight, address, port, maintenance, agent-check, agent-addr, agent-send, health_check_port)
//
// Condition 2 prevents the optimized path from silently skipping a reload that would be required
// for non-runtime-eligible field changes (e.g., check, ssl). Without this guard, such changes
// would be written to disk but not applied at runtime until the next HAProxy reload.
func (o *orchestrator) areAllOperationsRuntimeEligible(operations []comparator.Operation) bool {
	if len(operations) == 0 {
		return false
	}

	for _, op := range operations {
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if !ok || op.Type() != sections.OperationUpdate {
			return false
		}
		if !serverOp.IsFullyRuntimeEligible() {
			return false
		}
	}

	return true
}

// executeRuntimeOperations executes runtime-eligible operations without transaction.
// Uses version caching to minimize GetVersion calls: fetches version once at start,
// then passes it to each operation and increments after success.
//
// Performance optimization: Without caching, each server update calls GetVersion(),
// resulting in 2N HTTP calls for N operations. With caching, only N+1 calls are made
// (1 initial GetVersion + N update calls), cutting runtime in half.
//
// Correctness: On 409 version conflict, the function re-fetches the version and retries.
// Returns applied operations, reload count, and error.
func (o *orchestrator) executeRuntimeOperations(
	ctx context.Context,
	operations []comparator.Operation,
) (appliedOps []AppliedOperation, reloadCount int, err error) {
	if len(operations) == 0 {
		return nil, 0, nil
	}

	// Fetch version once at start for caching
	version, err := o.client.GetVersion(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("getting initial version: %w", err)
	}

	for i, op := range operations {
		// All runtime operations are server updates (checked by areAllOperationsRuntimeEligible)
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if !ok {
			// Fallback for unexpected operation types - use standard Execute
			if execErr := op.Execute(ctx, o.client, ""); execErr != nil {
				return nil, reloadCount, fmt.Errorf("runtime operation %d failed: %w", i, execErr)
			}
			// Check if this operation triggered a reload
			if tracker, ok := op.(sections.RuntimeReloadTracker); ok && tracker.TriggeredReload() {
				reloadCount++
			}
			continue
		}

		// Execute with cached version and retry on conflict
		reloaded, execErr := o.executeServerUpdateWithRetry(ctx, serverOp, &version)
		if execErr != nil {
			return nil, reloadCount, fmt.Errorf("runtime operation %d failed: %w", i, execErr)
		}
		if reloaded {
			reloadCount++
		}
	}

	return convertOperationsToApplied(operations), reloadCount, nil
}

// executeServerUpdateWithRetry executes a server update with version caching and retry on 409.
// On success, increments the version for the next operation.
// On 409 conflict, re-fetches the version and retries up to maxRetries times.
func (o *orchestrator) executeServerUpdateWithRetry(
	ctx context.Context,
	op *sections.ServerUpdateOp,
	version *int64,
) (reloadTriggered bool, err error) {
	const maxRetries = 3

	for attempt := range maxRetries {
		reloaded, err := executors.ServerUpdateWithReloadTracking(
			ctx, o.client, op.BackendName(), op.ServerName(), op.Server(), "", *version)

		if err == nil {
			// Success - increment version for next operation
			*version++
			return reloaded, nil
		}

		// Check for version conflict
		conflictErr, ok := errors.AsType[*client.VersionConflictError](err)
		if !ok {
			// Not a version conflict - return the error
			return false, err
		}

		// Re-fetch version and retry
		o.logger.Debug("Version conflict during runtime operation, retrying",
			"attempt", attempt+1,
			"expected_version", conflictErr.ExpectedVersion,
			"actual_version", conflictErr.ActualVersion)

		newVersion, fetchErr := o.client.GetVersion(ctx)
		if fetchErr != nil {
			return false, fmt.Errorf("re-fetching version after conflict: %w", fetchErr)
		}
		*version = newVersion
	}

	return false, fmt.Errorf("server update failed after %d retries due to version conflicts", maxRetries)
}

// executeConfigOperations executes configuration operations with retry logic.
// Returns applied operations, reload status, reload ID, retry count, and error.
func (o *orchestrator) executeConfigOperations(
	ctx context.Context,
	diff *comparator.ConfigDiff,
	opts *SyncOptions,
) (appliedOps []AppliedOperation, reloadTriggered bool, reloadID string, retries int, err error) {
	// If there are no config operations, skip sync entirely (no reload needed)
	// This happens when only auxiliary files changed
	if len(diff.Operations) == 0 {
		o.logger.Debug("No configuration operations to execute (auxiliary files only)")
		return nil, false, "", 0, nil
	}

	// Execute configuration operations
	adapter := client.NewVersionAdapter(o.client, opts.MaxRetries)

	// Check if all operations are runtime-eligible (server UPDATE only)
	// Runtime-eligible operations can be executed without reload via Runtime API
	allRuntimeEligible := o.areAllOperationsRuntimeEligible(diff.Operations)

	var commitResult *client.CommitResult

	if allRuntimeEligible {
		// Execute runtime-eligible operations without transaction.
		// areAllOperationsRuntimeEligible guarantees all changed fields are in the
		// runtime-supported set, so each ReplaceServerBackend call returns 200 (no reload).
		o.logger.Debug("All operations are runtime-eligible, executing without transaction")

		var runtimeReloads int
		appliedOps, runtimeReloads, err = o.executeRuntimeOperations(ctx, diff.Operations)
		retries = 1
		reloadTriggered = runtimeReloads > 0

		if err == nil && runtimeReloads > 0 {
			o.logger.Debug("Runtime operations triggered reloads",
				"reload_count", runtimeReloads,
				"total_operations", len(diff.Operations))
		}
	} else {
		// Execute with transaction (triggers reload)
		commitResult, err = adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
			retries++
			o.logger.Debug("Executing fine-grained sync",
				"attempt", retries,
				"transaction_id", tx.ID,
				"version", tx.Version)

			// Execute operations within the transaction
			_, err := synchronizer.SyncOperations(ctx, o.client, diff.Operations, tx, opts.MaxParallel)
			if err != nil {
				return err
			}

			// Convert operations to AppliedOperation (do this here while we have access to operations)
			appliedOps = convertOperationsToApplied(diff.Operations)

			return nil
			// VersionAdapter will commit the transaction after this callback returns
		})

		// Extract reload information from commit result (if successful).
		// Status 202 = async reload (has ReloadID for polling).
		// Status 200 = synchronous reload via forceReload (no ReloadID, already done).
		// In both cases a reload was triggered.
		if err == nil && commitResult != nil {
			reloadTriggered = commitResult.StatusCode == 202 || commitResult.StatusCode == 200
			reloadID = commitResult.ReloadID
		}
	}

	if err != nil {
		// Check if it's a version conflict error
		if versionConflictErr, ok := errors.AsType[*client.VersionConflictError](err); ok {
			return nil, false, "", retries, NewConflictError(retries, versionConflictErr.ExpectedVersion, versionConflictErr.ActualVersion)
		}

		// Other errors - return with details
		return nil, false, "", retries, &SyncError{
			Stage:   stageApply,
			Message: "failed to apply configuration changes",
			Cause:   err,
			Hints: []string{
				"Review the error message for specific operation failures",
				hintCheckHAProxyLogs,
				"Verify all resource references are valid",
			},
		}
	}

	return appliedOps, reloadTriggered, reloadID, retries, nil
}

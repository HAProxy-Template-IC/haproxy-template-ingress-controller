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

// attemptFineGrainedSyncWithDiffs attempts fine-grained sync with pre-computed auxiliary file diffs.
// This version accepts pre-computed diffs to avoid redundant comparison when diffs are already known.
// Returns (result, auxFilesSynced, error) where auxFilesSynced indicates if Phase 1 completed successfully.
// This is used to avoid re-syncing aux files in fallback if they were already synced.
func (o *orchestrator) attemptFineGrainedSyncWithDiffs(
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
	// Phase 1: Sync auxiliary files (pre-config) using pre-computed diffs
	auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, fileDiff, sslDiff, caFileDiff, mapDiff)
	if err != nil {
		return nil, false, err
	}

	// Phase 1.5: Verify auxiliary file reloads completed BEFORE config operations
	// This prevents the race condition where config operations reference files before their reloads complete.
	if err := o.verifyAuxiliaryReloads(ctx, auxReloadIDs, opts, "before config sync"); err != nil {
		return nil, false, err
	}

	// At this point, aux files are synced successfully (Phase 1 complete)
	auxFilesSynced := true

	// Phase 2: Execute configuration sync with retry logic
	appliedOps, reloadTriggered, reloadID, retries, err := o.executeConfigOperations(ctx, diff, opts)
	if err != nil {
		return nil, auxFilesSynced, err
	}

	// Phase 3: Delete obsolete files AFTER successful config sync
	o.deleteObsoleteFilesPostConfig(ctx, fileDiff, sslDiff, caFileDiff, mapDiff)

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
		SyncMode:          SyncModeFineGrained,
		Duration:          time.Since(startTime),
		Retries:           max(0, retries-1),
		Details:           details,
		Message:           fmt.Sprintf("Successfully applied %d operations", len(appliedOps)),
	}

	// Phase 4: Verify reload if triggered and verification enabled
	if reloadTriggered && opts.VerifyReload {
		if err := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); err != nil {
			result.Success = false
			result.ReloadVerified = false
			result.ReloadVerificationError = err.Error()
			result.Duration = time.Since(startTime)

			o.logger.Error("Fine-grained sync completed but reload verification failed",
				"operations", len(appliedOps),
				"reload_id", reloadID,
				"error", err)

			return result, auxFilesSynced, &SyncError{
				Stage:   "reload_verification",
				Message: "reload verification failed",
				Cause:   err,
				Hints: []string{
					"HAProxy reload failed, config may have been reverted",
					"Check HAProxy logs for detailed error information",
				},
			}
		}
		result.ReloadVerified = true
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

// executeRawPush performs raw configuration push with configurable behavior.
// This method is used for both intentional raw push (version=1, threshold exceeded) and fallback scenarios.
//
// Parameters:
//   - version: The current config version for optimistic locking. Version is incremented after push.
//   - mode: The SyncMode to record (SyncModeRawInitial, SyncModeRawThreshold, or SyncModeRawFallback)
//   - auxFilesAlreadySynced: If true, Phase 1 is skipped because aux files were already synced
//
// Uses the same auxiliary file sync and reload verification as the fine-grained path.
func (o *orchestrator) executeRawPush(ctx context.Context, desiredConfig string, diff *comparator.ConfigDiff, auxDiffs *auxiliaryFileDiffs, opts *SyncOptions, startTime time.Time, version int64, mode SyncMode, auxFilesAlreadySynced bool) (*SyncResult, error) {
	// Log at debug level - raw pushes are normal operational behavior
	o.logger.Debug("Executing raw configuration push", "mode", mode)

	// Phase 1: Sync auxiliary files BEFORE pushing raw config (same as fine-grained sync)
	// Files must exist before HAProxy validates the configuration.
	// Skip if aux files were already synced in the failed fine-grained sync attempt.
	if !auxFilesAlreadySynced {
		auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff)
		if err != nil {
			return nil, err
		}

		// Phase 1.5: Verify auxiliary file reloads completed BEFORE raw config push
		// This prevents the race condition where config operations reference files before their reloads complete.
		if err := o.verifyAuxiliaryReloads(ctx, auxReloadIDs, opts, "before raw config push"); err != nil {
			return nil, err
		}
	} else {
		o.logger.Info("Skipping aux file sync in fallback - already synced in fine-grained attempt")
	}

	// Phase 2: Push raw configuration (now that auxiliary files exist and reloads verified)
	reloadID, err := o.client.PushRawConfiguration(ctx, desiredConfig, version)
	if err != nil {
		return nil, &SyncError{
			Stage:   "fallback",
			Message: "failed to push raw configuration",
			Cause:   err,
			Hints: []string{
				"The configuration may have fundamental issues",
				"Validate the configuration with: haproxy -c -f <config>",
				"Check HAProxy logs for detailed validation errors",
			},
		}
	}

	// Preserve detailed operation information from diff
	// Even though we used raw config push, we still know what changes were applied
	appliedOps := convertOperationsToApplied(diff.Operations)

	// Build result with detailed diff information
	details := convertDiffSummary(&diff.Summary)
	addAuxiliaryFileCounts(&details, auxDiffs)

	// Merge config operations with aux file operations for consistent view
	appliedOps = append(appliedOps, auxDiffsToOperations(auxDiffs)...)

	result := &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps, // All operations including aux files
		ReloadTriggered:   true,       // Raw push always triggers reload
		ReloadID:          reloadID,
		SyncMode:          mode,
		Duration:          time.Since(startTime),
		Retries:           0,
		Details:           details,
		Message:           fmt.Sprintf("Successfully applied %d operations via raw config push (%s)", len(appliedOps), mode),
	}

	// Verify reload if verification enabled (raw push always triggers reload)
	if opts.VerifyReload {
		if err := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); err != nil {
			result.Success = false
			result.ReloadVerified = false
			result.ReloadVerificationError = err.Error()
			result.Duration = time.Since(startTime)

			o.logger.Error("Raw config push completed but reload verification failed",
				"reload_id", reloadID,
				"error", err)

			return result, &SyncError{
				Stage:   "reload_verification",
				Message: "reload verification failed after raw config push",
				Cause:   err,
				Hints: []string{
					"HAProxy reload failed, config may have been reverted",
					"Check HAProxy logs for detailed error information",
				},
			}
		}
		result.ReloadVerified = true
	}

	// Capture post-sync version: raw push increments version by 1
	if version > 0 {
		result.PostSyncVersion = version + 1
	}

	o.logger.Debug("Raw configuration push completed successfully",
		"duration", time.Since(startTime),
		"reload_id", reloadID,
		"reload_verified", result.ReloadVerified)

	return result, nil
}

// areAllOperationsRuntimeEligible checks if all operations can be executed via Runtime API without reload.
//
// Currently, only server UPDATE operations are runtime-eligible because they can modify
// server parameters (weight, address, port, state) without requiring HAProxy reload.
//
// All other operations (creates, deletes, structural changes) require transactions and trigger reload.
func (o *orchestrator) areAllOperationsRuntimeEligible(operations []comparator.Operation) bool {
	if len(operations) == 0 {
		return false
	}

	for _, op := range operations {
		// Only server UPDATE operations are runtime-eligible
		// Server creates/deletes require transaction, other sections require transaction
		if op.Section() != "server" || op.Type() != sections.OperationUpdate {
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
		return nil, 0, fmt.Errorf("failed to get initial version: %w", err)
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

	for attempt := 0; attempt < maxRetries; attempt++ {
		reloaded, err := executors.ServerUpdateWithReloadTracking(
			ctx, o.client, op.BackendName(), op.ServerName(), op.Server(), "", *version)

		if err == nil {
			// Success - increment version for next operation
			*version++
			return reloaded, nil
		}

		// Check for version conflict
		var conflictErr *client.VersionConflictError
		if !errors.As(err, &conflictErr) {
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
			return false, fmt.Errorf("failed to re-fetch version after conflict: %w", fetchErr)
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
		// Execute runtime-eligible operations without transaction
		// Note: Runtime API may still trigger reloads if server fields outside
		// the runtime-supported set are modified (returns 202 instead of 200)
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

		// Extract reload information from commit result (if successful)
		if err == nil && commitResult != nil {
			reloadTriggered = commitResult.StatusCode == 202
			reloadID = commitResult.ReloadID
		}
	}

	if err != nil {
		// Check if it's a version conflict error
		var conflictErr *client.VersionConflictError
		if errors.As(err, &conflictErr) {
			return nil, false, "", retries, NewConflictError(retries, conflictErr.ExpectedVersion, conflictErr.ActualVersion)
		}

		// Other errors - return with details
		return nil, false, "", retries, &SyncError{
			Stage:   "apply",
			Message: "failed to apply configuration changes",
			Cause:   err,
			Hints: []string{
				"Review the error message for specific operation failures",
				"Check HAProxy logs for detailed error information",
				"Verify all resource references are valid",
			},
		}
	}

	return appliedOps, reloadTriggered, reloadID, retries, nil
}

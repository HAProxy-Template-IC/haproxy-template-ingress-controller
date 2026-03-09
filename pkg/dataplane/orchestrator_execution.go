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
	"strings"
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

	// Phase 4: Verify reload if triggered and verification enabled.
	// When reloadID is empty (synchronous forceReload), the reload already succeeded —
	// mark as verified without polling.
	if reloadTriggered && reloadID == "" {
		result.ReloadVerified = true
	} else if reloadTriggered && opts.VerifyReload {
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

	// Raw push always triggers a reload. When reloadID is empty (synchronous forceReload),
	// the reload already succeeded — mark as verified without polling.
	reloadVerified := reloadID == ""

	result := &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps, // All operations including aux files
		ReloadTriggered:   true,       // Raw push always triggers reload
		ReloadID:          reloadID,
		ReloadVerified:    reloadVerified,
		SyncMode:          mode,
		Duration:          time.Since(startTime),
		Retries:           0,
		Details:           details,
		Message:           fmt.Sprintf("Successfully applied %d operations via raw config push (%s)", len(appliedOps), mode),
	}

	// Verify reload via polling only when an async reload ID was returned
	if !reloadVerified && opts.VerifyReload {
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
			return false, fmt.Errorf("failed to re-fetch version after conflict: %w", fetchErr)
		}
		*version = newVersion
	}

	return false, fmt.Errorf("server update failed after %d retries due to version conflicts", maxRetries)
}

// buildRuntimeActions converts server update operations into the semicolon-separated
// X-Runtime-Actions string expected by the DataPlane API's skip_reload endpoint.
// Covers all fields from DataPlane API's RuntimeSupportedFields["server"] (handlers/runtime.go).
// All generated commands are verified as valid X-Runtime-Actions entries in
// the DataPlane API handler (handlers/raw.go:executeRuntimeActions).
//
// IMPORTANT: Keep in sync with serverRuntimeSupportedJSONFields in
// pkg/dataplane/comparator/sections/factory_server.go. Every field listed there must have
// a corresponding action generated here; otherwise IsFullyRuntimeEligible() will approve
// a change that this function silently ignores.
func buildRuntimeActions(operations []comparator.Operation) string {
	var actions []string
	for _, op := range operations {
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if !ok {
			continue
		}
		s := serverOp.Server()
		b := serverOp.BackendName()
		n := serverOp.ServerName()

		// Address+Port: both applied atomically (matches changeThroughRuntimeAPI behavior)
		if s.Port != nil {
			actions = append(actions, fmt.Sprintf("SetServerAddr %s %s %s %d", b, n, s.Address, *s.Port))
		}

		// Admin state: maintenance "enabled" → maint, "disabled" → ready
		switch s.Maintenance {
		case "enabled":
			actions = append(actions, fmt.Sprintf("SetServerState %s %s maint", b, n))
		case "disabled":
			actions = append(actions, fmt.Sprintf("SetServerState %s %s ready", b, n))
		}

		// Weight (if set)
		if s.Weight != nil {
			actions = append(actions, fmt.Sprintf("SetServerWeight %s %s %d", b, n, *s.Weight))
		}

		// Health check port (if set)
		if s.HealthCheckPort != nil {
			actions = append(actions, fmt.Sprintf("SetServerCheckPort %s %s %d", b, n, *s.HealthCheckPort))
		}

		// Agent check enable/disable
		switch s.AgentCheck {
		case "enabled":
			actions = append(actions, fmt.Sprintf("EnableAgentCheck %s %s", b, n))
		case "disabled":
			actions = append(actions, fmt.Sprintf("DisableAgentCheck %s %s", b, n))
		}

		// Agent address (if set)
		if s.AgentAddr != "" {
			actions = append(actions, fmt.Sprintf("SetServerAgentAddr %s %s %s", b, n, s.AgentAddr))
		}

		// Agent send string (if set)
		if s.AgentSend != "" {
			actions = append(actions, fmt.Sprintf("SetServerAgentSend %s %s %s", b, n, s.AgentSend))
		}
	}
	return strings.Join(actions, ";")
}

// tryRuntimeOptimizedPath attempts the runtime-optimized path when all operations are
// pure server updates with runtime-eligible field changes and no auxiliary file changes.
// Returns a non-nil result if the optimized path succeeded (caller should return it).
// Returns nil if conditions are not met or if the path failed (caller falls through to
// fine-grained sync).
func (o *orchestrator) tryRuntimeOptimizedPath(
	ctx context.Context,
	desiredConfig string,
	diff *comparator.ConfigDiff,
	auxDiffs *auxiliaryFileDiffs,
	version int64,
	startTime time.Time,
) *SyncResult {
	// version <= 0 means GetVersion() failed; passing an invalid version to
	// PushRawConfigurationSkipReload would cause a guaranteed 409 and a wasted
	// API call before falling through to fine-grained sync anyway.
	if version <= 0 || !o.areAllOperationsRuntimeEligible(diff.Operations) || auxDiffs.anyDiffHasChanges() {
		if !o.areAllOperationsRuntimeEligible(diff.Operations) {
			for _, op := range diff.Operations {
				serverOp, ok := op.(*sections.ServerUpdateOp)
				if !ok || serverOp.IsFullyRuntimeEligible() {
					continue
				}
				ineligible := sections.ServerIneligibleFields(serverOp.CurrentServer(), serverOp.Server())
				o.logger.Debug("server update requires reload: some changed fields are not runtime-eligible",
					"backend", serverOp.BackendName(),
					"server", serverOp.ServerName(),
					"reload_required_fields", ineligible,
					"tip", "move these fields to 'default-server' to enable zero-reload slot-swaps")
			}
		}
		return nil
	}

	o.logger.Debug("Using runtime-optimized path: single raw push with skip_reload",
		"operation_count", len(diff.Operations))

	runtimeActions := buildRuntimeActions(diff.Operations)
	o.logger.Debug("Executing runtime-optimized path",
		"operation_count", len(diff.Operations),
		"action_count", strings.Count(runtimeActions, ";")+1)

	if err := o.client.PushRawConfigurationSkipReload(ctx, desiredConfig, version, runtimeActions); err != nil {
		o.logger.Warn("Runtime-optimized path failed, falling back to fine-grained sync",
			"error", err)
		return nil
	}

	appliedOps := convertOperationsToApplied(diff.Operations)
	return &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   false,
		SyncMode:          SyncModeRuntime,
		Duration:          time.Since(startTime),
		Details:           convertDiffSummary(&diff.Summary),
		// PushRawConfigurationSkipReload increments the config version by 1,
		// same as PushRawConfiguration. Update the caller's version cache so
		// the next reconciliation can skip GetRawConfiguration() + parse.
		PostSyncVersion: version + 1,
		Message:         fmt.Sprintf("Applied %d server updates via runtime-optimized path", len(appliedOps)),
	}
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

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
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
)

// executeRawPush performs raw configuration push with configurable behavior.
// This method is used for both intentional raw push (version=1, threshold exceeded) and fallback scenarios.
//
// Parameters:
//   - version: The current config version for optimistic locking. Version is incremented after push.
//   - mode: The SyncMode to record (SyncModeRawInitial, SyncModeRawThreshold, or SyncModeRawFallback)
//   - auxFilesAlreadySynced: If true, PhasePreConfig is skipped because aux files were already synced
//
// Uses the same auxiliary file sync and reload verification as the fine-grained path.
func (o *orchestrator) executeRawPush(ctx context.Context, desiredConfig string, diff *comparator.ConfigDiff, auxDiffs *auxiliaryFileDiffs, opts *SyncOptions, startTime time.Time, version int64, mode SyncMode, auxFilesAlreadySynced bool) (*SyncResult, error) {
	// Log at debug level - raw pushes are normal operational behavior
	o.logger.Debug("Executing raw configuration push", "mode", mode)

	// PhasePreConfig: sync auxiliary files BEFORE pushing raw config (same as
	// fine-grained sync). Files must exist before HAProxy validates the
	// configuration. Skip if aux files were already synced in the failed
	// fine-grained sync attempt.
	if !auxFilesAlreadySynced {
		auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff)
		if err != nil {
			return nil, err
		}

		// Verify aux file reloads completed BEFORE PhaseConfig raw push.
		// Prevents config from referencing files whose reloads are still pending.
		if err := o.verifyAuxiliaryReloads(ctx, auxReloadIDs, opts, "before raw config push"); err != nil {
			return nil, err
		}
	} else {
		o.logger.Info("Skipping aux file sync in fallback - already synced in fine-grained attempt")
	}

	// PhaseConfig: push the raw configuration now that auxiliary files exist
	// and their reloads are verified. Raw push does not have a PhasePostConfig
	// delete step — the push itself replaces the full config.
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

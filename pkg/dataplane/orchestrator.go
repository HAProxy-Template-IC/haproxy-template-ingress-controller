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
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/enterprise"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// ConfigParser defines the interface for HAProxy configuration parsing.
// Both CE (parser.Parser) and EE (enterprise.Parser) parsers implement this interface.
type ConfigParser interface {
	ParseFromString(config string) (*parserconfig.StructuredConfig, error)
}

// Poll interval for reload verification (not exposed as config option).
const defaultReloadVerificationPollInterval = 500 * time.Millisecond

// orchestrator handles the complete sync workflow.
type orchestrator struct {
	client     *client.DataplaneClient
	parser     ConfigParser
	comparator *comparator.Comparator
	logger     *slog.Logger
}

// newOrchestrator creates a new orchestrator instance.
// It automatically selects the appropriate parser based on whether the client
// is connected to HAProxy Enterprise or Community edition.
func newOrchestrator(c *client.DataplaneClient, logger *slog.Logger) (*orchestrator, error) {
	var p ConfigParser
	var err error

	// Use EE parser when connected to HAProxy Enterprise
	if c.Clientset().IsEnterprise() {
		logger.Info("Using Enterprise Edition parser for HAProxy EE")
		p, err = enterprise.NewParser()
		if err != nil {
			return nil, fmt.Errorf("failed to create EE parser: %w", err)
		}
	} else {
		p, err = parser.New()
		if err != nil {
			return nil, fmt.Errorf("failed to create parser: %w", err)
		}
	}

	return &orchestrator{
		client:     c,
		parser:     p,
		comparator: comparator.New(),
		logger:     logger,
	}, nil
}

// verifyReload polls the reload status until it succeeds, fails, or times out.
// Returns nil if the reload succeeded, or an error describing the failure.
func (o *orchestrator) verifyReload(ctx context.Context, reloadID string, timeout time.Duration) error {
	if reloadID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(defaultReloadVerificationPollInterval)
	defer ticker.Stop()

	o.logger.Debug("Starting reload verification", "reload_id", reloadID)

	for {
		select {
		case <-ticker.C:
			info, err := o.client.GetReloadStatus(ctx, reloadID)
			if err != nil {
				// Log and continue polling - transient errors shouldn't fail immediately
				o.logger.Warn("Reload status check failed, retrying",
					"reload_id", reloadID, "error", err)
				continue
			}

			switch info.Status {
			case client.ReloadStatusSucceeded:
				o.logger.Debug("Reload verified successful", "reload_id", reloadID)
				return nil
			case client.ReloadStatusFailed:
				o.logger.Error("Reload failed",
					"reload_id", reloadID,
					"response", info.Response)
				return fmt.Errorf("reload failed: %s", info.Response)
			case client.ReloadStatusInProgress:
				o.logger.Debug("Reload still in progress", "reload_id", reloadID)
			}

		case <-ctx.Done():
			return fmt.Errorf("reload verification timed out after %v", timeout)
		}
	}
}

// verifyAuxiliaryReloads verifies that all auxiliary file reloads completed successfully.
// This is called after syncing auxiliary files (Phase 1) and before config operations (Phase 2)
// to prevent race conditions where config operations reference files before their reloads complete.
//
// Parameters:
//   - ctx: Context for cancellation
//   - reloadIDs: List of reload IDs to verify
//   - opts: Sync options (must have VerifyReload=true for verification to occur)
//   - context: Description of the context (e.g., "before config sync") for log messages
//
// Returns error if any reload verification fails.
func (o *orchestrator) verifyAuxiliaryReloads(ctx context.Context, reloadIDs []string, opts *SyncOptions, logContext string) error {
	if !opts.VerifyReload || len(reloadIDs) == 0 {
		return nil
	}

	o.logger.Debug("Verifying auxiliary file reloads",
		"context", logContext,
		"count", len(reloadIDs),
		"reload_ids", reloadIDs)

	for _, reloadID := range reloadIDs {
		if err := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); err != nil {
			return &SyncError{
				Stage:   "auxiliary_reload_verification",
				Message: fmt.Sprintf("auxiliary file reload %s failed", reloadID),
				Cause:   err,
				Hints: []string{
					"HAProxy auxiliary file reload failed",
					"Map file or SSL certificate update may not have been applied",
					"Check HAProxy logs for detailed error information",
				},
			}
		}
	}

	o.logger.Debug("all auxiliary file reloads verified",
		"count", len(reloadIDs))
	return nil
}

// sync implements the complete sync workflow with automatic fallback.
func (o *orchestrator) sync(ctx context.Context, desiredConfig string, opts *SyncOptions, auxFiles *AuxiliaryFiles) (*SyncResult, error) {
	startTime := time.Now()

	// Step 1: Fetch current configuration (with optional version cache optimization)
	currentConfigStr, preParsedCurrent, preCachedVersion, err := o.fetchCurrentConfig(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Step 2-4: Parse and compare configurations
	diff, err := o.parseAndCompareConfigs(currentConfigStr, desiredConfig, opts.PreParsedConfig, preParsedCurrent)
	if err != nil {
		return nil, err
	}

	// Step 5: Compare auxiliary files and check if sync is needed
	auxDiffs, err := o.checkForChanges(ctx, diff, auxFiles, opts)
	if err != nil {
		return nil, err
	}

	// Early return if no changes
	if !auxDiffs.hasChanges {
		result := o.createNoChangesResult(startTime, &diff.Summary)
		// Capture version for cache: use pre-cached version if available, otherwise fetch
		if preCachedVersion > 0 {
			result.PostSyncVersion = preCachedVersion
		}
		return result, nil
	}

	// Step 6: Fetch current config version to determine if raw push should be used
	// Reuse pre-cached version if we already called GetVersion() during cache check
	var version int64
	if preCachedVersion > 0 {
		version = preCachedVersion
	} else {
		versionRetryConfig := client.RetryConfig{
			MaxAttempts: 3,
			RetryIf:     client.IsConnectionError(),
			Backoff:     client.BackoffExponential,
			BaseDelay:   100 * time.Millisecond,
			Logger:      o.logger.With("operation", "fetch_version"),
		}

		var versionErr error
		version, versionErr = client.WithRetry(ctx, versionRetryConfig, func(attempt int) (int64, error) {
			return o.client.GetVersion(ctx)
		})
		if versionErr != nil {
			o.logger.Warn("Failed to get config version, skipping version-based raw push decision",
				"error", versionErr)
			version = -1
		}
	}

	// Step 7: Check if raw push should be used instead of fine-grained sync
	// Priority: version=1 > threshold exceeded > fine-grained sync
	if version == 1 {
		o.logger.Info("Using raw push for initial configuration", "version", version)
		return o.executeRawPush(ctx, desiredConfig, diff, auxDiffs, opts, startTime, version, SyncModeRawInitial, false)
	}

	// Use TotalOperations() for threshold check - this includes all operations including
	// runtime-eligible server UPDATEs. While server UPDATEs don't require HAProxy reload,
	// they are processed sequentially and can take a long time for large deployments.
	// A raw push (single reload) is faster than processing 100+ individual operations.
	totalOps := diff.Summary.TotalOperations()
	if opts.RawPushThreshold > 0 && totalOps > opts.RawPushThreshold {
		o.logger.Info("Using raw push due to high change count",
			"total_changes", totalOps,
			"structural_changes", diff.Summary.StructuralOperations(),
			"threshold", opts.RawPushThreshold)
		return o.executeRawPush(ctx, desiredConfig, diff, auxDiffs, opts, startTime, version, SyncModeRawThreshold, false)
	}

	// Step 8: Attempt fine-grained sync with retry logic (pass pre-computed diffs)
	// The function returns whether Phase 1 (aux files) completed successfully
	result, auxFilesSynced, err := o.attemptFineGrainedSyncWithDiffs(ctx, diff, opts, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff, auxDiffs.crtlistDiff, startTime)

	// Step 9: If fine-grained sync failed and fallback is enabled, try raw config push
	if err != nil && opts.FallbackToRaw {
		o.logger.Warn("Fine-grained sync failed, attempting fallback to raw config push",
			"error", err)

		fallbackResult, fallbackErr := o.executeRawPush(ctx, desiredConfig, diff, auxDiffs, opts, startTime, version, SyncModeRawFallback, auxFilesSynced)
		if fallbackErr != nil {
			return nil, NewFallbackError(err, fallbackErr)
		}

		return fallbackResult, nil
	}

	return result, err
}

// diff generates a diff without applying any changes.
func (o *orchestrator) diff(ctx context.Context, desiredConfig string) (*DiffResult, error) {
	// Step 1: Fetch current configuration
	currentConfigStr, err := o.client.GetRawConfiguration(ctx)
	if err != nil {
		return nil, NewConnectionError(o.client.Endpoint.URL, err)
	}

	// Step 2: Parse current configuration
	currentConfig, err := o.parser.ParseFromString(currentConfigStr)
	if err != nil {
		snippet := currentConfigStr
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, NewParseError("current", snippet, err)
	}

	// Note: Normalization of metadata format is now done automatically by the parser
	// during caching. Both currentConfig and desiredParsed are pre-normalized.

	// Step 3: Parse desired configuration
	desiredParsed, err := o.parser.ParseFromString(desiredConfig)
	if err != nil {
		snippet := desiredConfig
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, NewParseError("desired", snippet, err)
	}

	// Step 4: Compare configurations
	diff, err := o.comparator.Compare(currentConfig, desiredParsed)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare",
			Message: "failed to compare configurations",
			Cause:   err,
		}
	}

	// Convert to DiffResult
	plannedOps := convertOperationsToPlanned(diff.Operations)

	return &DiffResult{
		HasChanges:        diff.Summary.HasChanges(),
		PlannedOperations: plannedOps,
		Details:           convertDiffSummary(&diff.Summary),
	}, nil
}

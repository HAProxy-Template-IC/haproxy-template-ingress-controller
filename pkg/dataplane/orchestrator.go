package dataplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/enterprise"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/synchronizer"
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

	// Step 1: Fetch current configuration from dataplane API (with retry for transient connection errors)
	o.logger.Debug("Fetching current configuration from dataplane API",
		"endpoint", o.client.Endpoint.URL)

	// Configure retry for transient connection errors (e.g., dataplane API not yet ready)
	retryConfig := client.RetryConfig{
		MaxAttempts: 3,
		RetryIf:     client.IsConnectionError(),
		Backoff:     client.BackoffExponential,
		BaseDelay:   100 * time.Millisecond,
		Logger:      o.logger.With("operation", "fetch_config"),
	}

	currentConfigStr, err := client.WithRetry(ctx, retryConfig, func(attempt int) (string, error) {
		return o.client.GetRawConfiguration(ctx)
	})

	if err != nil {
		return nil, NewConnectionError(o.client.Endpoint.URL, err)
	}

	// Step 2-4: Parse and compare configurations
	diff, err := o.parseAndCompareConfigs(currentConfigStr, desiredConfig)
	if err != nil {
		return nil, err
	}

	// Step 5: Compare auxiliary files and check if sync is needed
	auxDiffs, err := o.checkForChanges(ctx, diff, auxFiles)
	if err != nil {
		return nil, err
	}

	// Early return if no changes
	if !auxDiffs.hasChanges {
		return o.createNoChangesResult(startTime, &diff.Summary), nil
	}

	// Step 7: Attempt fine-grained sync with retry logic (pass pre-computed diffs)
	// The function returns whether Phase 1 (aux files) completed successfully
	result, auxFilesSynced, err := o.attemptFineGrainedSyncWithDiffs(ctx, diff, opts, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.mapDiff, auxDiffs.crtlistDiff, startTime)

	// Step 7: If fine-grained sync failed and fallback is enabled, try raw config push
	if err != nil && opts.FallbackToRaw {
		o.logger.Warn("Fine-grained sync failed, attempting fallback to raw config push",
			"error", err)

		fallbackResult, fallbackErr := o.attemptRawFallback(ctx, desiredConfig, diff, auxDiffs, opts, startTime, auxFilesSynced)
		if fallbackErr != nil {
			return nil, NewFallbackError(err, fallbackErr)
		}

		return fallbackResult, nil
	}

	return result, err
}

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
	mapDiff *auxiliaryfiles.MapFileDiff,
	crtlistDiff *auxiliaryfiles.CRTListDiff,
	startTime time.Time,
) (*SyncResult, bool, error) {
	// Phase 1: Sync auxiliary files (pre-config) using pre-computed diffs
	auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, fileDiff, sslDiff, mapDiff, crtlistDiff)
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
	o.deleteObsoleteFilesPostConfig(ctx, fileDiff, sslDiff, mapDiff, crtlistDiff)

	// Build result
	result := &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   reloadTriggered,
		ReloadID:          reloadID,
		FallbackToRaw:     false,
		Duration:          time.Since(startTime),
		Retries:           max(0, retries-1),
		Details:           convertDiffSummary(&diff.Summary),
		Message:           fmt.Sprintf("Successfully applied %d configuration changes", len(appliedOps)),
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

	o.logger.Debug("fine-grained sync completed",
		"operations", len(appliedOps),
		"reload_triggered", reloadTriggered,
		"reload_verified", result.ReloadVerified,
		"retries", max(0, retries-1),
		"duration", time.Since(startTime))

	return result, auxFilesSynced, nil
}

// attemptRawFallback attempts to sync using raw configuration push.
// Uses the same auxiliary file sync and reload verification as the fine-grained path.
// If auxFilesAlreadySynced is true, Phase 1 is skipped because aux files were already synced
// in the failed fine-grained sync attempt.
func (o *orchestrator) attemptRawFallback(ctx context.Context, desiredConfig string, diff *comparator.ConfigDiff, auxDiffs *auxiliaryFileDiffs, opts *SyncOptions, startTime time.Time, auxFilesAlreadySynced bool) (*SyncResult, error) {
	o.logger.Warn("Falling back to raw configuration push")

	// Phase 1: Sync auxiliary files BEFORE pushing raw config (same as fine-grained sync)
	// Files must exist before HAProxy validates the configuration.
	// Skip if aux files were already synced in the failed fine-grained sync attempt.
	if !auxFilesAlreadySynced {
		auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.mapDiff, auxDiffs.crtlistDiff)
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
	reloadID, err := o.client.PushRawConfiguration(ctx, desiredConfig)
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

	// Build result
	result := &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps, // Preserve detailed operations instead of generic message
		ReloadTriggered:   true,       // Raw push always triggers reload
		ReloadID:          reloadID,   // Capture reload ID from raw config push
		FallbackToRaw:     true,
		Duration:          time.Since(startTime),
		Retries:           0,
		Details:           convertDiffSummary(&diff.Summary),
		Message:           "Successfully applied configuration via raw config push (fallback)",
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

	o.logger.Info("Raw configuration push completed successfully",
		"duration", time.Since(startTime),
		"reload_id", reloadID,
		"reload_verified", result.ReloadVerified)

	return result, nil
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

	// Normalize metadata format in current config to ensure consistent comparison.
	// The API stores comments as JSON (nested format), but templates produce flat format.
	// This normalization converts nested back to flat for accurate comparison.
	parser.NormalizeConfigMetadata(currentConfig)

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

// Helper functions to convert internal types to public API types

func convertOperationsToApplied(ops []comparator.Operation) []AppliedOperation {
	applied := make([]AppliedOperation, 0, len(ops))
	for _, op := range ops {
		applied = append(applied, AppliedOperation{
			Type:        operationTypeToString(op.Type()),
			Section:     op.Section(),
			Resource:    extractResourceName(op),
			Description: op.Describe(),
		})
	}
	return applied
}

func convertOperationsToPlanned(ops []comparator.Operation) []PlannedOperation {
	planned := make([]PlannedOperation, 0, len(ops))
	for _, op := range ops {
		planned = append(planned, PlannedOperation{
			Type:        operationTypeToString(op.Type()),
			Section:     op.Section(),
			Resource:    extractResourceName(op),
			Description: op.Describe(),
			Priority:    op.Priority(),
		})
	}
	return planned
}

func operationTypeToString(opType sections.OperationType) string {
	switch opType {
	case sections.OperationCreate:
		return "create"
	case sections.OperationUpdate:
		return "update"
	case sections.OperationDelete:
		return "delete"
	default:
		return "unknown"
	}
}

func extractResourceName(op comparator.Operation) string {
	desc := op.Describe()
	// Extract resource name from description (format: "Action section 'name'")
	// This is a simple heuristic - we look for text between single quotes
	start := -1
	for i, ch := range desc {
		if ch == '\'' {
			if start == -1 {
				start = i + 1
			} else {
				return desc[start:i]
			}
		}
	}
	return "unknown"
}

func convertDiffSummary(summary *comparator.DiffSummary) DiffDetails {
	return DiffDetails{
		TotalOperations:   summary.TotalOperations(),
		Creates:           summary.TotalCreates,
		Updates:           summary.TotalUpdates,
		Deletes:           summary.TotalDeletes,
		GlobalChanged:     summary.GlobalChanged,
		DefaultsChanged:   summary.DefaultsChanged,
		FrontendsAdded:    summary.FrontendsAdded,
		FrontendsModified: summary.FrontendsModified,
		FrontendsDeleted:  summary.FrontendsDeleted,
		BackendsAdded:     summary.BackendsAdded,
		BackendsModified:  summary.BackendsModified,
		BackendsDeleted:   summary.BackendsDeleted,
		ServersAdded:      summary.ServersAdded,
		ServersModified:   summary.ServersModified,
		ServersDeleted:    summary.ServersDeleted,
		ACLsAdded:         make(map[string][]string),
		ACLsModified:      make(map[string][]string),
		ACLsDeleted:       make(map[string][]string),
		HTTPRulesAdded:    make(map[string]int),
		HTTPRulesModified: make(map[string]int),
		HTTPRulesDeleted:  make(map[string]int),
	}
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

// deleteObsoleteFilesPostConfig deletes obsolete auxiliary files AFTER successful config sync.
// Errors are logged as warnings but do not fail the sync since config is already applied.
func (o *orchestrator) deleteObsoleteFilesPostConfig(ctx context.Context, fileDiff *auxiliaryfiles.FileDiff, sslDiff *auxiliaryfiles.SSLCertificateDiff, mapDiff *auxiliaryfiles.MapFileDiff, crtlistDiff *auxiliaryfiles.CRTListDiff) {
	// Delete general files
	if fileDiff != nil && len(fileDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete general files", "count", len(fileDiff.ToDelete))

		postConfigDiff := &auxiliaryfiles.FileDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: fileDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncGeneralFiles(ctx, o.client, postConfigDiff); err != nil {
			o.logger.Warn("Failed to delete obsolete general files", "error", err, "files", fileDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete general files deleted successfully")
		}
	}

	// Delete SSL certificates
	if sslDiff != nil && len(sslDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete SSL certificates", "count", len(sslDiff.ToDelete))

		postConfigSSL := &auxiliaryfiles.SSLCertificateDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: sslDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncSSLCertificates(ctx, o.client, postConfigSSL); err != nil {
			o.logger.Warn("Failed to delete obsolete SSL certificates", "error", err, "certificates", sslDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete SSL certificates deleted successfully")
		}
	}

	// Delete map files
	if mapDiff != nil && len(mapDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete map files", "count", len(mapDiff.ToDelete))

		postConfigMap := &auxiliaryfiles.MapFileDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: mapDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncMapFiles(ctx, o.client, postConfigMap); err != nil {
			o.logger.Warn("Failed to delete obsolete map files", "error", err, "maps", mapDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete map files deleted successfully")
		}
	}

	// Delete crt-list files
	if crtlistDiff != nil && len(crtlistDiff.ToDelete) > 0 {
		o.logger.Info("Deleting obsolete crt-list files", "count", len(crtlistDiff.ToDelete))

		postConfigCRTList := &auxiliaryfiles.CRTListDiff{
			ToCreate: nil,
			ToUpdate: nil,
			ToDelete: crtlistDiff.ToDelete,
		}

		if _, err := auxiliaryfiles.SyncCRTLists(ctx, o.client, postConfigCRTList); err != nil {
			o.logger.Warn("Failed to delete obsolete crt-list files", "error", err, "crtlists", crtlistDiff.ToDelete)
		} else {
			o.logger.Info("Obsolete crt-list files deleted successfully")
		}
	}
}

// parseAndCompareConfigs parses both current and desired configurations and compares them.
// Returns the configuration diff or an error if parsing or comparison fails.
func (o *orchestrator) parseAndCompareConfigs(currentConfigStr, desiredConfig string) (*comparator.ConfigDiff, error) {
	// Parse current configuration
	o.logger.Debug("Parsing current configuration")
	currentConfig, err := o.parser.ParseFromString(currentConfigStr)
	if err != nil {
		snippet := currentConfigStr
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, NewParseError("current", snippet, err)
	}

	// Normalize metadata format in current config to ensure consistent comparison.
	// The API stores comments as JSON (nested format), but templates produce flat format.
	// This normalization converts nested back to flat for accurate comparison.
	parser.NormalizeConfigMetadata(currentConfig)

	// Parse desired configuration
	o.logger.Debug("Parsing desired configuration")
	desiredParsed, err := o.parser.ParseFromString(desiredConfig)
	if err != nil {
		snippet := desiredConfig
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, NewParseError("desired", snippet, err)
	}

	// Compare configurations
	o.logger.Debug("Comparing configurations")
	diff, err := o.comparator.Compare(currentConfig, desiredParsed)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare",
			Message: "failed to compare configurations",
			Cause:   err,
			Hints: []string{
				"Check that both configurations are valid",
				"Review the comparison error for details",
			},
		}
	}

	return diff, nil
}

// compareAuxiliaryFiles compares all auxiliary file types in parallel.
// Returns file diffs for general files, SSL certificates, map files, and crt-list files.
func (o *orchestrator) compareAuxiliaryFiles(
	ctx context.Context,
	auxFiles *AuxiliaryFiles,
) (*auxiliaryfiles.FileDiff, *auxiliaryfiles.SSLCertificateDiff, *auxiliaryfiles.MapFileDiff, *auxiliaryfiles.CRTListDiff, error) {
	var fileDiff *auxiliaryfiles.FileDiff
	var sslDiff *auxiliaryfiles.SSLCertificateDiff
	var mapDiff *auxiliaryfiles.MapFileDiff
	var crtlistDiff *auxiliaryfiles.CRTListDiff

	g, gCtx := errgroup.WithContext(ctx)

	// Compare general files
	g.Go(func() error {
		var err error
		fileDiff, err = o.compareGeneralFiles(gCtx, auxFiles.GeneralFiles)
		return err
	})

	// Compare SSL certificates
	g.Go(func() error {
		var err error
		sslDiff, err = o.compareSSLCertificates(gCtx, auxFiles.SSLCertificates)
		return err
	})

	// Compare map files
	g.Go(func() error {
		var err error
		mapDiff, err = o.compareMapFiles(gCtx, auxFiles.MapFiles)
		return err
	})

	// Compare crt-list files
	g.Go(func() error {
		var err error
		crtlistDiff, err = o.compareCRTListFiles(gCtx, auxFiles.CRTListFiles)
		return err
	})

	// Wait for all auxiliary file comparisons to complete
	if err := g.Wait(); err != nil {
		return nil, nil, nil, nil, err
	}

	return fileDiff, sslDiff, mapDiff, crtlistDiff, nil
}

// compareGeneralFiles compares current and desired general files (comparison only, no sync).
func (o *orchestrator) compareGeneralFiles(ctx context.Context, generalFiles []auxiliaryfiles.GeneralFile) (*auxiliaryfiles.FileDiff, error) {
	if len(generalFiles) == 0 {
		return &auxiliaryfiles.FileDiff{}, nil
	}

	o.logger.Debug("Comparing general files", "desired_files", len(generalFiles))

	fileDiff, err := auxiliaryfiles.CompareGeneralFiles(ctx, o.client, generalFiles)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare_files",
			Message: "failed to compare general files",
			Cause:   err,
			Hints: []string{
				"Verify Dataplane API is accessible",
				"Check file permissions on HAProxy storage",
			},
		}
	}

	return fileDiff, nil
}

// compareSSLCertificates compares current and desired SSL certificates (comparison only, no sync).
func (o *orchestrator) compareSSLCertificates(ctx context.Context, sslCerts []auxiliaryfiles.SSLCertificate) (*auxiliaryfiles.SSLCertificateDiff, error) {
	if len(sslCerts) == 0 {
		return &auxiliaryfiles.SSLCertificateDiff{}, nil
	}

	o.logger.Debug("Comparing SSL certificates", "desired_certs", len(sslCerts))

	sslDiff, err := auxiliaryfiles.CompareSSLCertificates(ctx, o.client, sslCerts)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare_ssl",
			Message: "failed to compare SSL certificates",
			Cause:   err,
			Hints: []string{
				"Verify Dataplane API is accessible",
				"Check SSL storage permissions",
			},
		}
	}

	return sslDiff, nil
}

// compareMapFiles compares current and desired map files (comparison only, no sync).
func (o *orchestrator) compareMapFiles(ctx context.Context, mapFiles []auxiliaryfiles.MapFile) (*auxiliaryfiles.MapFileDiff, error) {
	if len(mapFiles) == 0 {
		return &auxiliaryfiles.MapFileDiff{}, nil
	}

	o.logger.Debug("Comparing map files", "desired_maps", len(mapFiles))

	mapDiff, err := auxiliaryfiles.CompareMapFiles(ctx, o.client, mapFiles)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare_maps",
			Message: "failed to compare map files",
			Cause:   err,
			Hints: []string{
				"Verify Dataplane API is accessible",
				"Check map storage permissions",
			},
		}
	}

	return mapDiff, nil
}

// compareCRTListFiles compares current and desired crt-list files (comparison only, no sync).
func (o *orchestrator) compareCRTListFiles(ctx context.Context, crtlistFiles []auxiliaryfiles.CRTListFile) (*auxiliaryfiles.CRTListDiff, error) {
	if len(crtlistFiles) == 0 {
		return &auxiliaryfiles.CRTListDiff{}, nil
	}

	o.logger.Debug("Comparing crt-list files", "desired_crtlists", len(crtlistFiles))

	crtlistDiff, err := auxiliaryfiles.CompareCRTLists(ctx, o.client, crtlistFiles)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare_crtlists",
			Message: "failed to compare crt-list files",
			Cause:   err,
			Hints: []string{
				"Verify Dataplane API is accessible",
				"Check crt-list storage permissions",
			},
		}
	}

	return crtlistDiff, nil
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
		// Execute runtime-eligible operations without transaction (no reload)
		o.logger.Info("All operations are runtime-eligible, executing without transaction")

		// Execute operations directly using runtime API (empty transactionID)
		for _, op := range diff.Operations {
			if execErr := op.Execute(ctx, o.client, ""); execErr != nil {
				err = fmt.Errorf("runtime operation failed: %w", execErr)
				break
			}
		}

		retries = 1             // Count single execution
		reloadTriggered = false // Runtime API doesn't trigger reload
		reloadID = ""           // No reload ID

		if err == nil {
			appliedOps = convertOperationsToApplied(diff.Operations)
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

// auxiliaryFileDiffs groups all auxiliary file diff results.
type auxiliaryFileDiffs struct {
	fileDiff    *auxiliaryfiles.FileDiff
	sslDiff     *auxiliaryfiles.SSLCertificateDiff
	mapDiff     *auxiliaryfiles.MapFileDiff
	crtlistDiff *auxiliaryfiles.CRTListDiff
	hasChanges  bool
}

// checkForChanges compares auxiliary files and determines if sync is needed.
// Returns auxiliary file diffs grouped in a struct and any error.
func (o *orchestrator) checkForChanges(
	ctx context.Context,
	diff *comparator.ConfigDiff,
	auxFiles *AuxiliaryFiles,
) (*auxiliaryFileDiffs, error) {
	// Compare auxiliary files
	fileDiff, sslDiff, mapDiff, crtlistDiff, err := o.compareAuxiliaryFiles(ctx, auxFiles)
	if err != nil {
		return nil, err
	}

	// Check if there are auxiliary file changes
	hasAuxChanges := (fileDiff != nil && fileDiff.HasChanges()) ||
		(sslDiff != nil && sslDiff.HasChanges()) ||
		(mapDiff != nil && mapDiff.HasChanges()) ||
		(crtlistDiff != nil && crtlistDiff.HasChanges())

	// Check if there are any changes (config OR auxiliary files)
	if !diff.Summary.HasChanges() && !hasAuxChanges {
		return &auxiliaryFileDiffs{
			fileDiff:    fileDiff,
			sslDiff:     sslDiff,
			mapDiff:     mapDiff,
			crtlistDiff: crtlistDiff,
			hasChanges:  false,
		}, nil
	}

	// Log changes
	if diff.Summary.HasChanges() {
		o.logger.Debug("configuration changes detected",
			"total_operations", diff.Summary.TotalOperations(),
			"creates", diff.Summary.TotalCreates,
			"updates", diff.Summary.TotalUpdates,
			"deletes", diff.Summary.TotalDeletes)
	}

	if hasAuxChanges {
		o.logger.Debug("auxiliary file changes detected",
			"general_files", fileDiff != nil && fileDiff.HasChanges(),
			"ssl_certs", sslDiff != nil && sslDiff.HasChanges(),
			"maps", mapDiff != nil && mapDiff.HasChanges(),
			"crtlists", crtlistDiff != nil && crtlistDiff.HasChanges())
	}

	return &auxiliaryFileDiffs{
		fileDiff:    fileDiff,
		sslDiff:     sslDiff,
		mapDiff:     mapDiff,
		crtlistDiff: crtlistDiff,
		hasChanges:  true,
	}, nil
}

// createNoChangesResult creates a SyncResult for when no changes are detected.
func (o *orchestrator) createNoChangesResult(startTime time.Time, summary *comparator.DiffSummary) *SyncResult {
	o.logger.Debug("No configuration or auxiliary file changes detected")
	return &SyncResult{
		Success:           true,
		AppliedOperations: nil,
		ReloadTriggered:   false,
		FallbackToRaw:     false,
		Duration:          time.Since(startTime),
		Retries:           0,
		Details:           convertDiffSummary(summary),
		Message:           "No configuration or auxiliary file changes detected",
	}
}

// auxiliaryFileSyncParams contains parameters for auxiliary file synchronization.
type auxiliaryFileSyncParams struct {
	resourceType string
	creates      int
	updates      int
	deletes      int
	stage        string
	message      string
	hints        []string
	syncFunc     func(context.Context) ([]string, error) // Returns (reloadIDs, error)
}

// syncAuxiliaryFileType is a helper that executes auxiliary file sync with the common pattern.
// It logs changes, executes the sync function, handles errors, and logs success.
// Returns reload IDs from create/update operations that triggered reloads.
func (o *orchestrator) syncAuxiliaryFileType(ctx context.Context, params *auxiliaryFileSyncParams) ([]string, error) {
	o.logger.Debug(params.resourceType+" changes detected",
		"creates", params.creates,
		"updates", params.updates,
		"deletes", params.deletes)

	reloadIDs, err := params.syncFunc(ctx)
	if err != nil {
		return nil, &SyncError{
			Stage:   params.stage,
			Message: params.message,
			Cause:   err,
			Hints:   params.hints,
		}
	}

	o.logger.Debug(params.resourceType+" synced successfully (pre-config phase)",
		"reload_ids", len(reloadIDs))
	return reloadIDs, nil
}

// scheduleAuxiliarySync schedules an auxiliary file sync task in the errgroup and collects reload IDs.
// This helper reduces cognitive complexity by extracting the common goroutine pattern.
func (o *orchestrator) scheduleAuxiliarySync(
	g *errgroup.Group,
	ctx context.Context,
	params *auxiliaryFileSyncParams,
	reloadIDs *[]string,
	mu *sync.Mutex,
) {
	g.Go(func() error {
		ids, err := o.syncAuxiliaryFileType(ctx, params)
		if err != nil {
			return err
		}
		mu.Lock()
		*reloadIDs = append(*reloadIDs, ids...)
		mu.Unlock()
		return nil
	})
}

// syncAuxiliaryFilesPreConfig syncs all auxiliary files before config sync (Phase 1).
// Only creates and updates are synced; deletions are deferred until post-config phase.
// Returns reload IDs from create/update operations that triggered reloads.
//
// IMPORTANT: SSL certificates are synced FIRST (synchronously) before other aux files.
// This ordering prevents a race condition where CRT-list, map, or general file syncs
// trigger a HAProxy reload before all SSL certificates are uploaded. When HAProxy reloads,
// it validates the current config which may reference SSL certs still being uploaded in parallel.
// By syncing SSL certs first, we ensure they exist before any reload can be triggered.
func (o *orchestrator) syncAuxiliaryFilesPreConfig(
	ctx context.Context,
	fileDiff *auxiliaryfiles.FileDiff,
	sslDiff *auxiliaryfiles.SSLCertificateDiff,
	mapDiff *auxiliaryfiles.MapFileDiff,
	crtlistDiff *auxiliaryfiles.CRTListDiff,
) ([]string, error) {
	var allReloadIDs []string
	var mu sync.Mutex

	// Phase 1a: Sync SSL certificates FIRST (synchronously)
	// SSL certificates must exist before any other aux file sync triggers a reload,
	// because the HAProxy config may reference these certificates.
	if sslDiff != nil && sslDiff.HasChanges() {
		reloadIDs, err := o.syncAuxiliaryFileType(ctx, &auxiliaryFileSyncParams{
			resourceType: "SSL certificate",
			creates:      len(sslDiff.ToCreate),
			updates:      len(sslDiff.ToUpdate),
			deletes:      len(sslDiff.ToDelete),
			stage:        "sync_ssl_pre",
			message:      "failed to sync SSL certificates before config sync",
			hints: []string{
				"Check SSL storage permissions",
				"Verify certificate contents are valid PEM format",
				"Review error message for specific certificate failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigSSL := &auxiliaryfiles.SSLCertificateDiff{
					ToCreate: sslDiff.ToCreate,
					ToUpdate: sslDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncSSLCertificates(ctx, o.client, preConfigSSL)
			},
		})
		if err != nil {
			return nil, err
		}
		allReloadIDs = append(allReloadIDs, reloadIDs...)
	}

	// Phase 1b: Sync remaining aux files in parallel
	// Now that SSL certs exist, other aux file syncs can safely trigger reloads.
	g, gCtx := errgroup.WithContext(ctx)

	// Sync general files if there are changes
	if fileDiff != nil && fileDiff.HasChanges() {
		o.scheduleAuxiliarySync(g, gCtx, &auxiliaryFileSyncParams{
			resourceType: "General file",
			creates:      len(fileDiff.ToCreate),
			updates:      len(fileDiff.ToUpdate),
			deletes:      len(fileDiff.ToDelete),
			stage:        "sync_files_pre",
			message:      "failed to sync general files before config sync",
			hints: []string{
				"Check HAProxy storage is writable",
				"Verify file contents are valid",
				"Review error message for specific file failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigDiff := &auxiliaryfiles.FileDiff{
					ToCreate: fileDiff.ToCreate,
					ToUpdate: fileDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncGeneralFiles(ctx, o.client, preConfigDiff)
			},
		}, &allReloadIDs, &mu)
	}

	// Sync map files if there are changes
	if mapDiff != nil && mapDiff.HasChanges() {
		o.scheduleAuxiliarySync(g, gCtx, &auxiliaryFileSyncParams{
			resourceType: "Map file",
			creates:      len(mapDiff.ToCreate),
			updates:      len(mapDiff.ToUpdate),
			deletes:      len(mapDiff.ToDelete),
			stage:        "sync_maps_pre",
			message:      "failed to sync map files before config sync",
			hints: []string{
				"Check map storage permissions",
				"Verify map file format is correct",
				"Review error message for specific map failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigMap := &auxiliaryfiles.MapFileDiff{
					ToCreate: mapDiff.ToCreate,
					ToUpdate: mapDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncMapFiles(ctx, o.client, preConfigMap)
			},
		}, &allReloadIDs, &mu)
	}

	// Sync crt-list files if there are changes
	if crtlistDiff != nil && crtlistDiff.HasChanges() {
		o.scheduleAuxiliarySync(g, gCtx, &auxiliaryFileSyncParams{
			resourceType: "CRT-list file",
			creates:      len(crtlistDiff.ToCreate),
			updates:      len(crtlistDiff.ToUpdate),
			deletes:      len(crtlistDiff.ToDelete),
			stage:        "sync_crtlists_pre",
			message:      "failed to sync crt-list files before config sync",
			hints: []string{
				"Check crt-list storage permissions",
				"Verify crt-list file format is correct",
				"Review error message for specific crt-list failures",
			},
			syncFunc: func(ctx context.Context) ([]string, error) {
				preConfigCRTList := &auxiliaryfiles.CRTListDiff{
					ToCreate: crtlistDiff.ToCreate,
					ToUpdate: crtlistDiff.ToUpdate,
					ToDelete: nil,
				}
				return auxiliaryfiles.SyncCRTLists(ctx, o.client, preConfigCRTList)
			},
		}, &allReloadIDs, &mu)
	}

	// Wait for all remaining auxiliary file syncs to complete
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return allReloadIDs, nil
}

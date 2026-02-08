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
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// fetchCurrentConfig obtains the current HAProxy configuration, either from cache or by fetching.
//
// When CachedCurrentConfig is set in opts, it first calls GetVersion() (lightweight ~100 bytes)
// to check if the pod's config version matches CachedConfigVersion. On match, returns the cached
// parsed config directly, skipping the expensive GetRawConfiguration() + parse.
// On mismatch or error, falls through to the full fetch path.
//
// Returns:
//   - currentConfigStr: raw config string (empty when cache hit - not needed)
//   - preParsedCurrent: pre-parsed current config (non-nil on cache hit)
//   - preCachedVersion: pod's version from cache check (-1 if not checked, >0 if checked)
//   - err: connection error if fetch fails
func (o *orchestrator) fetchCurrentConfig(ctx context.Context, opts *SyncOptions) (currentConfigStr string, preParsedCurrent *parserconfig.StructuredConfig, preCachedVersion int64, err error) {
	preCachedVersion = -1

	if opts.CachedCurrentConfig != nil {
		versionRetryConfig := client.RetryConfig{
			MaxAttempts: 3,
			RetryIf:     client.IsConnectionError(),
			Backoff:     client.BackoffExponential,
			BaseDelay:   100 * time.Millisecond,
			Logger:      o.logger.With("operation", "version_cache_check"),
		}

		podVersion, versionErr := client.WithRetry(ctx, versionRetryConfig, func(attempt int) (int64, error) {
			return o.client.GetVersion(ctx)
		})
		if versionErr != nil {
			o.logger.Warn("Version cache check failed, falling through to full fetch",
				"error", versionErr)
		} else {
			preCachedVersion = podVersion
			if podVersion == opts.CachedConfigVersion {
				o.logger.Debug("Config version cache hit, skipping full fetch+parse",
					"version", podVersion)
				return "", opts.CachedCurrentConfig, preCachedVersion, nil
			}
			o.logger.Debug("Config version cache miss, fetching full config",
				"cached_version", opts.CachedConfigVersion,
				"pod_version", podVersion)
		}
	}

	// Full fetch path
	o.logger.Debug("Fetching current configuration from dataplane API",
		"endpoint", o.client.Endpoint.URL)

	fetchRetryConfig := client.RetryConfig{
		MaxAttempts: 3,
		RetryIf:     client.IsConnectionError(),
		Backoff:     client.BackoffExponential,
		BaseDelay:   100 * time.Millisecond,
		Logger:      o.logger.With("operation", "fetch_config"),
	}

	currentConfigStr, err = client.WithRetry(ctx, fetchRetryConfig, func(attempt int) (string, error) {
		return o.client.GetRawConfiguration(ctx)
	})
	if err != nil {
		return "", nil, preCachedVersion, NewConnectionError(o.client.Endpoint.URL, err)
	}

	return currentConfigStr, nil, preCachedVersion, nil
}

// parseAndCompareConfigs parses both current and desired configurations and compares them.
// If preParsedDesired is provided, it is used directly instead of parsing desiredConfig.
// If preParsedCurrent is provided, it is used directly instead of parsing currentConfigStr.
// Returns the configuration diff or an error if parsing or comparison fails.
func (o *orchestrator) parseAndCompareConfigs(currentConfigStr, desiredConfig string, preParsedDesired, preParsedCurrent *parserconfig.StructuredConfig) (*comparator.ConfigDiff, error) {
	// Use pre-parsed current config if available, otherwise parse from string
	var currentConfig *parserconfig.StructuredConfig
	var err error
	if preParsedCurrent != nil {
		o.logger.Debug("Using cached current configuration")
		currentConfig = preParsedCurrent
	} else {
		o.logger.Debug("Parsing current configuration")
		currentConfig, err = o.parser.ParseFromString(currentConfigStr)
		if err != nil {
			snippet := currentConfigStr
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, NewParseError("current", snippet, err)
		}
	}

	// Note: Normalization of metadata format is now done automatically by the parser
	// during caching. Both currentConfig and desiredParsed are pre-normalized.

	// Use pre-parsed desired config if available, otherwise parse
	var desiredParsed *parserconfig.StructuredConfig
	if preParsedDesired != nil {
		o.logger.Debug("Using pre-parsed desired configuration")
		desiredParsed = preParsedDesired
	} else {
		o.logger.Debug("Parsing desired configuration")
		desiredParsed, err = o.parser.ParseFromString(desiredConfig)
		if err != nil {
			snippet := desiredConfig
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, NewParseError("desired", snippet, err)
		}
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
// Returns file diffs for general files, SSL certificates, SSL CA files, map files, and crt-list files.
func (o *orchestrator) compareAuxiliaryFiles(
	ctx context.Context,
	auxFiles *AuxiliaryFiles,
) (*auxiliaryFileDiffs, error) {
	var fileDiff *auxiliaryfiles.FileDiff
	var sslDiff *auxiliaryfiles.SSLCertificateDiff
	var caFileDiff *auxiliaryfiles.SSLCaFileDiff
	var mapDiff *auxiliaryfiles.MapFileDiff
	var crtlistDiff *auxiliaryfiles.CRTListDiff

	g, gCtx := errgroup.WithContext(ctx)

	// Merge CRT-lists into general files for unified comparison.
	// Since CRT-lists are stored as general files (to avoid reload on create),
	// we must compare them together to prevent conflicting delete operations.
	// Without this merge, each comparison would mark the other's files for deletion.
	mergedGeneralFiles := auxFiles.GeneralFiles
	if len(auxFiles.CRTListFiles) > 0 {
		crtListsAsGeneral := auxiliaryfiles.CRTListsToGeneralFiles(auxFiles.CRTListFiles)
		mergedGeneralFiles = append(mergedGeneralFiles, crtListsAsGeneral...)
	}

	// Compare general files (now includes CRT-lists for unified deletion handling)
	g.Go(func() error {
		var err error
		fileDiff, err = o.compareGeneralFiles(gCtx, mergedGeneralFiles)
		return err
	})

	// Compare SSL certificates
	g.Go(func() error {
		var err error
		sslDiff, err = o.compareSSLCertificates(gCtx, auxFiles.SSLCertificates)
		return err
	})

	// Compare SSL CA files
	g.Go(func() error {
		var err error
		caFileDiff, err = o.compareSSLCaFiles(gCtx, auxFiles.SSLCaFiles)
		return err
	})

	// Compare map files
	g.Go(func() error {
		var err error
		mapDiff, err = o.compareMapFiles(gCtx, auxFiles.MapFiles)
		return err
	})

	// Compare crt-list files (for create/update operations and metrics)
	g.Go(func() error {
		var err error
		crtlistDiff, err = o.compareCRTListFiles(gCtx, auxFiles.CRTListFiles)
		return err
	})

	// Wait for all auxiliary file comparisons to complete
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Clear CRT-list ToDelete - deletion is handled by unified general files comparison.
	// The CRT-list comparison still provides create/update operations for sync and metrics.
	if crtlistDiff != nil {
		crtlistDiff.ToDelete = nil
	}

	return &auxiliaryFileDiffs{
		fileDiff:    fileDiff,
		sslDiff:     sslDiff,
		caFileDiff:  caFileDiff,
		mapDiff:     mapDiff,
		crtlistDiff: crtlistDiff,
	}, nil
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

// compareSSLCaFiles compares current and desired SSL CA files (comparison only, no sync).
func (o *orchestrator) compareSSLCaFiles(ctx context.Context, caFiles []auxiliaryfiles.SSLCaFile) (*auxiliaryfiles.SSLCaFileDiff, error) {
	if len(caFiles) == 0 {
		return &auxiliaryfiles.SSLCaFileDiff{}, nil
	}

	o.logger.Debug("Comparing SSL CA files", "desired_ca_files", len(caFiles))

	caFileDiff, err := auxiliaryfiles.CompareSSLCaFiles(ctx, o.client, caFiles)
	if err != nil {
		return nil, &SyncError{
			Stage:   "compare_ssl_ca",
			Message: "failed to compare SSL CA files",
			Cause:   err,
			Hints: []string{
				"Verify Dataplane API is accessible",
				"Check SSL CA storage permissions",
				"SSL CA file storage requires DataPlane API v3.2+",
			},
		}
	}

	return caFileDiff, nil
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

// auxiliaryFileDiffs groups all auxiliary file diff results.
type auxiliaryFileDiffs struct {
	fileDiff    *auxiliaryfiles.FileDiff
	sslDiff     *auxiliaryfiles.SSLCertificateDiff
	caFileDiff  *auxiliaryfiles.SSLCaFileDiff
	mapDiff     *auxiliaryfiles.MapFileDiff
	crtlistDiff *auxiliaryfiles.CRTListDiff
	hasChanges  bool
}

// anyDiffHasChanges returns true if any auxiliary file type has pending changes.
func (d *auxiliaryFileDiffs) anyDiffHasChanges() bool {
	return (d.fileDiff != nil && d.fileDiff.HasChanges()) ||
		(d.sslDiff != nil && d.sslDiff.HasChanges()) ||
		(d.caFileDiff != nil && d.caFileDiff.HasChanges()) ||
		(d.mapDiff != nil && d.mapDiff.HasChanges()) ||
		(d.crtlistDiff != nil && d.crtlistDiff.HasChanges())
}

// checksumMatchesLastDeployed returns true if the content checksum matches the
// last deployed checksum, meaning aux file comparison can be skipped.
func checksumMatchesLastDeployed(opts *SyncOptions) bool {
	return opts.ContentChecksum != "" &&
		opts.LastDeployedChecksum != "" &&
		opts.ContentChecksum == opts.LastDeployedChecksum
}

// checkForChanges compares auxiliary files and determines if sync is needed.
// Returns auxiliary file diffs grouped in a struct and any error.
//
// When ContentChecksum and LastDeployedChecksum are both set in opts and match,
// AND the config diff shows no changes, the expensive auxiliary file comparison
// (which downloads content from each HAProxy pod via Dataplane API) is skipped
// entirely. This is safe because the content checksum covers config + all aux
// file content -- a matching checksum means the desired state is identical to
// what was last successfully deployed.
func (o *orchestrator) checkForChanges(
	ctx context.Context,
	diff *comparator.ConfigDiff,
	auxFiles *AuxiliaryFiles,
	opts *SyncOptions,
) (*auxiliaryFileDiffs, error) {
	// Fast path: skip expensive aux file comparison when content hasn't changed.
	// Both checksums must be non-empty and equal, AND config must have no changes.
	if !diff.Summary.HasChanges() && checksumMatchesLastDeployed(opts) {
		o.logger.Debug("Skipping auxiliary file comparison - content checksum unchanged",
			"checksum", opts.ContentChecksum[:min(8, len(opts.ContentChecksum))])
		return &auxiliaryFileDiffs{hasChanges: false}, nil
	}

	// Compare auxiliary files
	auxDiffs, err := o.compareAuxiliaryFiles(ctx, auxFiles)
	if err != nil {
		return nil, err
	}

	hasAuxChanges := auxDiffs.anyDiffHasChanges()

	// Check if there are any changes (config OR auxiliary files)
	if !diff.Summary.HasChanges() && !hasAuxChanges {
		auxDiffs.hasChanges = false
		return auxDiffs, nil
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
			"general_files", auxDiffs.fileDiff != nil && auxDiffs.fileDiff.HasChanges(),
			"ssl_certs", auxDiffs.sslDiff != nil && auxDiffs.sslDiff.HasChanges(),
			"ssl_ca_files", auxDiffs.caFileDiff != nil && auxDiffs.caFileDiff.HasChanges(),
			"maps", auxDiffs.mapDiff != nil && auxDiffs.mapDiff.HasChanges(),
			"crtlists", auxDiffs.crtlistDiff != nil && auxDiffs.crtlistDiff.HasChanges())
	}

	auxDiffs.hasChanges = true
	return auxDiffs, nil
}

// createNoChangesResult creates a SyncResult for when no changes are detected.
func (o *orchestrator) createNoChangesResult(startTime time.Time, summary *comparator.DiffSummary) *SyncResult {
	o.logger.Debug("No configuration or auxiliary file changes detected")
	return &SyncResult{
		Success:           true,
		AppliedOperations: nil,
		ReloadTriggered:   false,
		SyncMode:          SyncModeFineGrained, // No actual sync happened, but semantically fine-grained path
		Duration:          time.Since(startTime),
		Retries:           0,
		Details:           convertDiffSummary(summary),
		Message:           "No configuration or auxiliary file changes detected",
	}
}

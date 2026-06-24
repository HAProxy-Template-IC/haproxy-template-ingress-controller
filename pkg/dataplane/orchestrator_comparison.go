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
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// headerlessConfigVersion is what GetVersion reports when the pod's config
// file carries no `# _version=N` header. The dataplane API (via
// client-native) writes the pushed body VERBATIM on skip_version pushes —
// no header, no version increment (client-native raw.go / transaction.go) —
// so after every runtime-bypass apply the pod reads as version 1 no matter
// what body is on disk. Version 1 therefore cannot discriminate states:
// it must never satisfy a version-cache check and never be cached.
const headerlessConfigVersion = 1

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
			// Version 1 is the headerless sentinel, not a real version:
			// a skip_version push (runtime bypass) writes the config body
			// verbatim WITHOUT the `# _version=N` header, and GetVersion
			// reads a missing header as 1 — regardless of what body is on
			// disk. Two different post-bypass states both report 1, so
			// equality at 1 proves nothing; force a full fetch to compare
			// against the pod's actual config.
			switch podVersion {
			case headerlessConfigVersion:
				o.logger.Debug("Pod config version is the headerless sentinel; forcing full fetch",
					"pod_version", podVersion)
			case opts.CachedConfigVersion:
				o.logger.Debug("Config version cache hit, skipping full fetch+parse",
					"version", podVersion)
				return "", opts.CachedCurrentConfig, preCachedVersion, nil
			default:
				o.logger.Debug("Config version cache miss, fetching full config",
					"cached_version", opts.CachedConfigVersion,
					"pod_version", podVersion)
			}
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
			return nil, NewParseError(configTypeCurrent, snippet, err)
		}
	}

	// Metadata-format normalization is handled by the parser during caching;
	// both currentConfig and desiredParsed arrive here pre-normalized.

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
	return compareAuxFiles(ctx, o.client, o.logger, generalFiles, auxiliaryfiles.CompareGeneralFiles,
		"general files", "compare_files", "failed to compare general files",
		"Verify Dataplane API is accessible",
		"Check file permissions on HAProxy storage",
	)
}

// compareSSLCertificates compares current and desired SSL certificates (comparison only, no sync).
func (o *orchestrator) compareSSLCertificates(ctx context.Context, sslCerts []auxiliaryfiles.SSLCertificate) (*auxiliaryfiles.SSLCertificateDiff, error) {
	return compareAuxFiles(ctx, o.client, o.logger, sslCerts, auxiliaryfiles.CompareSSLCertificates,
		"SSL certificates", "compare_ssl", "failed to compare SSL certificates",
		"Verify Dataplane API is accessible",
		"Check SSL storage permissions",
	)
}

// compareSSLCaFiles compares current and desired SSL CA files (comparison only, no sync).
func (o *orchestrator) compareSSLCaFiles(ctx context.Context, caFiles []auxiliaryfiles.SSLCaFile) (*auxiliaryfiles.SSLCaFileDiff, error) {
	return compareAuxFiles(ctx, o.client, o.logger, caFiles, auxiliaryfiles.CompareSSLCaFiles,
		"SSL CA files", "compare_ssl_ca", "failed to compare SSL CA files",
		"Verify Dataplane API is accessible",
		"Check SSL CA storage permissions",
		"SSL CA file storage requires DataPlane API v3.2+",
	)
}

// compareMapFiles compares current and desired map files (comparison only, no sync).
func (o *orchestrator) compareMapFiles(ctx context.Context, mapFiles []auxiliaryfiles.MapFile) (*auxiliaryfiles.MapFileDiff, error) {
	return compareAuxFiles(ctx, o.client, o.logger, mapFiles, auxiliaryfiles.CompareMapFiles,
		"map files", "compare_maps", "failed to compare map files",
		"Verify Dataplane API is accessible",
		"Check map storage permissions",
	)
}

// compareCRTListFiles compares current and desired crt-list files (comparison only, no sync).
func (o *orchestrator) compareCRTListFiles(ctx context.Context, crtlistFiles []auxiliaryfiles.CRTListFile) (*auxiliaryfiles.CRTListDiff, error) {
	return compareAuxFiles(ctx, o.client, o.logger, crtlistFiles, auxiliaryfiles.CompareCRTLists,
		"crt-list files", "compare_crtlists", "failed to compare crt-list files",
		"Verify Dataplane API is accessible",
		"Check crt-list storage permissions",
	)
}

// compareAuxFiles is the shared implementation behind the per-type
// compare*Files orchestrator methods. It short-circuits on empty input,
// emits a debug log, runs the supplied comparator, and wraps any failure in
// a SyncError with the caller-supplied stage/message/hints.
func compareAuxFiles[T, D any](
	ctx context.Context,
	c *client.DataplaneClient,
	logger *slog.Logger,
	files []T,
	compareFn func(context.Context, *client.DataplaneClient, []T) (*D, error),
	logName, stage, message string,
	hints ...string,
) (*D, error) {
	if len(files) == 0 {
		return new(D), nil
	}
	logger.Debug("Comparing "+logName, "desired_count", len(files))
	diff, err := compareFn(ctx, c, files)
	if err != nil {
		return nil, &SyncError{
			Stage:   stage,
			Message: message,
			Cause:   err,
			Hints:   hints,
		}
	}
	return diff, nil
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

// runtimeEligibleAuxUpdates partitions the auxiliary diff for the runtime fast
// path. It returns the map files (mapDiff.ToUpdate, v3.0+) and SSL certificates
// (sslDiff.ToUpdate, v3.2+ per caps) whose CONTENT changed — appliable to the
// live worker via ReplaceRuntimeMap / ReplaceRuntimeSSLCert without a reload —
// and auxNeedsReload reporting whether any OTHER auxiliary change still forces
// one.
//
// The reload can be skipped only when auxNeedsReload is false: every auxiliary
// change in the batch must be a content update to an already-existing map or
// (on v3.2+) cert. File creation/deletion, a cert content update on <v3.2, and
// any other auxiliary change (general files, CA files, crt-lists) remain
// structural — consistent with the all-or-nothing runtime gate, where a single
// non-runtime change makes a reload unavoidable and the runtime applies moot.
func (d *auxiliaryFileDiffs) runtimeEligibleAuxUpdates(caps Capabilities) (mapUpdates []auxiliaryfiles.MapFile, certUpdates []auxiliaryfiles.SSLCertificate, auxNeedsReload bool) {
	if d == nil {
		return nil, nil, false
	}

	// General, CA and crt-list changes always take the reload path.
	otherAuxChanged := (d.fileDiff != nil && d.fileDiff.HasChanges()) ||
		(d.caFileDiff != nil && d.caFileDiff.HasChanges()) ||
		(d.crtlistDiff != nil && d.crtlistDiff.HasChanges())

	// Maps: content updates to existing maps are runtime-eligible (v3.0+);
	// creating or deleting a map file stays structural.
	mapStructural := d.mapDiff != nil && (len(d.mapDiff.ToCreate) > 0 || len(d.mapDiff.ToDelete) > 0)
	if d.mapDiff != nil {
		mapUpdates = d.mapDiff.ToUpdate
	}

	// SSL certs: content updates to an existing cert are runtime-eligible only on
	// v3.2+ (set ssl cert + commit). Create/delete stays structural, and on <v3.2
	// a content update must also reload.
	certStructural := false
	if d.sslDiff != nil {
		if len(d.sslDiff.ToCreate) > 0 || len(d.sslDiff.ToDelete) > 0 {
			certStructural = true
		}
		if len(d.sslDiff.ToUpdate) > 0 {
			if caps.SupportsRuntimeSSLCerts {
				certUpdates = d.sslDiff.ToUpdate
			} else {
				certStructural = true
			}
		}
	}

	return mapUpdates, certUpdates, otherAuxChanged || mapStructural || certStructural
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
		SyncMode:          SyncModeNoChanges,
		Duration:          time.Since(startTime),
		Details:           convertDiffSummary(summary),
		Message:           "No configuration or auxiliary file changes detected",
	}
}

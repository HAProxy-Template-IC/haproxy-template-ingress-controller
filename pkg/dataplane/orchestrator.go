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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/enterprise"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// ConfigParser defines the interface for HAProxy configuration parsing.
// Both CE (parser.Parser) and EE (enterprise.Parser) parsers implement this interface.
type ConfigParser interface {
	ParseFromString(config string) (*parserconfig.StructuredConfig, error)
}

const (
	defaultReloadVerificationPollInterval = 500 * time.Millisecond

	stateEnabled  = "enabled"
	stateDisabled = "disabled"
)

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

	if c.Clientset().IsEnterprise() {
		logger.Info("Using Enterprise Edition parser for HAProxy EE")
		p, err = enterprise.NewParser()
		if err != nil {
			return nil, fmt.Errorf("creating EE parser: %w", err)
		}
	} else {
		p, err = parser.New()
		if err != nil {
			return nil, fmt.Errorf("creating parser: %w", err)
		}
	}

	return &orchestrator{
		client:     c,
		parser:     p,
		comparator: comparator.New(),
		logger:     logger,
	}, nil
}

// sync implements the complete sync workflow. There are only two outgoing
// shapes against the dataplane API:
//
//  1. Pure-runtime path (no structural diff, no aux changes): one
//     PushRawConfigurationSkipReload with X-Runtime-Actions. The dataplane
//     writes the new config to disk *and* applies `set server …` socket
//     commands to the live worker. No reload.
//
//  2. Reload path (any structural op, or any aux change): if any
//     runtime-eligible ops are present, a best-effort skip_reload push
//     seeds the old worker with the new state before the drain begins;
//     then a PushRawConfiguration with force_reload triggers the reload.
//
// The auxiliary file phases run on either side of the config push:
// pre-config uploads create/update files referenced by the new config;
// post-config cleanup deletes orphaned files only after the reload has
// been verified, so a delete can't race a worker that's still reading
// the on-disk config.
func (o *orchestrator) sync(ctx context.Context, desiredConfig string, opts *SyncOptions, auxFiles *AuxiliaryFiles) (result *SyncResult, err error) {
	startTime := time.Now()

	// Cache the pod's actual post-sync state for the caller (see SyncResult
	// for the cross-pod-drift rationale). Only fetch when ops applied — the
	// no-changes path already left the pod where the cache says it is.
	defer func() {
		if err != nil || result == nil || len(result.AppliedOperations) == 0 {
			return
		}
		o.populatePostSyncParsedConfig(ctx, result)
	}()

	currentConfigStr, preParsedCurrent, preCachedVersion, currentConfigChecksum, fetchErr := o.fetchCurrentConfig(ctx, opts)
	if fetchErr != nil {
		return nil, fetchErr
	}

	diff, err := o.parseAndCompareConfigs(currentConfigStr, desiredConfig, opts.PreParsedConfig, preParsedCurrent)
	if err != nil {
		return nil, err
	}

	o.logger.Debug("Sync diff computed", "op_count", len(diff.Operations))
	logOperationDetail(o.logger, "Sync.diff", diff.Operations)

	auxDiffs, err := o.checkForChanges(ctx, diff, desiredConfig, auxFiles, opts)
	if err != nil {
		return nil, err
	}

	// A headerless on-disk config (no `# _version=N` header) means the last
	// write was a skip_version push (the runtime bypass) — unverified content
	// no versioned, reload-coupled push vouches for. Both branches below must
	// treat it as such (see the comments at their use sites).
	currentIsHeaderless := currentConfigIsHeaderless(preCachedVersion, currentConfigStr)

	if !auxDiffs.hasChanges {
		// A headerless on-disk config means the last write was a skip_version
		// push — the runtime bypass, or the scheduler's fast-track apply of a
		// pending render's runtime subset.
		// Those pushes write the body VERBATIM without a reload, and the
		// dataplane writes the file even when the accompanying X-Runtime-
		// Actions FAIL. So "disk == desired" proves nothing about the
		// RUNNING worker: structural content can sit parked on disk that no
		// worker ever loaded, while the diff (desired vs disk) reads empty.
		// Returning no-changes here reports the deploy as successful and the
		// parked content stays hidden from a reload indefinitely (observed
		// in CI job 15180387459: new TCP listeners parked on disk for 90s,
		// Gateway reported Programmed, every connection refused). An empty
		// diff is only trustworthy when the config was written by a
		// versioned, reload-coupled push — otherwise force one reload to
		// activate whatever is on disk, which also re-stamps the header.
		if !o.onDiskIsProvenActivated(currentConfigChecksum, opts) {
			o.logger.Info("No diff against an on-disk config whose activation was never proven; forcing a reload to activate potentially parked content",
				"on_disk_checksum", currentConfigChecksum,
				"last_activated_checksum", lastActivatedChecksum(opts),
				"headerless", currentIsHeaderless)
			version := o.resolveCurrentVersion(ctx, preCachedVersion)
			if version <= 0 {
				return nil, &SyncError{
					Stage:   "version_resolve",
					Message: "failed to fetch dataplane config version",
					Hints:   []string{"Dataplane API unreachable or returned non-integer version"},
				}
			}
			return o.applyWithReload(ctx, desiredConfig, diff, nil, nil, auxDiffs, "", version, opts, startTime)
		}

		result := o.createNoChangesResult(startTime, &diff.Summary)
		// Carry the proof forward. This branch was reached BECAUSE the on-disk
		// config matches a proven activation, so it is still proven — dropping
		// it here would make the next sync force a reload against a config it
		// just verified, once per sync forever.
		result.ActivatedConfigChecksum = lastActivatedChecksum(opts)
		// Never report the headerless sentinel (1) as a cacheable
		// version: after a skip_version push (runtime bypass) every
		// state reads as 1, so a version-1 cache entry could later
		// false-hit against a body the bypass has since changed and
		// silently skip a needed sync (permanent drift). See
		// fetchCurrentConfig and SyncResult.PostSyncVersion.
		if preCachedVersion > headerlessConfigVersion {
			result.PostSyncVersion = preCachedVersion
		}
		return result, nil
	}

	return o.applyChanges(ctx, desiredConfig, diff, auxDiffs, opts, preCachedVersion, currentIsHeaderless, startTime)
}

// applyChanges executes the config + aux changes against the dataplane API
// using the two-shape strategy documented on sync().
func (o *orchestrator) applyChanges(
	ctx context.Context,
	desiredConfig string,
	diff *comparator.ConfigDiff,
	auxDiffs *auxiliaryFileDiffs,
	opts *SyncOptions,
	preCachedVersion int64,
	currentIsHeaderless bool,
	startTime time.Time,
) (*SyncResult, error) {
	// PhasePreConfig: create/update aux files before the config that
	// references them. Update* sends skip_reload=true; CREATE responses may
	// briefly reload, in which case verifyAuxiliaryReloads waits.
	auxReloadIDs, err := o.syncAuxiliaryFilesPreConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff)
	if err != nil {
		return nil, err
	}
	if err := o.verifyAuxiliaryReloads(ctx, auxReloadIDs, opts, "before config push"); err != nil {
		return nil, err
	}

	runtimeOps, structuralOps := partitionByRuntimeEligibility(diff.Operations)

	// Aux files normally force a reload, but content updates to existing maps
	// (v3.0+), SSL certs (v3.2+), and ca-files / mTLS trust bundles (v3.2+) are
	// split out as runtime-eligible (applied live via ReplaceRuntimeMap /
	// ReplaceRuntimeSSLCert / ReplaceRuntimeSSLCaFiles); other aux changes and
	// file create/delete keep forcing one.
	mapRuntimeUpdates, certRuntimeUpdates, caRuntimeUpdates, auxNeedsReload := auxDiffs.runtimeEligibleAuxUpdates(o.client.Capabilities())
	needsReload := len(structuralOps) > 0 || auxNeedsReload

	// A runtime/aux-only delta against a HEADERLESS on-disk config must still
	// reload (issue #84 mode B): headerless means the last write was an
	// unverified skip_version push — the dataplane writes such bodies to disk
	// even when their runtime actions FAIL, so the file can carry structural
	// content no worker ever loaded while the diff against it reads only
	// runtime deltas. A reload-free apply here would stamp the version header
	// over that parked content and report success without ever activating it
	// (routes 404 until an unrelated change reloads). Only a reload makes the
	// success truthful — the same rationale as the empty-diff headerless guard
	// in sync(). Endpoint-change latency is unaffected: the runtime bypass
	// never takes this path, and this full sync is already the interval-gated
	// structural deploy.
	if !needsReload && currentIsHeaderless {
		o.logger.Info("Runtime/aux-only delta against a headerless on-disk config; forcing a reload to activate potentially parked skip_version content")
		needsReload = true
	}

	actions := buildRuntimeActions(runtimeOps)

	version := o.resolveCurrentVersion(ctx, preCachedVersion)
	if version <= 0 {
		return nil, &SyncError{
			Stage:   "version_resolve",
			Message: "failed to fetch dataplane config version",
			Hints:   []string{"Dataplane API unreachable or returned non-integer version"},
		}
	}

	logOperationDetail(o.logger, "runtime", runtimeOps)
	logOperationDetail(o.logger, "structural", structuralOps)

	if !needsReload {
		return o.applyRuntimeOnly(ctx, desiredConfig, diff, runtimeOps, mapRuntimeUpdates, certRuntimeUpdates, caRuntimeUpdates, auxDiffs, actions, version, opts, startTime)
	}
	return o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, structuralOps, auxDiffs, actions, version, opts, startTime)
}

// verifyRuntimeMapRecheckDelay is the pause before the single re-check of a
// runtime-map read-back that reported divergence: long enough for a racy or
// momentarily-404 read to settle, short enough to keep the runtime lane fast.
const verifyRuntimeMapRecheckDelay = 200 * time.Millisecond

// verifyRuntimeMaps read-backs each replaced runtime map and returns the names
// of ALL that diverged from their desired content (empty when all converged).
// A divergence is re-checked once after verifyRuntimeMapRecheckDelay before it
// counts: a single stale/racy read (or a transient 404 before the map loads)
// must not disrupt the reload-free lane — only a PERSISTENT mismatch (the
// latching defect from issue #48) makes the caller pay for the reload
// fallback. The returned error is ctx cancellation only.
//
// Every map is checked even once one has diverged, although the outcome (one
// reload) is already decided: stopping early would report whichever map came
// first in slice order and hide the rest, so the operator chasing
// haptic_runtime_map_divergence_total would see an arbitrary one of the
// culprits. The extra reads are socket round-trips on a path that is about to
// reload anyway.
func (o *orchestrator) verifyRuntimeMaps(ctx context.Context, mapUpdates []auxiliaryfiles.MapFile) ([]string, error) {
	var diverged []string
	for _, m := range mapUpdates {
		pending, err := o.client.VerifyRuntimeMap(ctx, m.GetIdentifier(), m.GetContent())
		if err != nil || pending > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(verifyRuntimeMapRecheckDelay):
			}
			pending, err = o.client.VerifyRuntimeMap(ctx, m.GetIdentifier(), m.GetContent())
		}
		if err != nil || pending > 0 {
			o.logger.Warn("Runtime map diverged from desired state after apply; falling back to reload",
				"map", m.GetIdentifier(), "pending_ops", pending, "error", err)
			diverged = append(diverged, m.GetIdentifier())
		}
	}
	return diverged, nil
}

// applyRuntimeOnly applies all changes without a reload: runtime-eligible map
// and SSL-cert content updates go to the live worker via ReplaceRuntimeMap /
// ReplaceRuntimeSSLCert, then one PushRawConfigurationSkipReload writes the new
// config to disk and applies the batched `set server …` X-Runtime-Actions.
//
// If a runtime apply fails it falls back to applyWithReload. That is safe and
// convergent: syncAuxiliaryFilesPreConfig already wrote the new map/cert content
// to disk (skip_reload), so the force_reload re-reads it. structuralOps is empty
// on this path by construction.
func (o *orchestrator) applyRuntimeOnly(
	ctx context.Context,
	desiredConfig string,
	diff *comparator.ConfigDiff,
	runtimeOps []comparator.Operation,
	mapUpdates []auxiliaryfiles.MapFile,
	certUpdates []auxiliaryfiles.SSLCertificate,
	caUpdates []auxiliaryfiles.GeneralFile,
	auxDiffs *auxiliaryFileDiffs,
	actions string,
	version int64,
	opts *SyncOptions,
	startTime time.Time,
) (*SyncResult, error) {
	for _, m := range mapUpdates {
		if err := o.client.ReplaceRuntimeMap(ctx, m.GetIdentifier(), m.GetContent()); err != nil {
			o.logger.Warn("Runtime map apply failed; falling back to reload",
				"map", m.GetIdentifier(), "error", err)
			return o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, nil, auxDiffs, actions, version, opts, startTime)
		}
	}
	// Read-back verification: runtime map mutations are acknowledged by the
	// Dataplane API even when the master-socket command was lost in flight
	// (observed on the haproxytech 3.1 image under reload churn — issue #48:
	// the same entries were re-added 201-OK in consecutive deploy cycles with
	// no reload in between, while the live worker kept missing them). Without
	// this check the divergence LATCHES: the pre-config phase already wrote
	// the desired content to the on-disk file, so later deploys see no map
	// content diff, never re-run ReplaceRuntimeMap, and only an unrelated
	// reload heals routing. Falling back to a reload is always convergent —
	// the new worker reads the file this deploy just wrote.
	if divergedMaps, err := o.verifyRuntimeMaps(ctx, mapUpdates); err != nil {
		return nil, err
	} else if len(divergedMaps) > 0 {
		// Stamp the maps on the result so the reload-free lane degrading is
		// visible as a metric, not only as a log line.
		res, rerr := o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, nil, auxDiffs, actions, version, opts, startTime)
		if res != nil {
			res.DivergedRuntimeMaps = append(res.DivergedRuntimeMaps, divergedMaps...)
		}
		return res, rerr
	}
	if len(certUpdates) > 0 {
		pemByName := make(map[string]string, len(certUpdates))
		for _, cert := range certUpdates {
			pemByName[cert.GetIdentifier()] = cert.GetContent()
		}
		if err := o.client.ReplaceRuntimeSSLCerts(ctx, pemByName); err != nil {
			o.logger.Warn("Runtime SSL cert apply failed; falling back to reload", "error", err)
			return o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, nil, auxDiffs, actions, version, opts, startTime)
		}
	}
	if len(caUpdates) > 0 {
		contentByPath := make(map[string]string, len(caUpdates))
		for _, ca := range caUpdates {
			contentByPath[ca.Path] = ca.GetContent()
		}
		if err := o.client.ReplaceRuntimeSSLCaFiles(ctx, contentByPath); err != nil {
			o.logger.Warn("Runtime SSL ca-file apply failed; falling back to reload", "error", err)
			return o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, nil, auxDiffs, actions, version, opts, startTime)
		}
	}

	o.logger.Debug("Pure-runtime sync: runtime map/cert/ca-file updates + single skip_reload push with X-Runtime-Actions",
		"op_count", len(runtimeOps),
		"map_updates", len(mapUpdates),
		"cert_updates", len(certUpdates),
		"ca_updates", len(caUpdates),
		"action_count", actionCount(actions))

	if err := o.client.PushRawConfigurationSkipReload(ctx, desiredConfig, version, actions); err != nil {
		return nil, wrapApplyError(err)
	}

	// Orphan deletes are deferred to post-config on BOTH lanes — the pre-config
	// phase drops ToDelete — so this lane has to run them too. Without it the
	// files stay on disk until some unrelated change takes the reload path,
	// while buildAppliedOps below already reports them deleted.
	//
	// Safe without a reload: the only deletes that reach this lane are general
	// files the desired config does not name (every other auxiliary delete sets
	// structural and routes to applyWithReload), and this lane runs only when
	// the config diff is runtime-eligible server fields — which name no
	// auxiliary file — so the running worker's config cannot name them either.
	// applyChanges additionally forces the reload path on a headerless config,
	// so "the worker is running these bytes" holds here.
	o.deleteUnreferencedFilesPostConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff)

	appliedOps := o.buildAppliedOps(runtimeOps, nil, auxDiffs)
	return &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   false,
		SyncMode:          SyncModeRuntime,
		// The versioned skip_reload push wrote this body AND the live worker
		// accepted the runtime actions, so the running state matches these bytes
		// without a reload. The push is versioned, so the dataplane prepends a
		// header — activationChecksum strips it, which is why checksumming the
		// pushed body matches what the next sync reads off disk.
		//
		// Omitting this does not merely lose an optimisation: the deployer writes
		// whatever comes back, so an empty value CLEARS the proof and the next
		// sync reloads against a config this one just activated. Reload per sync.
		ActivatedConfigChecksum: activationChecksum(desiredConfig),
		Duration:                time.Since(startTime),
		Details:                 o.buildDetails(diff, auxDiffs),
		PostSyncVersion:         version + 1,
		Message:                 fmt.Sprintf("Applied %d runtime operations (%d map, %d cert, %d ca-file updates) without reload", len(appliedOps), len(mapUpdates), len(certUpdates), len(caUpdates)),
	}, nil
}

// applyWithReload issues a best-effort skip_reload+actions push (when any
// runtime-eligible ops exist, to seed the old worker before drain) followed
// by a force_reload push. After verifying the reload, deletes orphaned aux
// files.
func (o *orchestrator) applyWithReload(
	ctx context.Context,
	desiredConfig string,
	diff *comparator.ConfigDiff,
	runtimeOps, structuralOps []comparator.Operation,
	auxDiffs *auxiliaryFileDiffs,
	actions string,
	version int64,
	opts *SyncOptions,
	startTime time.Time,
) (*SyncResult, error) {
	o.logger.Debug("Reload sync: optional skip_reload+actions then force_reload push",
		"runtime_ops", len(runtimeOps),
		"structural_ops", len(structuralOps),
		"aux_changed", auxDiffs.anyDiffHasChanges())

	if actions != "" {
		// Best-effort: write the new config to disk and seed the live
		// worker with `set server …` socket commands before force_reload
		// drains it. Failure here doesn't break correctness — force_reload
		// re-stages the config and the new worker reads it from disk —
		// but loses the in-flight-drain benefit.
		//
		// This seed reaches only the CURRENT worker. A keep-alive request
		// pinned to the LEAVING worker after an EndpointSlice flip that
		// races this POST is the bounded residual in issue #70 / ADR-0013.
		// Do NOT "fix" it by re-collecting `actions` here against live pod
		// state: `desiredConfig` is the pre-flip render, so a late re-diff
		// against a post-flip on-disk body (written by a concurrent runtime
		// bypass) would emit REVERT actions that stomp the fresher state onto
		// the draining worker. Reaching the leaving worker needs Dataplane API
		// master-CLI worker routing (`@!<pid>`), which no deployed version
		// exposes — see ADR-0013.
		if err := o.client.PushRawConfigurationSkipReload(ctx, desiredConfig, version, actions); err != nil {
			o.logger.Warn("Skip_reload+actions push failed; force_reload will converge state",
				"error", err)
		} else {
			// Successful skip_reload push bumped the version.
			refetched, vErr := o.client.GetVersion(ctx)
			if vErr != nil {
				return nil, &SyncError{
					Stage:   "version_refetch",
					Message: "failed to refetch version after skip_reload push",
					Cause:   vErr,
				}
			}
			version = refetched
		}
	}

	reloadID, err := o.client.PushRawConfiguration(ctx, desiredConfig, version)
	if err != nil {
		return nil, wrapApplyError(err)
	}

	reloadVerified := reloadID == "" // sync 200 means reload already finished
	if !reloadVerified && opts.VerifyReload {
		if verifyErr := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); verifyErr != nil {
			// Skip the orphan-delete: deleting aux files while the new
			// worker is still loading the on-disk config can turn a
			// recoverable failure into a stuck reload loop.
			o.logger.Error("Reload verification failed; skipping orphan aux-file delete to avoid mid-reload race",
				"reload_id", reloadID, "error", verifyErr)
			return &SyncResult{
					Success:                 false,
					AppliedOperations:       o.buildAppliedOps(runtimeOps, structuralOps, auxDiffs),
					ReloadTriggered:         true,
					ReloadID:                reloadID,
					ReloadVerified:          false,
					ReloadVerificationError: verifyErr.Error(),
					SyncMode:                SyncModeReload,
					Duration:                time.Since(startTime),
					Details:                 o.buildDetails(diff, auxDiffs),
				}, &SyncError{
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

	// Post-reload read-back (issue #84): a synchronous 2xx — or even a
	// verified async reload — only proves the dataplane processed OUR push,
	// not that the body it wrote is what the master's re-exec actually read.
	// A concurrent skip_version writer can clobber the file between this
	// deploy's write and the re-exec read (observed: the deploy's 201 echoed
	// 97,996 B when 111,893 B was pushed — three consecutive fresh workers
	// activated pre-route configs while status.deployedToPods advanced).
	// Read the disk back and refuse to report success when it STRUCTURALLY
	// diverged from the pushed body; the fast deploy retry (#72) then
	// redeploys. Runtime-only byte divergence is tolerated — a concurrent
	// runtime-bypass push legitimately patches server fields onto this very
	// body. Runs before the orphan aux-file delete: on divergence the worker's
	// loaded config is unknown, so deleting files it may reference is unsafe.
	readBackParsed, activatedChecksum, readBackMatchesDesired, readBackErr := o.verifyPostReloadReadBack(ctx, desiredConfig, reloadID, opts)
	if readBackErr != nil {
		o.logger.Error("Post-reload read-back failed; skipping orphan aux-file delete and reporting the deploy failed",
			"reload_id", reloadID, "error", readBackErr)
		return &SyncResult{
			Success:           false,
			AppliedOperations: o.buildAppliedOps(runtimeOps, structuralOps, auxDiffs),
			ReloadTriggered:   true,
			ReloadID:          reloadID,
			ReloadVerified:    reloadVerified,
			SyncMode:          SyncModeReload,
			Duration:          time.Since(startTime),
			Details:           o.buildDetails(diff, auxDiffs),
		}, readBackErr
	}

	// Safe to delete orphaned aux files only after the reload is verified.
	o.deleteUnreferencedFilesPostConfig(ctx, auxDiffs.fileDiff, auxDiffs.sslDiff, auxDiffs.caFileDiff, auxDiffs.mapDiff)

	appliedOps := o.buildAppliedOps(runtimeOps, structuralOps, auxDiffs)
	return &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   true,
		ReloadID:          reloadID,
		ReloadVerified:    reloadVerified,
		SyncMode:          SyncModeReload,
		Duration:          time.Since(startTime),
		Details:           o.buildDetails(diff, auxDiffs),
		PostSyncVersion:   version + 1,
		// The read-back already fetched + parsed the pod's actual post-sync
		// config; hand it to the caller's cache so populatePostSyncParsedConfig
		// doesn't fetch a second time. Nil when the read-back parse failed
		// (best-effort — the deferred populate then retries).
		PostSyncParsedConfig: readBackParsed,
		// The comparator, rather than byte equality, proves when this endpoint
		// can share the caller's desired parsed graph.
		PostSyncConfigMatchesDesired: readBackMatchesDesired,
		// Proof for the next sync: the reload was verified AND the read-back
		// confirmed these exact bytes are on disk. Without both, an empty diff
		// against this content must not be trusted.
		ActivatedConfigChecksum: activatedChecksum,
		Message:                 fmt.Sprintf("Successfully applied %d operations", len(appliedOps)),
	}, nil
}

// verifyPostReloadReadBack fetches the pod's on-disk config after a reload and
// verifies it still matches the pushed body (issue #84). Returns the parsed
// read-back config for the caller's post-sync cache (parsed may be nil on
// success when parsing failed on byte-identical content — best-effort).
//
// Verdicts:
//   - byte-identical (ignoring the `# _version`/`# _md5hash` header lines the
//     versioned push prepends): success.
//   - byte-divergent but structurally identical (only runtime-eligible server
//     field diffs): success — a concurrent runtime-bypass push patched pod
//     addresses onto this body after our write; the reload truthfully
//     activated this render's structural content.
//   - structurally divergent (or unparseable/uncomparable): a
//     stagePostReloadDivergence SyncError — a concurrent writer replaced the
//     config between this deploy's write and the read-back, so success would
//     be untruthful.
//   - fetch failure after retries: a stagePostReloadReadback SyncError — the
//     pod's state is unknown; the retry re-syncs it.
//
// Each per-deploy read-back logs the pushed and on-disk checksums so a
// divergence is diagnosable from the controller log alone.
func (o *orchestrator) verifyPostReloadReadBack(ctx context.Context, pushedBody, reloadID string, opts *SyncOptions) (parsed *parserconfig.StructuredConfig, onDiskChecksum string, matchesDesired bool, err error) {
	retry := client.RetryConfig{
		MaxAttempts: 4,
		// The reload already succeeded and was verified; this read is purely
		// observational (clobber detection). The dataplane API can briefly 5xx
		// while HAProxy re-execs right after the reload, so retry transient
		// server errors too — not just connection failures. A read that keeps
		// failing across all attempts is genuine unknown-state and re-syncs.
		RetryIf:   client.IsTransientReadError(),
		Backoff:   client.BackoffExponential,
		BaseDelay: 100 * time.Millisecond,
		Logger:    o.logger.With("operation", "post_reload_readback"),
	}
	readBack, err := client.WithRetry(ctx, retry, func(int) (string, error) {
		return o.client.GetRawConfiguration(ctx)
	})
	if err != nil {
		return nil, "", false, &SyncError{
			Stage:   stagePostReloadReadback,
			Message: "failed to read back on-disk config after reload",
			Cause:   err,
			Hints: []string{
				"Dataplane API stopped answering right after a verified reload",
				hintCheckHAProxyLogs,
			},
		}
	}

	pushedChecksum := configTextChecksum(pushedBody)
	readBackChecksum := activationChecksum(readBack)
	// No `match` field: the dataplane re-serialises what it stores (it names an
	// anonymous `defaults`, reorders directives, drops blank lines), so a
	// production config essentially never comes back byte-identical — 0 of 78
	// read-backs in one e2e run. Logging that as a boolean reads like an
	// anomaly indicator that is permanently tripped, and cost real time during
	// the #84 investigation before it turned out to be a constant. The
	// structural comparison below is the actual check; these checksums are here
	// to identify WHICH bytes diverged, not to flag THAT they did.
	o.logger.Info("Post-reload config read-back",
		"pushed_checksum", pushedChecksum,
		"readback_checksum", readBackChecksum,
		"reload_id", reloadID,
		"endpoint", o.client.Endpoint.URL)

	readBackParsed, parseErr := o.parser.ParseFromString(readBack)
	matchesDesired, err = o.classifyPostReloadReadBack(readBackParsed, parseErr, pushedBody, opts, pushedChecksum, readBackChecksum)
	if err != nil {
		return nil, "", false, err
	}
	// The activation proof is always taken over what is ACTUALLY on disk,
	// including when the parsed graph can be shared with desired.
	return readBackParsed, readBackChecksum, matchesDesired, nil
}

// classifyPostReloadReadBack proves graph equivalence only for a zero-op diff.
// Byte-identical but unproven content remains a successful deploy; byte-
// divergent structural or unprovable content fails.
func (o *orchestrator) classifyPostReloadReadBack(readBackParsed *parserconfig.StructuredConfig, parseErr error, pushedBody string, opts *SyncOptions, pushedChecksum, readBackChecksum string) (bool, error) {
	divergence := func(message string, cause error) error {
		return &SyncError{
			Stage:   stagePostReloadDivergence,
			Message: message,
			Cause:   cause,
			Hints: []string{
				"A concurrent writer replaced the on-disk config between this deploy's write and the read-back",
				"The fast deploy retry redeploys this render",
			},
		}
	}
	byteIdentical := pushedChecksum == readBackChecksum
	unproven := func(message string, cause error) (bool, error) {
		if byteIdentical {
			o.logger.Debug(message, "error", cause)
			return false, nil
		}
		return false, divergence(message, cause)
	}
	if parseErr != nil {
		return unproven(
			fmt.Sprintf("cannot parse on-disk config after reload (pushed %s, on disk %s)", pushedChecksum, readBackChecksum),
			parseErr)
	}
	desiredParsed := opts.PreParsedConfig
	if desiredParsed == nil {
		var err error
		desiredParsed, err = o.parser.ParseFromString(pushedBody)
		if err != nil {
			return unproven("cannot parse the pushed body to prove read-back equivalence", err)
		}
	}
	diff, err := o.comparator.Compare(readBackParsed, desiredParsed)
	if err != nil {
		return unproven("cannot compare the read-back config against the pushed body", err)
	}
	if len(diff.Operations) == 0 {
		return true, nil
	}
	if _, structuralOps := partitionByRuntimeEligibility(diff.Operations); len(structuralOps) > 0 {
		if byteIdentical {
			o.logger.Debug("Post-reload comparator disagreed with byte-identical pushed content; retaining the actual parsed config",
				"operation_count", len(diff.Operations))
			return false, nil
		}
		logOperationDetail(o.logger, "post_reload_divergence", structuralOps)
		return false, divergence(
			fmt.Sprintf("on-disk config after reload structurally diverged from the pushed body (%d structural ops; pushed %s, on disk %s)", len(structuralOps), pushedChecksum, readBackChecksum),
			nil)
	}
	return false, nil
}

// onDiskIsProvenActivated reports whether an empty diff can be trusted: the
// config sitting on disk is byte-identical to one this endpoint was PROVEN to
// be running.
//
// "Disk == desired" is not that proof. A skip_version push writes the body
// verbatim with no reload, and the dataplane writes it even when the runtime
// actions that accompany it fail — so structural content can be parked on disk
// that no worker ever loaded, while the diff reads empty (#112).
//
// With no proof recorded (first sync to this pod, a restarted controller, a
// cleared entry) the answer is NO. Forcing one reload is cheap and correct;
// trusting an unproven config is what strands a render indefinitely.
func (o *orchestrator) onDiskIsProvenActivated(currentConfigChecksum string, opts *SyncOptions) bool {
	activated := lastActivatedChecksum(opts)
	if activated == "" || currentConfigChecksum == "" {
		return false
	}
	return currentConfigChecksum == activated
}

// activationChecksum is the single definition of the bytes an activation proof
// is taken over. The `# _version=N` / `# _md5hash=` lines a versioned push
// prepends are stripped, because they change on every versioned write without
// the config changing — comparing with them included would report every
// endpoint as unproven and force a reload on every sync.
//
// Both the recorder (the post-reload read-back) and the reader (the empty-diff
// guard) go through here so they cannot drift apart; when they did, the
// mismatch was silent and cost one reload per sync.
func activationChecksum(config string) string {
	return configTextChecksum(stripVersionHeaderLines(config))
}

// lastActivatedChecksum reads the caller's recorded proof, tolerating nil opts.
func lastActivatedChecksum(opts *SyncOptions) string {
	if opts == nil {
		return ""
	}
	return opts.LastActivatedConfigChecksum
}

// configTextChecksum returns a short sha256 hex digest of the config text with
// surrounding whitespace trimmed — the pushed-vs-on-disk comparison unit for
// the post-reload read-back.
func configTextChecksum(config string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(config)))
	return hex.EncodeToString(sum[:8])
}

// stripVersionHeaderLines drops the leading `# _version=N` / `# _md5hash=…`
// header lines a versioned dataplane push prepends, so the remaining text is
// comparable to the body the caller pushed.
func stripVersionHeaderLines(config string) string {
	for {
		line, rest, found := strings.Cut(config, "\n")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# _version=") || strings.HasPrefix(trimmed, "# _md5hash=") {
			if !found {
				return ""
			}
			config = rest
			continue
		}
		return config
	}
}

func (o *orchestrator) buildAppliedOps(runtimeOps, structuralOps []comparator.Operation, auxDiffs *auxiliaryFileDiffs) []AppliedOperation {
	out := make([]AppliedOperation, 0, len(runtimeOps)+len(structuralOps))
	out = append(out, convertOperationsToApplied(runtimeOps)...)
	out = append(out, convertOperationsToApplied(structuralOps)...)
	out = append(out, auxDiffsToOperations(auxDiffs)...)
	return out
}

func (o *orchestrator) buildDetails(diff *comparator.ConfigDiff, auxDiffs *auxiliaryFileDiffs) DiffDetails {
	details := convertDiffSummary(&diff.Summary)
	addAuxiliaryFileCounts(&details, auxDiffs)
	return details
}

// resolveCurrentVersion returns the pod's current config version, reusing the
// pre-cached value when fetchCurrentConfig already called GetVersion(),
// otherwise issuing a retried GetVersion call. Returns -1 on persistent
// failure.
func (o *orchestrator) resolveCurrentVersion(ctx context.Context, preCachedVersion int64) int64 {
	if preCachedVersion > 0 {
		return preCachedVersion
	}
	retry := client.RetryConfig{
		MaxAttempts: 3,
		RetryIf:     client.IsConnectionError(),
		Backoff:     client.BackoffExponential,
		BaseDelay:   100 * time.Millisecond,
		Logger:      o.logger.With("operation", "fetch_version"),
	}
	version, err := client.WithRetry(ctx, retry, func(attempt int) (int64, error) {
		return o.client.GetVersion(ctx)
	})
	if err != nil {
		o.logger.Warn("Failed to get config version", "error", err)
		return -1
	}
	return version
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
				o.logger.Warn("Reload status check failed, retrying", "reload_id", reloadID, "error", err)
				continue
			}

			switch info.Status {
			case client.ReloadStatusSucceeded:
				o.logger.Debug("Reload verified successful", "reload_id", reloadID)
				return nil
			case client.ReloadStatusFailed:
				o.logger.Error("Reload failed", "reload_id", reloadID, "response", info.Response)
				return fmt.Errorf("reload failed: %s", info.Response)
			case client.ReloadStatusInProgress:
				o.logger.Debug("Reload still in progress", "reload_id", reloadID)
			}

		case <-ctx.Done():
			return fmt.Errorf("reload verification timed out after %v", timeout)
		}
	}
}

// verifyAuxiliaryReloads waits for any aux-file uploads that triggered reloads
// to finish before the config push references those files.
func (o *orchestrator) verifyAuxiliaryReloads(ctx context.Context, reloadIDs []string, opts *SyncOptions, logContext string) error {
	if !opts.VerifyReload || len(reloadIDs) == 0 {
		return nil
	}

	o.logger.Debug("Verifying auxiliary file reloads", "context", logContext, "count", len(reloadIDs), "reload_ids", reloadIDs)

	for _, reloadID := range reloadIDs {
		if err := o.verifyReload(ctx, reloadID, opts.ReloadVerificationTimeout); err != nil {
			return &SyncError{
				Stage:   "auxiliary_reload_verification",
				Message: fmt.Sprintf("auxiliary file reload %s failed", reloadID),
				Cause:   err,
				Hints: []string{
					"HAProxy auxiliary file reload failed",
					"Map file or SSL certificate update may not have been applied",
					hintCheckHAProxyLogs,
				},
			}
		}
	}

	return nil
}

// populatePostSyncParsedConfig fetches the pod's actual configuration after a
// successful sync and parses it into result.PostSyncParsedConfig. Best-effort:
// a failed fetch or parse logs at Debug and leaves the field nil. No-op when
// the field is already set (applyWithReload's post-reload read-back captured
// it — no second fetch needed).
func (o *orchestrator) populatePostSyncParsedConfig(ctx context.Context, result *SyncResult) {
	if result.PostSyncParsedConfig != nil {
		return
	}
	rawConfig, err := o.client.GetRawConfiguration(ctx)
	if err != nil {
		o.logger.Debug("Failed to fetch post-sync config for caller's cache",
			"endpoint", o.client.Endpoint.URL, "error", err)
		return
	}
	// Uncached: GetRawConfiguration returns HAProxy's own config, which carries
	// the _version header and so is a different string on every push. Caching it
	// could never hit and evicted the desired config instead. The caller reuses
	// this via SyncResult.PostSyncParsedConfig, not via the cache.
	parsed, err := o.parser.ParseFromString(rawConfig)
	if err != nil {
		o.logger.Debug("Failed to parse post-sync config for caller's cache",
			"endpoint", o.client.Endpoint.URL, "error", err)
		return
	}
	result.PostSyncParsedConfig = parsed
}

// partitionByRuntimeEligibility splits ops into runtime-eligible server
// updates (apply via X-Runtime-Actions, no reload) and everything else
// (requires force_reload).
func partitionByRuntimeEligibility(ops []comparator.Operation) (runtime, structural []comparator.Operation) {
	for _, op := range ops {
		if serverOp, ok := op.(*sections.ServerUpdateOp); ok &&
			op.Type() == sections.OperationUpdate && serverOp.IsFullyRuntimeEligible() {
			runtime = append(runtime, op)
			continue
		}
		// A frontend maxconn-only change applies via `set maxconn frontend`
		// (X-Runtime-Actions). The comparator only produces this op when maxconn
		// is the sole differing attribute and the desired value is set, so it is
		// runtime-eligible by construction.
		if _, ok := op.(*sections.FrontendMaxconnUpdateOp); ok {
			runtime = append(runtime, op)
			continue
		}
		structural = append(structural, op)
	}
	return runtime, structural
}

// buildRuntimeActions converts runtime-eligible server update operations into
// the semicolon-separated X-Runtime-Actions string expected by the DataPlane
// API's skip_reload endpoint. Every action generated here is a valid stats
// socket command verified in dataplaneapi handlers/raw.go:executeRuntimeActions.
//
// It is a *delta* function: for each ServerUpdateOp it emits one action per
// field that actually differs between current and desired. A diff that only
// touches the inline `# Pod: …` metadata comment produces no actions; a diff
// that only changes weight produces only `SetServerWeight`. Without this gate
// the chart's typical 30-slot backend would spam ~30 redundant `SetServerAddr`
// calls per render during pod churn.
//
// Action ordering within a single server respects maintenance transitions to
// avoid serving traffic at a half-applied state:
//
//   - Entering maint  (cur != "enabled", des == "enabled"):
//     `SetServerState … maint` is emitted FIRST, then addr/weight/agent.
//     Live worker drains before its destination changes.
//   - Leaving maint   (cur != "disabled", des == "disabled"):
//     addr/weight/agent are emitted first, then `SetServerState … ready`.
//     Server is fully configured before traffic resumes.
//   - No transition:  setup actions first, then any state action (this branch
//     never fires under the current chart, but documents the fallback).
//
// Keep in sync with serverRuntimeSupportedJSONFields in
// pkg/dataplane/comparator/sections/factory_server.go: every field listed
// there must produce a corresponding action, otherwise IsFullyRuntimeEligible
// approves a change this function silently ignores. Conversely, fields with
// no runtime command (metadata, or clearing a previously-set agent string)
// are intentionally absent — the on-disk config the skip_reload push wrote
// converges on the next reload.
func buildRuntimeActions(operations []comparator.Operation) string {
	var actions []string
	for _, op := range operations {
		switch o := op.(type) {
		case *sections.ServerUpdateOp:
			actions = append(actions, serverDeltaActions(o)...)
		case *sections.FrontendMaxconnUpdateOp:
			actions = append(actions, o.RuntimeAction())
		}
	}
	return strings.Join(actions, ";")
}

// serverDeltaActions returns the X-Runtime-Actions commands needed to take a
// single server from its current to its desired state. See buildRuntimeActions
// for the ordering and emission rules.
func serverDeltaActions(op *sections.ServerUpdateOp) []string {
	cur := op.CurrentServer()
	des := op.Server()
	backend := op.BackendName()
	name := op.ServerName()

	enterMaint := des.Maintenance == stateEnabled && cur.Maintenance != stateEnabled
	leaveMaint := des.Maintenance == stateDisabled && cur.Maintenance != stateDisabled

	stateAction := ""
	switch {
	case enterMaint:
		stateAction = fmt.Sprintf("SetServerState %s %s maint", backend, name)
	case leaveMaint:
		stateAction = fmt.Sprintf("SetServerState %s %s ready", backend, name)
	}

	setup := serverDeltaSetupActions(cur, des, backend, name)

	switch {
	case enterMaint:
		// Drain first, then reconfigure.
		return append([]string{stateAction}, setup...)
	case leaveMaint:
		// Reconfigure first, then accept traffic.
		return append(setup, stateAction)
	default:
		return setup
	}
}

// serverDeltaSetupActions emits the non-state runtime actions (addr/port,
// weight, check-port, agent fields) for the delta between cur and des.
// Each action is gated on "this field actually changed". Fields whose new
// value is not representable as a runtime command (empty address with set
// port, AgentAddr/AgentSend containing the parser's whitespace/semicolon
// delimiters, a now-cleared field that has no clear-style command) are
// skipped — the on-disk config carries the new value into the next reload.
func serverDeltaSetupActions(cur, des *models.Server, backend, name string) []string {
	var actions []string

	if (cur.Address != des.Address || !int64PtrEqual(cur.Port, des.Port)) &&
		des.Address != "" && des.Port != nil {
		actions = append(actions, fmt.Sprintf("SetServerAddr %s %s %s %d", backend, name, des.Address, *des.Port))
	}

	if !int64PtrEqual(cur.Weight, des.Weight) && des.Weight != nil {
		actions = append(actions, fmt.Sprintf("SetServerWeight %s %s %d", backend, name, *des.Weight))
	}

	if !int64PtrEqual(cur.HealthCheckPort, des.HealthCheckPort) && des.HealthCheckPort != nil {
		actions = append(actions, fmt.Sprintf("SetServerCheckPort %s %s %d", backend, name, *des.HealthCheckPort))
	}

	if cur.AgentCheck != des.AgentCheck {
		switch des.AgentCheck {
		case stateEnabled:
			actions = append(actions, fmt.Sprintf("EnableAgentCheck %s %s", backend, name))
		case stateDisabled:
			actions = append(actions, fmt.Sprintf("DisableAgentCheck %s %s", backend, name))
		}
	}

	// AgentAddr / AgentSend: dataplane parses actions by splitting on " " and
	// the actions list by splitting on ";", so either delimiter in the value
	// silently corrupts the command. Refuse to emit; rely on the reload to
	// pick up the on-disk value.
	if cur.AgentAddr != des.AgentAddr && des.AgentAddr != "" && safeRuntimeArg(des.AgentAddr) {
		actions = append(actions, fmt.Sprintf("SetServerAgentAddr %s %s %s", backend, name, des.AgentAddr))
	}
	if cur.AgentSend != des.AgentSend && des.AgentSend != "" && safeRuntimeArg(des.AgentSend) {
		actions = append(actions, fmt.Sprintf("SetServerAgentSend %s %s %s", backend, name, des.AgentSend))
	}

	return actions
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// safeRuntimeArg reports whether s can be safely embedded as a single argument
// in the semicolon-separated, space-tokenized X-Runtime-Actions string the
// dataplane parses in handlers/raw.go:executeRuntimeActions.
func safeRuntimeArg(s string) bool {
	return !strings.ContainsAny(s, " ;")
}

func actionCount(actions string) int {
	if actions == "" {
		return 0
	}
	return strings.Count(actions, ";") + 1
}

func logOperationDetail(logger interface {
	Debug(msg string, args ...any)
}, partition string, ops []comparator.Operation) {
	for i, op := range ops {
		logger.Debug("Diff op",
			"partition", partition,
			"i", i,
			"type", op.Type(),
			"section", op.Section(),
			"describe", op.Describe())
	}
}

func wrapApplyError(err error) error {
	return &SyncError{
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

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

	currentConfigStr, preParsedCurrent, preCachedVersion, fetchErr := o.fetchCurrentConfig(ctx, opts)
	if fetchErr != nil {
		return nil, fetchErr
	}

	diff, err := o.parseAndCompareConfigs(currentConfigStr, desiredConfig, opts.PreParsedConfig, preParsedCurrent)
	if err != nil {
		return nil, err
	}

	o.logger.Debug("Sync diff computed", "op_count", len(diff.Operations))
	logOperationDetail(o.logger, "Sync.diff", diff.Operations)

	auxDiffs, err := o.checkForChanges(ctx, diff, auxFiles, opts)
	if err != nil {
		return nil, err
	}

	if !auxDiffs.hasChanges {
		result := o.createNoChangesResult(startTime, &diff.Summary)
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

	return o.applyChanges(ctx, desiredConfig, diff, auxDiffs, opts, preCachedVersion, startTime)
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
	// (v3.0+) and SSL certs (v3.2+) are split out as runtime-eligible (applied
	// live via ReplaceRuntimeMap / ReplaceRuntimeSSLCert); other aux changes and
	// file create/delete keep forcing one.
	mapRuntimeUpdates, certRuntimeUpdates, auxNeedsReload := auxDiffs.runtimeEligibleAuxUpdates(o.client.Capabilities())
	needsReload := len(structuralOps) > 0 || auxNeedsReload
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
		return o.applyRuntimeOnly(ctx, desiredConfig, diff, runtimeOps, mapRuntimeUpdates, certRuntimeUpdates, auxDiffs, actions, version, opts, startTime)
	}
	return o.applyWithReload(ctx, desiredConfig, diff, runtimeOps, structuralOps, auxDiffs, actions, version, opts, startTime)
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

	o.logger.Debug("Pure-runtime sync: runtime map/cert updates + single skip_reload push with X-Runtime-Actions",
		"op_count", len(runtimeOps),
		"map_updates", len(mapUpdates),
		"cert_updates", len(certUpdates),
		"action_count", actionCount(actions))

	if err := o.client.PushRawConfigurationSkipReload(ctx, desiredConfig, version, actions); err != nil {
		return nil, wrapApplyError(err)
	}

	appliedOps := o.buildAppliedOps(runtimeOps, nil, auxDiffs)
	return &SyncResult{
		Success:           true,
		AppliedOperations: appliedOps,
		ReloadTriggered:   false,
		SyncMode:          SyncModeRuntime,
		Duration:          time.Since(startTime),
		Details:           o.buildDetails(diff, auxDiffs),
		PostSyncVersion:   version + 1,
		Message:           fmt.Sprintf("Applied %d runtime operations (%d map, %d cert updates) without reload", len(appliedOps), len(mapUpdates), len(certUpdates)),
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
		if err := o.client.PushRawConfigurationSkipReload(ctx, desiredConfig, version, actions); err != nil {
			o.logger.Warn("skip_reload+actions push failed; force_reload will converge state",
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
		Message:           fmt.Sprintf("Successfully applied %d operations", len(appliedOps)),
	}, nil
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
// a failed fetch or parse logs at Debug and leaves the field nil.
func (o *orchestrator) populatePostSyncParsedConfig(ctx context.Context, result *SyncResult) {
	rawConfig, err := o.client.GetRawConfiguration(ctx)
	if err != nil {
		o.logger.Debug("Failed to fetch post-sync config for caller's cache",
			"endpoint", o.client.Endpoint.URL, "error", err)
		return
	}
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
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if op.Type() == sections.OperationUpdate && ok && serverOp.IsFullyRuntimeEligible() {
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
		serverOp, ok := op.(*sections.ServerUpdateOp)
		if !ok {
			continue
		}
		actions = append(actions, serverDeltaActions(serverOp)...)
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
		logger.Debug("diff op",
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

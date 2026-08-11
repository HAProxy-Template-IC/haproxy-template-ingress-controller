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

package deployer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// performDeployment executes a single deployment.
//
// This method is called from HandleEvent for each dispatched
// DeploymentScheduledEvent. "Latest wins" coalescing of pending coalescible
// DeploymentScheduledEvents is provided by the embedded component.Base via
// the CoalescesOn hook (see component.go) — after each dispatch the Base
// drains the subscription channel and re-dispatches only the latest pending
// coalescible event, so intermediate configs queued during a deployment are
// superseded instead of deployed one by one.
//
// Defensive: drops duplicate events if a deployment is already in progress.
func (c *Component) performDeployment(ctx context.Context, event *events.DeploymentScheduledEvent) {
	// Track processing for health check stall detection
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	correlationID := event.CorrelationID()
	deploymentID := event.EventID()

	// Defensive check: atomically set deploymentInProgress from false to true
	// This prevents concurrent deployments if scheduler has bugs
	if !c.deploymentInProgress.CompareAndSwap(false, true) {
		c.Logger().Error("Dropping duplicate DeploymentScheduledEvent - deployment already in progress",
			"reason", event.Reason,
			"endpoint_count", len(event.Endpoints),
			"correlation_id", correlationID)
		return
	}
	// Note: flag will be cleared by deployToEndpoints after deployment completes

	// Create cancellable context for this deployment
	deployCtx, cancel := context.WithCancel(ctx)

	// Store cancel function so it can be called on timeout
	c.cancelMu.Lock()
	c.activeDeploymentID = deploymentID
	c.activeCorrelationID = correlationID
	c.activeCancelFunc = cancel
	c.deploymentDone = make(chan struct{})
	c.cancelMu.Unlock()

	// Ensure we clean up cancel state when deployment completes
	defer func() {
		c.cancelMu.Lock()
		c.activeDeploymentID = ""
		c.activeCorrelationID = ""
		c.activeCancelFunc = nil
		if c.deploymentDone != nil {
			close(c.deploymentDone)
			c.deploymentDone = nil
		}
		c.cancelMu.Unlock()
	}()

	c.Logger().Debug("Deployment scheduled, starting execution",
		"reason", event.Reason,
		"endpoint_count", len(event.Endpoints),
		"config_bytes", len(event.Config),
		"has_parsed_config", event.ParsedConfig != nil,
		"deployment_id", deploymentID,
		"correlation_id", correlationID)

	// Execute deployment with cancellable context
	c.deployToEndpoints(deployCtx, event.Config, event.AuxiliaryFiles, event.ParsedConfig, event.Endpoints, event.RuntimeConfigName, event.RuntimeConfigNamespace, event.Reason, event.ContentChecksum, event.StatusPatches, deploymentID, correlationID)
}

// deployToEndpoints deploys configuration to all HAProxy endpoints in parallel.
//
// This method:
//  1. Publishes DeploymentStartedEvent
//  2. Deploys to all endpoints in parallel
//  3. Logs successful endpoints and publishes InstanceDeploymentFailedEvent for failures
//  4. Publishes ConfigAppliedToPodEvent for successful deployments
//  5. Publishes DeploymentCompletedEvent with summary
func (c *Component) deployToEndpoints(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	endpoints []dataplane.Endpoint,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	reason string,
	contentChecksum string,
	statusPatches []templating.StatusPatch,
	deploymentID string,
	correlationID string,
) {
	// Clear deployment flag after this function completes (after wg.Wait())
	defer c.deploymentInProgress.Store(false)

	startTime := time.Now()

	if len(endpoints) == 0 {
		c.Logger().Error("No valid endpoints to deploy to")
		// Publish completion event so downstream components know deployment didn't happen.
		// Forward the status patches anyway so the StatusApplier can still write the
		// "deployed" variant if appropriate (the zero-endpoint guard in StatusApplier
		// will skip the apply, but the data is on the event for consistency).
		// ContentChecksum stays empty — nothing was deployed, so the scheduler
		// must not record this as a successful deploy.
		c.EventBus().Publish(events.NewDeploymentCompletedEvent(
			&events.DeploymentResult{DeploymentID: deploymentID, StatusPatches: statusPatches},
			events.WithCorrelation(correlationID, deploymentID),
		))
		return
	}

	// Use the content checksum (config + auxiliary files) as the per-pod
	// "what was applied here" checksum so HAProxyCfg.status.deployedToPods[].Checksum
	// is directly comparable to HAProxyCfg.spec.Checksum (which the publisher
	// derives from the same dataplane.ComputeContentChecksum). When every
	// pod's per-pod checksum equals spec.Checksum, the cluster has fully
	// converged on the current spec — that's the post-convergence signal
	// operators and the e2e suite poll for. Previously the deployer computed
	// a separate sha256(config) which used a different format (full hex vs
	// truncated) AND a different input set (config-only vs config+aux), so
	// the two could never match and there was no clean "everyone at current"
	// signal.
	checksum := contentChecksum

	c.Logger().Debug("Starting deployment",
		"reason", reason,
		"endpoint_count", len(endpoints),
		"config_bytes", len(config),
		"has_aux_files", auxFiles != nil,
		"correlation_id", correlationID)

	// Publish DeploymentStartedEvent with correlation
	c.EventBus().Publish(events.NewDeploymentStartedEvent(
		len(endpoints),
		events.WithCorrelation(correlationID, deploymentID),
	))

	// Deploy to all endpoints in parallel
	var wg sync.WaitGroup

	// deploymentState holds aggregated metrics protected for concurrent access
	state := &deploymentState{
		operationBreakdown: make(map[string]int),
	}

	for i := range endpoints {
		wg.Add(1)
		go func(ep *dataplane.Endpoint) {
			defer wg.Done()
			c.processEndpointDeployment(ctx, ep, config, auxFiles, parsedConfig, checksum, reason,
				runtimeConfigName, runtimeConfigNamespace, contentChecksum, correlationID, state)
		}(&endpoints[i])
	}

	// Wait for all deployments to complete
	wg.Wait()

	totalDurationMs := time.Since(startTime).Milliseconds()

	c.Logger().Debug("Deployment completed",
		"total_endpoints", len(endpoints),
		"succeeded", state.successCount,
		"failed", state.failureCount,
		"reloads_triggered", state.reloadsTriggered,
		"total_operations", state.totalOperations,
		"duration_ms", totalDurationMs,
		"correlation_id", correlationID)

	// Publish DeploymentCompletedEvent with correlation. StatusPatches and
	// ContentChecksum are forwarded unchanged from the DeploymentScheduledEvent
	// so downstream consumers (StatusApplier, DeploymentScheduler's
	// lastDeployedConfigHash cache) describe what THIS deployment carried —
	// not what the latest in-memory render happens to hold at completion
	// time (which an intervening reconcile may have changed).
	c.EventBus().Publish(events.NewDeploymentCompletedEvent(
		&events.DeploymentResult{
			DeploymentID:       deploymentID,
			Total:              len(endpoints),
			Succeeded:          int(state.successCount),
			Failed:             int(state.failureCount),
			DurationMs:         totalDurationMs,
			ReloadsTriggered:   int(state.reloadsTriggered),
			TotalAPIOperations: int(state.totalOperations),
			StatusPatches:      statusPatches,
			ContentChecksum:    contentChecksum,
			OperationBreakdown: state.operationBreakdown,
			BackendDiffFields:  state.backendDiffFields,
		},
		events.WithCorrelation(correlationID, deploymentID),
	))

	// Publish the just-deployed config as the HAProxyCfg spec so its checksum —
	// the same value stamped onto each pod's status.deployedToPods entry above —
	// is always observable as a published spec.Checksum. Without this, a render
	// whose validation-driven publish was throttled/coalesced away under churn
	// leaves pods recorded at a checksum that no consumer (operators, the e2e
	// convergence wait) can verify against spec. Gated on a real, successful,
	// non-drift deploy: drift checks are GET-only and carry an already-deployed
	// (hence already-published) checksum.
	if state.successCount > 0 && runtimeConfigName != "" && contentChecksum != "" && reason != events.TriggerReasonDriftPrevention {
		c.EventBus().Publish(events.NewDeployedConfigPublishRequest(
			runtimeConfigName, runtimeConfigNamespace, config, auxFiles, contentChecksum,
		))
	}
}

// deploymentState holds aggregated metrics protected for concurrent access.
type deploymentState struct {
	successCount       int32
	failureCount       int32
	reloadsTriggered   int32
	totalOperations    int32
	breakdownMu        sync.Mutex
	operationBreakdown map[string]int
	backendDiffFields  string // set from first endpoint's diff fields (same for all)
}

// processEndpointDeployment handles deployment to a single endpoint and updates shared state.
// This method is called from goroutines and must be thread-safe.
func (c *Component) processEndpointDeployment(
	ctx context.Context,
	ep *dataplane.Endpoint,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	checksum string,
	reason string,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	contentChecksum string,
	correlationID string,
	state *deploymentState,
) {
	// Check if context is already cancelled (e.g., timeout fired)
	if ctx.Err() != nil {
		c.Logger().Debug("Skipping endpoint deployment - context cancelled",
			"endpoint", ep.URL,
			"pod", ep.PodName,
			"error", ctx.Err(),
			"correlation_id", correlationID)
		atomic.AddInt32(&state.failureCount, 1)
		return
	}

	instanceStart := time.Now()
	syncResult, err := c.deployToSingleEndpoint(ctx, config, auxFiles, parsedConfig, contentChecksum, reason, ep)
	durationMs := time.Since(instanceStart).Milliseconds()

	// Determine if this is a drift check based on deployment reason
	isDriftCheck := reason == "drift_prevention"

	if err != nil {
		c.handleEndpointFailure(ep, err, durationMs, checksum, isDriftCheck,
			runtimeConfigName, runtimeConfigNamespace, correlationID, state)
	} else {
		c.handleEndpointSuccess(ep, syncResult, durationMs, checksum, isDriftCheck,
			runtimeConfigName, runtimeConfigNamespace, correlationID, state)
	}
}

// handleEndpointFailure processes a failed endpoint deployment.
func (c *Component) handleEndpointFailure(
	ep *dataplane.Endpoint,
	err error,
	durationMs int64,
	checksum string,
	isDriftCheck bool,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	correlationID string,
	state *deploymentState,
) {
	c.Logger().Error("Deployment failed for endpoint",
		"endpoint", ep.URL,
		"pod", ep.PodName,
		"error", err,
		"duration_ms", durationMs,
		"correlation_id", correlationID)

	// Publish InstanceDeploymentFailedEvent with correlation
	c.EventBus().Publish(events.NewInstanceDeploymentFailedEvent(
		ep,
		err.Error(),
		true, // retryable
		events.WithCorrelation(correlationID, correlationID),
	))

	// A confirmed post-reload read-back divergence (issue #84) gets its own
	// counter (haptic_deploy_runtime_divergence_total): unlike ordinary
	// transient sync failures, it means a concurrent writer clobbered the
	// on-disk config a reload had just activated — a defect signal to alert
	// on, even though the fast retry self-heals the pod.
	if dataplane.IsPostReloadDivergence(err) && c.metrics != nil {
		c.metrics.RecordDeployRuntimeDivergence()
	}

	// Publish ConfigAppliedToPodEvent with error info (for status tracking)
	if runtimeConfigName != "" && runtimeConfigNamespace != "" {
		syncMetadata := &events.SyncMetadata{
			Error: err.Error(),
		}
		c.EventBus().Publish(events.NewConfigAppliedToPodEvent(
			runtimeConfigName,
			runtimeConfigNamespace,
			ep.PodName,
			ep.PodNamespace,
			checksum,
			isDriftCheck,
			syncMetadata,
		))
	}

	atomic.AddInt32(&state.failureCount, 1)
}

// handleEndpointSuccess processes a successful endpoint deployment.
func (c *Component) handleEndpointSuccess(
	ep *dataplane.Endpoint,
	syncResult *dataplane.SyncResult,
	durationMs int64,
	checksum string,
	isDriftCheck bool,
	runtimeConfigName string,
	runtimeConfigNamespace string,
	correlationID string,
	state *deploymentState,
) {
	c.Logger().Debug("Deployment succeeded for endpoint",
		"endpoint", ep.URL,
		"pod", ep.PodName,
		"duration_ms", durationMs,
		"reload_triggered", syncResult.ReloadTriggered,
		"correlation_id", correlationID)

	// A runtime map that failed its read-back cost this sync the reload-free
	// lane. The sync still SUCCEEDED (the reload fallback is convergent), so
	// this rides the success path — without it the degradation is a WARN line
	// nothing alerts on.
	if c.metrics != nil {
		for _, mapName := range syncResult.DivergedRuntimeMaps {
			c.metrics.RecordRuntimeMapDivergence(mapName)
		}
	}

	// Publish ConfigAppliedToPodEvent unconditionally, regardless of
	// whether the sync did any HAProxy operations.
	//
	// The earlier "skip no-op deployments to reduce API load" optimisation
	// broke the spec.checksum ↔ status.deployedToPods[].checksum invariant.
	// Scenario from CI pipeline 2559825226 / TestIngressBackendSSL etc.:
	//
	//   1. Deploy with content X — sync applies operations, ConfigApplied
	//      event fires with checksum=X, pod's deployedToPods entry → X.
	//   2. Render produces content Y (same HAProxy operations but different
	//      checksum: e.g. an aux file's byte order changed, or a label-only
	//      delta on a watched resource shifted the content hash without
	//      changing the rendered HAProxy directives).
	//   3. Deploy with content Y — sync reports 0 ops, no reload. The old
	//      skip-path treated this as a no-op and DIDN'T publish the
	//      ConfigAppliedToPodEvent.
	//   4. config-publisher writes spec.checksum=Y (it dedups against
	//      content, not ops). status.deployedToPods[].checksum stays at X.
	//   5. waitForControllerDeployed polls (Y vs X) → 90 s timeout.
	//
	// The Kubernetes API saving from skipping was illusory: server-side
	// apply with a no-change payload doesn't bump resourceVersion when the
	// managedFields entry is byte-identical, so the cost is bounded to
	// the request round-trip. The correctness cost of skipping is
	// unbounded — operators see permanently mismatched status until the
	// next content change happens to also be a non-no-op sync. Always
	// publish.
	if runtimeConfigName != "" && runtimeConfigNamespace != "" {
		syncMetadata := syncResultToMetadata(syncResult)
		c.EventBus().Publish(events.NewConfigAppliedToPodEvent(
			runtimeConfigName,
			runtimeConfigNamespace,
			ep.PodName,
			ep.PodNamespace,
			checksum,
			isDriftCheck,
			syncMetadata,
		))
	}

	atomic.AddInt32(&state.successCount, 1)

	// Track reloads and operations for aggregate metrics
	if syncResult.ReloadTriggered {
		atomic.AddInt32(&state.reloadsTriggered, 1)
	}

	// Details is always populated per dataplane.SyncResult contract
	atomic.AddInt32(&state.totalOperations, safeIntToInt32(syncResult.Details.TotalOperations))

	// Accumulate operation breakdown from AppliedOperations
	// All operations (config + aux files) are now in AppliedOperations
	state.breakdownMu.Lock()
	for _, op := range syncResult.AppliedOperations {
		key := op.Section + "_" + op.Type
		state.operationBreakdown[key]++
	}
	// Capture backend diff fields from first endpoint (same for all since config is identical)
	if state.backendDiffFields == "" {
		state.backendDiffFields = formatBackendDiffFields(syncResult.Details.BackendDiffFields)
	}
	state.breakdownMu.Unlock()
}

// deployToSingleEndpoint deploys configuration to a single HAProxy endpoint.
//
// Returns the sync result containing detailed operation metadata, or an error if the sync failed.
func (c *Component) deployToSingleEndpoint(
	ctx context.Context,
	config string,
	auxFiles *dataplane.AuxiliaryFiles,
	parsedConfig *parser.StructuredConfig,
	contentChecksum string,
	reason string,
	endpoint *dataplane.Endpoint,
) (*dataplane.SyncResult, error) {
	// Create client for this endpoint
	client, err := dataplane.NewClient(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	defer client.Close()

	// Use default sync options and apply configuration limits
	opts := dataplane.DefaultSyncOptions()
	if c.reloadVerificationTimeout > 0 {
		opts.ReloadVerificationTimeout = c.reloadVerificationTimeout
	}
	if c.syncTimeout > 0 {
		opts.Timeout = c.syncTimeout
	}

	// Pass pre-parsed config to skip redundant parsing during sync
	if parsedConfig != nil {
		opts.PreParsedConfig = parsedConfig
	}

	// Populate cached current config from version cache (if available)
	cachedVersion, cachedConfig, cachedChecksum := c.versionCache.get(endpoint.URL)
	if cachedConfig != nil {
		opts.CachedCurrentConfig = cachedConfig
		opts.CachedConfigVersion = cachedVersion
	}

	// Pass content checksum for aux file comparison cache.
	// Drift prevention deployments must always force comparison (bypass cache)
	// to detect out-of-band changes on the HAProxy pod.
	opts.ContentChecksum = contentChecksum
	if reason != events.TriggerReasonDriftPrevention && cachedChecksum != "" {
		opts.LastDeployedChecksum = cachedChecksum
	}

	// What this endpoint was last PROVEN to be running. Unlike the caches above
	// this is passed on EVERY sync including drift prevention: it does not skip
	// work, it decides whether an empty diff may be trusted at all, and drift
	// prevention is exactly when a stale answer is most costly.
	opts.LastActivatedConfigChecksum = c.versionCache.activated(endpoint.URL)

	// Sync configuration
	result, err := client.Sync(ctx, config, auxFiles, opts)
	if err != nil {
		// Invalidate cache on failure - pod state is uncertain. invalidate()
		// drops the activation proof with the rest of the entry, which is the
		// right call: a push that errored may still have written its body to
		// disk (a skip_version push does so even when its runtime actions
		// fail), so nothing about this pod's running state is provable now.
		c.versionCache.invalidate(endpoint.URL)
		return nil, fmt.Errorf("sync failed: %w", err)
	}

	// Record what this sync proved, or clear the proof when it proved nothing.
	// Both directions matter: without the record every sync would force a
	// reload; without the clear a parked config would keep short-circuiting.
	c.versionCache.setActivated(endpoint.URL, result.ActivatedConfigChecksum)

	// Update version cache with post-sync state (including content checksum).
	// Prefer result.PostSyncParsedConfig (the pod's ACTUAL post-sync state,
	// fetched and parsed by the orchestrator) over parsedConfig (the caller's
	// desired intent). The two diverge when the dataplane API applies
	// incremental patches against pods with different starting baselines — e.g.
	// a rolling HAProxy Deployment where one pod is synced twice and another
	// once. Both end up "logically desired" but byte-different on disk; caching
	// the input desired would hide that drift from every subsequent reconcile
	// and the divergent pod would never be re-synced. The orchestrator only
	// populates PostSyncParsedConfig when ops were applied AND the post-sync
	// fetch+parse succeeded; otherwise desired is equivalent to the live state
	// (the no-changes path already verified pod==desired).
	cachedParsed := result.PostSyncParsedConfig
	if cachedParsed == nil {
		cachedParsed = parsedConfig
	}
	if result.PostSyncVersion > 0 && cachedParsed != nil {
		c.versionCache.set(endpoint.URL, result.PostSyncVersion, cachedParsed, contentChecksum)
	}

	c.Logger().Debug("Sync completed for endpoint",
		"endpoint", endpoint.URL,
		"pod", endpoint.PodName,
		"applied_operations", len(result.AppliedOperations),
		"reload_triggered", result.ReloadTriggered,
		"used_preparsed_config", parsedConfig != nil,
		"cache_hit", cachedConfig != nil,
		"post_sync_version", result.PostSyncVersion,
		"cached_actual_post_sync", result.PostSyncParsedConfig != nil,
		"duration", result.Duration)

	return result, nil
}

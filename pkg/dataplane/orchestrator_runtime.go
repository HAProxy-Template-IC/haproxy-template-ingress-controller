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
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"
)

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
		case stateEnabled:
			actions = append(actions, fmt.Sprintf("SetServerState %s %s maint", b, n))
		case stateDisabled:
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
		case stateEnabled:
			actions = append(actions, fmt.Sprintf("EnableAgentCheck %s %s", b, n))
		case stateDisabled:
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

// Copyright 2026 Philipp Hossner
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
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// handleEndpointFailure reports a pod the controller could not reach or whose
// apply errored before the agent judged it.
func (c *Component) handleEndpointFailure(
	endpoint *dataplane.Endpoint,
	err error,
	durationMs int64,
	event *events.DeploymentScheduledEvent,
	state *deploymentState,
) {
	c.Logger().Error("Deployment failed for endpoint",
		"pod", endpoint.PodName,
		"endpoint", endpoint.URL,
		"error", err,
		"duration_ms", durationMs,
		"correlation_id", event.CorrelationID())

	c.EventBus().Publish(events.NewInstanceDeploymentFailedEvent(
		endpoint, err.Error(), true,
		events.WithCorrelation(event.CorrelationID(), event.CorrelationID()),
	))
	c.publishPodStatus(endpoint, event, &events.SyncMetadata{Error: err.Error()})
	atomic.AddInt32(&state.failureCount, 1)
}

// handleEndpointRejection reports a NACK: the agent judged the apply and
// refused it. The pod's baseline is dropped so its next apply carries the
// complete file set and a reload rather than ops composed against a state the
// refusal may have left behind.
func (c *Component) handleEndpointRejection(
	endpoint *dataplane.Endpoint,
	outcome *podOutcome,
	event *events.DeploymentScheduledEvent,
	state *deploymentState,
) {
	message := "the agent rejected the apply"
	if outcome.result.Error != nil {
		message = outcome.result.Error.Stage + ": " + outcome.result.Error.Message
	}
	c.Logger().Error("Agent rejected the apply",
		"pod", endpoint.PodName,
		"plan", outcome.result.PlanID,
		"error", message,
		"correlation_id", event.CorrelationID())

	c.invalidateBaseline(endpoint)
	if c.metrics != nil {
		c.metrics.RecordApplyRejected(endpoint.PodName)
	}
	c.EventBus().Publish(events.NewInstanceDeploymentFailedEvent(
		endpoint, message, true,
		events.WithCorrelation(event.CorrelationID(), event.CorrelationID()),
	))

	metadata := applyResultToMetadata(outcome)
	metadata.Error = message
	c.publishPodStatus(endpoint, event, metadata)
	atomic.AddInt32(&state.failureCount, 1)
}

// handleEndpointSuccess records an ACK: what the pod applied, what it runs, and
// whether it has reached the render.
func (c *Component) handleEndpointSuccess(
	endpoint *dataplane.Endpoint,
	outcome *podOutcome,
	durationMs int64,
	event *events.DeploymentScheduledEvent,
	state *deploymentState,
) {
	result := outcome.result
	c.Logger().Debug("Deployment succeeded for endpoint",
		"pod", endpoint.PodName,
		"mode", result.Mode,
		"applied_plan", result.AppliedPlanID,
		"running_plan", result.RunningPlanID,
		"duration_ms", durationMs,
		"correlation_id", event.CorrelationID())

	c.clearBaselineInvalidation(endpoint)
	c.recordAppliedOps(endpoint, result.Mode, outcome.sent)
	c.recordRuntimeFallback(result.OpResults)
	state.noteRunning(endpoint, result.RunningPlanID)

	c.publishPodStatus(endpoint, event, applyResultToMetadata(outcome))

	atomic.AddInt32(&state.ackCount, 1)
	if outcome.converged {
		atomic.AddInt32(&state.convergedCount, 1)
	}
	if result.Mode == api.ResultScheduled {
		scheduledAt := ""
		if result.Reload != nil {
			scheduledAt = result.Reload.ScheduledAt
		}
		state.notePendingReload(scheduledAt)
	}
	if result.Reload != nil && result.Reload.Performed {
		atomic.AddInt32(&state.reloadsTriggered, 1)
	}
	c.notePodPlans(endpoint, result.AppliedPlanID, result.RunningPlanID, result.WorkerOpsPlanID)

	state.mu.Lock()
	defer state.mu.Unlock()
	state.totalOperations += len(outcome.sent)
	for i := range outcome.sent {
		state.operationBreakdown[outcome.sent[i].Kind]++
	}
}

// recordAppliedOps counts what one pod accepted: the apply itself by mode, and
// every lifecycle op by kind.
func (c *Component) recordAppliedOps(endpoint *dataplane.Endpoint, mode string, ops []api.Op) {
	if c.metrics == nil {
		return
	}
	c.metrics.RecordAgentApply(endpoint.PodName, mode)
	for i := range ops {
		switch ops[i].Kind {
		case api.OpBackendAdd, api.OpBackendPublish, api.OpBackendUnpublish,
			api.OpBackendDel, api.OpBackendWaitRemovable:
			c.metrics.RecordRuntimeBackendOp(ops[i].Kind)
		case api.OpServerAdd, api.OpServerEnable, api.OpServerDisable,
			api.OpServerSetAddr, api.OpServerSetWeight, api.OpServerSetState,
			api.OpServerWaitRemovable, api.OpShutdownSessions, api.OpServerDel:
			c.metrics.RecordRuntimeServerOp(ops[i].Kind)
		}
	}
}

// nameCollisionOutput is HAProxy's own words when `add backend` hits a name
// that is already registered — the A5 leftover a deferred delete has not yet
// retired. It is matched, not the op kind, because a backend_add can be refused
// for other reasons too and only this one is the reload-free lane losing to a
// name it will get back.
const nameCollisionOutput = "already used by other proxy"

// recordRuntimeFallback counts a runtime batch a pod could not run: a failed
// op result means HAProxy refused a command and the agent reloaded the desired
// set instead. The reason is read from HAProxy's own message — a name collision
// (A5) versus any other refusal — so a backend_add refused for a different
// reason is not mislabelled. A successful runtime apply carries no failed
// result, so nothing is counted on the reload-free path. The agent stops at the
// first rejected op and reloads the desired set, so a batch carries at most one
// failed result — the early return counts each fallback once.
func (c *Component) recordRuntimeFallback(results []api.OpResult) {
	if c.metrics == nil {
		return
	}
	for i := range results {
		if results[i].OK {
			continue
		}
		reason := "op_rejected"
		if strings.Contains(strings.ToLower(results[i].Output), nameCollisionOutput) {
			reason = "name_collision"
		}
		c.metrics.RecordRuntimeBackendFallback(reason)
		return
	}
}

// publishPodStatus carries one pod's outcome into HAProxyCfg.status.
func (c *Component) publishPodStatus(
	endpoint *dataplane.Endpoint,
	event *events.DeploymentScheduledEvent,
	metadata *events.SyncMetadata,
) {
	if event.RuntimeConfigName == "" || event.RuntimeConfigNamespace == "" {
		return
	}
	c.EventBus().Publish(events.NewConfigAppliedToPodEvent(
		event.RuntimeConfigName,
		event.RuntimeConfigNamespace,
		endpoint.PodName,
		endpoint.PodNamespace,
		endpoint.PodUID,
		endpoint.PodRuntimeID,
		event.ContentChecksum,
		event.Reason == events.TriggerReasonDriftPrevention,
		metadata,
	))
}

// publishCompleted reports the fleet's answer. Succeeded is the number of pods
// that now RUN the render, not the number whose apply was accepted: a pod
// whose reload is still pending accepted the files but does not serve them.
func (c *Component) publishCompleted(
	event *events.DeploymentScheduledEvent,
	deploymentID string,
	podSetHash string,
	state *deploymentState,
	durationMs int64,
) {
	state.mu.Lock()
	breakdown := make(map[string]int, len(state.operationBreakdown))
	for kind, count := range state.operationBreakdown {
		breakdown[kind] = count
	}
	operations := state.totalOperations
	pendingUntil := state.pendingReloadUntil
	state.mu.Unlock()

	c.EventBus().Publish(events.NewDeploymentCompletedEvent(
		&events.DeploymentResult{
			DeploymentID:       deploymentID,
			Total:              len(event.Endpoints),
			Succeeded:          int(atomic.LoadInt32(&state.convergedCount)),
			Failed:             int(atomic.LoadInt32(&state.failureCount)),
			PendingReloads:     int(atomic.LoadInt32(&state.pendingReloads)),
			PendingReloadUntil: pendingUntil,
			DurationMs:         durationMs,
			ReloadsTriggered:   int(atomic.LoadInt32(&state.reloadsTriggered)),
			TotalAPIOperations: operations,
			StatusPatches:      event.StatusPatches,
			ContentChecksum:    event.ContentChecksum,
			PodSetHash:         podSetHash,
			OperationBreakdown: breakdown,
		},
		events.WithCorrelation(event.CorrelationID(), deploymentID),
	))
}

// publishDeployedConfig republishes the just-deployed config as the HAProxyCfg
// spec, so the checksum stamped on every pod is always observable as a
// published spec.Checksum. The drift pass carries an already-published
// checksum and is skipped.
func (c *Component) publishDeployedConfig(event *events.DeploymentScheduledEvent, acked int) {
	if acked == 0 || event.RuntimeConfigName == "" || event.ContentChecksum == "" ||
		event.Reason == events.TriggerReasonDriftPrevention {
		return
	}
	c.EventBus().Publish(events.NewDeployedConfigPublishRequest(
		event.RuntimeConfigName, event.RuntimeConfigNamespace,
		event.Config, event.AuxiliaryFiles, event.ContentChecksum,
	))
}

// applyResultToMetadata projects one pod's ACK onto the status the publisher
// writes: which plan it holds, which one it runs, how it got there and why.
func applyResultToMetadata(outcome *podOutcome) *events.SyncMetadata {
	result := outcome.result
	metadata := &events.SyncMetadata{
		ReloadTriggered: result.Reload != nil && result.Reload.Performed,
		AppliedPlanID:   result.AppliedPlanID,
		RunningPlanID:   result.RunningPlanID,
		Mode:            result.Mode,
		Reasons:         cappedReasons(outcome.reasons()),
		OperationCounts: operationCounts(outcome.sent),
	}
	if result.Reload != nil && result.Reload.TookMs > 0 {
		metadata.SyncDuration = time.Duration(result.Reload.TookMs) * time.Millisecond
	}
	return metadata
}

// cappedReasons truncates in Go as well as in the CRD: MaxItems rejects a
// longer list rather than trimming it, and a rejected status patch is a silent
// status stall. The last kept entry says how many were dropped, so the status
// never reads as complete when it is not.
func cappedReasons(reasons []string) []string {
	if len(reasons) <= maxStatusReasons {
		return reasons
	}
	kept := maxStatusReasons - 1
	capped := make([]string, 0, maxStatusReasons)
	capped = append(capped, reasons[:kept]...)
	return append(capped, fmt.Sprintf("… %d more reasons omitted", len(reasons)-kept))
}

// operationCounts groups the ops that went out by what they changed, which is
// what the status and the commentator report.
func operationCounts(ops []api.Op) events.OperationCounts {
	counts := events.OperationCounts{TotalAPIOperations: len(ops)}
	for i := range ops {
		switch ops[i].Kind {
		case api.OpBackendAdd:
			counts.BackendsAdded++
		case api.OpBackendDel:
			counts.BackendsRemoved++
		case api.OpBackendPublish, api.OpBackendUnpublish:
			counts.BackendsModified++
		case api.OpServerAdd:
			counts.ServersAdded++
		case api.OpServerDel:
			counts.ServersRemoved++
		case api.OpServerSetAddr, api.OpServerSetWeight, api.OpServerSetState,
			api.OpServerEnable, api.OpServerDisable:
			counts.ServersModified++
		case api.OpMapAdd, api.OpMapSet, api.OpMapReplace:
			counts.MapsModified++
		case api.OpMapDel:
			counts.MapsRemoved++
		case api.OpCertNew, api.OpCANew, api.OpCRTListAdd:
			counts.SSLCertsAdded++
		case api.OpCertSet, api.OpCASet:
			counts.SSLCertsModified++
		case api.OpCRTListDel:
			counts.SSLCertsRemoved++
		}
	}
	return counts
}

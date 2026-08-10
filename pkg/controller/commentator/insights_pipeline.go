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

package commentator

import (
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// resourceInsight handles ResourceIndexUpdated, ResourceSyncComplete, and IndexSynchronized events.
func (ec *EventCommentator) resourceInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ResourceIndexUpdatedEvent:
		// Don't log during initial sync to reduce noise
		if e.ChangeStats.IsInitialSync {
			return fmt.Sprintf("Resource index loading: %s (created=%d, modified=%d, deleted=%d)",
					e.ResourceTypeName, e.ChangeStats.Created, e.ChangeStats.Modified, e.ChangeStats.Deleted),
				append(attrs,
					"resource_type", e.ResourceTypeName,
					"created", e.ChangeStats.Created,
					"modified", e.ChangeStats.Modified,
					"deleted", e.ChangeStats.Deleted,
					"initial_sync", true)
		}
		return fmt.Sprintf("Resource index updated: %s (created=%d, modified=%d, deleted=%d)",
				e.ResourceTypeName, e.ChangeStats.Created, e.ChangeStats.Modified, e.ChangeStats.Deleted),
			append(attrs,
				"resource_type", e.ResourceTypeName,
				"created", e.ChangeStats.Created,
				"modified", e.ChangeStats.Modified,
				"deleted", e.ChangeStats.Deleted,
				"initial_sync", false)

	case *events.ResourceSyncCompleteEvent:
		return fmt.Sprintf("Initial sync complete for %s (%d resources)",
				e.ResourceTypeName, e.InitialCount),
			append(attrs, "resource_type", e.ResourceTypeName, "initial_count", e.InitialCount)

	case *events.IndexSynchronizedEvent:
		totalResources := 0
		for _, count := range e.ResourceCounts {
			totalResources += count
		}
		return fmt.Sprintf("All resource indexes synchronized (%d resources across %d types)",
				totalResources, len(e.ResourceCounts)),
			append(attrs, "resource_types", len(e.ResourceCounts), "total_resources", totalResources)

	default:
		return "", attrs
	}
}

// reconciliationInsight handles ReconciliationTriggered, ReconciliationStarted,
// ReconciliationCompleted, and ReconciliationFailed events.
func (ec *EventCommentator) reconciliationInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ReconciliationTriggeredEvent:
		// Correlate: when was the last reconciliation?
		recentReconciliations := ec.ringBuffer.FindByTypeInWindow(events.EventTypeReconciliationCompleted, reconciliationLookbackWindow)
		var correlationMsg string
		if len(recentReconciliations) > 0 {
			timeSince := event.Timestamp().Sub(recentReconciliations[0].Timestamp())
			correlationMsg = fmt.Sprintf(" (previous reconciliation was %v ago)", timeSince.Round(time.Second))
		}
		return fmt.Sprintf("Reconciliation triggered: %s%s", e.Reason, correlationMsg),
			append(attrs, "reason", e.Reason)

	case *events.ReconciliationStartedEvent:
		return fmt.Sprintf("Reconciliation started: %s", e.Trigger),
			append(attrs, "trigger", e.Trigger)

	case *events.ReconciliationCompletedEvent:
		// Correlate: find the ReconciliationStartedEvent
		startEvents := ec.ringBuffer.FindByTypeInWindow(events.EventTypeReconciliationStarted, startEventLookbackWindow)
		var phaseInfo string
		if len(startEvents) > 0 {
			totalDuration := event.Timestamp().Sub(startEvents[0].Timestamp())
			phaseInfo = fmt.Sprintf(" (total cycle: %v, reconciliation: %dms)",
				totalDuration.Round(time.Millisecond), e.DurationMs)
		} else {
			phaseInfo = fmt.Sprintf(" (%dms)", e.DurationMs)
		}
		return fmt.Sprintf("Reconciliation completed successfully%s", phaseInfo),
			append(attrs, "duration_ms", e.DurationMs)

	case *events.ReconciliationFailedEvent:
		return fmt.Sprintf("Reconciliation failed in %s phase: %s", e.Phase, e.Error),
			append(attrs, "phase", e.Phase, "error", e.Error)

	default:
		return "", attrs
	}
}

// templateInsight handles TemplateRendered and TemplateRenderFailed events.
func (ec *EventCommentator) templateInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		sizeKB := float64(e.ConfigBytes) / 1024.0
		triggerInfo := ""
		if e.TriggerReason != "" {
			triggerInfo = fmt.Sprintf(" (trigger: %s)", e.TriggerReason)
		}
		return fmt.Sprintf("Template rendered: %.1f KB config + %d auxiliary files in %dms%s",
				sizeKB, e.AuxiliaryFileCount, e.DurationMs, triggerInfo),
			append(attrs, "config_bytes", e.ConfigBytes, "aux_files", e.AuxiliaryFileCount, "duration_ms", e.DurationMs, "trigger_reason", e.TriggerReason)

	case *events.TemplateRenderFailedEvent:
		// Error string is produced by RenderService.Render and propagated by
		// the Coordinator (no event-adapter renderer — see ADR-0001), so it's
		// already formatted by the time we see it; just pass it through.
		return fmt.Sprintf("Template rendering failed:\n%s", e.Error),
			append(attrs, "template", e.TemplateName)

	default:
		return "", attrs
	}
}

// deploymentInsight handles DeploymentStarted, InstanceDeploymentFailed, and DeploymentCompleted events.
func (ec *EventCommentator) deploymentInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.DeploymentStartedEvent:
		return fmt.Sprintf("Deployment started to %d HAProxy instances", e.EndpointCount),
			append(attrs, "instance_count", e.EndpointCount)

	case *events.InstanceDeploymentFailedEvent:
		retryableInfo := ""
		if e.Retryable {
			retryableInfo = " (retryable)"
		}
		return fmt.Sprintf("Instance deployment failed%s: %s", retryableInfo, e.Error),
			append(attrs, "error", e.Error, "retryable", e.Retryable)

	case *events.DeploymentCompletedEvent:
		// Compute consolidated reconciliation summary using dedicated method
		summary := ec.computeReconciliationSummary(e)

		attrs = append(attrs,
			"trigger", summary.Trigger,
			"instances", summary.Instances,
			"reloads", summary.Reloads,
			"ops", summary.Operations,
			"render_ms", summary.RenderMs,
			"validate_ms", summary.ValidateMs,
			"deploy_ms", summary.DeployMs,
			"total_ms", summary.TotalMs,
			// Queue wait metrics - time events spent waiting in channels before processing
			"queue_trigger_to_render_ms", summary.TriggerToRenderQueueMs,
			"queue_render_to_validate_ms", summary.RenderToValidateQueueMs,
			"queue_validate_to_deploy_ms", summary.ValidateToDeployQueueMs,
			"queue_total_ms", summary.TotalQueueMs)

		// Add non-zero operation breakdown entries
		// Keys are formatted as "section_type" (e.g., "backend_create", "server_update")
		for key, count := range e.OperationBreakdown {
			if count > 0 {
				attrs = append(attrs, key, count)
			}
		}

		// Add backend diff field diagnostics when backend updates are caused by attribute diffs
		if e.BackendDiffFields != "" {
			attrs = append(attrs, "backend_diff_fields", e.BackendDiffFields)
		}

		return "Reconciliation", attrs

	default:
		return "", attrs
	}
}

// podInsight handles HAProxyPodsDiscovered and HAProxyPodTerminated events.
func (ec *EventCommentator) podInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.HAProxyPodsDiscoveredEvent:
		// Correlate: was this a change?
		recentDiscoveries := ec.ringBuffer.FindByTypeInWindow(events.EventTypeHAProxyPodsDiscovered, discoveryLookbackWindow)
		var changeInfo string
		if len(recentDiscoveries) > 1 {
			// Compare with previous discovery
			changeInfo = " (pods changed)"
		}
		return fmt.Sprintf("HAProxy pods discovered: %d instances%s", e.Count, changeInfo),
			append(attrs, "count", e.Count)

	case *events.HAProxyPodTerminatedEvent:
		return fmt.Sprintf("HAProxy pod terminated: %s/%s", e.PodNamespace, e.PodName),
			append(attrs, "pod_name", e.PodName, "pod_namespace", e.PodNamespace)

	case *events.HAProxyPodRejectedEvent:
		// Surfaces controller-vs-HAProxy version mismatches (and similar admission
		// failures) at WARN so operators see them in logs, not just in the
		// haptic_haproxy_pods_rejected_total counter.
		return fmt.Sprintf("HAProxy pod rejected: %s (reason: %s)", e.PodName, e.Reason),
			append(attrs, "pod_name", e.PodName, "reason", e.Reason)

	default:
		return "", attrs
	}
}

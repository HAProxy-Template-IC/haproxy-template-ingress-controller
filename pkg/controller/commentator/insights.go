package commentator

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Lookback window durations for event correlation in the ring buffer.
const (
	validationLookbackWindow     = 30 * time.Second
	reconciliationLookbackWindow = 5 * time.Minute
	startEventLookbackWindow     = 1 * time.Minute
	discoveryLookbackWindow      = 30 * time.Second
)

// generateInsight creates a contextual message and structured attributes for the event.
//
// This applies domain knowledge and uses the ring buffer for event correlation.
func (ec *EventCommentator) generateInsight(event busevents.Event) (insight string, args []any) {
	eventType := event.EventType()
	attrs := []any{
		"event_type", eventType,
		"timestamp", event.Timestamp(),
	}

	switch event.(type) {
	// Configuration Events
	case *events.ConfigParsedEvent, *events.ConfigValidationRequest, *events.ConfigValidationResponse,
		*events.ConfigValidatedEvent, *events.ConfigInvalidEvent,
		*events.CertResourceChangedEvent, *events.CertParsedEvent:
		return ec.configInsight(event, attrs)

	// Resource Events
	case *events.ResourceIndexUpdatedEvent, *events.ResourceSyncCompleteEvent,
		*events.IndexSynchronizedEvent:
		return ec.resourceInsight(event, attrs)

	// Reconciliation Events
	case *events.ReconciliationTriggeredEvent, *events.ReconciliationStartedEvent,
		*events.ReconciliationCompletedEvent, *events.ReconciliationFailedEvent:
		return ec.reconciliationInsight(event, attrs)

	// Template Events
	case *events.TemplateRenderedEvent, *events.TemplateRenderFailedEvent:
		return ec.templateInsight(event, attrs)

	// Validation Events
	case *events.ValidationStartedEvent, *events.ValidationCompletedEvent,
		*events.ValidationFailedEvent,
		*events.ValidationTestsStartedEvent, *events.ValidationTestsCompletedEvent,
		*events.ValidationTestsFailedEvent:
		return ec.validationInsight(event, attrs)

	// Deployment Events
	case *events.DeploymentStartedEvent, *events.InstanceDeployedEvent,
		*events.InstanceDeploymentFailedEvent, *events.DeploymentCompletedEvent:
		return ec.deploymentInsight(event, attrs)

	// HAProxy Pod Events
	case *events.HAProxyPodsDiscoveredEvent, *events.HAProxyPodTerminatedEvent:
		return ec.podInsight(event, attrs)

	// Webhook Validation Events
	case *events.WebhookValidationRequestEvent, *events.WebhookValidationAllowedEvent,
		*events.WebhookValidationDeniedEvent, *events.WebhookValidationErrorEvent:
		return ec.webhookInsight(event, attrs)

	// Leader Election Events
	case *events.LeaderElectionStartedEvent, *events.BecameLeaderEvent,
		*events.LostLeadershipEvent, *events.NewLeaderObservedEvent:
		return ec.leaderInsight(event, attrs)

	// Status Update Events
	case *events.StatusUpdateCompletedEvent, *events.StatusUpdateFailedEvent:
		return ec.statusInsight(event, attrs)

	default:
		// Fallback for unknown event types
		return fmt.Sprintf("Event: %s", eventType), attrs
	}
}

// configInsight handles ConfigParsed, ConfigValidationRequest, ConfigValidationResponse,
// ConfigValidated, ConfigInvalid, CertResourceChanged, and CertParsed events.
func (ec *EventCommentator) configInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ConfigParsedEvent:
		return fmt.Sprintf("Configuration parsed successfully (version %s)", e.Version),
			append(attrs, "version", e.Version, "secret_version", e.SecretVersion)

	case *events.ConfigValidationRequest:
		// Get validator count from validator package constants
		validatorCount := len(validator.AllValidatorNames())
		return fmt.Sprintf("Configuration validation started (version %s, expecting %d validators)",
				e.Version, validatorCount),
			append(attrs, "version", e.Version, "validator_count", validatorCount)

	case *events.ConfigValidationResponse:
		// Show real-time validator results with performance metrics
		statusSymbol := "✓"
		statusText := "OK"
		if !e.Valid {
			statusSymbol = "✗"
			statusText = "FAILED"
		}

		// Build metrics message based on validator type
		var metricsMsg string
		if e.Valid {
			// For successful validation, show positive metrics
			switch e.ValidatorName {
			case "basic":
				metricsMsg = ""
			case "template":
				// Template validator logs template_count
				metricsMsg = ""
			case "jsonpath":
				// JSONPath validator logs expression_count
				metricsMsg = ""
			}
		} else {
			// For failures, show error count
			metricsMsg = fmt.Sprintf(", %d errors", len(e.Errors))
		}

		return fmt.Sprintf("Validator '%s': %s %s%s",
				e.ValidatorName, statusSymbol, statusText, metricsMsg),
			append(attrs, "validator", e.ValidatorName, "valid", e.Valid, "error_count", len(e.Errors))

	case *events.ConfigValidatedEvent:
		// Correlate: how long did validation take?
		validationRequests := ec.ringBuffer.FindByTypeInWindow(events.EventTypeConfigValidationRequest, validationLookbackWindow)
		var correlationMsg string
		if len(validationRequests) > 0 {
			duration := event.Timestamp().Sub(validationRequests[0].Timestamp())
			correlationMsg = fmt.Sprintf(" (validation completed in %v)", duration.Round(time.Millisecond))
		}
		return fmt.Sprintf("Configuration validated successfully%s", correlationMsg),
			append(attrs, "version", e.Version, "secret_version", e.SecretVersion)

	case *events.ConfigInvalidEvent:
		// Build detailed breakdown per validator for the summary message
		errorCount := 0
		var validatorBreakdown []string
		for validatorName, errs := range e.ValidationErrors {
			errorCount += len(errs)
			if len(errs) > 0 {
				// Show first error as example (truncated for message readability)
				firstError := errs[0]
				if len(firstError) > maxErrorPreviewLength {
					firstError = firstError[:maxErrorPreviewLength-3] + "..."
				}
				validatorBreakdown = append(validatorBreakdown,
					fmt.Sprintf("%s: %d errors (e.g., %q)", validatorName, len(errs), firstError))
			}
		}

		detailMsg := ""
		if len(validatorBreakdown) > 0 {
			detailMsg = fmt.Sprintf(": %s", strings.Join(validatorBreakdown, "; "))
		}

		// Include full untruncated validation errors as structured attribute for debugging
		return fmt.Sprintf("Configuration validation failed with %d errors across %d validators%s",
				errorCount, len(e.ValidationErrors), detailMsg),
			append(attrs, "version", e.Version, "validator_count", len(e.ValidationErrors), "error_count", errorCount, "validation_errors", e.ValidationErrors)

	// Webhook Certificate Events
	case *events.CertResourceChangedEvent:
		return "Webhook certificate Secret changed",
			attrs

	case *events.CertParsedEvent:
		return fmt.Sprintf("Webhook certificates parsed successfully (version %s)", e.Version),
			append(attrs, "version", e.Version, "cert_size", len(e.CertPEM), "key_size", len(e.KeyPEM))

	default:
		return "", attrs
	}
}

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
		// Error is already formatted by renderer component, just pass it through
		return fmt.Sprintf("Template rendering failed:\n%s", e.Error),
			append(attrs, "template", e.TemplateName)

	default:
		return "", attrs
	}
}

// validationInsight handles ValidationStarted, ValidationCompleted, ValidationFailed,
// ValidationTestsStarted, ValidationTestsCompleted, and ValidationTestsFailed events.
func (ec *EventCommentator) validationInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.ValidationStartedEvent:
		return "HAProxy configuration validation started",
			attrs

	case *events.ValidationCompletedEvent:
		warningInfo := ""
		if len(e.Warnings) > 0 {
			warningInfo = fmt.Sprintf(" with %d warnings", len(e.Warnings))
		}
		triggerInfo := ""
		if e.TriggerReason != "" {
			triggerInfo = fmt.Sprintf(" (trigger: %s)", e.TriggerReason)
		}
		return fmt.Sprintf("HAProxy configuration validation succeeded%s (%dms)%s", warningInfo, e.DurationMs, triggerInfo),
			append(attrs, "warnings", len(e.Warnings), "duration_ms", e.DurationMs, "trigger_reason", e.TriggerReason)

	case *events.ValidationFailedEvent:
		triggerInfo := ""
		if e.TriggerReason != "" {
			triggerInfo = fmt.Sprintf(" (trigger: %s)", e.TriggerReason)
		}
		return fmt.Sprintf("HAProxy configuration validation failed with %d errors (%dms)%s",
				len(e.Errors), e.DurationMs, triggerInfo),
			append(attrs, "error_count", len(e.Errors), "duration_ms", e.DurationMs, "trigger_reason", e.TriggerReason)

	// Validation Test Events
	case *events.ValidationTestsStartedEvent:
		return fmt.Sprintf("Starting validation tests (%d tests)", e.TestCount),
			append(attrs, "test_count", e.TestCount)

	case *events.ValidationTestsCompletedEvent:
		return fmt.Sprintf("Validation tests completed: %d passed, %d failed (%dms)",
				e.PassedTests, e.FailedTests, e.DurationMs),
			append(attrs,
				"total_tests", e.TotalTests,
				"passed_tests", e.PassedTests,
				"failed_tests", e.FailedTests,
				"duration_ms", e.DurationMs)

	case *events.ValidationTestsFailedEvent:
		return fmt.Sprintf("Validation tests failed: %d tests",
				len(e.FailedTests)),
			append(attrs,
				"failed_count", len(e.FailedTests),
				"failed_tests", e.FailedTests)

	default:
		return "", attrs
	}
}

// deploymentInsight handles DeploymentStarted, InstanceDeployed, InstanceDeploymentFailed,
// and DeploymentCompleted events.
func (ec *EventCommentator) deploymentInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.DeploymentStartedEvent:
		return fmt.Sprintf("Deployment started to %d HAProxy instances", len(e.Endpoints)),
			append(attrs, "instance_count", len(e.Endpoints))

	case *events.InstanceDeployedEvent:
		reloadInfo := ""
		if e.ReloadRequired {
			reloadInfo = " (reload triggered)"
		}
		return fmt.Sprintf("Instance deployed successfully in %dms%s", e.DurationMs, reloadInfo),
			append(attrs, "duration_ms", e.DurationMs, "reload_required", e.ReloadRequired)

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

	default:
		return "", attrs
	}
}

// webhookInsight handles WebhookValidationRequest, WebhookValidationAllowed,
// WebhookValidationDenied, and WebhookValidationError events.
func (ec *EventCommentator) webhookInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.WebhookValidationRequestEvent:
		resourceRef := fmt.Sprintf("%s/%s", e.Namespace, e.Name)
		if e.Namespace == "" {
			resourceRef = e.Name
		}
		return fmt.Sprintf("Webhook validation request: %s %s %s",
				e.Operation, e.Kind, resourceRef),
			append(attrs,
				"request_uid", e.RequestUID,
				"kind", e.Kind,
				"name", e.Name,
				"namespace", e.Namespace,
				"operation", e.Operation)

	case *events.WebhookValidationAllowedEvent:
		resourceRef := fmt.Sprintf("%s/%s", e.Namespace, e.Name)
		if e.Namespace == "" {
			resourceRef = e.Name
		}
		return fmt.Sprintf("Webhook validation allowed: %s %s", e.Kind, resourceRef),
			append(attrs,
				"request_uid", e.RequestUID,
				"kind", e.Kind,
				"name", e.Name,
				"namespace", e.Namespace)

	case *events.WebhookValidationDeniedEvent:
		resourceRef := fmt.Sprintf("%s/%s", e.Namespace, e.Name)
		if e.Namespace == "" {
			resourceRef = e.Name
		}
		// Truncate long reasons for log readability
		reason := e.Reason
		if len(reason) > maxErrorPreviewLength {
			reason = reason[:maxErrorPreviewLength-3] + "..."
		}
		return fmt.Sprintf("Webhook validation denied: %s %s - %s",
				e.Kind, resourceRef, reason),
			append(attrs,
				"request_uid", e.RequestUID,
				"kind", e.Kind,
				"name", e.Name,
				"namespace", e.Namespace,
				"reason", e.Reason)

	case *events.WebhookValidationErrorEvent:
		return fmt.Sprintf("Webhook validation error for %s: %s",
				e.Kind, e.Error),
			append(attrs,
				"request_uid", e.RequestUID,
				"kind", e.Kind,
				"error", e.Error)

	default:
		return "", attrs
	}
}

// leaderInsight handles LeaderElectionStarted, BecameLeader, LostLeadership,
// and NewLeaderObserved events.
func (ec *EventCommentator) leaderInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.LeaderElectionStartedEvent:
		return fmt.Sprintf("Leader election started: identity=%s, lease=%s/%s",
				e.Identity, e.LeaseNamespace, e.LeaseName),
			append(attrs,
				"identity", e.Identity,
				"lease_name", e.LeaseName,
				"lease_namespace", e.LeaseNamespace)

	case *events.BecameLeaderEvent:
		return fmt.Sprintf("Became leader: %s", e.Identity),
			append(attrs, "identity", e.Identity)

	case *events.LostLeadershipEvent:
		reasonMsg := ""
		if e.Reason != "" {
			reasonMsg = fmt.Sprintf(" (reason: %s)", e.Reason)
		}
		return fmt.Sprintf("Lost leadership: %s%s", e.Identity, reasonMsg),
			append(attrs,
				"identity", e.Identity,
				"reason", e.Reason)

	case *events.NewLeaderObservedEvent:
		observerMsg := "another replica"
		if e.IsSelf {
			observerMsg = "this replica"
		}
		return fmt.Sprintf("New leader observed: %s (%s)",
				e.NewLeaderIdentity, observerMsg),
			append(attrs,
				"leader_identity", e.NewLeaderIdentity,
				"is_self", e.IsSelf)

	default:
		return "", attrs
	}
}

// statusInsight handles StatusUpdateCompleted and StatusUpdateFailed events.
func (ec *EventCommentator) statusInsight(event busevents.Event, attrs []any) (insight string, args []any) {
	switch e := event.(type) {
	case *events.StatusUpdateCompletedEvent:
		return fmt.Sprintf("Status patches applied (%s phase): %d applied, %d skipped (%dms)",
				e.Phase, e.AppliedCount, e.SkippedCount, e.DurationMs),
			append(attrs,
				"phase", string(e.Phase),
				"applied", e.AppliedCount,
				"skipped", e.SkippedCount,
				"duration_ms", e.DurationMs)

	case *events.StatusUpdateFailedEvent:
		retriableInfo := ""
		if e.Retriable {
			retriableInfo = " (retriable)"
		}
		return fmt.Sprintf("Status patch failed for %s/%s [%s]%s: %s",
				e.Namespace, e.Name, e.GVR, retriableInfo, e.Error),
			append(attrs,
				"namespace", e.Namespace,
				"name", e.Name,
				"gvr", e.GVR,
				"error", e.Error,
				"retriable", e.Retriable)

	default:
		return "", attrs
	}
}

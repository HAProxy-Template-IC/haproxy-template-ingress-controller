package commentator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "commentator"

	// maxErrorPreviewLength is the maximum length for error message previews
	// in validation failure summaries. Longer errors are truncated.
	maxErrorPreviewLength = 80
)

// ReconciliationSummary contains aggregated metrics from a complete reconciliation cycle.
// It is computed by correlating events with the same correlation ID.
type ReconciliationSummary struct {
	// Trigger is the reason that initiated the reconciliation.
	Trigger string

	// RenderMs is the time spent rendering templates.
	RenderMs int64

	// ValidateMs is the time spent validating the HAProxy configuration.
	ValidateMs int64

	// DeployMs is the time spent deploying to HAProxy instances.
	DeployMs int64

	// TotalMs is the wall-clock time from reconciliation trigger to deployment completion.
	// This may be less than the sum of individual phase durations if there are gaps.
	TotalMs int64

	// Queue wait times between phases (derived from event timestamps).
	// These represent the time events spent waiting in channels before processing.
	TriggerToRenderQueueMs  int64
	RenderToValidateQueueMs int64
	ValidateToDeployQueueMs int64
	TotalQueueMs            int64

	// Instances is the "succeeded/total" string for deployment.
	Instances string

	// Reloads is the number of HAProxy reloads triggered.
	Reloads int

	// Operations is the total number of API operations performed.
	Operations int
}

// EventCommentator provides domain-aware logging for all events flowing through the EventBus.
// It decouples logging from business logic.
type EventCommentator struct {
	eventBus   *busevents.EventBus
	eventChan  <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger     *slog.Logger
	ringBuffer *RingBuffer
	stopCh     chan struct{}
}

// NewEventCommentator creates a new Event Commentator.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to
//   - logger: The structured logger to use
//   - bufferSize: Ring buffer capacity (recommended: 1000)
//
// Returns:
//   - *EventCommentator ready to start
func NewEventCommentator(eventBus *busevents.EventBus, logger *slog.Logger, bufferSize int) *EventCommentator {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps.
	// Use SubscribeLossy because commentator is an observability component where
	// occasional event drops are acceptable and should not trigger WARN logs.
	eventChan := eventBus.SubscribeLossy(ComponentName, buffers.Observability())

	return &EventCommentator{
		eventBus:   eventBus,
		eventChan:  eventChan,
		logger:     logger.With("component", ComponentName),
		ringBuffer: NewRingBuffer(bufferSize),
		stopCh:     make(chan struct{}),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (ec *EventCommentator) Name() string {
	return ComponentName
}

// Start begins processing events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// The component is already subscribed to the EventBus (subscription happens in constructor).
// Returns nil on graceful shutdown.
//
// Example:
//
//	go commentator.Start(ctx)
func (ec *EventCommentator) Start(ctx context.Context) error {
	ec.logger.Debug("event commentator starting", "buffer_capacity", ec.ringBuffer.Capacity())

	for {
		select {
		case <-ctx.Done():
			ec.logger.Info("Event commentator shutting down", "reason", ctx.Err())
			return nil
		case <-ec.stopCh:
			ec.logger.Info("Event commentator shutting down")
			return nil
		case event := <-ec.eventChan:
			ec.processEvent(event)
		}
	}
}

// Stop gracefully stops the commentator.
func (ec *EventCommentator) Stop() {
	close(ec.stopCh)
}

// FindByCorrelationID returns events matching the specified correlation ID.
// This method is used for debugging event flows through the reconciliation pipeline.
//
// Parameters:
//   - correlationID: The correlation ID to search for
//   - maxCount: Maximum number of events to return (0 = no limit)
//
// Returns:
//   - Slice of events matching the correlation ID, newest first
func (ec *EventCommentator) FindByCorrelationID(correlationID string, maxCount int) []busevents.Event {
	return ec.ringBuffer.FindByCorrelationID(correlationID, maxCount)
}

// FindRecent returns the N most recent events, newest first.
// This method is used for debugging recent event activity.
func (ec *EventCommentator) FindRecent(n int) []busevents.Event {
	return ec.ringBuffer.FindRecent(n)
}

// computeReconciliationSummary aggregates metrics from a reconciliation cycle
// by correlating events with the same correlation ID.
//
// This method extracts SRP-violating logic from generateInsight into a dedicated
// function focused solely on event correlation and metric aggregation.
//
// Queue wait calculation:
// Each event has a timestamp (when it was created, after processing completed) and
// DurationMs (how long processing took). Therefore:
//
//	processing_start = event.timestamp - DurationMs
//	queue_wait = processing_start - previous_event.timestamp
func (ec *EventCommentator) computeReconciliationSummary(
	deploymentEvent *events.DeploymentCompletedEvent,
) ReconciliationSummary {
	summary := ReconciliationSummary{
		Instances:  fmt.Sprintf("%d/%d", deploymentEvent.Succeeded, deploymentEvent.Total),
		Reloads:    deploymentEvent.ReloadsTriggered,
		Operations: deploymentEvent.TotalAPIOperations,
		DeployMs:   deploymentEvent.DurationMs,
	}

	correlationID := deploymentEvent.CorrelationID()
	if correlationID == "" {
		return summary
	}

	// Find correlated events to extract phase timings and timestamps
	correlatedEvents := ec.ringBuffer.FindByCorrelationID(correlationID, 0)

	var triggerTimestamp time.Time
	var renderTimestamp time.Time
	var validateTimestamp time.Time

	for _, evt := range correlatedEvents {
		switch te := evt.(type) {
		case *events.ReconciliationTriggeredEvent:
			summary.Trigger = te.Reason
			triggerTimestamp = te.Timestamp()
		case *events.TemplateRenderedEvent:
			summary.RenderMs = te.DurationMs
			renderTimestamp = te.Timestamp()
		case *events.ValidationCompletedEvent:
			summary.ValidateMs = te.DurationMs
			validateTimestamp = te.Timestamp()
		}
	}

	// Calculate actual wall-clock time from trigger to completion
	// This is more accurate than summing individual phase durations
	if !triggerTimestamp.IsZero() {
		summary.TotalMs = deploymentEvent.Timestamp().Sub(triggerTimestamp).Milliseconds()
	} else {
		// Fallback to sum of individual phases if trigger event not found
		summary.TotalMs = summary.RenderMs + summary.ValidateMs + summary.DeployMs
	}

	// Calculate queue wait times between phases
	// Queue wait = (event.timestamp - event.DurationMs) - previous_event.timestamp
	// This represents the time the event spent waiting in the channel before processing started

	// Trigger → Render queue wait
	if !triggerTimestamp.IsZero() && !renderTimestamp.IsZero() && summary.RenderMs > 0 {
		renderStartTime := renderTimestamp.Add(-time.Duration(summary.RenderMs) * time.Millisecond)
		summary.TriggerToRenderQueueMs = renderStartTime.Sub(triggerTimestamp).Milliseconds()
		if summary.TriggerToRenderQueueMs < 0 {
			summary.TriggerToRenderQueueMs = 0
		}
	}

	// Render → Validate queue wait
	if !renderTimestamp.IsZero() && !validateTimestamp.IsZero() && summary.ValidateMs > 0 {
		validateStartTime := validateTimestamp.Add(-time.Duration(summary.ValidateMs) * time.Millisecond)
		summary.RenderToValidateQueueMs = validateStartTime.Sub(renderTimestamp).Milliseconds()
		if summary.RenderToValidateQueueMs < 0 {
			summary.RenderToValidateQueueMs = 0
		}
	}

	// Validate → Deploy queue wait
	if !validateTimestamp.IsZero() && summary.DeployMs > 0 {
		deployStartTime := deploymentEvent.Timestamp().Add(-time.Duration(summary.DeployMs) * time.Millisecond)
		summary.ValidateToDeployQueueMs = deployStartTime.Sub(validateTimestamp).Milliseconds()
		if summary.ValidateToDeployQueueMs < 0 {
			summary.ValidateToDeployQueueMs = 0
		}
	}

	// Total queue overhead
	summary.TotalQueueMs = summary.TriggerToRenderQueueMs + summary.RenderToValidateQueueMs + summary.ValidateToDeployQueueMs

	return summary
}

// shouldStoreInBuffer determines if an event should be stored in the ring buffer.
// Heavyweight events containing large payloads are filtered out to reduce memory usage.
// These events are still logged but not retained in the ring buffer.
func shouldStoreInBuffer(event busevents.Event) bool {
	switch event.(type) {
	case *events.TemplateRenderedEvent,
		*events.ValidationCompletedEvent,
		*events.DeploymentScheduledEvent:
		return false
	default:
		return true
	}
}

// processEvent handles a single event: adds to buffer and logs with domain insights.
func (ec *EventCommentator) processEvent(event busevents.Event) {
	// Add to ring buffer first (for correlation), filtering heavyweight events
	if shouldStoreInBuffer(event) {
		ec.ringBuffer.Add(event)
	}

	// Generate domain-aware log message with correlation
	ec.logWithInsight(event)
}

// logWithInsight produces a domain-aware log message for the event.
//
// This is where the "commentator" intelligence lives - applying domain knowledge
// and correlating recent events to provide contextual insights.
func (ec *EventCommentator) logWithInsight(event busevents.Event) {
	// Determine log level based on event type and content
	level := ec.determineLogLevel(event)

	// Generate contextual message and structured attributes
	message, attrs := ec.generateInsight(event)

	// Add correlation ID if the event supports it
	attrs = ec.appendCorrelation(event, attrs)

	// Log with appropriate level
	ec.logger.Log(context.Background(), level, message, attrs...)
}

// appendCorrelation adds event tracking IDs to attributes if the event implements CorrelatedEvent.
// It adds event_id, correlation_id, and causation_id for tracing event flows.
func (ec *EventCommentator) appendCorrelation(event busevents.Event, attrs []any) []any {
	if correlated, ok := event.(events.CorrelatedEvent); ok {
		if eventID := correlated.EventID(); eventID != "" {
			attrs = append(attrs, "event_id", eventID)
		}
		if correlationID := correlated.CorrelationID(); correlationID != "" {
			attrs = append(attrs, "correlation_id", correlationID)
		}
		if causationID := correlated.CausationID(); causationID != "" {
			attrs = append(attrs, "causation_id", causationID)
		}
	}
	return attrs
}

// determineLogLevel maps events to appropriate log levels.
// Most events are mapped by type, but some events like DeploymentCompleted
// are mapped based on content (e.g., no-op deployments are demoted to DEBUG).
func (ec *EventCommentator) determineLogLevel(event busevents.Event) slog.Level {
	switch event.EventType() {
	// Error level - failures and invalid config (user needs to fix)
	case events.EventTypeReconciliationFailed,
		events.EventTypeTemplateRenderFailed,
		events.EventTypeValidationFailed,
		events.EventTypeInstanceDeploymentFailed,
		events.EventTypeWebhookValidationError,
		events.EventTypeConfigInvalid:
		return slog.LevelError

	// Warn level - recoverable states and leadership loss
	case events.EventTypeCredentialsInvalid,
		events.EventTypeWebhookValidationDenied,
		events.EventTypeLostLeadership:
		return slog.LevelWarn

	// Info level - lifecycle and completion events
	// Note: ReconciliationCompleted and ValidationCompleted are demoted to DEBUG
	// because DeploymentCompletedEvent now produces a consolidated summary
	case events.EventTypeConfigValidated,
		events.EventTypeIndexSynchronized,
		events.EventTypeLeaderElectionStarted,
		events.EventTypeBecameLeader,
		events.EventTypeNewLeaderObserved:
		return slog.LevelInfo

	// Deployment completed - demote to DEBUG when nothing changed
	case events.EventTypeDeploymentCompleted:
		if deploymentEvent, ok := event.(*events.DeploymentCompletedEvent); ok {
			if deploymentEvent.ReloadsTriggered == 0 && deploymentEvent.TotalAPIOperations == 0 {
				return slog.LevelDebug
			}
		}
		return slog.LevelInfo

	// Debug level - everything else (detailed operational events)
	default:
		return slog.LevelDebug
	}
}

// generateInsight creates a contextual message and structured attributes for the event.
//
// This applies domain knowledge and uses the ring buffer for event correlation.
//
//nolint:gocyclo,revive // Large switch statement handling many event types - refactoring would reduce readability
func (ec *EventCommentator) generateInsight(event busevents.Event) (insight string, args []any) {
	eventType := event.EventType()
	attrs := []any{
		"event_type", eventType,
		"timestamp", event.Timestamp(),
	}

	switch e := event.(type) {
	// Configuration Events
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
		validationRequests := ec.ringBuffer.FindByTypeInWindow(events.EventTypeConfigValidationRequest, 30*time.Second)
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

	// Resource Events
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

	// Reconciliation Events
	case *events.ReconciliationTriggeredEvent:
		// Correlate: when was the last reconciliation?
		recentReconciliations := ec.ringBuffer.FindByTypeInWindow(events.EventTypeReconciliationCompleted, 5*time.Minute)
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
		startEvents := ec.ringBuffer.FindByTypeInWindow(events.EventTypeReconciliationStarted, 1*time.Minute)
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

	// Template Events
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

	// Validation Events
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

	// Deployment Events
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

		return "Reconciliation", attrs

	// HAProxy Pod Events
	case *events.HAProxyPodsDiscoveredEvent:
		// Correlate: was this a change?
		recentDiscoveries := ec.ringBuffer.FindByTypeInWindow(events.EventTypeHAProxyPodsDiscovered, 30*time.Second)
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

	// Webhook Validation Events
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

	// Leader Election Events
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
		// Fallback for unknown event types
		return fmt.Sprintf("Event: %s", eventType), attrs
	}
}

package commentator

import (
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

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

package commentator

import (
	"context"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/buffers"
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

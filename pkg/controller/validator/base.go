package validator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"haptic/pkg/controller/events"
	busevents "haptic/pkg/events"
)

const (
	// EventBufferSize is the size of the event subscription buffer.
	// Size 10: Low-volume component (~1 validation request per reconciliation).
	EventBufferSize = 10
)

// ValidationHandler defines the interface for validator-specific validation logic.
//
// Each validator (basic, template, jsonpath) implements this interface to provide
// their specific validation logic while reusing the common event loop infrastructure.
type ValidationHandler interface {
	// HandleRequest processes a ConfigValidationRequest and publishes a response.
	// The implementation should validate the config and publish a ConfigValidationResponse
	// event to the bus.
	HandleRequest(req *events.ConfigValidationRequest)
}

// BaseValidator provides common event loop infrastructure for all validators.
//
// It handles:
//   - Event subscription and routing
//   - Panic recovery
//   - Graceful shutdown
//   - Stop idempotency
//
// Validators embed this struct and provide a ValidationHandler implementation
// for their specific validation logic.
type BaseValidator struct {
	eventBus    *busevents.EventBus
	eventChan   <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger      *slog.Logger
	stopCh      chan struct{}
	stopOnce    sync.Once
	name        string
	description string
	handler     ValidationHandler
}

// NewBaseValidator creates a new base validator with the given configuration.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//   - name: Validator name (for error messages and responses)
//   - description: Human-readable component description (for logging)
//   - handler: ValidationHandler implementation for validator-specific logic
//
// Returns:
//   - *BaseValidator ready to start
func NewBaseValidator(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	name string,
	description string,
	handler ValidationHandler,
) *BaseValidator {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &BaseValidator{
		eventBus:    eventBus,
		eventChan:   eventChan,
		logger:      logger.With("component", name+"-validator"),
		stopCh:      make(chan struct{}),
		name:        name,
		description: description,
		handler:     handler,
	}
}

// Start begins processing validation requests from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// The component is already subscribed to the EventBus (subscription happens in constructor).
// Returns nil on graceful shutdown.
//
// The event loop:
//  1. Filters for ConfigValidationRequest events
//  2. Wraps handling in panic recovery
//  3. Delegates to the ValidationHandler
//
// Example:
//
//	go validator.Start(ctx)
func (v *BaseValidator) Start(ctx context.Context) error {
	v.logger.Info(fmt.Sprintf("%s starting", v.description))

	for {
		select {
		case <-ctx.Done():
			v.logger.Info(fmt.Sprintf("%s shutting down", v.description), "reason", ctx.Err())
			return nil
		case <-v.stopCh:
			v.logger.Info(fmt.Sprintf("%s shutting down", v.description))
			return nil
		case event := <-v.eventChan:
			v.handleEvent(event)
		}
	}
}

// handleEvent processes a single event with panic recovery.
// Filters for ConfigValidationRequest events and delegates to the ValidationHandler.
func (v *BaseValidator) handleEvent(event busevents.Event) {
	defer func() {
		if r := recover(); r != nil {
			v.logger.Error(fmt.Sprintf("%s panicked during validation", v.name),
				"panic", r,
				"event_type", fmt.Sprintf("%T", event))

			// Publish error response to prevent scatter-gather timeout
			if req, ok := event.(*events.ConfigValidationRequest); ok {
				response := events.NewConfigValidationResponse(
					req.RequestID(),
					v.name,
					false,
					[]string{fmt.Sprintf("validator panicked: %v", r)},
				)
				v.eventBus.Publish(response)
			}
		}
	}()

	if req, ok := event.(*events.ConfigValidationRequest); ok {
		v.handler.HandleRequest(req)
	}
}

// Stop gracefully stops the validator.
// Safe to call multiple times.
func (v *BaseValidator) Stop() {
	v.stopOnce.Do(func() {
		close(v.stopCh)
	})
}

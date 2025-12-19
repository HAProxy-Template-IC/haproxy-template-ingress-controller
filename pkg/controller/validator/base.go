package validator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
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
	return &BaseValidator{
		eventBus:    eventBus,
		logger:      logger,
		stopCh:      make(chan struct{}),
		name:        name,
		description: description,
		handler:     handler,
	}
}

// Start begins processing validation requests from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// It should typically be run in a goroutine.
//
// The event loop:
//  1. Subscribes to all events on the bus
//  2. Filters for ConfigValidationRequest events
//  3. Wraps handling in panic recovery
//  4. Delegates to the ValidationHandler
//
// Example:
//
//	go validator.Start(ctx)
func (v *BaseValidator) Start(ctx context.Context) {
	eventCh := v.eventBus.Subscribe(10)

	v.logger.Info(fmt.Sprintf("%s component started", v.description))

	for {
		select {
		case <-ctx.Done():
			v.logger.Info(fmt.Sprintf("%s component stopped", v.description), "reason", ctx.Err())
			return
		case <-v.stopCh:
			v.logger.Info(fmt.Sprintf("%s component stopped", v.description))
			return
		case event := <-eventCh:
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

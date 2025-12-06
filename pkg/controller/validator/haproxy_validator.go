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

// Package validator implements validation components for HAProxy configuration.
//
// The HAProxyValidatorComponent validates rendered HAProxy configurations
// using a two-phase approach: syntax validation (client-native parser) and
// semantic validation (haproxy binary with -c flag).
package validator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/dataplane"
	busevents "haproxy-template-ic/pkg/events"
)

const (
	// HAProxyValidatorComponentName is the unique identifier for this component.
	HAProxyValidatorComponentName = "haproxy-validator"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = 50
)

// HAProxyValidatorComponent validates rendered HAProxy configurations.
//
// It subscribes to TemplateRenderedEvent and BecameLeaderEvent, validates the configuration using
// dataplane.ValidateConfiguration(), and publishes validation result events
// for the next phase (deployment).
//
// Validation is performed in two phases:
//  1. Syntax validation using client-native parser
//  2. Semantic validation using haproxy binary (-c flag)
//
// The component caches the last validation result to support state replay during
// leadership transitions (when new leader-only components start subscribing).
type HAProxyValidatorComponent struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger    *slog.Logger

	// State protected by mutex (for leadership transition replay)
	mu                       sync.RWMutex
	lastValidationSucceeded  bool
	lastValidationWarnings   []string
	lastValidationDurationMs int64
	lastCorrelationID        string // Correlation ID from triggering event
	hasValidationResult      bool
}

// NewHAProxyValidator creates a new HAProxy validator component.
//
// The validator extracts validation paths from TemplateRenderedEvent, which are created
// per-render by the Renderer component for isolated validation.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - logger: Structured logger for component logging
//
// Returns:
//   - A new HAProxyValidatorComponent instance ready to be started
func NewHAProxyValidator(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *HAProxyValidatorComponent {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &HAProxyValidatorComponent{
		eventBus:  eventBus,
		eventChan: eventChan,
		logger:    logger,
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (v *HAProxyValidatorComponent) Name() string {
	return HAProxyValidatorComponentName
}

// Start begins the validator's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// The component is already subscribed to the EventBus (subscription happens in NewHAProxyValidator()),
// so this method only processes events:
//   - TemplateRenderedEvent: Starts HAProxy configuration validation
//   - BecameLeaderEvent: Replays last validation state for new leader-only components
//
// The component runs until the context is cancelled, at which point it
// performs cleanup and returns.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (v *HAProxyValidatorComponent) Start(ctx context.Context) error {
	v.logger.Info("HAProxy Validator starting")

	for {
		select {
		case event := <-v.eventChan:
			v.handleEvent(event)

		case <-ctx.Done():
			v.logger.Info("HAProxy Validator shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (v *HAProxyValidatorComponent) handleEvent(event busevents.Event) {
	switch ev := event.(type) {
	case *events.TemplateRenderedEvent:
		v.handleTemplateRendered(ev)

	case *events.BecameLeaderEvent:
		v.handleBecameLeader(ev)
	}
}

// handleTemplateRendered validates the rendered HAProxy configuration.
// Propagates correlation ID from the triggering event to validation events.
func (v *HAProxyValidatorComponent) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	startTime := time.Now()
	correlationID := event.CorrelationID()

	v.logger.Info("HAProxy configuration validation started",
		"validation_config_bytes", event.ValidationConfigBytes,
		"auxiliary_files", event.AuxiliaryFileCount,
		"correlation_id", correlationID)

	// Publish validation started event with correlation
	v.eventBus.Publish(events.NewValidationStartedEvent(
		events.PropagateCorrelation(event),
	))

	// Extract validation auxiliary files from event
	// These contain pending HTTP content (for testing new content before promotion)
	// Type-assert from interface{} to *dataplane.AuxiliaryFiles
	auxiliaryFiles, ok := event.ValidationAuxiliaryFiles.(*dataplane.AuxiliaryFiles)
	if !ok {
		v.publishValidationFailure(
			[]string{"failed to extract validation auxiliary files from event"},
			time.Since(startTime).Milliseconds(),
			correlationID,
		)
		return
	}

	// Extract validation paths from event
	// Type-assert from interface{} to *dataplane.ValidationPaths
	validationPaths, ok := event.ValidationPaths.(*dataplane.ValidationPaths)
	if !ok {
		v.publishValidationFailure(
			[]string{"failed to extract validation paths from event"},
			time.Since(startTime).Milliseconds(),
			correlationID,
		)
		return
	}

	// Validate configuration using validation config and paths from event
	// Use ValidationHAProxyConfig (rendered with temp paths) instead of HAProxyConfig (production paths)
	// Pass nil version to use default v3.0 schema (safest for validation)
	err := dataplane.ValidateConfiguration(event.ValidationHAProxyConfig, auxiliaryFiles, validationPaths, nil)
	if err != nil {
		// Simplify error message for user-facing output
		// Keep full error in logs for debugging
		simplified := dataplane.SimplifyValidationError(err)

		v.logger.Error("HAProxy configuration validation failed",
			"error", simplified,
			"correlation_id", correlationID)

		v.publishValidationFailure(
			[]string{simplified},
			time.Since(startTime).Milliseconds(),
			correlationID,
		)
		return
	}

	// Validation succeeded
	durationMs := time.Since(startTime).Milliseconds()

	v.logger.Info("HAProxy configuration validation completed",
		"duration_ms", durationMs,
		"correlation_id", correlationID)

	// Cache validation result for leadership transition replay
	v.mu.Lock()
	v.lastValidationSucceeded = true
	v.lastValidationWarnings = []string{} // No warnings
	v.lastValidationDurationMs = durationMs
	v.lastCorrelationID = correlationID
	v.hasValidationResult = true
	v.mu.Unlock()

	v.eventBus.Publish(events.NewValidationCompletedEvent(
		[]string{}, // No warnings
		durationMs,
		events.PropagateCorrelation(event),
	))
}

// handleBecameLeader handles BecameLeaderEvent by re-publishing the last validation result.
//
// This ensures DeploymentScheduler (which starts subscribing only after becoming leader)
// receives the current validation state, even if validation occurred before leadership was acquired.
//
// This prevents the "late subscriber problem" where leader-only components miss events
// that were published before they started subscribing.
func (v *HAProxyValidatorComponent) handleBecameLeader(_ *events.BecameLeaderEvent) {
	v.mu.RLock()
	hasResult := v.hasValidationResult
	succeeded := v.lastValidationSucceeded
	warnings := v.lastValidationWarnings
	durationMs := v.lastValidationDurationMs
	correlationID := v.lastCorrelationID
	v.mu.RUnlock()

	if !hasResult {
		v.logger.Debug("became leader but no validation result available yet, skipping state replay")
		return
	}

	if succeeded {
		v.logger.Info("became leader, re-publishing last validation result (success) for DeploymentScheduler",
			"warnings", len(warnings),
			"duration_ms", durationMs,
			"correlation_id", correlationID)

		// Re-publish with original correlation ID so the deployment can be traced
		v.eventBus.Publish(events.NewValidationCompletedEvent(
			warnings,
			durationMs,
			events.WithCorrelation(correlationID, correlationID),
		))
	} else {
		v.logger.Info("became leader, last validation failed, skipping state replay")
		// Note: We only replay ValidationCompletedEvent (success), not ValidationFailedEvent.
		// DeploymentScheduler only acts on successful validation, so replaying failures
		// would be unnecessary and could cause confusion.
	}
}

// publishValidationFailure publishes a validation failure event and caches the failure state.
func (v *HAProxyValidatorComponent) publishValidationFailure(errors []string, durationMs int64, correlationID string) {
	// Cache validation failure for leadership transition state
	v.mu.Lock()
	v.lastValidationSucceeded = false
	v.lastValidationDurationMs = durationMs
	v.lastCorrelationID = correlationID
	v.hasValidationResult = true
	v.mu.Unlock()

	v.eventBus.Publish(events.NewValidationFailedEvent(
		errors,
		durationMs,
		events.WithCorrelation(correlationID, correlationID),
	))
}

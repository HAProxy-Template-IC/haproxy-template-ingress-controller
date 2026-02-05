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

package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// PipelineExecutor defines the interface for executing the render-validate pipeline.
// This allows mocking in tests.
type PipelineExecutor interface {
	Execute(ctx context.Context, provider stores.StoreProvider) (*pipeline.PipelineResult, error)
}

const (
	// CoordinatorComponentName is the unique identifier for the ReconciliationCoordinator.
	CoordinatorComponentName = "reconciliation-coordinator"

	// CoordinatorEventBufferSize is the size of the event subscription buffer.
	CoordinatorEventBufferSize = 50
)

// Coordinator orchestrates reconciliation by calling the Pipeline directly.
//
// This component replaces the event-driven flow where Renderer and Validator
// are separate components publishing events. Instead, it calls Pipeline.Execute()
// synchronously and publishes the appropriate events for downstream components.
//
// Flow:
//  1. ReconciliationTriggeredEvent received
//  2. Publish ReconciliationStartedEvent
//  3. Call Pipeline.Execute() (renders and validates)
//  4. If success: Publish TemplateRenderedEvent + ValidationCompletedEvent
//  5. If failure: Publish ReconciliationFailedEvent
//
// The DeploymentScheduler still operates event-driven, receiving TemplateRenderedEvent
// and ValidationCompletedEvent to schedule deployments.
type Coordinator struct {
	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	pipeline      PipelineExecutor
	storeProvider stores.StoreProvider
	logger        *slog.Logger

	// subscriptionReady is closed when the component has subscribed to events.
	// Implements lifecycle.SubscriptionReadySignaler for leader-only components.
	subscriptionReady chan struct{}
}

// CoordinatorConfig contains configuration for creating a Coordinator.
type CoordinatorConfig struct {
	// EventBus is the event bus for subscribing to events and publishing results.
	EventBus *busevents.EventBus

	// Pipeline is the render-validate pipeline to execute.
	// Must implement PipelineExecutor interface.
	Pipeline PipelineExecutor

	// StoreProvider provides access to resource stores.
	StoreProvider stores.StoreProvider

	// Logger is the structured logger.
	Logger *slog.Logger
}

// NewCoordinator creates a new ReconciliationCoordinator.
//
// Note: eventChan is NOT subscribed here - subscription happens in Start().
// This is a leader-only component that subscribes when Start() is called
// (after leadership is acquired). All-replica components replay their state
// on BecameLeaderEvent to ensure leader-only components receive current state.
//
// Parameters:
//   - cfg: Configuration for the coordinator
//
// Returns:
//   - A new Coordinator instance ready to be started
func NewCoordinator(cfg *CoordinatorConfig) *Coordinator {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Coordinator{
		eventBus:          cfg.EventBus,
		pipeline:          cfg.Pipeline,
		storeProvider:     cfg.StoreProvider,
		logger:            logger.With("component", CoordinatorComponentName),
		subscriptionReady: make(chan struct{}),
	}
}

// Name returns the unique identifier for this component.
func (c *Coordinator) Name() string {
	return CoordinatorComponentName
}

// SubscriptionReady returns a channel that is closed when the component has
// completed its event subscription. This implements lifecycle.SubscriptionReadySignaler.
func (c *Coordinator) SubscriptionReady() <-chan struct{} {
	return c.subscriptionReady
}

// Start begins the coordinator's event loop.
//
// This method blocks until the context is cancelled.
func (c *Coordinator) Start(ctx context.Context) error {
	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(
		CoordinatorComponentName,
		CoordinatorEventBufferSize,
		events.EventTypeReconciliationTriggered,
	)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	close(c.subscriptionReady)

	c.logger.Debug("reconciliation coordinator starting")

	for {
		select {
		case event := <-c.eventChan:
			if triggered, ok := event.(*events.ReconciliationTriggeredEvent); ok {
				c.handleReconciliationTriggered(ctx, triggered)
			}

		case <-ctx.Done():
			c.logger.Info("Reconciliation coordinator shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleReconciliationTriggered orchestrates a reconciliation cycle.
func (c *Coordinator) handleReconciliationTriggered(ctx context.Context, event *events.ReconciliationTriggeredEvent) {
	startTime := time.Now()
	correlationID := event.CorrelationID()

	c.logger.Debug("Reconciliation triggered",
		"reason", event.Reason,
		"correlation_id", correlationID)

	// Publish reconciliation started event
	c.eventBus.Publish(events.NewReconciliationStartedEvent(event.Reason))

	// Execute the render-validate pipeline
	result, err := c.pipeline.Execute(ctx, c.storeProvider)
	if err != nil {
		c.handlePipelineFailure(err, event, startTime)
		return
	}

	// Pipeline succeeded - publish events for downstream components
	c.handlePipelineSuccess(result, event, startTime)
}

// handlePipelineSuccess publishes events for successful render+validate.
func (c *Coordinator) handlePipelineSuccess(
	result *pipeline.PipelineResult,
	triggerEvent *events.ReconciliationTriggeredEvent,
	startTime time.Time,
) {
	coalescible := triggerEvent.Coalescible()

	// Publish TemplateRenderedEvent for DeploymentScheduler
	// Config uses relative paths that work everywhere with `default-path config`
	templateEvent := events.NewTemplateRenderedEvent(
		result.HAProxyConfig,
		result.AuxiliaryFiles,
		result.AuxFileCount,
		result.RenderDurationMs,
		triggerEvent.Reason,
		result.ContentChecksum,
		coalescible,
		events.PropagateCorrelation(triggerEvent),
	)
	c.eventBus.Publish(templateEvent)

	// Publish ValidationCompletedEvent to trigger deployment scheduling
	// Pass ParsedConfig from pipeline result to enable downstream sync optimization
	validationEvent := events.NewValidationCompletedEvent(
		nil, // No warnings
		result.ValidateDurationMs,
		triggerEvent.Reason,
		result.ParsedConfig,
		coalescible,
		events.PropagateCorrelation(templateEvent),
	)
	c.eventBus.Publish(validationEvent)

	// Publish ReconciliationCompletedEvent
	totalDuration := time.Since(startTime).Milliseconds()
	c.eventBus.Publish(events.NewReconciliationCompletedEvent(totalDuration))

	c.logger.Debug("Reconciliation completed",
		"correlation_id", triggerEvent.CorrelationID(),
		"render_ms", result.RenderDurationMs,
		"validate_ms", result.ValidateDurationMs,
		"total_ms", totalDuration)
}

// handlePipelineFailure publishes phase-specific failure events followed by ReconciliationFailedEvent.
//
// This ensures downstream components (e.g., StateCache) that subscribe to phase-specific
// events like ValidationFailedEvent or TemplateRenderFailedEvent receive proper status updates
// on failure, not just on success.
func (c *Coordinator) handlePipelineFailure(
	err error,
	triggerEvent *events.ReconciliationTriggeredEvent,
	startTime time.Time,
) {
	correlationID := triggerEvent.CorrelationID()
	duration := time.Since(startTime).Milliseconds()

	c.logger.Error("Pipeline execution failed",
		"error", err,
		"correlation_id", correlationID,
		"duration_ms", duration)

	// Extract phase from structured PipelineError using errors.As
	phase := "render" // Default to render for unexpected errors
	var pipelineErr *pipeline.PipelineError
	if errors.As(err, &pipelineErr) {
		phase = string(pipelineErr.Phase)
	}

	// Publish phase-specific failure event before the general ReconciliationFailedEvent.
	// This mirrors handlePipelineSuccess which publishes TemplateRenderedEvent and
	// ValidationCompletedEvent before ReconciliationCompletedEvent.
	switch phase {
	case string(pipeline.PhaseValidation):
		c.eventBus.Publish(events.NewValidationFailedEvent(
			[]string{err.Error()},
			duration,
			triggerEvent.Reason,
			events.PropagateCorrelation(triggerEvent),
		))
	default:
		c.eventBus.Publish(events.NewTemplateRenderFailedEvent(
			"", // No specific template name available from pipeline
			err.Error(),
			"", // No stack trace available from pipeline error
			events.PropagateCorrelation(triggerEvent),
		))
	}

	c.eventBus.Publish(events.NewReconciliationFailedEvent(
		err.Error(),
		phase,
	))
}

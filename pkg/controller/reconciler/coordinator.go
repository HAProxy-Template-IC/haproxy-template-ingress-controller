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
	"slices"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// PipelineExecutor defines the interface for executing the render-validate pipeline.
// This allows mocking in tests.
type PipelineExecutor interface {
	Execute(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*pipeline.PipelineResult, error)
}

// CurrentFilesAuthority binds accepted auxiliary output to a leader term.
type CurrentFilesAuthority interface {
	BeginTerm() uint64
	EndTerm(generation uint64)
	Snapshot(generation uint64) (map[string]string, error)
	Accept(generation uint64, auxiliaryFiles *dataplane.AuxiliaryFiles)
}

const (
	// CoordinatorComponentName is the unique identifier for the ReconciliationCoordinator.
	CoordinatorComponentName = "reconciliation-coordinator"

	// CoordinatorEventBufferSize is the size of the event subscription buffer.
	// ReconciliationTriggeredEvents are tiny (a reason string + correlation),
	// so a large buffer is cheap, and it must be large: under churn the
	// Reconciler fires one trigger per resource change and a StandardSubscriber-
	// Buffer (50) overflows, dropping triggers (and thus renders). The Start
	// loop drains this buffer to a single trigger per render (coalesceQueuedTriggers),
	// so it only ever needs to hold the triggers that arrive during one
	// render+validate cycle.
	CoordinatorEventBufferSize = busevents.DebugSubscriberBuffer
)

// Coordinator orchestrates reconciliation by calling the Pipeline directly.
//
// Render and validate run synchronously inside Pipeline.Execute() (ADR-0001 —
// no event hop), and the Coordinator publishes the appropriate events for
// downstream consumers based on the result.
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
	*component.ReadySignal

	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	pipeline      PipelineExecutor
	storeProvider stores.StoreProvider
	currentFiles  CurrentFilesAuthority
	logger        *slog.Logger

	// lastStatusPatches caches the most recent successful render's status patches.
	// Used by StatusApplier (via events) to apply failure variants (renderFailed,
	// deployFailed) when a subsequent pipeline execution fails.
	lastStatusPatches []templating.StatusPatch
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

	// CurrentFiles owns the last accepted auxiliary output for each leader term.
	CurrentFiles CurrentFilesAuthority

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
		ReadySignal:   component.NewReadySignal(),
		eventBus:      cfg.EventBus,
		pipeline:      cfg.Pipeline,
		storeProvider: cfg.StoreProvider,
		currentFiles:  cfg.CurrentFiles,
		logger:        logger.With("component", CoordinatorComponentName),
	}
}

// Name returns the unique identifier for this component.
func (c *Coordinator) Name() string {
	return CoordinatorComponentName
}

// Start begins the coordinator's event loop.
//
// This method blocks until the context is cancelled.
//
// NOTE: deliberately NOT converted to embed component.Base. Base subscribes
// at construction, but the coordinator is a leader-only component whose
// input (ReconciliationTriggeredEvent) is published by the all-replica
// Reconciler on EVERY replica — a constructor-time subscription would have
// follower replicas fill the buffer and log critical drops continuously.
// Subscribing here, on leadership, keeps followers unsubscribed entirely.
func (c *Coordinator) Start(ctx context.Context) error {
	var generation uint64
	if c.currentFiles != nil {
		generation = c.currentFiles.BeginTerm()
		defer c.currentFiles.EndTerm(generation)
	}

	// Subscribe when starting (after leadership acquired).
	// Use SubscribeTypesLeaderOnly() to suppress late subscription warning.
	// All-replica components replay their cached state on BecameLeaderEvent.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(
		CoordinatorComponentName,
		CoordinatorEventBufferSize,
		events.EventTypeReconciliationTriggered,
	)
	// Unsubscribe on loop exit: without this, every leadership
	// re-acquisition on the same instance would stack another subscription
	// whose orphaned channel fills up and logs critical drops forever.
	defer c.eventBus.UnsubscribeTyped(c.eventChan)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	c.MarkReady()

	c.logger.Debug("Reconciliation coordinator starting")

	for {
		select {
		case event := <-c.eventChan:
			if triggered, ok := event.(*events.ReconciliationTriggeredEvent); ok {
				triggered = c.coalesceQueuedTriggers(triggered)
				c.handleReconciliationTriggered(ctx, triggered, generation)
			}

		case <-ctx.Done():
			c.logger.Info("Reconciliation coordinator shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// coalesceQueuedTriggers drains any reconciliation triggers already queued
//
// NOTE: deliberately NOT pkg/controller/coalesce.DrainLatest and not
// component.Base's mailbox — those preserve per-event dispatch with
// arrival ordering, while this merges the whole drained run into ONE
// re-render (correct here because a render always reads current store
// state, so intermediate triggers carry no information of their own).
// behind `first` and returns a single representative to render. A render reads
// the LATEST store state, so ONE render after draining N triggers is equivalent
// to N serial renders — but it collapses a churn burst into O(1) renders
// instead of O(N). This bounds the render rate and, with it, the downstream
// template.rendered / reconciliation.completed event volume: without it, a
// conformance-scale burst floods the status-applier and resource-applier
// subscriber buffers, their (coalescible) events get dropped, and a dropped
// deployment.completed leaves a Gateway's Programmed=True unapplied for tens of
// seconds (the Programmed-lag stall). Draining is non-blocking, so it never
// waits: it stops the instant the queue is empty.
//
// If the first trigger or any drained trigger is non-coalescible, the returned
// trigger is non-coalescible too, so the downstream deploy scheduler does not
// skip the resulting deployment.
func (c *Coordinator) coalesceQueuedTriggers(first *events.ReconciliationTriggeredEvent) *events.ReconciliationTriggeredEvent {
	latest := first
	var forced *events.ReconciliationTriggeredEvent // first non-coalescible seen, if any
	if !first.Coalescible() {
		forced = first
	}
	drained := 0
	for {
		select {
		case ev := <-c.eventChan:
			t, ok := ev.(*events.ReconciliationTriggeredEvent)
			if !ok {
				continue
			}
			drained++
			latest = t
			if forced == nil && !t.Coalescible() {
				forced = t
			}
		default:
			if drained > 0 {
				c.logger.Debug("coalesced queued reconciliation triggers", "drained", drained)
			}
			if forced != nil {
				return forced
			}
			return latest
		}
	}
}

// handleReconciliationTriggered orchestrates a reconciliation cycle.
func (c *Coordinator) handleReconciliationTriggered(ctx context.Context, event *events.ReconciliationTriggeredEvent, generation uint64) {
	if context.Cause(ctx) != nil {
		return
	}
	startTime := time.Now()
	correlationID := event.CorrelationID()

	c.logger.Debug("Reconciliation triggered",
		"reason", event.Reason,
		"correlation_id", correlationID)

	// Publish reconciliation started event, propagating the correlation ID so
	// downstream components (e.g. metrics) can correlate it with the trigger.
	c.eventBus.Publish(events.NewReconciliationStartedEvent(event.Reason, events.PropagateCorrelation(event)))

	var renderOpts []rendercontext.Option
	if c.currentFiles != nil {
		currentFiles, err := c.currentFiles.Snapshot(generation)
		if err != nil {
			c.handlePipelineFailure(ctx, &pipeline.PipelineError{Phase: pipeline.PhaseRender, Cause: err}, event, startTime)
			return
		}
		renderOpts = append(renderOpts, rendercontext.WithCurrentAuxFiles(currentFiles))
	}
	result, err := c.pipeline.Execute(ctx, c.storeProvider, rendercontext.RenderModeReconcile, renderOpts...)
	if cause := context.Cause(ctx); cause != nil {
		c.logger.Debug("Discarding reconciliation result after authority expired",
			"cause", cause,
			"correlation_id", correlationID)
		return
	}
	if err != nil {
		c.handlePipelineFailure(ctx, err, event, startTime)
		return
	}
	if c.currentFiles != nil {
		c.currentFiles.Accept(generation, result.AuxiliaryFiles)
	}

	// Pipeline succeeded - publish events for downstream components
	c.handlePipelineSuccess(ctx, result, event, startTime)
}

// handlePipelineSuccess publishes events for successful render+validate.
func (c *Coordinator) handlePipelineSuccess(
	ctx context.Context,
	result *pipeline.PipelineResult,
	triggerEvent *events.ReconciliationTriggeredEvent,
	startTime time.Time,
) {
	if context.Cause(ctx) != nil {
		return
	}
	coalescible := triggerEvent.Coalescible()

	// Cache status patches for failure variant application.
	// If a subsequent render fails, StatusApplier can apply renderFailed variants
	// using the most recent successful patches.
	c.lastStatusPatches = result.StatusPatches

	// Publish TemplateRenderedEvent for DeploymentScheduler
	// Config uses relative paths that work everywhere with `default-path origin`
	templateEvent := events.NewTemplateRenderedEvent(
		result.HAProxyConfig,
		result.AuxiliaryFiles,
		result.StatusPatches,
		result.RenderedResources,
		result.AuxFileCount,
		result.RenderDurationMs,
		triggerEvent.Reason,
		result.ContentChecksum,
		coalescible,
		events.PropagateCorrelation(triggerEvent),
	)
	if context.Cause(ctx) != nil {
		return
	}
	c.eventBus.Publish(templateEvent)

	// Publish ValidationCompletedEvent to trigger deployment scheduling
	// Pass ParsedConfig from pipeline result to enable downstream sync optimization
	validationEvent := events.NewValidationCompletedEvent(
		result.ValidationWarnings,
		result.ValidateDurationMs,
		triggerEvent.Reason,
		result.ParsedConfig,
		coalescible,
		events.PropagateCorrelation(templateEvent),
	)
	if context.Cause(ctx) != nil {
		return
	}
	c.eventBus.Publish(validationEvent)

	// Publish ReconciliationCompletedEvent carrying the rendered resources so
	// ResourceApplier reads them directly from the event payload (stateless
	// on the success path, same pattern as StatusApplier + status patches).
	totalDuration := time.Since(startTime).Milliseconds()
	completed := events.NewReconciliationCompletedEvent(
		totalDuration,
		result.RenderedResources,
		result.StatusPatches,
		events.PropagateCorrelation(triggerEvent),
	)
	// Carry the render's Events (recordEvent) for the leader-only EventEmitter.
	// Cloned so the published event never aliases the pipeline result; set on
	// the freshly-built local event before Publish (no subscriber holds it yet).
	completed.Events = slices.Clone(result.Events)
	if context.Cause(ctx) != nil {
		return
	}
	c.eventBus.Publish(completed)

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
	ctx context.Context,
	err error,
	triggerEvent *events.ReconciliationTriggeredEvent,
	startTime time.Time,
) {
	if context.Cause(ctx) != nil {
		return
	}
	correlationID := triggerEvent.CorrelationID()
	duration := time.Since(startTime).Milliseconds()

	c.logger.Error("Pipeline execution failed",
		"error", err,
		"correlation_id", correlationID,
		"duration_ms", duration)

	// Extract phase from structured PipelineError
	phase := "render" // Default to render for unexpected errors
	if pipelineErr, ok := errors.AsType[*pipeline.PipelineError](err); ok {
		phase = string(pipelineErr.Phase)
	}

	// Publish phase-specific failure event before the general ReconciliationFailedEvent.
	// This mirrors handlePipelineSuccess which publishes TemplateRenderedEvent and
	// ValidationCompletedEvent before ReconciliationCompletedEvent.
	switch phase {
	case string(pipeline.PhaseValidation):
		if context.Cause(ctx) != nil {
			return
		}
		c.eventBus.Publish(events.NewValidationFailedEvent(
			[]string{err.Error()},
			duration,
			triggerEvent.Reason,
			events.PropagateCorrelation(triggerEvent),
		))
	default:
		if context.Cause(ctx) != nil {
			return
		}
		c.eventBus.Publish(events.NewTemplateRenderFailedEvent(
			"", // No specific template name available from pipeline
			err.Error(),
			"", // No stack trace available from pipeline error
			events.PropagateCorrelation(triggerEvent),
		))
	}

	// Forward the last successful render's patches so StatusApplier can
	// apply the renderFailed / deployFailed variant. May be nil if no
	// successful render has happened yet (early bootstrap failure); the
	// applier skips the apply in that case.
	if context.Cause(ctx) != nil {
		return
	}
	c.eventBus.Publish(events.NewReconciliationFailedEvent(
		err.Error(),
		phase,
		c.lastStatusPatches,
	))
}

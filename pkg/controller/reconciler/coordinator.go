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
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// PipelineExecutor defines the interface for executing the render-validate pipeline.
// This allows mocking in tests.
type PipelineExecutor interface {
	Execute(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*pipeline.PipelineResult, error)
}

// CurrentFilesAuthority binds exact render outputs to a leader term.
type CurrentFilesAuthority interface {
	BeginTerm() uint64
	EndTerm(generation uint64)
	Snapshot(generation uint64) (map[string]string, error)
	AcceptOutput(generation uint64, output *renderoutput.Snapshot) error
	ConfirmOutput(generation uint64, output *renderoutput.Snapshot) error
	RollbackOutput(generation uint64, output *renderoutput.Snapshot) error
}

type exactCurrentFilesAuthority interface {
	ExactSource(generation uint64) (rendercontext.CurrentAuxFilesSource, error)
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
	CoordinatorEventBufferSize = busevents.ResourceChurnSubscriberBuffer
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
//  4. If success: Publish TemplateRenderedEvent
//  5. If failure: Publish ReconciliationFailedEvent
//
// The DeploymentScheduler still operates event-driven, receiving
// TemplateRenderedEvent and the render gate's RenderGateCompletedEvent to
// schedule deployments.
type Coordinator struct {
	*component.ReadySignal

	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	pipeline      PipelineExecutor
	storeProvider stores.StoreProvider
	currentFiles  CurrentFilesAuthority
	metrics       *metrics.Metrics
	logger        *slog.Logger

	// lastStatusPatches caches the most recent successful render's status patches.
	// Used by StatusApplier (via events) to apply failure variants (renderFailed,
	// deployFailed) when a subsequent pipeline execution fails.
	lastStatusPatches       []templating.StatusPatch
	lastStatusPatchSnapshot *templating.StatusPatchSnapshot
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

	// Metrics receives one render count per reconcile; optional.
	Metrics *metrics.Metrics

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
		metrics:       cfg.Metrics,
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
	defer c.Rearm()
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(
		CoordinatorComponentName,
		CoordinatorEventBufferSize,
		events.EventTypeReconciliationTriggered,
		events.EventTypeRenderGateCompleted,
	)
	// Unsubscribe on loop exit: without this, every leadership
	// re-acquisition on the same instance would stack another subscription
	// whose orphaned channel fills up and logs critical drops forever.
	defer c.eventBus.UnsubscribeTyped(c.eventChan)

	// Signal that subscription is complete for SubscriptionReadySignaler interface.
	c.MarkReady()

	c.logger.Debug("Reconciliation coordinator starting")

	var pending busevents.Event
	for {
		var event busevents.Event
		if pending != nil {
			select {
			case <-ctx.Done():
				c.logger.Info("Reconciliation coordinator shutting down", "reason", ctx.Err())
				return nil
			default:
			}
			event = pending
			pending = nil
		} else {
			select {
			case event = <-c.eventChan:
			case <-ctx.Done():
				c.logger.Info("Reconciliation coordinator shutting down", "reason", ctx.Err())
				return nil
			}
		}

		switch e := event.(type) {
		case *events.ReconciliationTriggeredEvent:
			var boundary busevents.Event
			e, boundary = c.coalesceQueuedTriggers(e)
			pending = boundary
			c.handleReconciliationTriggered(ctx, e, generation)
		case *events.RenderGateCompletedEvent:
			c.settleCurrentFiles(generation, e)
		}
	}
}

// settleCurrentFiles moves the term's auxiliary baseline with the render gate's
// verdict: a pass makes the render's files the baseline the next render reads
// back, a refusal restores the ones HAProxy last accepted. Verdicts for
// superseded plans judge a render the baseline has moved past.
func (c *Coordinator) settleCurrentFiles(generation uint64, event *events.RenderGateCompletedEvent) {
	if c.currentFiles == nil || !event.Newest {
		return
	}
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		c.logger.Error("currentFiles could not authenticate render occurrence", "error", err)
		return
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		c.logger.Error("currentFiles could not authenticate render occurrence", "error", err)
		return
	}
	outputSnapshot, err := cycle.OutputSnapshot()
	if err != nil {
		c.logger.Error("currentFiles could not read render output", "error", err)
		return
	}
	switch {
	case event.OK:
		err = c.currentFiles.ConfirmOutput(generation, outputSnapshot)
	case event.Refused:
		err = c.currentFiles.RollbackOutput(generation, outputSnapshot)
	}
	if err != nil {
		c.logger.Error("currentFiles could not settle render output", "error", err)
	}
}

// coalesceQueuedTriggers collapses one uninterrupted trigger run and returns
// the first different event as the event loop's ordering boundary.
func (c *Coordinator) coalesceQueuedTriggers(
	first *events.ReconciliationTriggeredEvent,
) (*events.ReconciliationTriggeredEvent, busevents.Event) {
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
				return c.finishTriggerCoalescing(latest, forced, drained, ev)
			}
			drained++
			latest = t
			if forced == nil && !t.Coalescible() {
				forced = t
			}
		default:
			return c.finishTriggerCoalescing(latest, forced, drained, nil)
		}
	}
}

func (c *Coordinator) finishTriggerCoalescing(
	latest *events.ReconciliationTriggeredEvent,
	forced *events.ReconciliationTriggeredEvent,
	drained int,
	boundary busevents.Event,
) (*events.ReconciliationTriggeredEvent, busevents.Event) {
	if drained > 0 {
		c.logger.Debug("coalesced queued reconciliation triggers", "drained", drained)
	}
	if forced != nil {
		return forced, boundary
	}
	return latest, boundary
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
		if exact, ok := c.currentFiles.(exactCurrentFilesAuthority); ok {
			source, err := exact.ExactSource(generation)
			if err != nil {
				c.handlePipelineFailure(ctx, &pipeline.PipelineError{Phase: pipeline.PhaseRender, Cause: err}, event, startTime)
				return
			}
			renderOpts = append(renderOpts, rendercontext.WithCurrentAuxFilesSource(source))
		} else {
			currentFiles, err := c.currentFiles.Snapshot(generation)
			if err != nil {
				c.handlePipelineFailure(ctx, &pipeline.PipelineError{Phase: pipeline.PhaseRender, Cause: err}, event, startTime)
				return
			}
			renderOpts = append(renderOpts, rendercontext.WithCurrentAuxFiles(currentFiles))
		}
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
	if result == nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: errors.New("pipeline returned no result"),
		}, event, startTime)
		return
	}
	if err := c.acceptCurrentFiles(generation, result); err != nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: err,
		}, event, startTime)
		return
	}

	// Pipeline succeeded - publish events for downstream components
	c.handlePipelineSuccess(ctx, result, event, startTime)
}

func (c *Coordinator) acceptCurrentFiles(generation uint64, result *pipeline.PipelineResult) error {
	if result == nil || result.CycleSnapshot == nil {
		return errors.New("pipeline returned no authenticated render cycle")
	}
	outputSnapshot, err := result.CycleSnapshot.OutputSnapshot()
	if err != nil {
		return fmt.Errorf("reading render cycle output: %w", err)
	}
	if c.currentFiles == nil {
		return nil
	}
	if err := c.currentFiles.AcceptOutput(generation, outputSnapshot); err != nil {
		return fmt.Errorf("accepting render output: %w", err)
	}
	return nil
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
	if result.CycleSnapshot == nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: errors.New("pipeline returned no authenticated render cycle"),
		}, triggerEvent, startTime)
		return
	}
	coalescible := triggerEvent.Coalescible()

	statusSnapshot, err := result.CycleSnapshot.StatusPatchSnapshot()
	if err != nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: fmt.Errorf("reading render cycle status patches: %w", err),
		}, triggerEvent, startTime)
		return
	}
	c.lastStatusPatches = nil
	c.lastStatusPatchSnapshot = statusSnapshot

	templateEvent, err := events.NewTemplateRenderedEventWithCycle(
		result.CycleSnapshot, result.RenderDurationMs, triggerEvent.Reason,
		coalescible, events.PropagateCorrelation(triggerEvent),
	)
	if err != nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: fmt.Errorf("building rendered cycle event: %w", err),
		}, triggerEvent, startTime)
		return
	}
	occurrence, err := templateEvent.RenderOccurrence()
	if err != nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: fmt.Errorf("reading rendered occurrence: %w", err),
		}, triggerEvent, startTime)
		return
	}

	totalDuration := time.Since(startTime).Milliseconds()
	completed, err := events.NewReconciliationCompletedEventWithCycle(
		totalDuration, occurrence, events.PropagateCorrelation(triggerEvent),
	)
	if err != nil {
		c.handlePipelineFailure(ctx, &pipeline.PipelineError{
			Phase: pipeline.PhaseRender,
			Cause: fmt.Errorf("building reconciliation cycle event: %w", err),
		}, triggerEvent, startTime)
		return
	}
	if context.Cause(ctx) != nil {
		return
	}
	c.eventBus.Publish(templateEvent)

	// No validation event follows: TemplateRenderedEvent is the deploy trigger
	// now, and HAProxy's verdict arrives asynchronously from the render gate.
	for _, warning := range result.ValidationWarnings {
		c.logger.Warn("Rendered output validator warning",
			"warning", warning, "correlation_id", triggerEvent.CorrelationID())
	}
	c.eventBus.Publish(completed)

	if c.metrics != nil {
		c.metrics.RecordRender(result.CacheState)
	}
	c.logger.Debug("Reconciliation completed",
		"correlation_id", triggerEvent.CorrelationID(),
		"render_ms", result.RenderDurationMs,
		"validate_ms", result.ValidateDurationMs,
		"total_ms", totalDuration,
		"cache_state", result.CacheState,
		"cache_build_ms", result.CacheBuildMs)
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
	// This mirrors handlePipelineSuccess which publishes TemplateRenderedEvent
	// before ReconciliationCompletedEvent.
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
	if c.lastStatusPatchSnapshot != nil {
		c.eventBus.Publish(events.NewReconciliationFailedEventWithStatusSnapshot(
			err.Error(), phase, c.lastStatusPatchSnapshot,
		))
	} else {
		c.eventBus.Publish(events.NewReconciliationFailedEvent(
			err.Error(), phase, c.lastStatusPatches,
		))
	}
}

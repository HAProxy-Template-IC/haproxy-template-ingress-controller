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

// Package reconciler implements the Reconciler component that triggers
// reconciliation events on resource changes.
//
// The Reconciler is a Stage 5 controller component. It subscribes to resource
// change events and publishes a ReconciliationTriggeredEvent IMMEDIATELY for
// each one — there is no reconciler-level refractory/debounce. Batching of
// rapid changes happens upstream, per watched-resource kind (the
// pkg/k8s/watcher leading-edge debouncer; default 2s, EndpointSlice "0"), and
// reload throttling happens downstream (the deployer's minDeploymentInterval,
// which the runtime-eligible fast path bypasses). Keeping the reconciler
// immediate means a runtime-eligible endpoint change reaches the deployer with
// no latency added by this layer.
package reconciler

import (
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

// EventBufferSize is the size of the event subscription buffer.
// High-volume component that receives resource change events from every
// configured watcher, sized to handle bursts when many resources change
// simultaneously.
const EventBufferSize = busevents.HighVolumeSubscriberBuffer

// ComponentName is the unique identifier for this component.
const ComponentName = "reconciler"

// Trigger reasons published on ReconciliationTriggeredEvent. The two state
// updates (reasonResourceChange, reasonHTTPResourceChange) are coalescible; the
// command reasons are always processed. Drift uses
// events.TriggerReasonDriftPrevention.
const (
	reasonResourceChange       = "resource_change"
	reasonHTTPResourceChange   = "http_resource_change"
	reasonIndexSynchronized    = "index_synchronized"
	reasonHTTPResourceAccepted = "http_resource_accepted"
	reasonBecameLeader         = "became_leader"
)

// Reconciler triggers reconciliation immediately on every resource/HTTP change.
//
// There is NO reconciler-level debounce. Rapid changes are batched upstream by
// the per-watcher debouncer (pkg/k8s/watcher), and reload-inducing structural
// deploys are throttled downstream by the deployer's minDeploymentInterval
// (which the runtime-eligible fast path skips). Firing immediately here ensures
// runtime-eligible endpoint changes (e.g. EndpointSlice watchers with
// debounceInterval: "0") propagate with no added latency. All trigger paths
// fire immediately:
//   - ResourceIndexUpdatedEvent → "resource_change"
//   - HTTPResourceUpdatedEvent  → "http_resource_change"
//   - IndexSynchronizedEvent    → "index_synchronized" (initial reconciliation)
//   - HTTPResourceAcceptedEvent / DriftPreventionTriggeredEvent / BecameLeaderEvent → immediate command triggers
//
// The component publishes ReconciliationTriggeredEvent to signal the Coordinator
// to begin a reconciliation cycle. Coalescible state-update triggers
// (resource_change, http_resource_change) may be superseded downstream by a
// newer trigger; command triggers are always processed.
type Reconciler struct {
	*component.Base

	// Health check: stall detection for event-driven component
	healthTracker *lifecycle.HealthTracker
}

// New creates a new Reconciler component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing triggers
//   - logger: Structured logger for component logging
//
// Returns:
//   - A new Reconciler instance ready to be started
func New(eventBus *busevents.EventBus, logger *slog.Logger) *Reconciler {
	r := &Reconciler{
		healthTracker: lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
	}
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps.
	// Use typed subscription to only receive events we handle (reduces buffer pressure).
	r.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    r,
		EventTypes: []string{
			events.EventTypeResourceIndexUpdated,
			events.EventTypeIndexSynchronized,
			events.EventTypeHTTPResourceUpdated,
			events.EventTypeHTTPResourceAccepted,
			events.EventTypeDriftPreventionTriggered,
			events.EventTypeBecameLeader,
		},
	})
	return r
}

// HandleEvent implements component.EventHandler: it dispatches events to
// their handlers, tracking processing time for the health check. Each
// handler triggers reconciliation immediately — there is no reconciler-level
// debounce.
func (r *Reconciler) HandleEvent(event busevents.Event) {
	// Track processing for health check stall detection
	r.healthTracker.StartProcessing()
	defer r.healthTracker.EndProcessing()

	switch e := event.(type) {
	case *events.ResourceIndexUpdatedEvent:
		r.handleResourceChange(e)

	case *events.IndexSynchronizedEvent:
		r.handleIndexSynchronized(e)

	case *events.HTTPResourceUpdatedEvent:
		r.handleHTTPResourceChange(e)

	case *events.HTTPResourceAcceptedEvent:
		r.handleHTTPResourceAccepted(e)

	case *events.DriftPreventionTriggeredEvent:
		r.handleDriftPrevention(e)

	case *events.BecameLeaderEvent:
		r.handleBecameLeader(e)
	}
}

// handleResourceChange triggers reconciliation immediately for a resource change.
//
// Initial-sync events are skipped (the first reconciliation is driven by
// IndexSynchronizedEvent once all watchers have synced). HAProxy pod changes
// are skipped — they are deployment targets, not configuration sources, and are
// handled by the Deployer via HAProxyPodsDiscoveredEvent.
func (r *Reconciler) handleResourceChange(event *events.ResourceIndexUpdatedEvent) {
	// Skip initial sync events - we don't want to trigger reconciliation
	// until the initial sync is complete.
	if event.ChangeStats.IsInitialSync {
		r.Logger().Debug("Skipping initial sync event",
			"resource_type", event.ResourceTypeName,
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted)
		return
	}

	// Skip HAProxy pod changes - they are deployment targets, not configuration sources.
	// Pod changes trigger deployment via HAProxyPodsDiscoveredEvent → Deployer component.
	if event.ResourceTypeName == names.HAProxyPodsResourceType {
		r.Logger().Debug("Skipping HAProxy pod change (deployment target, not config source)",
			"created", event.ChangeStats.Created,
			"modified", event.ChangeStats.Modified,
			"deleted", event.ChangeStats.Deleted)
		return
	}

	r.Logger().Debug("Resource change detected, triggering reconciliation",
		"resource_type", event.ResourceTypeName,
		"created", event.ChangeStats.Created,
		"modified", event.ChangeStats.Modified,
		"deleted", event.ChangeStats.Deleted)
	r.triggerReconciliation(reasonResourceChange)
}

// handleIndexSynchronized processes index synchronized events.
//
// When all resource watchers have completed their initial sync, this triggers
// the initial reconciliation so the first render happens with a complete view
// of cluster state.
func (r *Reconciler) handleIndexSynchronized(event *events.IndexSynchronizedEvent) {
	r.Logger().Info("All indices synchronized, triggering initial reconciliation",
		"resource_counts", event.ResourceCounts)
	r.triggerReconciliation(reasonIndexSynchronized)
}

// handleHTTPResourceChange triggers reconciliation immediately for an HTTP content change.
//
// When external HTTP content changes (e.g. IP blocklists, API responses), this
// triggers a re-render to incorporate the new content.
func (r *Reconciler) handleHTTPResourceChange(event *events.HTTPResourceUpdatedEvent) {
	r.Logger().Debug("HTTP resource change detected, triggering reconciliation",
		"url", event.URL,
		"content_size", event.ContentSize)
	r.triggerReconciliation(reasonHTTPResourceChange)
}

// handleHTTPResourceAccepted processes HTTP resource accepted events.
//
// When HTTP content is promoted from pending to accepted (after validation
// succeeds), trigger reconciliation to re-render the production configuration
// with the new accepted content.
func (r *Reconciler) handleHTTPResourceAccepted(event *events.HTTPResourceAcceptedEvent) {
	r.Logger().Debug("HTTP resource accepted, triggering immediate reconciliation",
		"url", event.URL,
		"content_size", event.ContentSize)
	r.triggerReconciliation(reasonHTTPResourceAccepted)
}

// handleDriftPrevention processes drift prevention triggered events.
//
// Drift prevention triggers immediate full reconciliation (render → validate →
// deploy). This refreshes HTTP-store LastAccessTime during rendering, preventing
// premature eviction. If validation fails, the DeploymentScheduler falls back to
// the cached last known good config.
func (r *Reconciler) handleDriftPrevention(_ *events.DriftPreventionTriggeredEvent) {
	// The TriggerReason propagates through the event chain so the
	// DeploymentScheduler can deploy cached config if validation fails.
	r.triggerReconciliation(events.TriggerReasonDriftPrevention)
}

// handleBecameLeader triggers immediate reconciliation when leadership is acquired.
//
// This bootstraps leader-only components (renderer, drift monitor) with fresh
// state — the new leader's first reconciliation produces a current render.
func (r *Reconciler) handleBecameLeader(_ *events.BecameLeaderEvent) {
	r.Logger().Info("Became leader, triggering immediate reconciliation")
	r.triggerReconciliation(reasonBecameLeader)
}

// triggerReconciliation publishes a ReconciliationTriggeredEvent with a new correlation ID.
//
// The correlation ID is generated here and propagated through the entire
// reconciliation pipeline (Coordinator → Pipeline → Scheduler → Deployer),
// enabling end-to-end tracing of a single reconciliation cycle.
//
// The coalescible flag is set from the trigger reason:
//   - true for state updates (resource_change, http_resource_change) that may be
//     safely superseded by a newer trigger of the same kind
//   - false for commands that must each be processed (index_synchronized,
//     http_resource_accepted, drift_prevention, became_leader)
func (r *Reconciler) triggerReconciliation(reason string) {
	coalescible := isCoalescibleReason(reason)

	// Create event with new correlation ID to trace this reconciliation cycle.
	event := events.NewReconciliationTriggeredEvent(reason, coalescible, events.WithNewCorrelation())

	r.Logger().Debug("Triggering reconciliation",
		"reason", reason,
		"coalescible", coalescible,
		"correlation_id", event.CorrelationID())

	r.EventBus().Publish(event)
}

// isCoalescibleReason determines if a trigger reason produces coalescible events.
// Coalescible events can be safely skipped when a newer event of the same type
// is available downstream.
//
// State updates (coalescible=true):
//   - resource_change: a watched resource changed
//   - http_resource_change: HTTP content changed
//
// Commands (coalescible=false):
//   - index_synchronized: initial sync complete - must process
//   - http_resource_accepted: HTTP content promoted - must deploy
//   - drift_prevention: drift prevention cycle - must enforce
//   - became_leader: leadership acquired - must initialize
func isCoalescibleReason(reason string) bool {
	switch reason {
	case reasonResourceChange, reasonHTTPResourceChange:
		return true
	default:
		return false
	}
}

// HealthCheck implements the lifecycle.HealthChecker interface.
// Returns an error if the component appears to be stalled (processing for > timeout).
// Returns nil when idle (not processing) - idle is always healthy for event-driven components.
func (r *Reconciler) HealthCheck() error {
	return r.healthTracker.Check()
}

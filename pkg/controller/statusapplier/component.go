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

// Package statusapplier applies template-driven status patches to Kubernetes resources.
//
// The StatusApplier is a stateless consumer: successful lifecycle events carry
// a sealed render occurrence, and the applier reads its authenticated status
// snapshot. There is no side-channel cache or mutable-shadow fallback.
//
// Status patches are fully defined by templates — the controller never hardcodes
// knowledge of specific resource types or condition names. Templates register patches
// via the statusPatch() template function during rendering, including outcome-keyed
// variants for each pipeline phase (rendered, deployed, renderFailed, deployFailed).
//
// Event mapping:
//
//   - ResourcesAppliedEvent: apply the "rendered" variant from the sealed
//     occurrence after the same render's resources were applied. Ordering matters: conditions like
//     Accepted=True must not precede the infrastructure resources they
//     describe, so the rendered variant rides the post-apply event rather
//     than TemplateRenderedEvent.
//   - DeploymentCompletedEvent: apply the "deployed" variant from the sealed
//     occurrence that deployment carried. Programmed=True
//     genuinely means "HAProxy is serving this config" because reload
//     verification gates DeploymentCompletedEvent.
//   - DeploymentSkippedEvent: apply the "deployed" variant from the sealed
//     occurrence. Same data-plane-is-converged semantics as
//     DeploymentCompletedEvent, reached by the scheduler determining the
//     data plane is already at this config. Without this branch, any
//     resource whose addition or update produces no config change (a
//     status-only delta) would stay at the CRD-default condition state
//     indefinitely.
//   - ReconciliationFailedEvent: apply the failure variant ("renderFailed" /
//     "deployFailed") from event.StatusPatches. The Coordinator forwards
//     the patches from the last successful render — failure paths don't
//     produce fresh patches, so a "last good" snapshot is the only thing
//     the chart's failure variants can be applied against.
//
// Leader transitions: the Reconciler triggers an immediate reconciliation on
// BecameLeaderEvent (per pkg/controller/reconciler/CLAUDE.md), producing a
// fresh render whose patches arrive via ResourcesAppliedEvent. The applier
// therefore has no replay responsibility on leadership change.
package statusapplier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	k8stypes "gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "status-applier"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate volume: receives template rendered, reconciliation completed/failed,
	// and leadership events.
	// High volume: template.rendered fires on every reconcile. Even with the
	// coordinator coalescing renders, an occasional slow SSA apply can briefly
	// back this up, so use a Publishing-tier buffer to avoid dropping the
	// (coalescible) template.rendered / deployment.completed events — a dropped
	// deployment.completed leaves Programmed=True unapplied until the next deploy.
	EventBufferSize = busevents.PublishingSubscriberBuffer

	// fieldManagerPrefix is the SSA field manager prefix for status patches.
	// The full manager name is suffixed with the phase (e.g. "haptic-rendered",
	// "haptic-deployed", "haptic-deployFailed") so each phase owns a disjoint
	// set of conditions. Server-Side Apply's listType=map semantics relinquish
	// ownership of any list entry not present in the most recent apply by the
	// same manager — so reusing one manager across phases requires every apply
	// to enumerate every condition, which forces the rendered phase to claim
	// Programmed=Pending (causing flicker against the deployed phase's
	// Programmed=True). Phase-scoped managers sidestep that entirely.
	fieldManagerPrefix = "haptic"

	statusKey = "status"

	// statusCacheMaxEntries bounds the SSA-skip cache. Sized for
	// ~64k live resources — far above any realistic
	// steady state (the gateway-api conformance suite peaks below 500) —
	// so only pathological create/delete churn over a long leader tenure
	// ever triggers the wholesale reset.
	statusCacheMaxEntries = 65536
	statusApplyLockCount  = 256
)

// GVRResolver resolves apiVersion + kind to a GroupVersionResource.
// This abstracts the REST mapper for testability.
type GVRResolver interface {
	Resolve(apiVersion, kind string) (schema.GroupVersionResource, error)
}

// Component applies template-driven status patches to Kubernetes resources
// via Server-Side Apply (SSA).
//
// This is an all-replica component that subscribes in the constructor and
// applies the appropriate variant based on pipeline lifecycle events. Only the
// leader applies patches to avoid conflicts.
//
// Event flow (success phases read the authenticated occurrence snapshot):
//
//	ResourcesAppliedEvent → apply "rendered" variant (if leader)
//	DeploymentCompletedEvent → apply "deployed" variant (if leader)
//	DeploymentSkippedEvent → apply "deployed" variant (if leader); the data
//	    plane is already at the rendered config so Programmed conditions
//	    should reflect the current generation
//	ReconciliationFailedEvent → apply "renderFailed" or "deployFailed" variant (if leader)
//	BecameLeaderEvent → clear status cache; rely on Reconciler to fire a fresh reconcile
//	LostLeadershipEvent → flip the leader flag off
type Component struct {
	*component.Base

	dynamicClient dynamic.Interface
	gvrResolver   GVRResolver
	healthTracker *lifecycle.HealthTracker

	// ctx is the event-loop context captured by Start. Handlers run only
	// on the loop goroutine and use it for Kubernetes API calls so SSA
	// patches abort on shutdown.
	ctx context.Context

	// mu protects all mutable state below.
	mu       sync.RWMutex
	isLeader bool

	statusCache map[string]statusCacheEntry
	applyLocks  [statusApplyLockCount]sync.Mutex

	// selfWrites receives the resourceVersion of every applied patch (nil-safe).
	selfWrites *k8stypes.SelfWriteRegistry
}

// Config contains configuration for creating a StatusApplier Component.
type Config struct {
	// EventBus is the event bus for subscribing to events and publishing results.
	EventBus *busevents.EventBus

	// DynamicClient is the Kubernetes dynamic client for SSA patch operations.
	DynamicClient dynamic.Interface

	// GVRResolver resolves apiVersion + kind to GroupVersionResource.
	GVRResolver GVRResolver

	// Logger is the structured logger.
	Logger *slog.Logger

	// SelfWrites records the resourceVersion of every applied patch so the
	// watchers can refresh from the echoed event without re-rendering.
	// Optional.
	SelfWrites *k8stypes.SelfWriteRegistry
}

// New creates a new StatusApplier component.
//
// The component subscribes to events in the constructor (all-replica pattern).
// It only applies patches when it is the leader.
func New(cfg *Config) *Component {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	c := &Component{
		dynamicClient: cfg.DynamicClient,
		gvrResolver:   cfg.GVRResolver,
		healthTracker: lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		statusCache:   make(map[string]statusCacheEntry),
		selfWrites:    cfg.SelfWrites,
	}
	// Typed subscription (EventTypes, not a catch-all) — the bus prefilters
	// by event type at publish, so the 50-event buffer holds ONLY events we
	// actually dispatch on. With a catch-all subscription the buffer would
	// fill within seconds during conformance setup (resource.index.updated
	// and reconciliation.* fire at kHz) and overflow — silently dropping
	// deployment.completed events along with the rest. We saw exactly that
	// in CI: the "deployed" status patches never fired, Gateways never got
	// Programmed=True, and conformance Test Setup timed out on every shard.
	c.Base = component.New(&component.Config{
		EventBus:   cfg.EventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeResourcesApplied,
			events.EventTypeDeploymentCompleted,
			events.EventTypeDeploymentSkipped,
			events.EventTypeReconciliationFailed,
			events.EventTypeBecameLeader,
			events.EventTypeLostLeadership,
		},
	})
	return c
}

// CoalescesOn opts this applier into component.Base's mailbox coalescing.
// All three declared types are latest-wins FOR THIS COMPONENT: rendered
// patches ride every ResourcesAppliedEvent, and the deployed variant rides
// every DeploymentCompleted/SkippedEvent — each event carries the FULL
// current patch set, so only the newest of an uninterrupted run matters.
// Collapsing runs keeps the mailbox queue bounded by the deploy cadence
// instead of the render rate: without it a burst of deployment events (each
// costing an SSA fan-out to apply) backlogs the queue and status latency
// grows unboundedly (observed: 512-deep backlog and 90s Programmed lag in
// gateway-api conformance).
func (c *Component) CoalescesOn() []string {
	return []string{
		events.EventTypeResourcesApplied,
		events.EventTypeDeploymentCompleted,
		events.EventTypeDeploymentSkipped,
	}
}

// CoalescesAcrossQueue makes the latest-wins statement hold across the whole
// mailbox, not just within a run: under churn the three types arrive strictly
// alternating (render → resources.applied, deploy → deployment.completed), so
// run-only collapsing never fired and the queue grew to 2048 full patch sets
// (6 GB live heap on a 1,900-route churn). Each event carries the complete
// patch set for its phase, so only the newest of each type matters.
func (c *Component) CoalescesAcrossQueue() bool { return true }

// HealthCheck returns nil if the component is healthy.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

// Start captures the loop context for handlers and runs the embedded
// component.Base event loop until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.ctx = ctx
	return c.Base.Start(ctx)
}

// HandleEvent implements component.EventHandler: it routes events to the
// appropriate handler, tracking processing time for the health check.
func (c *Component) HandleEvent(event busevents.Event) {
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	ctx := c.ctx
	switch e := event.(type) {
	case *events.ResourcesAppliedEvent:
		c.handleResourcesApplied(ctx, e)

	case *events.DeploymentCompletedEvent:
		c.handleDeploymentCompleted(ctx, e)

	case *events.DeploymentSkippedEvent:
		c.handleDeploymentSkipped(ctx, e)

	case *events.ReconciliationFailedEvent:
		c.handleReconciliationFailed(ctx, e)

	case *events.BecameLeaderEvent:
		c.handleBecameLeader(ctx)

	case *events.LostLeadershipEvent:
		c.handleLostLeadership()
	}
}

// handleResourcesApplied applies the "rendered" variant directly from the
// event payload. Patches are config-level (Accepted/ResolvedRefs); no
// data-plane gate is needed, but the ResourceApplier publishing this event
// guarantees the same render's k8sResources already exist.
func (c *Component) handleResourcesApplied(ctx context.Context, event *events.ResourcesAppliedEvent) {
	if !c.leaderRLocked() {
		return
	}
	c.applyOccurrenceStatusPatchSet(ctx, event, events.StatusPatchPhaseRendered)
}

// handleDeploymentCompleted applies the status variant that matches the deploy
// outcome, from the event payload. The Deployer forwards the patches from the
// DeploymentScheduledEvent that triggered this deploy, so the patches describe
// exactly the config the deploy shipped — no cache, no LATEST-vs-deployed race.
//
// Outcome mapping:
//   - Total == 0: nothing was deployed (no HAProxy pods yet). No-op — not a
//     failure; leave the status where it is.
//   - Succeeded == Total: every instance runs the config, so "deployed"
//     (Programmed=True) is true of the whole data plane.
//   - Failed > 0: apply "deployFailed". Gateway API defines Programmed as the
//     data plane being configured, and a fleet where one replica still serves
//     the old config is not that — a request round-robined to it gets the old
//     routing (503 SC--, see ingress_rolling_restart_test.go). Per-endpoint detail
//     surfaces independently via InstanceDeploymentFailedEvent.
//   - Otherwise every pod accepted the config behind a paced reload: neither
//     variant applies yet. The Deployer publishes a DeploymentSkippedEvent
//     once the fleet is observed running it.
func (c *Component) handleDeploymentCompleted(ctx context.Context, event *events.DeploymentCompletedEvent) {
	// Zero-endpoint deployment (no HAProxy pods discovered yet) doesn't actually
	// put any HAProxy on the new config — that's "nothing deployed", not a
	// failure, so leave the status where it is.
	if event.Total == 0 {
		return
	}
	if !c.leaderRLocked() {
		return
	}
	switch {
	case event.Succeeded == event.Total:
		c.applyOccurrenceStatusPatchSet(ctx, event, events.StatusPatchPhaseDeployed)
	case event.Failed > 0:
		// Anything short of the whole fleet is not Programmed. Reporting it as
		// deployed advertises an address the fleet does not uniformly serve,
		// which external-dns and cert-manager then act on.
		c.applyOccurrenceStatusPatchSet(ctx, event, events.StatusPatchPhaseDeployFailed)
	}
}

// handleDeploymentSkipped applies the "deployed" variant when the deployer
// determines that the data plane is already at the just-rendered config and
// no deployment was performed. Patches are carried on the event so they
// match the config that the data plane is already serving.
//
// This covers the case where a resource is added/modified but the rendered
// HAProxy config is byte-identical to the last deployed config (a
// status-only delta) — without this,
// the deployed-variant patches would never be applied and the resource
// would stay at the CRD-default condition state indefinitely.
func (c *Component) handleDeploymentSkipped(ctx context.Context, event *events.DeploymentSkippedEvent) {
	if event.Total == 0 {
		return
	}
	if !c.leaderRLocked() {
		return
	}
	c.applyOccurrenceStatusPatchSet(ctx, event, events.StatusPatchPhaseDeployed)
}

// handleReconciliationFailed applies the failure variant based on which phase
// failed. The Coordinator forwards the patches from the most recent successful
// render — failure paths don't produce fresh patches, so a "last good"
// snapshot is the only thing the chart's failure variants can apply against.
// May be nil on early bootstrap failures, in which case the apply is skipped.
//
// Phase mapping (event.Phase → StatusPatchPhase):
//
//	"render"     → StatusPatchPhaseRenderFailed     (templating produced no output)
//	"validation" → StatusPatchPhaseValidateFailed   (rendered, but rejected pre-deploy)
//	anything else (including "deploy" and "") → StatusPatchPhaseDeployFailed
//
// validation gets its own variant rather than collapsing into renderFailed
// or deployFailed: the chart's failure-variant templates can keep emitting
// the same payload for renderFailed and validateFailed until a use case
// motivates differentiation, but the phase label downstream stays accurate.
// See issue #44.
func (c *Component) handleReconciliationFailed(ctx context.Context, event *events.ReconciliationFailedEvent) {
	if !c.leaderRLocked() {
		return
	}
	var phase events.StatusPatchPhase
	switch event.Phase {
	case "render":
		phase = events.StatusPatchPhaseRenderFailed
	case "validation":
		phase = events.StatusPatchPhaseValidateFailed
	default:
		phase = events.StatusPatchPhaseDeployFailed
	}
	c.applyStatusPatchSet(ctx, event.StatusPatches, event.StatusPatchSnapshot, phase)
}

func (c *Component) applyStatusPatchSet(
	ctx context.Context,
	patches []templating.StatusPatch,
	snapshot *templating.StatusPatchSnapshot,
	phase events.StatusPatchPhase,
) {
	if snapshot == nil {
		if len(patches) > 0 {
			c.applyVariant(ctx, patches, phase)
		}
		return
	}
	if len(patches) > 0 {
		c.rejectStatusPatchSnapshot(errors.New("event carries both mutable and immutable status patches"))
		return
	}
	count, err := snapshot.Len()
	if err != nil {
		c.rejectStatusPatchSnapshot(err)
		return
	}
	if count == 0 {
		return
	}
	phasePatches, err := snapshot.PatchesForPhase(string(phase))
	if err != nil {
		c.rejectStatusPatchSnapshot(err)
		return
	}
	c.applyVariant(ctx, phasePatches, phase)
}

type renderOccurrenceEvent interface {
	RenderOccurrence() (*rendercycle.Occurrence, error)
}

func (c *Component) applyOccurrenceStatusPatchSet(
	ctx context.Context,
	event renderOccurrenceEvent,
	phase events.StatusPatchPhase,
) {
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		c.rejectStatusPatchSnapshot(err)
		return
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		c.rejectStatusPatchSnapshot(err)
		return
	}
	cycleSnapshot, err := cycle.StatusPatchSnapshot()
	if err != nil {
		c.rejectStatusPatchSnapshot(err)
		return
	}
	c.applyStatusPatchSet(ctx, nil, cycleSnapshot, phase)
}

func (c *Component) rejectStatusPatchSnapshot(err error) {
	c.Logger().Error("Status patch snapshot is invalid; no status was applied", "error", err)
	c.EventBus().Publish(events.NewStatusUpdateFailedEvent(
		"", "", "status-patch-snapshot", err.Error(), false,
	))
}

// handleBecameLeader flips the leader flag and clears the status apply cache.
// No patches replay — the Reconciler fires an immediate reconciliation on
// BecameLeaderEvent (see pkg/controller/reconciler/CLAUDE.md), which produces
// a fresh TemplateRenderedEvent carrying the patches the new leader needs.
func (c *Component) handleBecameLeader(_ context.Context) {
	c.mu.Lock()
	c.isLeader = true
	c.statusCache = make(map[string]statusCacheEntry)
	c.mu.Unlock()
	c.Logger().Info("Became leader, clearing status apply cache")
}

// leaderRLocked returns the current leader flag under a read lock.
func (c *Component) leaderRLocked() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isLeader
}

// handleLostLeadership clears the leader flag.
func (c *Component) handleLostLeadership() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isLeader {
		c.Logger().Info("Lost leadership, pausing status patch application")
	}

	c.isLeader = false
}

// applyVariant applies the given phase variant from each patch to the target resource.
func (c *Component) applyVariant(ctx context.Context, patches []templating.StatusPatch, phase events.StatusPatchPhase) {
	startTime := time.Now()
	phaseKey := string(phase)

	var applied, skipped atomic.Int64

	// Apply the per-resource SSA patches CONCURRENTLY with bounded parallelism.
	// Serially this loop is O(patches) sequential Kubernetes API round-trips
	// (~tens of ms each on the status subresource). Under a full Gateway API
	// conformance run (~90 Gateways) a single applyVariant call then takes
	// seconds, so the applier falls behind the TemplateRendered/DeploymentCompleted
	// event stream: the deployed-phase (Programmed) status for many Gateways
	// lands minutes late, spuriously failing observedGeneration/Setup-readiness
	// checks (this regressed when gateway-api v1.6 added many more conformance
	// resources, pushing the resource count past what a serial applier keeps up
	// with). Each patch targets a distinct resource, so the SSA calls are
	// independent; running them in parallel cuts wall-clock to ~one round-trip.
	// statusCache is guarded by c.mu; counters are atomic. errgroup never
	// returns an error here (per-patch failures are logged + published, not
	// propagated) so Wait's return is ignored.
	// 64 (not 16): under conformance-grade churn every deployed-variant
	// apply touches 30-60 resources with changed payloads; at 16 the batch
	// costs 4 round-trip waves (~0.6-1.8s wall), which is slower than the
	// deploy cadence (~1/s) — the mailbox queue then grows without bound
	// (observed 512 deep). At 64 the batch is ~one wave and the worker
	// keeps up.
	const maxStatusApplyConcurrency = 64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxStatusApplyConcurrency)

	for i := range patches {
		patch := &patches[i]
		statusPayload, ok := patch.Variants[phaseKey]
		if !ok {
			continue
		}

		g.Go(func() error {
			switch c.applyOnePatch(gctx, patch, statusPayload, phaseKey) {
			case patchApplied:
				applied.Add(1)
			case patchSkipped:
				skipped.Add(1)
			case patchFailed:
				// Logged and published inside applyOnePatch.
			}
			return nil
		})
	}
	_ = g.Wait()

	appliedN := int(applied.Load())
	skippedN := int(skipped.Load())
	durationMs := time.Since(startTime).Milliseconds()

	if appliedN > 0 || skippedN > 0 {
		c.Logger().Debug("Status patches applied",
			"phase", phaseKey,
			"applied", appliedN,
			"skipped", skippedN,
			"duration_ms", durationMs)
	}

	c.EventBus().Publish(events.NewStatusUpdateCompletedEvent(
		phase, appliedN, skippedN, durationMs,
	))
}

// patchOutcome classifies one status-patch apply attempt.
type patchOutcome int

const (
	patchFailed patchOutcome = iota
	patchApplied
	patchSkipped
)

type statusCacheEntry struct {
	uid                   string
	baseResourceVersion   string
	latestResourceVersion string
	lastPhase             string
	lastPayload           []byte
}

// applyOnePatch applies one phase variant, using exact lineage to skip when available.
func (c *Component) applyOnePatch(ctx context.Context, patch *templating.StatusPatch, statusPayload map[string]any, phaseKey string) patchOutcome {
	gvr, err := c.gvrResolver.Resolve(patch.APIVersion, patch.Kind)
	if err != nil {
		c.Logger().Error("Failed to resolve GVR for status patch",
			"api_version", patch.APIVersion,
			"kind", patch.Kind,
			"error", err)
		c.EventBus().Publish(events.NewStatusUpdateFailedEvent(
			patch.Namespace, patch.Name,
			fmt.Sprintf("%s/%s", patch.APIVersion, patch.Kind),
			err.Error(), false,
		))
		return patchFailed
	}

	gvrStr := gvr.String()

	payloadBytes, err := json.Marshal(statusPayload)
	if err != nil {
		c.Logger().Error("Failed to marshal status payload",
			"namespace", patch.Namespace,
			"name", patch.Name,
			"error", err)
		return patchFailed
	}

	cacheKey := fmt.Sprintf("%s/%s/%s", patch.Namespace, patch.Name, gvrStr)
	applyLock := &c.applyLocks[statusApplyLockIndex(cacheKey)]
	applyLock.Lock()
	defer applyLock.Unlock()

	exactLineage, expectedResourceVersion, skip := c.prepareStatusApply(
		cacheKey, patch, phaseKey, payloadBytes,
	)
	if skip {
		return patchSkipped
	}

	ssaBytes, err := encodeStatusApplyPayload(patch, statusPayload, exactLineage, expectedResourceVersion)
	if err != nil {
		c.Logger().Error("Failed to marshal SSA payload",
			"namespace", patch.Namespace,
			"name", patch.Name,
			"error", err)
		return patchFailed
	}

	applied, err := c.applyStatus(ctx, gvr, patch, phaseKey, ssaBytes)
	if err != nil && exactLineage && apierrors.IsConflict(err) {
		applied, err = c.retryStatusAtCurrentResourceVersion(ctx, gvr, patch, phaseKey, statusPayload, err)
	}
	if err != nil {
		// The resource was deleted between render and apply — a benign
		// race that is common under churn (the store snapshot still had
		// it when we rendered, but it has since been deleted, e.g. by a
		// conformance test's per-test cleanup). There is no status to
		// write, so this is NOT a failure. Skip it silently: at volume
		// (hundreds of stale patches per run under heavy churn) logging
		// an error and publishing a StatusUpdateFailedEvent for each
		// would flood the event bus and the commentator/metrics
		// subscribers, degrading the very pipeline whose status we are
		// applying. The next render (with the delete propagated) drops
		// the patch. Do NOT cache the result — a same-name resource
		// recreated with identical status content must still be applied.
		if apierrors.IsNotFound(err) {
			return patchSkipped
		}
		c.Logger().Error("Failed to apply status patch",
			"namespace", patch.Namespace,
			"name", patch.Name,
			"gvr", gvrStr,
			"phase", phaseKey,
			"error", err)
		c.EventBus().Publish(events.NewStatusUpdateFailedEvent(
			patch.Namespace, patch.Name, gvrStr,
			err.Error(), IsRetriable(err),
		))
		return patchFailed
	}

	if applied == nil || applied.GetResourceVersion() == "" ||
		(exactLineage && string(applied.GetUID()) != patch.UID) {
		err := errors.New("status patch response has different or missing object lineage")
		c.Logger().Error("Status patch response has invalid object lineage",
			"namespace", patch.Namespace,
			"name", patch.Name,
			"gvr", gvrStr,
			"phase", phaseKey)
		c.EventBus().Publish(events.NewStatusUpdateFailedEvent(
			patch.Namespace, patch.Name, gvrStr, err.Error(), false,
		))
		return patchFailed
	}
	c.selfWrites.Record(gvr.GroupResource(), patch.Namespace, patch.Name, applied.GetResourceVersion())

	if exactLineage {
		c.cacheStatusApplySuccess(cacheKey, patch, phaseKey, payloadBytes, applied.GetResourceVersion())
	}

	return patchApplied
}

func (c *Component) applyStatus(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	patch *templating.StatusPatch,
	phaseKey string,
	ssaBytes []byte,
) (*unstructured.Unstructured, error) {
	return c.dynamicClient.Resource(gvr).Namespace(patch.Namespace).Patch(
		ctx,
		patch.Name,
		types.ApplyPatchType,
		ssaBytes,
		metav1.PatchOptions{
			FieldManager: fieldManagerPrefix + "-" + phaseKey,
			Force:        new(true),
		},
		statusKey,
	)
}

// retryStatusAtCurrentResourceVersion re-applies once at the object's current
// resourceVersion after a conflict. The render's version goes stale whenever
// something bumps the object without changing what the render reads: the
// controller's own spec apply, an annotation, a field the watcher ignores.
// No render follows such a bump, so without this the status never lands. The
// UID precondition stays: a recreated object gets its status from the next
// render.
func (c *Component) retryStatusAtCurrentResourceVersion(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	patch *templating.StatusPatch,
	phaseKey string,
	statusPayload map[string]any,
	conflict error,
) (*unstructured.Unstructured, error) {
	current, err := c.dynamicClient.Resource(gvr).Namespace(patch.Namespace).Get(ctx, patch.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if string(current.GetUID()) != patch.UID || current.GetResourceVersion() == "" {
		return nil, conflict
	}
	ssaBytes, err := encodeStatusApplyPayload(patch, statusPayload, true, current.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return c.applyStatus(ctx, gvr, patch, phaseKey, ssaBytes)
}

func (c *Component) prepareStatusApply(
	cacheKey string,
	patch *templating.StatusPatch,
	phase string,
	payload []byte,
) (exactLineage bool, expectedResourceVersion string, skip bool) {
	exactLineage = patch.UID != "" && patch.ResourceVersion != ""
	if exactLineage {
		expectedResourceVersion, skip = c.statusApplyDecision(
			cacheKey, patch.UID, patch.ResourceVersion, phase, payload,
		)
		return true, expectedResourceVersion, skip
	}
	c.mu.Lock()
	delete(c.statusCache, cacheKey)
	c.mu.Unlock()
	return false, "", false
}

func encodeStatusApplyPayload(
	patch *templating.StatusPatch,
	statusPayload map[string]any,
	exactLineage bool,
	expectedResourceVersion string,
) ([]byte, error) {
	metadata := map[string]any{"name": patch.Name}
	if patch.Namespace != "" {
		metadata["namespace"] = patch.Namespace
	}
	if exactLineage {
		metadata["uid"] = patch.UID
		metadata["resourceVersion"] = expectedResourceVersion
	}
	return json.Marshal(map[string]any{
		"apiVersion": patch.APIVersion,
		"kind":       patch.Kind,
		"metadata":   metadata,
		statusKey:    statusPayload,
	})
}

func (c *Component) cacheStatusApplySuccess(
	cacheKey string,
	patch *templating.StatusPatch,
	phase string,
	payload []byte,
	appliedResourceVersion string,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.statusCache) >= statusCacheMaxEntries {
		c.Logger().Info("Status apply cache reset after reaching size bound",
			"entries", len(c.statusCache))
		c.statusCache = make(map[string]statusCacheEntry, statusCacheMaxEntries/4)
	}
	c.statusCache[cacheKey] = statusCacheEntry{
		uid:                   patch.UID,
		baseResourceVersion:   patch.ResourceVersion,
		latestResourceVersion: appliedResourceVersion,
		lastPhase:             phase,
		lastPayload:           bytes.Clone(payload),
	}
}

func (c *Component) statusApplyDecision(
	cacheKey, uid, sourceResourceVersion, phase string,
	payload []byte,
) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.statusCache[cacheKey]
	if !exists || entry.uid != uid {
		delete(c.statusCache, cacheKey)
		return sourceResourceVersion, false
	}
	switch sourceResourceVersion {
	case entry.latestResourceVersion:
		entry.baseResourceVersion = sourceResourceVersion
		c.statusCache[cacheKey] = entry
		return sourceResourceVersion, entry.lastPhase == phase && bytes.Equal(entry.lastPayload, payload)
	case entry.baseResourceVersion:
		// The render has not observed the applied version, which stays so
		// for good when the write's echo changed nothing it reads. Any
		// content change re-executes the render with a new source version.
		return entry.latestResourceVersion, entry.lastPhase == phase && bytes.Equal(entry.lastPayload, payload)
	default:
		delete(c.statusCache, cacheKey)
		return sourceResourceVersion, false
	}
}

func statusApplyLockIndex(key string) int {
	var hash uint32 = 2166136261
	for index := range len(key) {
		hash ^= uint32(key[index])
		hash *= 16777619
	}
	return int(hash % statusApplyLockCount)
}

// IsRetriable returns true if the error is likely transient and the operation
// should be retried on the next reconciliation cycle. It is exported so the
// resourceapplier shares this single retry policy rather than duplicating it.
func IsRetriable(err error) bool {
	// Kubernetes API server transient errors
	if apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsInternalError(err) {
		return true
	}

	// Network-level transient errors
	if netErr, ok := errors.AsType[net.Error](err); ok {
		return netErr.Timeout()
	}

	// Context deadline exceeded is transient
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Permanent errors: not found, forbidden, invalid, conflict, etc.
	if apierrors.IsNotFound(err) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsMethodNotSupported(err) {
		return false
	}

	// Default to retriable for unknown errors to avoid silently dropping updates
	return true
}

// RestMapperResolver implements GVRResolver by consulting a Kubernetes
// RESTMapper. The kind→resource mapping comes from the cluster's discovery
// data, so any watched resource — including a CRD with an irregular or fully
// custom plural — resolves correctly, with no hardcoded or guessed
// pluralization (RULE #1: the controller stays resource-agnostic).
type RestMapperResolver struct {
	mapper meta.RESTMapper
}

// NewRestMapperResolver creates a GVRResolver backed by the given RESTMapper.
func NewRestMapperResolver(mapper meta.RESTMapper) *RestMapperResolver {
	return &RestMapperResolver{mapper: mapper}
}

// Resolve maps apiVersion + kind to a GroupVersionResource by consulting the
// RESTMapper. The resource name comes from the cluster's discovery data (and,
// for CRDs, each CRD's own spec.names.plural), so irregular and fully custom
// plurals resolve correctly — there are no resource-specific pluralization
// rules in Go (RULE #1). An unknown kind returns an error rather than a
// guessed plural.
func (r *RestMapperResolver) Resolve(apiVersion, kind string) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}

	gk := schema.GroupKind{Group: gv.Group, Kind: kind}
	mapping, err := r.mapper.RESTMapping(gk, gv.Version)
	if err != nil && meta.IsNoMatchError(err) {
		// A deferred discovery mapper caches discovery for its lifetime, so a
		// CRD (or apiVersion) registered after that cache was first populated
		// would resolve to NoMatch permanently. Refresh discovery once and
		// retry so a late-registered kind resolves without a controller
		// iteration restart.
		if resettable, ok := r.mapper.(meta.ResettableRESTMapper); ok {
			resettable.Reset()
			mapping, err = r.mapper.RESTMapping(gk, gv.Version)
		}
	}
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("resolving resource for apiVersion %q Kind %q: %w", apiVersion, kind, err)
	}

	return mapping.Resource, nil
}

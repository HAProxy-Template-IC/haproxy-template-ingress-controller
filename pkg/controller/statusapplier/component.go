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
// The StatusApplier is a stateless consumer: each event carries the patches it
// needs to apply. There is no side-channel cache. The patches travelling on a
// deploy event are tautologically the patches for the configuration that
// deploy carried — no LATEST-vs-deployed race is possible.
//
// Status patches are fully defined by templates — the controller never hardcodes
// knowledge of specific resource types or condition names. Templates register patches
// via the statusPatch() template function during rendering, including outcome-keyed
// variants for each pipeline phase (rendered, deployed, renderFailed, deployFailed).
//
// Event mapping:
//
//   - TemplateRenderedEvent: apply the "rendered" variant directly from
//     event.StatusPatches. Marks the resource as in-progress (Accepted=Unknown /
//     "rendering") well before HAProxy reload completes.
//   - DeploymentCompletedEvent: apply the "deployed" variant from
//     event.StatusPatches. The Deployer forwards the patches from the
//     DeploymentScheduledEvent that triggered the deploy, so the patches
//     describe exactly the config the deploy shipped. Programmed=True
//     genuinely means "HAProxy is serving this config" because reload
//     verification gates DeploymentCompletedEvent.
//   - DeploymentSkippedEvent: apply the "deployed" variant from
//     event.StatusPatches. Same data-plane-is-converged semantics as
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
// fresh TemplateRenderedEvent with patches. The applier therefore has no
// replay responsibility on leadership change.
package statusapplier

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "status-applier"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate volume: receives template rendered, reconciliation completed/failed,
	// and leadership events.
	EventBufferSize = busevents.StandardSubscriberBuffer

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
)

// GVRResolver resolves apiVersion + kind to a GroupVersionResource.
// This abstracts the REST mapper for testability.
type GVRResolver interface {
	Resolve(apiVersion, kind string) (schema.GroupVersionResource, error)
}

// Component applies template-driven status patches to Kubernetes resources
// via Server-Side Apply (SSA).
//
// This is an all-replica component that subscribes in the constructor. It caches
// patches from TemplateRenderedEvent and applies the appropriate variant based
// on pipeline lifecycle events. Only the leader applies patches to avoid conflicts.
//
// Event flow (every applied phase reads patches directly from event.StatusPatches):
//
//	TemplateRenderedEvent → apply "rendered" variant (if leader)
//	DeploymentCompletedEvent → apply "deployed" variant (if leader)
//	DeploymentSkippedEvent → apply "deployed" variant (if leader); the data
//	    plane is already at the rendered config so Programmed conditions
//	    should reflect the current generation
//	ReconciliationFailedEvent → apply "renderFailed" or "deployFailed" variant (if leader)
//	BecameLeaderEvent → clear checksum cache; rely on Reconciler to fire a fresh reconcile
//	LostLeadershipEvent → flip the leader flag off
type Component struct {
	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	dynamicClient dynamic.Interface
	gvrResolver   GVRResolver
	logger        *slog.Logger
	healthTracker *lifecycle.HealthTracker

	// mu protects all mutable state below.
	mu       sync.RWMutex
	isLeader bool

	// checksumCache maps "namespace/name/gvr" to the SHA-256 of the last
	// successfully applied patch payload. Used to skip redundant SSA calls.
	checksumCache map[string]string
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

	bus := cfg.EventBus
	// SubscribeTypes (not Subscribe) — the bus prefilters by event type at
	// publish, so the 50-event buffer holds ONLY events we actually dispatch
	// on. With a plain Subscribe the buffer would fill within seconds during
	// conformance setup (resource.index.updated and reconciliation.* fire at
	// kHz) and overflow — silently dropping deployment.completed events along
	// with the rest. We saw exactly that in CI: the "deployed" status patches
	// never fired, Gateways never got Programmed=True, and conformance Test
	// Setup timed out on every shard.
	eventChan := bus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeTemplateRendered,
		events.EventTypeDeploymentCompleted,
		events.EventTypeDeploymentSkipped,
		events.EventTypeReconciliationFailed,
		events.EventTypeBecameLeader,
		events.EventTypeLostLeadership,
	)

	return &Component{
		eventBus:      bus,
		eventChan:     eventChan,
		dynamicClient: cfg.DynamicClient,
		gvrResolver:   cfg.GVRResolver,
		logger:        logger.With("component", ComponentName),
		healthTracker: lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		checksumCache: make(map[string]string),
	}
}

// Name returns the unique identifier for this component.
func (c *Component) Name() string {
	return ComponentName
}

// HealthCheck returns nil if the component is healthy.
func (c *Component) HealthCheck() error {
	return c.healthTracker.Check()
}

// Start begins the StatusApplier event loop.
//
// This method blocks until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("status applier starting")

	for {
		select {
		case event := <-c.eventChan:
			c.healthTracker.StartProcessing()
			c.handleEvent(ctx, event)
			c.healthTracker.EndProcessing()

		case <-ctx.Done():
			c.logger.Info("status applier shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent routes events to the appropriate handler.
func (c *Component) handleEvent(ctx context.Context, event busevents.Event) {
	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		c.handleTemplateRendered(ctx, e)

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

// handleTemplateRendered applies the "rendered" variant directly from the
// event payload. Patches are config-level (Accepted/ResolvedRefs); no
// data-plane gate is needed.
func (c *Component) handleTemplateRendered(ctx context.Context, event *events.TemplateRenderedEvent) {
	if !c.leaderRLocked() || len(event.StatusPatches) == 0 {
		return
	}
	c.applyVariant(ctx, event.StatusPatches, events.StatusPatchPhaseRendered)
}

// handleDeploymentCompleted applies the "deployed" variant from the event
// payload. The Deployer forwards the patches from the DeploymentScheduledEvent
// that triggered this deploy, so the patches describe exactly the config the
// deploy shipped — no cache, no LATEST-vs-deployed race.
//
// Partial-failure handling: any successful endpoint observed the new config,
// so applying the "deployed" variant whenever Succeeded > 0 reflects reality
// for those instances. Per-endpoint failures surface via
// InstanceDeploymentFailedEvent and feed the "deployFailed" variant through
// ReconciliationFailedEvent independently.
func (c *Component) handleDeploymentCompleted(ctx context.Context, event *events.DeploymentCompletedEvent) {
	// Zero-endpoint deployment (no HAProxy pods discovered yet) doesn't
	// actually put any HAProxy on the new config — don't claim "deployed".
	if event.Total == 0 || event.Succeeded == 0 {
		return
	}
	if !c.leaderRLocked() || len(event.StatusPatches) == 0 {
		return
	}
	c.applyVariant(ctx, event.StatusPatches, events.StatusPatchPhaseDeployed)
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
	if !c.leaderRLocked() || len(event.StatusPatches) == 0 {
		return
	}
	c.applyVariant(ctx, event.StatusPatches, events.StatusPatchPhaseDeployed)
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
	if !c.leaderRLocked() || len(event.StatusPatches) == 0 {
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
	c.applyVariant(ctx, event.StatusPatches, phase)
}

// handleBecameLeader flips the leader flag and clears the SSA checksum cache.
// No patches replay — the Reconciler fires an immediate reconciliation on
// BecameLeaderEvent (see pkg/controller/reconciler/CLAUDE.md), which produces
// a fresh TemplateRenderedEvent carrying the patches the new leader needs.
func (c *Component) handleBecameLeader(_ context.Context) {
	c.mu.Lock()
	c.isLeader = true
	// Clear checksum cache — the previous leader may have applied different checksums.
	c.checksumCache = make(map[string]string)
	c.mu.Unlock()
	c.logger.Info("became leader, clearing status checksum cache")
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
		c.logger.Info("lost leadership, pausing status patch application")
	}

	c.isLeader = false
}

// applyVariant applies the given phase variant from each patch to the target resource.
func (c *Component) applyVariant(ctx context.Context, patches []templating.StatusPatch, phase events.StatusPatchPhase) {
	startTime := time.Now()
	phaseKey := string(phase)

	var applied, skipped int

	for i := range patches {
		patch := &patches[i]
		statusPayload, ok := patch.Variants[phaseKey]
		if !ok {
			continue
		}

		gvr, err := c.gvrResolver.Resolve(patch.APIVersion, patch.Kind)
		if err != nil {
			c.logger.Error("failed to resolve GVR for status patch",
				"api_version", patch.APIVersion,
				"kind", patch.Kind,
				"error", err)
			c.eventBus.Publish(events.NewStatusUpdateFailedEvent(
				patch.Namespace, patch.Name,
				fmt.Sprintf("%s/%s", patch.APIVersion, patch.Kind),
				err.Error(), false,
			))
			continue
		}

		gvrStr := gvr.String()

		// Compute checksum of the status payload.
		payloadBytes, err := json.Marshal(statusPayload)
		if err != nil {
			c.logger.Error("failed to marshal status payload",
				"namespace", patch.Namespace,
				"name", patch.Name,
				"error", err)
			continue
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(payloadBytes))

		// Check checksum cache — skip if already applied.
		// Cache key includes the phase so rendered and deployed track
		// separate "last applied checksum"s. Without that, rendered's
		// apply (content A) updates the cache, deployed's apply (content
		// B) updates the cache again, and the next rendered apply
		// (content A) sees mismatch and re-writes — overwriting the
		// deployed state in K8s. With phase-scoped keys: rendered cache
		// hits on the second pass, K8s keeps deployed's content. SSA
		// behaviour with field manager "haptic" still owns every field
		// each phase touches, so the LAST write wins and we let that
		// last write be deployed.
		cacheKey := fmt.Sprintf("%s/%s/%s/%s", phaseKey, patch.Namespace, patch.Name, gvrStr)
		c.mu.RLock()
		lastChecksum := c.checksumCache[cacheKey]
		c.mu.RUnlock()

		if lastChecksum == checksum {
			skipped++
			continue
		}

		// Build the SSA patch payload: wrap status content under .status.
		// For cluster-scoped resources (e.g. GatewayClass) the namespace is
		// empty; omit the field rather than serialising "namespace": "" so
		// the API server's SSA codec doesn't claim ownership of an empty
		// namespace string we'd then have to track.
		metadata := map[string]any{"name": patch.Name}
		if patch.Namespace != "" {
			metadata["namespace"] = patch.Namespace
		}
		ssaPayload := map[string]any{
			"apiVersion": patch.APIVersion,
			"kind":       patch.Kind,
			"metadata":   metadata,
			statusKey:    statusPayload,
		}

		ssaBytes, err := json.Marshal(ssaPayload)
		if err != nil {
			c.logger.Error("failed to marshal SSA payload",
				"namespace", patch.Namespace,
				"name", patch.Name,
				"error", err)
			continue
		}

		// Apply via SSA on the status subresource.
		_, err = c.dynamicClient.Resource(gvr).Namespace(patch.Namespace).Patch(
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
		if err != nil {
			c.logger.Error("failed to apply status patch",
				"namespace", patch.Namespace,
				"name", patch.Name,
				"gvr", gvrStr,
				"phase", phaseKey,
				"error", err)
			c.eventBus.Publish(events.NewStatusUpdateFailedEvent(
				patch.Namespace, patch.Name, gvrStr,
				err.Error(), isRetriable(err),
			))
			continue
		}

		// Update checksum cache on success.
		c.mu.Lock()
		c.checksumCache[cacheKey] = checksum
		c.mu.Unlock()

		applied++
	}

	durationMs := time.Since(startTime).Milliseconds()

	if applied > 0 || skipped > 0 {
		c.logger.Debug("status patches applied",
			"phase", phaseKey,
			"applied", applied,
			"skipped", skipped,
			"duration_ms", durationMs)
	}

	c.eventBus.Publish(events.NewStatusUpdateCompletedEvent(
		phase, applied, skipped, durationMs,
	))
}

// isRetriable returns true if the error is likely transient and the operation
// should be retried on the next reconciliation cycle.
func isRetriable(err error) bool {
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

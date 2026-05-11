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
// The StatusApplier subscribes to pipeline events (TemplateRenderedEvent,
// DeploymentCompletedEvent, ReconciliationFailedEvent) and applies the
// appropriate status patch variant for each lifecycle phase using Server-Side Apply (SSA).
//
// Status patches are fully defined by templates — the controller never hardcodes
// knowledge of specific resource types or condition names. Templates register patches
// via the statusPatch() template function during rendering, including outcome-keyed
// variants for each pipeline phase (rendered, deployed, renderFailed, deployFailed).
//
// Event mapping:
//
//   - TemplateRenderedEvent: cache patches + apply the "rendered" variant. This
//     marks the route as in-progress (Accepted=Unknown / "rendering") so the
//     world sees activity well before HAProxy is ready.
//   - DeploymentCompletedEvent: apply the "deployed" variant. This fires only
//     after the deployer pushes config to HAProxy endpoints and reload completes,
//     so Accepted=True genuinely means "HAProxy is serving this route." Listening
//     to ReconciliationCompletedEvent instead would flip Accepted=True after the
//     coordinator's in-memory pipeline (render + validate) finishes — which
//     happens BEFORE the deployer queues a deployment and BEFORE HAProxy reload.
//     That early-flip used to leak into the Gateway-API conformance suite as
//     intermittent route-not-found 404s: tests poll until Accepted=True, then
//     dial within milliseconds, racing the HAProxy reload.
//   - ReconciliationFailedEvent: apply the failure variant ("renderFailed" /
//     "validateFailed" / "deployFailed") based on the phase that failed.
package statusapplier

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	// patchCorrelationCacheCap bounds patchesByCorrelation under sustained
	// render churn. Renders that get superseded in the deployment queue
	// never see their own DeploymentCompletedEvent, so their patches sit
	// in the map until evicted. 64 is well above what the conformance
	// suite produces during a shard (peak ~10 inflight renders observed)
	// and small enough that the map stays trivially-sized in memory.
	patchCorrelationCacheCap = 64

	// ComponentName is the unique identifier for this component.
	ComponentName = "status-applier"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate volume: receives template rendered, reconciliation completed/failed,
	// and leadership events.
	EventBufferSize = busevents.StandardSubscriberBuffer

	// fieldManager is the SSA field manager name used for status patches.
	fieldManager = "haptic"

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
// Event flow:
//
//	TemplateRenderedEvent → cache patches, apply "rendered" variant (if leader)
//	DeploymentCompletedEvent → apply "deployed" variant (if leader)
//	ReconciliationFailedEvent → apply "renderFailed" or "deployFailed" variant (if leader)
//	BecameLeaderEvent → clear checksum cache, apply cached "rendered" variant
//	LostLeadershipEvent → clear pending state
type Component struct {
	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	dynamicClient dynamic.Interface
	gvrResolver   GVRResolver
	logger        *slog.Logger
	healthTracker *lifecycle.HealthTracker

	// mu protects all mutable state below.
	mu            sync.RWMutex
	isLeader      bool
	cachedPatches []templating.StatusPatch

	// patchesByCorrelation maps a render's correlation_id to its status
	// patches. Used by handleDeploymentCompleted to apply the "deployed"
	// variant ONLY for the render whose config actually got deployed.
	//
	// Without this, under conformance-test load (many fixtures appearing
	// in parallel, the deployer running 1-3s per deploy while renders 2-3
	// behind queue up), the cached "latest" patches reflect a render
	// that's NOT YET deployed — every deploy's completion would flip
	// Accepted=True for routes the deploy didn't ship, racing the test's
	// poll-then-dial loop. Key by correlation_id, evict on apply, and the
	// race goes away.
	//
	// correlationOrder records insertion order so we can evict the oldest
	// entry when the cap (patchCorrelationCacheCap) is exceeded. The
	// deployment scheduler only queues the LATEST config — so renders
	// between two completed deploys never get an individual deploy and
	// their patches stay in the map until evicted. The cap keeps memory
	// bounded under sustained churn.
	patchesByCorrelation map[string][]templating.StatusPatch
	correlationOrder     []string

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
	eventChan := bus.Subscribe(ComponentName, EventBufferSize)

	return &Component{
		eventBus:      bus,
		eventChan:     eventChan,
		dynamicClient: cfg.DynamicClient,
		gvrResolver:   cfg.GVRResolver,
		logger:        logger.With("component", ComponentName),
		healthTracker: lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		checksumCache:        make(map[string]string),
		patchesByCorrelation: make(map[string][]templating.StatusPatch),
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

	case *events.ReconciliationFailedEvent:
		c.handleReconciliationFailed(ctx, e)

	case *events.BecameLeaderEvent:
		c.handleBecameLeader(ctx)

	case *events.LostLeadershipEvent:
		c.handleLostLeadership()
	}
}

// handleTemplateRendered caches the status patches from a successful render
// and applies the "rendered" variant if this replica is the leader.
//
// Patches are stored under two keys:
//
//   - cachedPatches: the LATEST patches, used by handleReconciliationFailed
//     and handleBecameLeader where we want to act on the most recent render.
//   - patchesByCorrelation[correlationID]: per-render patches, used by
//     handleDeploymentCompleted to match patches to the specific render
//     whose config got deployed. Without this, under conformance-test load
//     the latest patches outrun the deployer and "deployed" status fires
//     for routes the deploy didn't ship.
func (c *Component) handleTemplateRendered(ctx context.Context, event *events.TemplateRenderedEvent) {
	c.mu.Lock()
	c.cachedPatches = event.StatusPatches
	if cid := event.CorrelationID(); cid != "" && len(event.StatusPatches) > 0 {
		if _, existed := c.patchesByCorrelation[cid]; !existed {
			c.correlationOrder = append(c.correlationOrder, cid)
		}
		c.patchesByCorrelation[cid] = event.StatusPatches
		// Evict oldest entries when over cap. Insertion-order eviction
		// works because the deployment scheduler queues the latest
		// validated render — orphaned (older, superseded) renders are
		// the ones that will never be matched, so dropping them first
		// is safe.
		for len(c.correlationOrder) > patchCorrelationCacheCap {
			oldest := c.correlationOrder[0]
			c.correlationOrder = c.correlationOrder[1:]
			delete(c.patchesByCorrelation, oldest)
		}
	}
	isLeader := c.isLeader
	c.mu.Unlock()

	if !isLeader || len(event.StatusPatches) == 0 {
		return
	}

	c.applyVariant(ctx, event.StatusPatches, events.StatusPatchPhaseRendered)
}

// handleDeploymentCompleted applies the "deployed" variant after the deployer
// has pushed config to HAProxy endpoints and reload has completed.
//
// This is the correct trigger for Accepted=True / Programmed=True style status
// conditions: it fires AFTER HAProxy actually serves the new routes, not
// after the in-memory pipeline (render + validate) finishes. Listening to
// ReconciliationCompletedEvent here used to flip Accepted=True too early
// and race the conformance test framework's poll-then-dial loop.
//
// Partial-failure handling: the deployer publishes DeploymentCompletedEvent
// once per scheduled deployment, with Total / Succeeded / Failed counts. We
// fire the "deployed" variant whenever ANY endpoint succeeded — every
// successful endpoint observed the new config, so "Accepted on the chart's
// data plane" is true for those instances. Per-endpoint failures surface
// via the deployer's InstanceDeploymentFailedEvent stream and feed the
// "deployFailed" variant through ReconciliationFailedEvent independently.
func (c *Component) handleDeploymentCompleted(ctx context.Context, event *events.DeploymentCompletedEvent) {
	// Zero-endpoint deployment (no HAProxy pods discovered yet) doesn't
	// actually put any HAProxy on the new config — don't claim "deployed".
	if event.Total == 0 || event.Succeeded == 0 {
		return
	}

	cid := event.CorrelationID()
	c.mu.Lock()
	isLeader := c.isLeader
	// Match patches to this specific render's correlation_id and evict
	// the matched entry. Older entries that haven't been matched by now
	// are orphans (their renders were superseded in the scheduler's
	// queue); they'll be evicted in insertion-order by handleTemplate
	// Rendered's cap enforcement.
	patches, hasMatch := c.patchesByCorrelation[cid]
	if hasMatch {
		delete(c.patchesByCorrelation, cid)
		for i, oc := range c.correlationOrder {
			if oc == cid {
				c.correlationOrder = append(c.correlationOrder[:i], c.correlationOrder[i+1:]...)
				break
			}
		}
	}
	c.mu.Unlock()

	if !isLeader {
		return
	}

	if !hasMatch || len(patches) == 0 {
		// Either no correlation_id on the event (older deployer / shouldn't
		// happen) or the matching patches were already consumed by a prior
		// DeploymentCompletedEvent. Either way, nothing to apply.
		return
	}

	c.applyVariant(ctx, patches, events.StatusPatchPhaseDeployed)
}

// handleReconciliationFailed applies the failure variant based on which phase failed.
func (c *Component) handleReconciliationFailed(ctx context.Context, event *events.ReconciliationFailedEvent) {
	c.mu.RLock()
	patches := c.cachedPatches
	isLeader := c.isLeader
	c.mu.RUnlock()

	if !isLeader || len(patches) == 0 {
		return
	}

	phase := events.StatusPatchPhaseDeployFailed
	if event.Phase == "render" {
		phase = events.StatusPatchPhaseRenderFailed
	}

	c.applyVariant(ctx, patches, phase)
}

// handleBecameLeader clears the checksum cache and applies cached patches.
func (c *Component) handleBecameLeader(ctx context.Context) {
	c.mu.Lock()
	c.isLeader = true
	// Clear checksum cache — the previous leader may have applied different checksums.
	c.checksumCache = make(map[string]string)
	patches := c.cachedPatches
	c.mu.Unlock()

	c.logger.Info("became leader, clearing status checksum cache")

	if len(patches) > 0 {
		c.logger.Info("replaying cached status patches for rendered phase",
			"patch_count", len(patches))
		c.applyVariant(ctx, patches, events.StatusPatchPhaseRendered)
	}
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
		cacheKey := fmt.Sprintf("%s/%s/%s", patch.Namespace, patch.Name, gvrStr)
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
				FieldManager: fieldManager,
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

// RestMapperResolver implements GVRResolver using a Kubernetes REST mapper.
type RestMapperResolver struct {
	// restMapper is used internally but we parse apiVersion+kind ourselves
	// to produce a GVR without needing the full meta.RESTMapper interface.
}

// NewRestMapperResolver creates a GVRResolver that maps apiVersion+kind to GVR
// using static conventions (pluralized lowercase kind).
//
// For production use, consider implementing a resolver backed by a real REST mapper
// if custom resources use non-standard pluralization.
func NewRestMapperResolver() *RestMapperResolver {
	return &RestMapperResolver{}
}

// Resolve maps apiVersion + kind to a GroupVersionResource.
//
// This uses the standard Kubernetes convention of pluralizing the lowercase kind
// as the resource name. For example:
//   - networking.k8s.io/v1 + Ingress → networking.k8s.io/v1/ingresses
//   - gateway.networking.k8s.io/v1 + Gateway → gateway.networking.k8s.io/v1/gateways
//   - gateway.networking.k8s.io/v1 + HTTPRoute → gateway.networking.k8s.io/v1/httproutes
func (r *RestMapperResolver) Resolve(apiVersion, kind string) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}

	// Standard Kubernetes pluralization: lowercase + "s"
	// Handles common cases like Ingress→ingresses, Gateway→gateways
	resource := pluralize(kind)

	return gv.WithResource(resource), nil
}

// pluralize returns the standard Kubernetes plural form of a kind.
func pluralize(kind string) string {
	lower := strings.ToLower(kind)
	switch {
	case strings.HasSuffix(lower, "s"):
		return lower + "es" // e.g., Ingress → ingresses
	case strings.HasSuffix(lower, "y") && len(lower) >= 2 && isConsonant(lower[len(lower)-2]):
		return lower[:len(lower)-1] + "ies" // e.g., Policy → policies
	default:
		return lower + "s" // e.g., Gateway → gateways, HTTPRoute → httproutes
	}
}

// isConsonant returns true if the byte is a lowercase ASCII consonant.
func isConsonant(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	default:
		return c >= 'a' && c <= 'z'
	}
}

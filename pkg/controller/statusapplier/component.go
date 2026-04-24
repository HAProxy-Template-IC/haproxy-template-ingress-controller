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
// ReconciliationCompletedEvent, ReconciliationFailedEvent) and applies the
// appropriate status patch variant for each lifecycle phase using Server-Side Apply (SSA).
//
// Status patches are fully defined by templates — the controller never hardcodes
// knowledge of specific resource types or condition names. Templates register patches
// via the statusPatch() template function during rendering, including outcome-keyed
// variants for each pipeline phase (rendered, deployed, renderFailed, deployFailed).
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
	// ComponentName is the unique identifier for this component.
	ComponentName = "status-applier"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate volume: receives template rendered, reconciliation completed/failed,
	// and leadership events.
	EventBufferSize = busevents.StandardSubscriberBuffer

	// fieldManager is the SSA field manager name used for status patches.
	fieldManager = "haptic"
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
//	ReconciliationCompletedEvent → apply "deployed" variant (if leader)
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

	case *events.ReconciliationCompletedEvent:
		c.handleReconciliationCompleted(ctx, e)

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
func (c *Component) handleTemplateRendered(ctx context.Context, event *events.TemplateRenderedEvent) {
	c.mu.Lock()
	c.cachedPatches = event.StatusPatches
	isLeader := c.isLeader
	c.mu.Unlock()

	if !isLeader || len(event.StatusPatches) == 0 {
		return
	}

	c.applyVariant(ctx, event.StatusPatches, events.StatusPatchPhaseRendered)
}

// handleReconciliationCompleted applies the "deployed" variant after successful deployment.
func (c *Component) handleReconciliationCompleted(ctx context.Context, _ *events.ReconciliationCompletedEvent) {
	c.mu.RLock()
	patches := c.cachedPatches
	isLeader := c.isLeader
	c.mu.RUnlock()

	if !isLeader || len(patches) == 0 {
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
		ssaPayload := map[string]any{
			"apiVersion": patch.APIVersion,
			"kind":       patch.Kind,
			"metadata": map[string]any{
				"namespace": patch.Namespace,
				"name":      patch.Name,
			},
			"status": statusPayload,
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
			"status",
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
	var netErr net.Error
	if errors.As(err, &netErr) {
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

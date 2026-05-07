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

// Package resourceapplier reconciles template-declared Kubernetes resources
// via Server-Side Apply (SSA).
//
// Mirrors statusapplier's leader-only / checksum-cached / event-driven shape
// exactly — it just operates on full resources rather than status sub-paths.
// Templates emit desired resources via the renderResource() template
// function (filters_resource.go); the renderer surfaces them on
// TemplateRenderedEvent.RenderedResources; this component applies them on
// the cluster after the deployment phase succeeds.
//
// Resource-agnostic by design: the controller never names "Service" or
// "Gateway" — it just applies whatever the template emits. Templates decide
// what to emit; the controller is the generic vehicle.
//
// API-traffic safety:
//   - SHA-256 checksum cache per (namespace, name, gvr) skips the SSA round-
//     trip when the payload matches the last-applied value.
//   - Cache is cleared on BecameLeaderEvent (the previous leader's checksums
//     aren't trustworthy for the new one), forcing a single re-apply burst on
//     leadership transitions but no hammering on steady-state renders.
//   - Default RestrictToOwnNamespace=true refuses cross-namespace and
//     cluster-scoped applies; opt-in via config for templates that need to
//     spawn cluster-scoped resources (corresponding ClusterRole RBAC must
//     also be granted).
//
// Orphan pruning: resources that disappear from the rendered set between
// reconciliations are detected via the in-memory checksum cache (key in
// cache but not in new render) and deleted. Startup orphans (resources
// the previous incarnation created and never cleaned up before crashing)
// require an offline kubectl sweep using the managed-by label this
// component injects.
package resourceapplier

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "resource-applier"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = busevents.StandardSubscriberBuffer

	// fieldManager is the SSA field manager name. Same value as
	// statusapplier deliberately — both subsystems are part of the same
	// controller and a single field-manager identity is the simplest
	// audit story (`kubectl get <kind> -o jsonpath='{.metadata.managedFields[?(@.manager=="haptic")]}'`).
	fieldManager = "haptic"

	// LabelManagedBy is injected onto every applied resource so operators
	// can locate everything the controller owns with a single
	// `kubectl get … -l haproxy-haptic.org/managed-by=<name>` selector.
	LabelManagedBy = "haproxy-haptic.org/managed-by"
)

// GVRResolver resolves apiVersion + kind to a GroupVersionResource.
// Reused from the statusapplier package to avoid duplicate logic.
type GVRResolver = statusapplier.GVRResolver

// Component reconciles template-declared resources to the cluster.
//
// All-replica subscriber, leader-only applier — same shape as
// statusapplier.Component. State (cachedResources, checksum cache) lives
// only on the active leader; replicas in standby just observe events.
type Component struct {
	eventBus      *busevents.EventBus
	eventChan     <-chan busevents.Event
	dynamicClient dynamic.Interface
	gvrResolver   GVRResolver
	logger        *slog.Logger
	healthTracker *lifecycle.HealthTracker

	// ownNamespace is the namespace the controller is deployed into. Used
	// to enforce RestrictToOwnNamespace; also the safe target for the
	// "managed-by" label-driven discovery the chart's static-addresses
	// templates use to locate the per-Gateway Services they create.
	ownNamespace           string
	restrictToOwnNamespace bool
	managedByValue         string

	// mu protects all mutable state below.
	mu               sync.RWMutex
	isLeader         bool
	cachedResources  []templating.RenderedResource
	checksumCache    map[string]string // key: "ns/name/gvr" → sha256(payload)
	lastAppliedKeys  map[string]appliedKeyMeta
}

// appliedKeyMeta tracks the GVR + namespace + name needed to delete an
// orphan that disappears from a later render.
type appliedKeyMeta struct {
	GVR       schema.GroupVersionResource
	Namespace string
	Name      string
}

// Config bundles the dependencies a New caller must provide.
type Config struct {
	EventBus      *busevents.EventBus
	DynamicClient dynamic.Interface
	GVRResolver   GVRResolver
	Logger        *slog.Logger

	// OwnNamespace is the namespace the controller pod runs in. Required
	// when RestrictToOwnNamespace is true.
	OwnNamespace string

	// RestrictToOwnNamespace, when true (default for the chart), refuses
	// to apply any rendered resource whose namespace is empty (cluster-
	// scoped) or differs from OwnNamespace. Combined with the chart's
	// namespace-scoped Role, this gives belt-and-suspenders safety: even
	// a misbehaving template can't escalate beyond the controller's
	// namespace.
	RestrictToOwnNamespace bool

	// ManagedByValue is the label value injected as
	// `haproxy-haptic.org/managed-by`. Defaults to the controller name
	// ("haptic-controller") so multiple haptic deployments in the same
	// cluster don't clobber each other's managed sets.
	ManagedByValue string
}

// New constructs an applier and subscribes to the events it needs.
// Subscription happens in the constructor so events buffered before
// EventBus.Start() are delivered after — the same all-replica pattern
// every haptic controller component follows.
func New(cfg *Config) *Component {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	managedBy := cfg.ManagedByValue
	if managedBy == "" {
		managedBy = "haptic-controller"
	}

	bus := cfg.EventBus
	eventChan := bus.Subscribe(ComponentName, EventBufferSize)

	return &Component{
		eventBus:               bus,
		eventChan:              eventChan,
		dynamicClient:          cfg.DynamicClient,
		gvrResolver:            cfg.GVRResolver,
		logger:                 logger.With("component", ComponentName),
		healthTracker:          lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		ownNamespace:           cfg.OwnNamespace,
		restrictToOwnNamespace: cfg.RestrictToOwnNamespace,
		managedByValue:         managedBy,
		checksumCache:          make(map[string]string),
		lastAppliedKeys:        make(map[string]appliedKeyMeta),
	}
}

// Name returns the component identifier (lifecycle.Component).
func (c *Component) Name() string { return ComponentName }

// HealthCheck returns nil if the component is healthy.
func (c *Component) HealthCheck() error { return c.healthTracker.Check() }

// Start runs the event loop until ctx is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("resource applier starting")
	for {
		select {
		case event := <-c.eventChan:
			c.healthTracker.StartProcessing()
			c.handleEvent(ctx, event)
			c.healthTracker.EndProcessing()
		case <-ctx.Done():
			c.logger.Info("resource applier shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent fans out by event type. Mirror of statusapplier.handleEvent
// shape — different events because resource lifecycle is "rendered → deployed
// (= apply)" rather than the four-phase status patches use.
func (c *Component) handleEvent(ctx context.Context, event busevents.Event) {
	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		c.handleTemplateRendered(e)
	case *events.ReconciliationCompletedEvent:
		c.handleReconciliationCompleted(ctx)
	case *events.BecameLeaderEvent:
		c.handleBecameLeader(ctx)
	case *events.LostLeadershipEvent:
		c.handleLostLeadership()
	}
}

// handleTemplateRendered caches the rendered resource set. Apply happens on
// ReconciliationCompletedEvent — we don't apply on rendered because we'd
// risk creating per-Gateway Services that point at HAProxy pods serving an
// old config.
func (c *Component) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	c.mu.Lock()
	c.cachedResources = event.RenderedResources
	c.mu.Unlock()
}

// handleReconciliationCompleted applies the cached resources after a
// successful deployment phase.
func (c *Component) handleReconciliationCompleted(ctx context.Context) {
	c.mu.RLock()
	resources := c.cachedResources
	isLeader := c.isLeader
	c.mu.RUnlock()
	if !isLeader || len(resources) == 0 {
		// Even with len == 0 we want to prune orphans if a previous
		// render had resources and the current one doesn't. Handle that
		// case explicitly:
		if isLeader {
			c.applyAndPrune(ctx, nil)
		}
		return
	}
	c.applyAndPrune(ctx, resources)
}

// handleBecameLeader clears the checksum cache. The previous leader's
// checksums aren't valid for us — the API server's resource versions
// reflect their applies, not ours.
func (c *Component) handleBecameLeader(ctx context.Context) {
	c.mu.Lock()
	c.isLeader = true
	c.checksumCache = make(map[string]string)
	c.lastAppliedKeys = make(map[string]appliedKeyMeta)
	resources := c.cachedResources
	c.mu.Unlock()
	c.logger.Info("became leader, clearing resource checksum cache")
	if len(resources) > 0 {
		c.applyAndPrune(ctx, resources)
	}
}

// handleLostLeadership clears the leader flag and pauses applies. The
// next leader gets the events that arrive in the meantime (they're
// buffered in our subscription channel and replayed when leadership is
// restored).
func (c *Component) handleLostLeadership() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isLeader {
		c.logger.Info("lost leadership, pausing resource applies")
	}
	c.isLeader = false
}

// applyAndPrune applies the new desired set and deletes any
// previously-applied resources that are no longer in it.
func (c *Component) applyAndPrune(ctx context.Context, resources []templating.RenderedResource) {
	startTime := time.Now()
	desiredKeys := make(map[string]appliedKeyMeta, len(resources))
	var applied, skipped, refused int

	for i := range resources {
		r := &resources[i]
		gvr, err := c.gvrResolver.Resolve(r.APIVersion, r.Kind)
		if err != nil {
			c.logger.Error("failed to resolve GVR for rendered resource",
				"api_version", r.APIVersion, "kind", r.Kind, "error", err)
			continue
		}
		if c.refused(r) {
			refused++
			continue
		}
		key := fmt.Sprintf("%s/%s/%s", r.Namespace, r.Name, gvr.String())
		desiredKeys[key] = appliedKeyMeta{GVR: gvr, Namespace: r.Namespace, Name: r.Name}

		object := c.injectManagedByLabel(r.Object)
		payload, err := json.Marshal(object)
		if err != nil {
			c.logger.Error("failed to marshal rendered resource",
				"namespace", r.Namespace, "name", r.Name, "kind", r.Kind, "error", err)
			continue
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(payload))

		c.mu.RLock()
		last := c.checksumCache[key]
		c.mu.RUnlock()
		if last == checksum {
			skipped++
			continue
		}

		_, err = c.dynamicClient.Resource(gvr).Namespace(r.Namespace).Patch(
			ctx,
			r.Name,
			types.ApplyPatchType,
			payload,
			metav1.PatchOptions{
				FieldManager: fieldManager,
				Force:        new(true),
			},
		)
		if err != nil {
			c.logger.Error("failed to apply rendered resource",
				"namespace", r.Namespace, "name", r.Name, "gvr", gvr.String(),
				"retriable", isRetriable(err), "error", err)
			continue
		}

		c.mu.Lock()
		c.checksumCache[key] = checksum
		c.lastAppliedKeys[key] = desiredKeys[key]
		c.mu.Unlock()
		applied++
	}

	// Prune orphans: anything in lastAppliedKeys NOT in desiredKeys.
	c.mu.Lock()
	prior := c.lastAppliedKeys
	stillApplied := make(map[string]appliedKeyMeta, len(desiredKeys))
	for k, v := range desiredKeys {
		stillApplied[k] = v
	}
	c.mu.Unlock()

	deleted := 0
	for key, meta := range prior {
		if _, kept := desiredKeys[key]; kept {
			continue
		}
		err := c.dynamicClient.Resource(meta.GVR).Namespace(meta.Namespace).Delete(
			ctx, meta.Name, metav1.DeleteOptions{},
		)
		if err != nil && !apierrors.IsNotFound(err) {
			c.logger.Error("failed to delete orphan resource",
				"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String(),
				"error", err)
			// Keep it in the cache so we'll try again next reconciliation.
			stillApplied[key] = meta
			continue
		}
		deleted++
		c.mu.Lock()
		delete(c.checksumCache, key)
		c.mu.Unlock()
	}

	c.mu.Lock()
	c.lastAppliedKeys = stillApplied
	c.mu.Unlock()

	if applied+skipped+deleted+refused > 0 {
		c.logger.Debug("resource applier pass complete",
			"applied", applied, "skipped", skipped,
			"deleted", deleted, "refused", refused,
			"duration_ms", time.Since(startTime).Milliseconds())
	}
}

// refused returns true when the policy says to skip this resource
// (RestrictToOwnNamespace + cluster-scoped or foreign-namespace target).
// Logs once per refusal so a misbehaving template surfaces in logs but
// doesn't bring down the reconciliation.
func (c *Component) refused(r *templating.RenderedResource) bool {
	if !c.restrictToOwnNamespace {
		return false
	}
	if r.Namespace == "" || (c.ownNamespace != "" && r.Namespace != c.ownNamespace) {
		c.logger.Warn("refusing to apply resource outside controller namespace",
			"target_namespace", r.Namespace,
			"controller_namespace", c.ownNamespace,
			"kind", r.Kind, "name", r.Name,
			"hint", "set Config.RestrictToOwnNamespace=false (and grant ClusterRole RBAC) to opt in")
		return true
	}
	return false
}

// injectManagedByLabel ensures every applied resource carries the
// managed-by label so operators can locate everything haptic owns.
// Returns a NEW map so the caller's object isn't mutated.
func (c *Component) injectManagedByLabel(object map[string]any) map[string]any {
	out := make(map[string]any, len(object)+1)
	for k, v := range object {
		out[k] = v
	}
	metadata, _ := out["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	} else {
		copied := make(map[string]any, len(metadata)+1)
		for k, v := range metadata {
			copied[k] = v
		}
		metadata = copied
	}
	labels, _ := metadata["labels"].(map[string]any)
	if labels == nil {
		labels = map[string]any{}
	} else {
		copied := make(map[string]any, len(labels)+1)
		for k, v := range labels {
			copied[k] = v
		}
		labels = copied
	}
	labels[LabelManagedBy] = c.managedByValue
	metadata["labels"] = labels
	out["metadata"] = metadata
	return out
}

// isRetriable mirrors statusapplier.isRetriable. Kept private here so the
// applier doesn't drift from the status one if either is updated.
func isRetriable(err error) bool {
	if apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsInternalError(err) {
		return true
	}
	if netErr, ok := errors.AsType[net.Error](err); ok {
		return netErr.Timeout()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if apierrors.IsNotFound(err) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsMethodNotSupported(err) {
		return false
	}
	return true
}


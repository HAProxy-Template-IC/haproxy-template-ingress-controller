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
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
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

	// AnnotationOwnership lets templates flag a rendered resource as
	// jointly owned with another field manager (helm / argocd / kubectl).
	// When set to OwnershipPartial, the applier:
	//   - does NOT inject the managed-by label (the resource isn't
	//     ours to claim end-to-end);
	//   - does NOT track the resource for orphan-delete (vanishing from
	//     the rendered set must release SSA-owned fields, never delete
	//     the whole object — that would clobber the chart's static
	//     spec);
	//   - always strips the annotation from the payload before SSA so
	//     it remains a controller-internal flag.
	// SSA's per-list-map-entry ownership (e.g. Service.spec.ports keyed
	// by (port, protocol)) handles the actual field-level merge with
	// the other field manager.
	AnnotationOwnership = "haproxy-haptic.org/ownership"

	// OwnershipPartial is the AnnotationOwnership value that activates
	// partial-ownership mode. Any other value (including absence) means
	// full ownership: existing behaviour, unchanged.
	OwnershipPartial = "partial"
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
	eventBus        *busevents.EventBus
	eventChan       <-chan busevents.Event
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	gvrResolver     GVRResolver
	logger          *slog.Logger
	healthTracker   *lifecycle.HealthTracker

	// ownNamespace is the namespace the controller is deployed into. Used
	// to enforce RestrictToOwnNamespace; also the safe target for the
	// "managed-by" label-driven discovery the chart's static-addresses
	// templates use to locate the per-Gateway Services they create.
	ownNamespace           string
	restrictToOwnNamespace bool
	managedByValue         string
	ownerRef               OwnerReference

	// mu protects all mutable state below.
	mu              sync.RWMutex
	isLeader        bool
	cachedResources []templating.RenderedResource
	checksumCache   map[string]string // key: "ns/name/gvr" → sha256(payload)
	lastAppliedKeys map[string]appliedKeyMeta
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

	// DiscoveryClient is used on leader-acquire to enumerate every
	// namespace-scoped API resource type the cluster supports, so the
	// applier can rebuild its in-memory `lastAppliedKeys` from cluster
	// state via the managed-by label selector. Without this, resources
	// the controller applied before a crash but whose desired state was
	// removed while the controller was down (e.g. user deleted the
	// Gateway during a controller upgrade) would leak as orphans until
	// manually swept. Optional: when nil, startup-orphan recovery is
	// skipped and operators must rely on the
	// `kubectl get … -l haproxy-haptic.org/managed-by=<name>` mitigation.
	DiscoveryClient discovery.DiscoveryInterface

	GVRResolver GVRResolver
	Logger      *slog.Logger

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

	// OwnerRef identifies the HAProxyTemplateConfig CR that owns the
	// applied resources. The applier injects an `ownerReferences`
	// entry pointing at this object on every full-ownership SSA
	// payload, with `controller: true` and `blockOwnerDeletion: true`
	// so Kubernetes garbage collection cascade-deletes the rendered
	// resources when the CR is removed (e.g. `helm uninstall`).
	//
	// Optional: when zero (UID empty), no OwnerReference is injected.
	// Partial-ownership entries never get an OwnerReference regardless
	// (the chart-static or other field manager already owns the
	// resource end-to-end).
	OwnerRef OwnerReference
}

// OwnerReference is the minimal identity of the HAProxyTemplateConfig
// CR — duplicated here so this package doesn't depend on the apis/
// types just to read four strings.
type OwnerReference struct {
	APIVersion string
	Kind       string
	Name       string
	UID        string
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
		discoveryClient:        cfg.DiscoveryClient,
		gvrResolver:            cfg.GVRResolver,
		logger:                 logger.With("component", ComponentName),
		healthTracker:          lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		ownNamespace:           cfg.OwnNamespace,
		restrictToOwnNamespace: cfg.RestrictToOwnNamespace,
		managedByValue:         managedBy,
		ownerRef:               cfg.OwnerRef,
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

// handleBecameLeader clears the checksum cache and rebuilds
// lastAppliedKeys from cluster state via the managed-by label so
// orphans surviving controller-down deletions get pruned on the next
// reconciliation. The previous leader's *checksums* aren't valid for
// us (API resource versions reflect their applies, not ours), but the
// *set* of resources we own is determined by the cluster, not by any
// in-memory state — recovering it from the cluster is the only way
// to guarantee no leaks.
//
// Discovery is best-effort: types we don't have RBAC to list (most of
// them — chart Role only grants a handful) return 403/Forbidden and
// are silently skipped. The recovery completes regardless; missed
// types just keep the existing manual-sweep mitigation as fallback.
func (c *Component) handleBecameLeader(ctx context.Context) {
	c.mu.Lock()
	c.isLeader = true
	c.checksumCache = make(map[string]string)
	c.lastAppliedKeys = make(map[string]appliedKeyMeta)
	resources := c.cachedResources
	c.mu.Unlock()
	c.logger.Info("became leader, clearing resource checksum cache")

	if c.discoveryClient != nil {
		c.recoverManagedResources(ctx)
	}

	if len(resources) > 0 {
		c.applyAndPrune(ctx, resources)
	}
}

// recoverManagedResources populates lastAppliedKeys from cluster state by
// listing every namespace-scoped resource type that supports list+delete
// and matches our managed-by label. Resource-agnostic by design: the
// applier discovers what it owns via the label, not via a hardcoded
// type list.
func (c *Component) recoverManagedResources(ctx context.Context) {
	if c.ownNamespace == "" {
		c.logger.Debug("skipping managed-resource recovery — OwnNamespace is empty")
		return
	}
	apiResourceLists, err := c.discoveryClient.ServerPreferredNamespacedResources()
	// ServerPreferredNamespacedResources returns partial results when some
	// API groups are unavailable (e.g. APIService not ready). We process
	// what we got rather than aborting — the missing groups will be
	// covered by subsequent reconciliations as the controller observes
	// applies on those types.
	if err != nil && len(apiResourceLists) == 0 {
		c.logger.Warn("managed-resource recovery failed: discovery returned no resources", "error", err)
		return
	}

	labelSelector := fmt.Sprintf("%s=%s", LabelManagedBy, c.managedByValue)
	recovered := 0
	skipped := 0
	for _, list := range apiResourceLists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			// Subresources (e.g. /status, /scale) appear in discovery with
			// "/" in their name; skip them — they aren't independently
			// listable as parents.
			if strings.Contains(r.Name, "/") {
				continue
			}
			if !verbsContain(r.Verbs, "list") || !verbsContain(r.Verbs, "delete") {
				continue
			}
			gvr := gv.WithResource(r.Name)
			items, err := c.listSafely(ctx, gvr, labelSelector)
			if err != nil {
				// 403 (no RBAC), 404 (CRD removed since discovery), and
				// MethodNotSupported (virtual resources) are expected and
				// silently skipped — the applier discovers what it can,
				// not what it must.
				skipped++
				continue
			}
			for i := range items.Items {
				obj := &items.Items[i]
				key := fmt.Sprintf("%s/%s/%s", obj.GetNamespace(), obj.GetName(), gvr.String())
				c.mu.Lock()
				c.lastAppliedKeys[key] = appliedKeyMeta{
					GVR:       gvr,
					Namespace: obj.GetNamespace(),
					Name:      obj.GetName(),
				}
				c.mu.Unlock()
				recovered++
			}
		}
	}
	if recovered > 0 || skipped > 0 {
		c.logger.Info("managed-resource recovery complete",
			"recovered", recovered, "skipped_types", skipped)
	}
}

// listSafely wraps the dynamic client's List with panic recovery. The
// real Kubernetes client never panics on a missing GVR — discovery
// already vouched for the type — but the dynamic-client fake (and any
// future test double or mis-registered scheme) does, and we'd rather
// skip the type than blow up the whole recovery on one buggy entry.
func (c *Component) listSafely(ctx context.Context, gvr schema.GroupVersionResource, labelSelector string) (items *unstructuredList, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Debug("dynamic-client panic during managed-resource recovery, skipping",
				"gvr", gvr.String(), "panic", r)
			items = nil
			err = fmt.Errorf("recovered: %v", r)
		}
	}()
	return c.dynamicClient.Resource(gvr).Namespace(c.ownNamespace).List(
		ctx, metav1.ListOptions{LabelSelector: labelSelector})
}

// unstructuredList aliases the dynamic-client return type so the
// listSafely signature stays readable.
type unstructuredList = unstructured.UnstructuredList

// verbsContain returns true if the verb is present in the slice.
// Mirrors what k8s.io/apimachinery does internally; kept private here
// to avoid a wider import surface.
func verbsContain(verbs metav1.Verbs, target string) bool {
	for _, v := range verbs {
		if v == target {
			return true
		}
	}
	return false
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
		partial := isPartialOwnership(r)
		// Track for orphan-delete only when haptic owns the resource
		// end-to-end. Partial-ownership entries are jointly owned with
		// another field manager (helm/argocd) and must never be deleted
		// — SSA's per-field ownership handles the actual cleanup when a
		// field disappears from haptic's rendered spec.
		if !partial {
			desiredKeys[key] = appliedKeyMeta{GVR: gvr, Namespace: r.Namespace, Name: r.Name}
		}

		object := c.prepareForApply(r.Object, partial)
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
		if !partial {
			c.lastAppliedKeys[key] = desiredKeys[key]
		}
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

// metadataNamespace reads metadata.namespace from a metadata map,
// returning "" when absent (cluster-scoped resources, or the
// namespace simply not yet injected).
func metadataNamespace(metadata map[string]any) string {
	ns, _ := metadata["namespace"].(string)
	return ns
}

// isPartialOwnership returns true when the rendered resource carries
// the AnnotationOwnership=OwnershipPartial annotation. Templates set this
// to flag a resource as jointly owned with another field manager.
func isPartialOwnership(r *templating.RenderedResource) bool {
	metadata, _ := r.Object["metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		return false
	}
	val, _ := annotations[AnnotationOwnership].(string)
	return val == OwnershipPartial
}

// prepareForApply builds the SSA payload from a rendered resource's
// object. It always strips AnnotationOwnership (controller-internal flag,
// must not reach the apiserver) and, for full-ownership resources,
// injects the managed-by label so operators can locate everything haptic
// owns. Partial-ownership resources skip the label because the resource
// isn't haptic's to claim end-to-end. Returns a NEW map so the caller's
// object isn't mutated.
func (c *Component) prepareForApply(object map[string]any, partial bool) map[string]any {
	out := make(map[string]any, len(object))
	for k, v := range object {
		out[k] = v
	}

	metadata, _ := out["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	} else {
		copied := make(map[string]any, len(metadata))
		for k, v := range metadata {
			copied[k] = v
		}
		metadata = copied
	}

	if annotations, _ := metadata["annotations"].(map[string]any); annotations != nil {
		copiedAnn := make(map[string]any, len(annotations))
		for k, v := range annotations {
			if k == AnnotationOwnership {
				continue
			}
			copiedAnn[k] = v
		}
		if len(copiedAnn) > 0 {
			metadata["annotations"] = copiedAnn
		} else {
			delete(metadata, "annotations")
		}
	}

	if !partial {
		labels, _ := metadata["labels"].(map[string]any)
		copiedLabels := make(map[string]any, len(labels)+1)
		for k, v := range labels {
			copiedLabels[k] = v
		}
		copiedLabels[LabelManagedBy] = c.managedByValue
		metadata["labels"] = copiedLabels

		// Inject OwnerReference to the HAProxyTemplateConfig CR so
		// Kubernetes garbage collection cascade-deletes resources
		// when the CR is removed (e.g. `helm uninstall`). Skipped
		// when the chart hasn't supplied a CR identity (UID empty)
		// or for cross-namespace resources — Kubernetes rejects
		// cross-namespace ownerRefs.
		if c.ownerRef.UID != "" && (c.ownNamespace == "" || metadataNamespace(metadata) == c.ownNamespace) {
			metadata["ownerReferences"] = []any{
				map[string]any{
					"apiVersion":         c.ownerRef.APIVersion,
					"kind":               c.ownerRef.Kind,
					"name":               c.ownerRef.Name,
					"uid":                c.ownerRef.UID,
					"controller":         true,
					"blockOwnerDeletion": true,
				},
			}
		}
	}

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


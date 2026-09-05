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
// It consumes resources from the authenticated render-cycle snapshot, applies
// them with Server-Side Apply, and prunes previously owned resources by exact
// UID and resourceVersion. Resource kinds remain template-defined.
package resourceapplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "resource-applier"

	// EventBufferSize is the size of the event subscription buffer.
	// High volume: reconciliation.completed fires on every reconcile. Use a
	// Publishing-tier buffer so churn bursts don't drop these (coalescible)
	// events before this applier drains them.
	EventBufferSize = busevents.PublishingSubscriberBuffer

	// fieldManager is the SSA field manager name. Same value as
	// statusapplier deliberately — both subsystems are part of the same
	// controller and a single field-manager identity is the simplest
	// audit story (`kubectl get <kind> -o jsonpath='{.metadata.managedFields[?(@.manager=="haptic")]}'`).
	fieldManager = "haptic"

	// LabelManagedBy is injected onto every applied resource so operators
	// can locate everything the controller owns with a single
	// `kubectl get … -l haproxy-haptic.org/managed-by=<name>` selector.
	LabelManagedBy = "haproxy-haptic.org/managed-by"

	// DefaultManagedByValue is the value injected into LabelManagedBy when
	// the chart doesn't override Config.ManagedByValue. Distinct deployments
	// in the same namespace should set their own values.
	DefaultManagedByValue = "haptic-controller"

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
// statusapplier.Component. State lives only on the active leader; replicas in
// standby just observe events.
type Component struct {
	*component.Base

	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	gvrResolver     GVRResolver
	healthTracker   *lifecycle.HealthTracker

	// ctx is the event-loop context captured by Start. Handlers run only
	// on the loop goroutine and use it for Kubernetes API calls so SSA
	// applies and orphan deletes abort on shutdown.
	ctx context.Context

	// ownNamespace is the namespace the controller is deployed into. Used
	// to enforce RestrictToOwnNamespace; also the safe target for the
	// "managed-by" label-driven discovery the chart's templates use to
	// locate the owned resources they create.
	ownNamespace           string
	restrictToOwnNamespace bool
	managedByValue         string
	ownerRef               OwnerReference

	// mu protects all mutable state below.
	mu              sync.RWMutex
	isLeader        bool
	lastAppliedKeys map[string]appliedKeyMeta

	gatePinned        bool
	heldCycle         *resourceCycle
	appliedCycle      *resourceCycle
	gateAcceptedCycle *resourceCycle
	passingVerdict    *resourceGateIdentity
	revertCycle       *resourceCycle
}

type appliedKeyMeta struct {
	GVR             schema.GroupVersionResource
	Namespace       string
	Name            string
	UID             types.UID
	ResourceVersion string
}

type resourceCycle struct {
	occurrence    *rendercycle.Occurrence
	resources     *templating.RenderedResourceSnapshot
	correlationID string
	sourceEventID string
}

type resourceGateIdentity struct {
	occurrence *rendercycle.Occurrence
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
	// removed while the controller was down (e.g. the user deleted the
	// parent resource during a controller upgrade) would leak as orphans
	// until manually swept. Optional: when nil, startup-orphan recovery is
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
		managedBy = DefaultManagedByValue
	}

	c := &Component{
		dynamicClient:          cfg.DynamicClient,
		discoveryClient:        cfg.DiscoveryClient,
		gvrResolver:            cfg.GVRResolver,
		healthTracker:          lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		ownNamespace:           cfg.OwnNamespace,
		restrictToOwnNamespace: cfg.RestrictToOwnNamespace,
		managedByValue:         managedBy,
		ownerRef:               cfg.OwnerRef,
		lastAppliedKeys:        make(map[string]appliedKeyMeta),
	}
	// Typed subscription (EventTypes, not a catch-all) — the bus prefilters
	// by event type at publish, so the buffer holds ONLY the events we
	// dispatch on. With a catch-all subscription the buffer fills within
	// seconds during conformance setup (resource.index.updated and
	// reconciliation.* fire at kHz) and overflows, silently dropping
	// reconciliation.completed — the event carrying the owned resources to
	// apply. statusapplier hit exactly that in CI and uses the same narrowed
	// subscription; this is its sibling.
	c.Base = component.New(&component.Config{
		EventBus:   cfg.EventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeReconciliationCompleted,
			events.EventTypeRenderGateCompleted,
			events.EventTypeBecameLeader,
			events.EventTypeLostLeadership,
		},
	})
	return c
}

// CoalescesOn opts this applier into component.Base's mailbox coalescing:
// under churn only the LATEST reconciliation.completed matters (it carries the
// latest rendered resources, superseding earlier ones), so runs of them
// collapse in the mailbox and the bus can never overflow this subscriber.
func (c *Component) CoalescesOn() []string {
	return []string{events.EventTypeReconciliationCompleted}
}

// HealthCheck returns nil if the component is healthy.
func (c *Component) HealthCheck() error { return c.healthTracker.Check() }

// Start captures the loop context for handlers and runs the embedded
// component.Base event loop until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.ctx = ctx
	return c.Base.Start(ctx)
}

// HandleEvent implements component.EventHandler: it fans out by event type,
// tracking processing time for the health check. Mirror of statusapplier's
// HandleEvent shape — different events because resource lifecycle is
// "rendered → deployed (= apply)" rather than the four-phase status patches
// use.
func (c *Component) HandleEvent(event busevents.Event) {
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	ctx := c.ctx
	switch e := event.(type) {
	case *events.ReconciliationCompletedEvent:
		c.handleReconciliationCompleted(ctx, e)
	case *events.RenderGateCompletedEvent:
		c.handleRenderGateCompleted(ctx, e)
	case *events.BecameLeaderEvent:
		c.handleBecameLeader(ctx)
	case *events.LostLeadershipEvent:
		c.handleLostLeadership()
	}
}

func (c *Component) handleReconciliationCompleted(ctx context.Context, event *events.ReconciliationCompletedEvent) {
	cycle, err := captureResourceCycle(event)
	if err != nil {
		c.Logger().Error("Rendered cycle has invalid provenance", "error", err)
		return
	}
	c.mu.Lock()
	if !c.isLeader {
		c.mu.Unlock()
		return
	}
	pinned := c.gatePinned
	if pinned {
		c.heldCycle = cycle
	}
	passing := c.passingVerdict
	revert := c.revertCycle
	applied := c.appliedCycle
	c.mu.Unlock()

	if pinned {
		if sameResourceCycleGate(cycle, passing) {
			c.releaseHeldCycle(ctx, cycle)
			return
		}
		if revert != nil && !sameResourceCycle(applied, revert) {
			c.revertAppliedCycle(ctx, revert)
			return
		}
		c.Logger().Warn("Render gate is holding renders; not applying this cycle's resources yet",
			"correlation_id", cycle.correlationID)
		return
	}
	if err := c.convergeCycle(ctx, cycle); err != nil {
		c.Logger().Error("Rendered resources did not converge; status publication deferred",
			"error", err,
			"correlation_id", cycle.correlationID)
		return
	}
	c.rememberAppliedCycle(cycle, false)
	c.publishResourcesApplied(cycle)
}

// handleRenderGateCompleted mirrors the render gate's latch.
//
// A refusal holds the resources of every later cycle and puts back the ones of
// the cycle HAProxy accepted, because these objects have to describe what the
// fleet runs — the deployer reverted the pods to that same render. The pass
// that names a held cycle applies it. Verdicts for superseded plans judge a
// render this applier has moved past and are ignored.
func (c *Component) handleRenderGateCompleted(ctx context.Context, event *events.RenderGateCompletedEvent) {
	if event == nil || !event.Newest {
		return
	}
	gate, err := captureResourceGateIdentity(event)
	if err != nil {
		c.Logger().Error("Render gate verdict has invalid provenance", "error", err)
		return
	}

	c.mu.Lock()
	isLeader := c.isLeader
	if !isLeader {
		c.mu.Unlock()
		return
	}
	if !event.OK {
		c.gatePinned = true
		c.passingVerdict = nil
		if event.Refused {
			c.revertCycle = c.gateAcceptedCycle
			if sameResourceCycle(c.appliedCycle, c.revertCycle) {
				c.revertCycle = nil
			}
		} else {
			c.revertCycle = nil
		}
		revert := c.revertCycle
		applied := c.appliedCycle
		c.mu.Unlock()
		if revert != nil && !sameResourceCycle(applied, revert) {
			c.revertAppliedCycle(ctx, revert)
		}
		return
	}

	c.passingVerdict = gate
	c.revertCycle = nil
	held := c.heldCycle
	applied := c.appliedCycle
	if held == nil && sameResourceCycleGate(applied, gate) {
		c.gatePinned = false
		c.gateAcceptedCycle = applied
	}
	c.mu.Unlock()

	if held == nil || !sameResourceCycleGate(held, gate) {
		return
	}
	c.releaseHeldCycle(ctx, held)
}

func (c *Component) convergeCycle(ctx context.Context, cycle *resourceCycle) error {
	resources, err := cycle.resources.Resources()
	if err != nil {
		return fmt.Errorf("materialize rendered resources: %w", err)
	}
	return c.applyAndPrune(ctx, resources)
}

func (c *Component) releaseHeldCycle(ctx context.Context, cycle *resourceCycle) {
	if err := c.convergeCycle(ctx, cycle); err != nil {
		c.Logger().Error("Rendered resources did not converge after the render gate's verdict",
			"error", err, "correlation_id", cycle.correlationID)
		return
	}

	c.mu.Lock()
	if !c.isLeader || !c.gatePinned || !sameResourceCycle(c.heldCycle, cycle) ||
		!sameResourceCycleGate(cycle, c.passingVerdict) {
		c.mu.Unlock()
		return
	}
	c.gatePinned = false
	c.heldCycle = nil
	c.revertCycle = nil
	c.appliedCycle = cycle
	c.gateAcceptedCycle = cycle
	c.mu.Unlock()
	c.publishResourcesApplied(cycle)
}

func (c *Component) revertAppliedCycle(ctx context.Context, cycle *resourceCycle) {
	if err := c.convergeCycle(ctx, cycle); err != nil {
		c.Logger().Error("Rendered resources did not converge while restoring the accepted cycle",
			"error", err, "correlation_id", cycle.correlationID)
		return
	}

	c.mu.Lock()
	if !c.isLeader || !sameResourceCycle(c.revertCycle, cycle) {
		c.mu.Unlock()
		return
	}
	c.appliedCycle = cycle
	c.revertCycle = nil
	c.mu.Unlock()
	c.publishResourcesApplied(cycle)
}

func captureResourceCycle(event *events.ReconciliationCompletedEvent) (*resourceCycle, error) {
	if event == nil {
		return nil, errors.New("reconciliation cycle is nil")
	}
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		return nil, fmt.Errorf("reconciliation completion occurrence: %w", err)
	}
	snapshot, err := occurrence.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("reconciliation completion cycle: %w", err)
	}
	resources, err := snapshot.RenderedResourceSnapshot()
	if err != nil {
		return nil, err
	}
	return &resourceCycle{
		occurrence:    occurrence,
		resources:     resources,
		correlationID: event.CorrelationID(), sourceEventID: event.EventID(),
	}, nil
}

func captureResourceGateIdentity(event *events.RenderGateCompletedEvent) (*resourceGateIdentity, error) {
	if event == nil {
		return nil, errors.New("render gate verdict is nil")
	}
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		return nil, fmt.Errorf("render gate occurrence: %w", err)
	}
	return &resourceGateIdentity{occurrence: occurrence}, nil
}

func sameResourceCycleGate(cycle *resourceCycle, gate *resourceGateIdentity) bool {
	if cycle == nil || gate == nil || cycle.occurrence == nil || gate.occurrence == nil {
		return false
	}
	same, err := cycle.occurrence.Same(gate.occurrence)
	return err == nil && same
}

func sameResourceCycle(left, right *resourceCycle) bool {
	if left == nil || right == nil || left.occurrence == nil || right.occurrence == nil {
		return false
	}
	same, err := left.occurrence.Same(right.occurrence)
	return err == nil && same
}

func (c *Component) rememberAppliedCycle(cycle *resourceCycle, gateAccepted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appliedCycle = cycle
	if gateAccepted || sameResourceCycleGate(cycle, c.passingVerdict) {
		c.gateAcceptedCycle = cycle
	}
}

func (c *Component) publishResourcesApplied(cycle *resourceCycle) {
	correlation := events.WithCorrelation(cycle.correlationID, cycle.sourceEventID)
	applied, err := events.NewResourcesAppliedEventWithCycle(
		cycle.occurrence, correlation,
	)
	if err != nil {
		c.Logger().Error("Applied resources could not retain render-cycle identity", "error", err)
		return
	}
	c.EventBus().Publish(applied)
}

// handleBecameLeader rebuilds deletion lineage from live managed resources so
// orphans surviving a controller restart can be pruned by the next cycle.
//
// No resource replay here: the Reconciler triggers an immediate
// reconciliation on BecameLeaderEvent (see pkg/controller/reconciler/CLAUDE.md),
// producing a fresh ReconciliationCompletedEvent carrying the current
// rendered set. Replay would be wrong — at the moment of becoming
// leader the applier has no state to replay from (by design).
//
// Discovery is best-effort: types we don't have RBAC to list (most of
// them — chart Role only grants a handful) return 403/Forbidden and
// are silently skipped. The recovery completes regardless; missed
// types just keep the existing manual-sweep mitigation as fallback.
func (c *Component) handleBecameLeader(ctx context.Context) {
	c.mu.Lock()
	c.isLeader = true
	c.lastAppliedKeys = make(map[string]appliedKeyMeta)
	c.resetCycleStateLocked()
	c.mu.Unlock()
	c.Logger().Info("Became leader, rebuilding managed resource state")

	if c.discoveryClient != nil {
		c.recoverManagedResources(ctx)
	}
}

// recoverManagedResources populates lastAppliedKeys from cluster state by
// listing every namespace-scoped resource type that supports list+delete
// and matches our managed-by label. Resource-agnostic by design: the
// applier discovers what it owns via the label, not via a hardcoded
// type list.
func (c *Component) recoverManagedResources(ctx context.Context) {
	if c.ownNamespace == "" {
		c.Logger().Debug("Skipping managed-resource recovery — OwnNamespace is empty")
		return
	}
	apiResourceLists, err := c.discoveryClient.ServerPreferredNamespacedResources()
	// ServerPreferredNamespacedResources returns partial results when some
	// API groups are unavailable (e.g. APIService not ready). We process
	// what we got rather than aborting — the missing groups will be
	// covered by subsequent reconciliations as the controller observes
	// applies on those types.
	if err != nil && len(apiResourceLists) == 0 {
		c.Logger().Warn("Managed-resource recovery failed: discovery returned no resources", "error", err)
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
		for j := range list.APIResources {
			r := &list.APIResources[j]
			rec, didSkip := c.recoverFromAPIResource(ctx, gv, r, labelSelector)
			recovered += rec
			if didSkip {
				skipped++
			}
		}
	}
	if recovered > 0 || skipped > 0 {
		c.Logger().Info("Managed-resource recovery complete",
			"recovered", recovered, "skipped_types", skipped)
	}
}

// recoverFromAPIResource lists managed objects of one resource type and
// stages each into lastAppliedKeys. Returns the number of objects recovered
// and whether the type itself was skipped (subresource, missing list/delete
// verb, or list call failed).
//
// 403 (no RBAC), 404 (CRD removed since discovery), and MethodNotSupported
// (virtual resources) are expected and silently rolled into the skip count
// — the applier discovers what it can, not what it must.
func (c *Component) recoverFromAPIResource(ctx context.Context, gv schema.GroupVersion, r *metav1.APIResource, labelSelector string) (recovered int, skipped bool) {
	// Subresources (e.g. /status, /scale) appear in discovery with "/" in
	// their name; skip them — they aren't independently listable as
	// parents.
	if strings.Contains(r.Name, "/") {
		return 0, false
	}
	if !verbsContain(r.Verbs, "list") || !verbsContain(r.Verbs, "delete") {
		return 0, false
	}
	gvr := gv.WithResource(r.Name)
	items, err := c.listSafely(ctx, gvr, labelSelector)
	if err != nil {
		return 0, true
	}
	for i := range items.Items {
		obj := &items.Items[i]
		// Filter out resources auto-generated by other controllers that
		// happen to inherit the managed-by label from a chart-applied
		// parent. The canonical case: a Service the chart applied
		// carries the label on its `spec.selector` → the kube
		// endpointslice controller stamps the same label onto every
		// EndpointSlice it auto-creates for that Service. Recovering
		// those EndpointSlices would track them as managed resources
		// and try to delete them on the next prune pass — but the
		// chart's namespace-scoped Role only grants delete on Services
		// + Secrets + the chart's own CRDs, so the delete fails with
		// 403 and the pipeline loops forever logging "failed to delete
		// orphan resource".
		//
		// The applier injects an OwnerReference (UID set) on every
		// chart-applied resource pointing at the HAProxyTemplateConfig
		// CR. Auto-generated children of those resources have a
		// DIFFERENT ownerReference (the parent Service / etc.). So
		// recovering only objects whose ownerReferences include the
		// chart's CR UID excludes the false positives without
		// hardcoding a resource-type denylist.
		//
		// When OwnerRef is unset (cfg.OwnerRef.UID == "") the filter
		// degrades to "recover everything labelled", matching the
		// pre-filter behaviour for deployments that don't plumb the
		// owner CR identity. The chart sets it via
		// configloader.OwnerRefFromHAProxyTemplateConfig, so production
		// always exercises the filter.
		if c.ownerRef.UID != "" && !hasOwnerRefUID(obj, c.ownerRef.UID) {
			continue
		}
		key := fmt.Sprintf("%s/%s/%s", obj.GetNamespace(), obj.GetName(), gvr.String())
		uid, resourceVersion := obj.GetUID(), obj.GetResourceVersion()
		if uid == "" || resourceVersion == "" {
			c.Logger().Warn("Skipping managed resource without deletion lineage",
				"namespace", obj.GetNamespace(), "name", obj.GetName(), "gvr", gvr.String())
			continue
		}
		c.mu.Lock()
		c.lastAppliedKeys[key] = appliedKeyMeta{
			GVR: gvr, Namespace: obj.GetNamespace(), Name: obj.GetName(),
			UID: uid, ResourceVersion: resourceVersion,
		}
		c.mu.Unlock()
		recovered++
	}
	return recovered, false
}

// hasOwnerRefUID returns true when obj.metadata.ownerReferences contains
// an entry with the given UID. Used by recovery to distinguish chart-
// applied resources from foreign auto-generated children that happen
// to share the managed-by label via label inheritance from a chart-
// applied parent (EndpointSlice → Service is the canonical case).
func hasOwnerRefUID(obj *unstructured.Unstructured, uid string) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if string(ref.UID) == uid {
			return true
		}
	}
	return false
}

// listSafely wraps the dynamic client's List with panic recovery. The
// real Kubernetes client never panics on a missing GVR — discovery
// already vouched for the type — but the dynamic-client fake (and any
// future test double or mis-registered scheme) does, and we'd rather
// skip the type than blow up the whole recovery on one buggy entry.
func (c *Component) listSafely(ctx context.Context, gvr schema.GroupVersionResource, labelSelector string) (items *unstructuredList, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.Logger().Debug("Dynamic-client panic during managed-resource recovery, skipping",
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
	return slices.Contains(verbs, target)
}

// handleLostLeadership discards every cycle baseline from the old leader term.
func (c *Component) handleLostLeadership() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isLeader {
		c.Logger().Info("Lost leadership, pausing resource applies")
	}
	c.isLeader = false
	c.resetCycleStateLocked()
}

func (c *Component) resetCycleStateLocked() {
	c.gatePinned = false
	c.heldCycle = nil
	c.appliedCycle = nil
	c.gateAcceptedCycle = nil
	c.passingVerdict = nil
	c.revertCycle = nil
}

// applyAndPrune applies the new desired set and deletes any
// previously-applied resources that are no longer in it.
func (c *Component) applyAndPrune(ctx context.Context, resources []templating.RenderedResource) error {
	startTime := time.Now()
	desiredKeys := make(map[string]appliedKeyMeta, len(resources))
	presentKeys := make(map[string]struct{}, len(resources))
	var keysMu sync.Mutex
	var applied, refused, failed atomic.Int64

	// Apply resources CONCURRENTLY (bounded fan-out). A serial loop made each
	// reconciliation's apply pass slow (one SSA round-trip per changed
	// resource), so under churn ReconciliationCompletedEvents piled up past the
	// subscriber buffer and the bus DROPPED them — and a dropped reconciliation
	// means the rendered output resources (HAProxyCfg, map files, …) silently
	// stop tracking reality: an incomplete reconciliation. Mirrors
	// statusapplier's bounded errgroup fan-out, which never overflows. The
	// lastAppliedKeys is c.mu-guarded and the pass-local maps are
	// keysMu-guarded, so concurrent applyOne calls are safe.
	const maxApplyConcurrency = 16
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxApplyConcurrency)
	for i := range resources {
		r := &resources[i]
		g.Go(func() error {
			switch outcome := c.applyOne(gctx, r, desiredKeys, presentKeys, &keysMu); outcome {
			case applyOutcomeError:
				failed.Add(1)
			case applyOutcomeApplied:
				applied.Add(1)
			case applyOutcomeRefused:
				refused.Add(1)
			}
			return nil
		})
	}
	_ = g.Wait()

	failedN := int(failed.Load())
	if failedN > 0 {
		return fmt.Errorf("%d of %d rendered resources failed; retry occurs on the next reconciliation", failedN, len(resources))
	}

	deleted, deleteFailures := c.pruneOrphans(ctx, desiredKeys, presentKeys)
	if deleteFailures > 0 {
		return fmt.Errorf("%d orphaned resources could not be deleted; retry occurs on the next reconciliation", deleteFailures)
	}

	appliedN, refusedN := int(applied.Load()), int(refused.Load())
	if appliedN+deleted+refusedN > 0 {
		c.Logger().Debug("Resource applier pass complete",
			"applied", appliedN,
			"deleted", deleted, "refused", refusedN,
			"duration_ms", time.Since(startTime).Milliseconds())
	}
	return nil
}

// applyOutcome enumerates per-resource results so applyAndPrune can keep
// counters without an inline type-switch.
type applyOutcome int

const (
	applyOutcomeError   applyOutcome = iota // resolve / marshal / apply failed; logged inside applyOne
	applyOutcomeApplied                     // SSA succeeded
	applyOutcomeRefused                     // policy refused (cross-namespace under RestrictToOwnNamespace)
)

// withCreateOnlyFieldsFromLive returns the object to apply with the fields the
// configuration says this template sets only at creation replaced by the values
// the object already has.
//
// Omitting them instead does not work: server-side apply DELETES a field its
// only owner stops sending, so `spec.replicas` disappeared and the API server
// defaulted the workload to one replica. The co-owner that then appears is
// kube-controller-manager writing that default, which is the damage, not a
// safeguard.
//
// Following the live value keeps HAPTIC's ownership and its guarantee that the
// object is whole, while making the template's value the size the object starts
// at rather than the size it is held to. An operator's `kubectl scale` or an
// autoscaler's decision is read back and re-applied as-is instead of being
// overwritten on the next reconcile.
//
// The read costs one request per declaring resource per pass, which is why it
// runs only for the ones that declare a path. If the object is deleted between
// the read and the apply, the apply recreates it from the template's values.
func (c *Component) withCreateOnlyFieldsFromLive(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	r *templating.RenderedResource,
	object map[string]any,
) (map[string]any, error) {
	if len(r.CreateOnlyFields) == 0 {
		return object, nil
	}
	live, err := c.dynamicClient.Resource(gvr).Namespace(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return object, nil
	}
	if err != nil {
		return nil, err
	}
	out := object
	for _, path := range r.CreateOnlyFields {
		fields := strings.Split(path, ".")
		if len(fields) == 0 || slices.Contains(fields, "") {
			return nil, fmt.Errorf("createOnlyFields path %q is not a dotted field path", path)
		}
		value, found, err := unstructured.NestedFieldNoCopy(live.Object, fields...)
		if err != nil || !found {
			continue
		}
		out = setNestedFieldCopying(out, value, fields...)
	}
	return out, nil
}

// setNestedFieldCopying sets one dotted path, cloning every map it descends
// into.
//
// prepareForApply copies only the top level, so the nested maps are the render
// cache's own. Writing through them would edit the cached object for every
// later render — the same aliasing that, when this deleted instead of set,
// emptied the field out of the cache for good.
func setNestedFieldCopying(object map[string]any, value any, fields ...string) map[string]any {
	out := maps.Clone(object)
	if out == nil {
		out = map[string]any{}
	}
	if len(fields) == 1 {
		out[fields[0]] = value
		return out
	}
	child, _ := out[fields[0]].(map[string]any)
	out[fields[0]] = setNestedFieldCopying(child, value, fields[1:]...)
	return out
}

// applyOne resolves, marshals, and SSA-applies a single rendered resource,
// updates lastAppliedKeys on success, and returns the
// outcome. It also stages the desiredKeys entry so pruneOrphans can compute
// the keep-set after every resource has been processed.
func (c *Component) applyOne(
	ctx context.Context,
	r *templating.RenderedResource,
	desiredKeys map[string]appliedKeyMeta,
	presentKeys map[string]struct{},
	keysMu *sync.Mutex,
) applyOutcome {
	gvr, err := c.gvrResolver.Resolve(r.APIVersion, r.Kind)
	if err != nil {
		c.Logger().Error("Failed to resolve GVR for rendered resource",
			"api_version", r.APIVersion, "kind", r.Kind, "error", err)
		return applyOutcomeError
	}
	if c.refused(r) {
		return applyOutcomeRefused
	}
	key := fmt.Sprintf("%s/%s/%s", r.Namespace, r.Name, gvr.String())
	partial := isPartialOwnership(r)

	object := c.prepareForApply(r.Object, partial)
	object, err = c.withCreateOnlyFieldsFromLive(ctx, gvr, r, object)
	if err != nil {
		c.Logger().Error("Failed to read a rendered resource before applying it",
			"namespace", r.Namespace, "name", r.Name, "gvr", gvr.String(), "error", err)
		return applyOutcomeError
	}
	payload, err := json.Marshal(object)
	if err != nil {
		c.Logger().Error("Failed to marshal rendered resource",
			"namespace", r.Namespace, "name", r.Name, "kind", r.Kind, "error", err)
		return applyOutcomeError
	}
	// No local digest can prove that an unwatched target wasn't mutated or
	// recreated after the previous apply, so every cycle reaches the API server.
	appliedObject, err := c.dynamicClient.Resource(gvr).Namespace(r.Namespace).Patch(
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
		c.Logger().Error("Failed to apply rendered resource",
			"namespace", r.Namespace, "name", r.Name, "gvr", gvr.String(),
			"retriable", statusapplier.IsRetriable(err), "error", err)
		return applyOutcomeError
	}

	keysMu.Lock()
	presentKeys[key] = struct{}{}
	keysMu.Unlock()
	if !partial {
		meta, lineageErr := appliedMetaFromResponse(gvr, r.Namespace, r.Name, appliedObject)
		if lineageErr != nil {
			c.Logger().Error("Applied resource response lacks safe deletion lineage",
				"namespace", r.Namespace, "name", r.Name, "gvr", gvr.String(), "error", lineageErr)
			return applyOutcomeError
		}
		keysMu.Lock()
		desiredKeys[key] = meta
		keysMu.Unlock()
		c.mu.Lock()
		c.lastAppliedKeys[key] = meta
		c.mu.Unlock()
	}
	return applyOutcomeApplied
}

func appliedMetaFromResponse(
	gvr schema.GroupVersionResource,
	namespace, name string,
	object *unstructured.Unstructured,
) (appliedKeyMeta, error) {
	if object == nil {
		return appliedKeyMeta{}, errors.New("SSA response is nil")
	}
	if object.GetName() != name || object.GetNamespace() != namespace {
		return appliedKeyMeta{}, fmt.Errorf(
			"SSA response names %s/%s instead of %s/%s",
			object.GetNamespace(), object.GetName(), namespace, name,
		)
	}
	uid, resourceVersion := object.GetUID(), object.GetResourceVersion()
	if uid == "" || resourceVersion == "" {
		return appliedKeyMeta{}, errors.New("SSA response UID or resourceVersion is empty")
	}
	return appliedKeyMeta{
		GVR: gvr, Namespace: namespace, Name: name, UID: uid, ResourceVersion: resourceVersion,
	}, nil
}

// pruneOrphans deletes resources that were applied last pass but aren't in
// the new desired set.
func (c *Component) pruneOrphans(
	ctx context.Context,
	desiredKeys map[string]appliedKeyMeta,
	presentKeys map[string]struct{},
) (deleted, failed int) {
	c.mu.Lock()
	prior := c.lastAppliedKeys
	stillApplied := maps.Clone(desiredKeys)
	c.mu.Unlock()

	for key, meta := range prior {
		if _, kept := presentKeys[key]; kept {
			continue
		}
		if meta.UID == "" || meta.ResourceVersion == "" {
			c.Logger().Error("Refusing to prune resource without deletion lineage",
				"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String())
			stillApplied[key] = meta
			failed++
			continue
		}
		uid, resourceVersion := meta.UID, meta.ResourceVersion
		err := c.dynamicClient.Resource(meta.GVR).Namespace(meta.Namespace).Delete(
			ctx, meta.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{
				UID: &uid, ResourceVersion: &resourceVersion,
			}},
		)
		if apierrors.IsConflict(err) {
			next, retain, retry := c.resolvePruneConflict(ctx, &meta)
			if retain {
				stillApplied[key] = next
			}
			if retry {
				failed++
			}
			continue
		}
		if err != nil && !apierrors.IsNotFound(err) {
			c.Logger().Error("Failed to delete orphan resource",
				"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String(),
				"error", err)
			// Keep it in the cache so we'll try again next reconciliation.
			stillApplied[key] = meta
			failed++
			continue
		}
		deleted++
	}

	c.mu.Lock()
	c.lastAppliedKeys = stillApplied
	c.mu.Unlock()
	return deleted, failed
}

func (c *Component) resolvePruneConflict(
	ctx context.Context,
	meta *appliedKeyMeta,
) (next appliedKeyMeta, retain, retry bool) {
	object, err := c.dynamicClient.Resource(meta.GVR).Namespace(meta.Namespace).Get(
		ctx, meta.Name, metav1.GetOptions{},
	)
	if apierrors.IsNotFound(err) {
		return appliedKeyMeta{}, false, false
	}
	if err != nil {
		c.Logger().Error("Failed to inspect conflicting orphan resource",
			"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String(), "error", err)
		return *meta, true, true
	}
	if object == nil || object.GetName() != meta.Name || object.GetNamespace() != meta.Namespace {
		c.Logger().Error("Conflicting orphan response has invalid identity",
			"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String())
		return *meta, true, true
	}
	if object.GetUID() != meta.UID {
		c.Logger().Warn("Orphan name now belongs to a different resource; leaving it untouched",
			"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String())
		return appliedKeyMeta{}, false, false
	}
	if !c.ownsAppliedResource(object) {
		c.Logger().Warn("Orphan resource ownership changed; leaving it untouched",
			"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String())
		return appliedKeyMeta{}, false, false
	}
	resourceVersion := object.GetResourceVersion()
	if resourceVersion == "" {
		c.Logger().Error("Conflicting orphan response lacks resourceVersion",
			"namespace", meta.Namespace, "name", meta.Name, "gvr", meta.GVR.String())
		return *meta, true, true
	}
	next = *meta
	next.ResourceVersion = resourceVersion
	return next, true, true
}

func (c *Component) ownsAppliedResource(object *unstructured.Unstructured) bool {
	if object.GetLabels()[LabelManagedBy] != c.managedByValue {
		return false
	}
	return c.ownerRef.UID == "" || hasOwnerRefUID(object, c.ownerRef.UID)
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
		c.Logger().Warn("Refusing to apply resource outside controller namespace",
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
		metadata = maps.Clone(metadata)
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

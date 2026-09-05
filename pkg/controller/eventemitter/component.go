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

// Package eventemitter emits template-requested Kubernetes Events against the
// resources they concern. Templates call recordEvent(resource, reason, message)
// during rendering (the resource's namespace/name/apiVersion/kind are read off
// it); those events ride on ReconciliationCompletedEvent and this leader-only
// component forwards each newly added event to the API server via an EventRecorder.
//
// It is resource-agnostic (RULE #1): every event carries its own
// apiVersion/kind/namespace/name, so the emitter builds a bare
// *corev1.ObjectReference for the involved object and never needs a typed
// client or hardcoded GVK. client-go's reference.GetReference short-circuits
// for an *ObjectReference, so the recorder's scheme need not know the type.
package eventemitter

import (
	"context"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	clientsetscheme "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/scheme"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "event-emitter"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = busevents.StandardSubscriberBuffer

	// eventSourceComponent identifies this controller as the source of the
	// emitted Events (shown as the source in `kubectl describe` / events).
	eventSourceComponent = "haptic-controller"
)

// Component forwards template-recorded Events to the Kubernetes API.
//
// All-replica subscription, leader-gated emission: the source
// ReconciliationCompletedEvent is published only by the leader-only Coordinator,
// and the leader flag is a defensive second gate so a stray event never makes a
// follower double-emit (the EventRecorder aggregates duplicates, but this keeps
// the count clean).
type Component struct {
	*component.Base

	// kubeClient sinks recorded Events to the API server. broadcaster/recorder
	// lifecycle is owned by Start/Stop.
	kubeClient  kubernetes.Interface
	broadcaster record.EventBroadcaster
	recorder    record.EventRecorder

	mu         sync.Mutex
	isLeader   bool
	lastEvents *templating.RenderedEventSnapshot
}

// Config wires the component's dependencies.
type Config struct {
	EventBus   *busevents.EventBus
	KubeClient kubernetes.Interface
	Logger     *slog.Logger
}

// New creates the event emitter. It subscribes during construction (before
// EventBus.Start()) via the shared component.Base scaffold.
func New(cfg *Config) *Component {
	c := &Component{kubeClient: cfg.KubeClient}
	c.Base = component.New(&component.Config{
		EventBus:   cfg.EventBus,
		Logger:     cfg.Logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeReconciliationCompleted,
			events.EventTypeInstanceDeploymentFailed,
			events.EventTypeBecameLeader,
			events.EventTypeLostLeadership,
		},
	})
	return c
}

// Start runs the embedded component.Base event loop. The broadcaster/recorder
// are built lazily on the first BecameLeaderEvent (ensureRecorder), so a replica
// that is only ever a follower never spawns a broadcaster goroutine or an
// API-server Event sink connection — only the leader (which actually emits) pays
// for them.
func (c *Component) Start(ctx context.Context) error {
	defer func() {
		if c.broadcaster != nil {
			c.broadcaster.Shutdown()
		}
	}()
	return c.Base.Start(ctx)
}

// ensureRecorder lazily builds the EventBroadcaster + recorder + API-server sink
// the first time this replica becomes leader. It runs on the single Base
// dispatch goroutine (same as handleReconciliationCompleted), so broadcaster and
// recorder need no lock. Kept across a LostLeadership so leader flapping doesn't
// churn broadcaster goroutines.
func (c *Component) ensureRecorder() {
	if c.recorder != nil {
		return
	}
	c.broadcaster = record.NewBroadcaster()
	c.recorder = c.broadcaster.NewRecorder(clientsetscheme.Scheme, corev1.EventSource{Component: eventSourceComponent})
	c.broadcaster.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: c.kubeClient.CoreV1().Events("")})
}

// HandleEvent implements component.EventHandler.
func (c *Component) HandleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ReconciliationCompletedEvent:
		c.handleReconciliationCompleted(e)
	case *events.InstanceDeploymentFailedEvent:
		c.handleInstanceDeploymentFailed(e)
	case *events.BecameLeaderEvent:
		c.ensureRecorder()
		c.setLeader(true)
	case *events.LostLeadershipEvent:
		c.setLeader(false)
	}
}

func (c *Component) setLeader(v bool) {
	c.mu.Lock()
	c.isLeader = v
	if !v {
		c.lastEvents = nil
	}
	c.mu.Unlock()
}

func (c *Component) leader() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isLeader
}

// handleReconciliationCompleted emits authenticated event-set additions.
func (c *Component) handleReconciliationCompleted(e *events.ReconciliationCompletedEvent) {
	if e == nil {
		return
	}
	occurrence, err := e.RenderOccurrence()
	if err != nil {
		c.Logger().Error("Rendered event occurrence has invalid provenance", "error", err,
			"correlation_id", e.CorrelationID())
		return
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		c.Logger().Error("Rendered event occurrence has invalid provenance", "error", err,
			"correlation_id", e.CorrelationID())
		return
	}
	c.emitCycle(cycle, e.CorrelationID())
}

func (c *Component) emitCycle(cycle *rendercycle.Snapshot, correlationID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isLeader || c.recorder == nil {
		return
	}
	snapshot, err := cycle.RenderedEventSnapshot()
	if err != nil {
		c.Logger().Error("Rendered event cycle has invalid provenance", "error", err,
			"correlation_id", correlationID)
		return
	}
	if c.lastEvents != nil {
		same, sameErr := snapshot.SameRoot(c.lastEvents)
		if sameErr != nil {
			c.Logger().Error("Rendered events have invalid provenance", "error", sameErr,
				"correlation_id", correlationID)
			return
		}
		if same {
			return
		}
	}
	delta, err := snapshot.AddedSince(c.lastEvents)
	if err != nil {
		c.Logger().Error("Rendered event delta has invalid provenance", "error", err,
			"correlation_id", correlationID)
		return
	}
	c.emit(delta)
	c.lastEvents = snapshot
}

func (c *Component) emit(renderedEvents []templating.RenderedEvent) {
	for _, ev := range renderedEvents {
		ref := &corev1.ObjectReference{
			APIVersion: ev.APIVersion,
			Kind:       ev.Kind,
			Namespace:  ev.Namespace,
			Name:       ev.Name,
		}
		c.recorder.Event(ref, ev.Type, ev.Reason, ev.Message)
	}
}

// applyFailedReason is the Event reason on an HAProxy pod whose apply failed
// or was refused; the message carries HAProxy's own words when there are any.
const applyFailedReason = "ApplyFailed"

// handleInstanceDeploymentFailed emits a Warning on the HAProxy pod an apply
// did not reach or that refused it, so the operator finds the cause with
// `kubectl describe pod` and not only in the controller's log.
func (c *Component) handleInstanceDeploymentFailed(e *events.InstanceDeploymentFailedEvent) {
	if !c.leader() || c.recorder == nil {
		return
	}
	endpoint, ok := e.Endpoint.(*dataplane.Endpoint)
	if !ok || endpoint == nil || endpoint.PodName == "" {
		return
	}
	c.recorder.Event(&corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Namespace:  endpoint.PodNamespace,
		Name:       endpoint.PodName,
		UID:        types.UID(endpoint.PodUID),
	}, corev1.EventTypeWarning, applyFailedReason, e.Error)
}

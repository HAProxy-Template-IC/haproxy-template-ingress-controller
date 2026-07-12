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

package eventemitter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// capturedEvent records the arguments of one recorder.Event call.
type capturedEvent struct {
	obj     runtime.Object
	etype   string
	reason  string
	message string
}

// fakeRecorder captures Event() calls for assertions.
type fakeRecorder struct{ events []capturedEvent }

func (f *fakeRecorder) Event(obj runtime.Object, etype, reason, message string) {
	f.events = append(f.events, capturedEvent{obj, etype, reason, message})
}
func (f *fakeRecorder) Eventf(runtime.Object, string, string, string, ...any) {}
func (f *fakeRecorder) AnnotatedEventf(runtime.Object, map[string]string, string, string, string, ...any) {
}

func newRenderedEvent(ns, name string) templating.RenderedEvent {
	return templating.RenderedEvent{
		Namespace:  ns,
		Name:       name,
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Type:       templating.EventTypeWarning,
		Reason:     "RouteConflict",
		Message:    "host x path / already served by " + name,
	}
}

func TestComponent_handleReconciliationCompleted(t *testing.T) {
	t.Run("leader emits one Event per rendered event against a bare ObjectReference", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: true}

		c.handleReconciliationCompleted(&events.ReconciliationCompletedEvent{
			Events: []templating.RenderedEvent{
				newRenderedEvent("team-a", "route-new"),
			},
		})

		require.Len(t, rec.events, 1)
		got := rec.events[0]
		assert.Equal(t, templating.EventTypeWarning, got.etype)
		assert.Equal(t, "RouteConflict", got.reason)
		assert.Contains(t, got.message, "route-new")

		// Resource-agnostic involved object: a bare *corev1.ObjectReference
		// carrying only apiVersion/kind/namespace/name (RULE #1 — no typed
		// client). client-go's reference.GetReference returns it unchanged.
		ref, ok := got.obj.(*corev1.ObjectReference)
		require.True(t, ok, "involved object is a bare ObjectReference")
		assert.Equal(t, "networking.k8s.io/v1", ref.APIVersion)
		assert.Equal(t, "Ingress", ref.Kind)
		assert.Equal(t, "team-a", ref.Namespace)
		assert.Equal(t, "route-new", ref.Name)
	})

	t.Run("follower (not leader) emits nothing", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: false}
		c.handleReconciliationCompleted(&events.ReconciliationCompletedEvent{
			Events: []templating.RenderedEvent{newRenderedEvent("ns", "n")},
		})
		assert.Empty(t, rec.events, "only the leader emits Events")
	})

	t.Run("no rendered events is a no-op", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: true}
		c.handleReconciliationCompleted(&events.ReconciliationCompletedEvent{Events: nil})
		assert.Empty(t, rec.events)
	})

	t.Run("nil recorder (before Start) is a safe no-op", func(t *testing.T) {
		c := &Component{recorder: nil, isLeader: true}
		assert.NotPanics(t, func() {
			c.handleReconciliationCompleted(&events.ReconciliationCompletedEvent{
				Events: []templating.RenderedEvent{newRenderedEvent("ns", "n")},
			})
		})
	})
}

func TestComponent_leaderTransitions(t *testing.T) {
	rec := &fakeRecorder{}
	c := &Component{recorder: rec}

	c.HandleEvent(&events.BecameLeaderEvent{})
	assert.True(t, c.leader())

	c.HandleEvent(&events.LostLeadershipEvent{})
	assert.False(t, c.leader())
}

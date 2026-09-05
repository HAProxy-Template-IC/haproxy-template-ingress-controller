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
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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

func newRenderedEvent(name string) templating.RenderedEvent {
	return templating.RenderedEvent{
		Namespace:  "team-a",
		Name:       name,
		APIVersion: "networking.k8s.io/v1",
		Kind:       "Ingress",
		Type:       templating.EventTypeWarning,
		Reason:     "RouteConflict",
		Message:    "host x path / already served by " + name,
	}
}

type eventCycleFixture struct {
	outputAuthority *renderoutput.Authority
	cycleAuthority  *rendercycle.Authority
	artifacts       *renderartifact.Snapshot
	status          *templating.StatusPatchSnapshot
	resources       *templating.RenderedResourceSnapshot
}

func newEventCycleFixture(t *testing.T) *eventCycleFixture {
	t.Helper()
	artifactAuthority := renderartifact.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
	require.NoError(t, err)
	cycleAuthority, err := rendercycle.NewAuthority(outputAuthority)
	require.NoError(t, err)
	artifactBuilder, err := renderartifact.NewBuilder(artifactAuthority, nil)
	require.NoError(t, err)
	artifacts, err := artifactBuilder.Build()
	require.NoError(t, err)
	status, err := templating.NewStatusPatchCollector().Snapshot()
	require.NoError(t, err)
	resources, err := templating.NewRenderedResourceCollector().Snapshot()
	require.NoError(t, err)
	return &eventCycleFixture{
		outputAuthority: outputAuthority, cycleAuthority: cycleAuthority,
		artifacts: artifacts, status: status, resources: resources,
	}
}

func (f *eventCycleFixture) snapshot(
	t *testing.T,
	config string,
	renderedEvents []templating.RenderedEvent,
	previous *rendercycle.Snapshot,
) *rendercycle.Snapshot {
	t.Helper()
	collector := templating.NewEventCollector()
	for _, rendered := range renderedEvents {
		require.NoError(t, collector.Register(
			rendered.Namespace, rendered.Name, rendered.APIVersion, rendered.Kind,
			rendered.Type, rendered.Reason, rendered.Message,
		))
	}
	var previousEvents *templating.RenderedEventSnapshot
	var previousOutput *renderoutput.Snapshot
	if previous != nil {
		var err error
		previousEvents, err = previous.RenderedEventSnapshot()
		require.NoError(t, err)
		previousOutput, err = previous.OutputSnapshot()
		require.NoError(t, err)
	}
	eventSnapshot, err := collector.Snapshot(previousEvents)
	require.NoError(t, err)
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	plan.ComputeID()
	output, err := renderoutput.NewSnapshot(
		f.outputAuthority, config, plan, f.artifacts, previousOutput,
	)
	require.NoError(t, err)
	cycle, err := rendercycle.NewSnapshot(
		f.cycleAuthority, output, f.status, eventSnapshot, f.resources, previous,
	)
	require.NoError(t, err)
	return cycle
}

func newTestComponent(t *testing.T, recorder record.EventRecorder) *Component {
	t.Helper()
	c := New(&Config{
		EventBus: busevents.NewEventBus(16),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	c.recorder = recorder
	c.isLeader = true
	return c
}

func completedEvent(t *testing.T, cycle *rendercycle.Snapshot) *events.ReconciliationCompletedEvent {
	t.Helper()
	occurrence, err := rendercycle.NewOccurrence(cycle)
	require.NoError(t, err)
	event, err := events.NewReconciliationCompletedEventWithCycle(0, occurrence)
	require.NoError(t, err)
	return event
}

func TestComponentHandleReconciliationCompletedEmitsAuthenticatedEvent(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route-new"),
	}, nil)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.handleReconciliationCompleted(completedEvent(t, cycle))

	require.Len(t, recorder.events, 1)
	got := recorder.events[0]
	assert.Equal(t, templating.EventTypeWarning, got.etype)
	assert.Equal(t, "RouteConflict", got.reason)
	assert.Contains(t, got.message, "route-new")
	ref, ok := got.obj.(*corev1.ObjectReference)
	require.True(t, ok, "involved object is a bare ObjectReference")
	assert.Equal(t, "networking.k8s.io/v1", ref.APIVersion)
	assert.Equal(t, "Ingress", ref.Kind)
	assert.Equal(t, "team-a", ref.Namespace)
	assert.Equal(t, "route-new", ref.Name)
}

func TestComponentSameEventChildAcrossDifferentCyclesDoesNotReemit(t *testing.T) {
	fixture := newEventCycleFixture(t)
	rendered := []templating.RenderedEvent{newRenderedEvent("route")}
	first := fixture.snapshot(t, "global\n", rendered, nil)
	second := fixture.snapshot(t, "global\n  daemon\n", rendered, first)
	sameCycle, err := first.SameRoot(second)
	require.NoError(t, err)
	assert.False(t, sameCycle)
	firstEvents, err := first.RenderedEventSnapshot()
	require.NoError(t, err)
	secondEvents, err := second.RenderedEventSnapshot()
	require.NoError(t, err)
	sameEvents, err := firstEvents.SameRoot(secondEvents)
	require.NoError(t, err)
	assert.True(t, sameEvents)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.handleReconciliationCompleted(completedEvent(t, first))
	c.handleReconciliationCompleted(completedEvent(t, second))

	require.Len(t, recorder.events, 1)
}

func TestComponentOneEventDeltaDoesNotReemitUnchangedEntries(t *testing.T) {
	fixture := newEventCycleFixture(t)
	stable := newRenderedEvent("stable")
	first := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		stable, newRenderedEvent("removed"),
	}, nil)
	second := fixture.snapshot(t, "global\n  daemon\n", []templating.RenderedEvent{
		stable, newRenderedEvent("added"),
	}, first)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.handleReconciliationCompleted(completedEvent(t, first))
	c.handleReconciliationCompleted(completedEvent(t, second))

	require.Len(t, recorder.events, 3)
	names := make([]string, 0, len(recorder.events))
	for _, recorded := range recorder.events {
		ref := recorded.obj.(*corev1.ObjectReference)
		names = append(names, ref.Name)
	}
	assert.Equal(t, []string{"removed", "stable", "added"}, names)
}

func TestComponentDuplicateCycleDoesNotMaterialize(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route"),
	}, nil)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)
	event := completedEvent(t, cycle)

	c.handleReconciliationCompleted(event)
	c.handleReconciliationCompleted(event)

	require.Len(t, recorder.events, 1)
}

func TestComponentInvalidCycleDoesNotPoisonCache(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("authenticated"),
	}, nil)
	copied := *cycle
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)
	poisoned := &events.ReconciliationCompletedEvent{
		CycleSnapshot: &copied,
		Events:        []templating.RenderedEvent{newRenderedEvent("poison")},
	}

	c.handleReconciliationCompleted(poisoned)
	c.handleReconciliationCompleted(completedEvent(t, cycle))

	require.Len(t, recorder.events, 1)
	ref := recorder.events[0].obj.(*corev1.ObjectReference)
	assert.Equal(t, "authenticated", ref.Name)
}

func TestComponentMissingCycleIgnoresUnauthenticatedShadows(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("authenticated"),
	}, nil)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)
	c.handleReconciliationCompleted(&events.ReconciliationCompletedEvent{
		Events:        []templating.RenderedEvent{newRenderedEvent("poison")},
		EventSnapshot: &templating.RenderedEventSnapshot{},
	})

	assert.Empty(t, recorder.events)
	c.handleReconciliationCompleted(completedEvent(t, cycle))
	require.Len(t, recorder.events, 1)
	ref := recorder.events[0].obj.(*corev1.ObjectReference)
	assert.Equal(t, "authenticated", ref.Name)
}

func TestComponentCycleIgnoresPoisonedMutableAndSnapshotShadows(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("authenticated"),
	}, nil)
	event := completedEvent(t, cycle)
	event.Events = []templating.RenderedEvent{newRenderedEvent("poison")}
	event.EventSnapshot = &templating.RenderedEventSnapshot{}
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.handleReconciliationCompleted(event)

	require.Len(t, recorder.events, 1)
	ref := recorder.events[0].obj.(*corev1.ObjectReference)
	assert.Equal(t, "authenticated", ref.Name)
}

func TestComponentABARemitsTheReturningEvent(t *testing.T) {
	fixture := newEventCycleFixture(t)
	eventA := newRenderedEvent("route-a")
	eventB := newRenderedEvent("route-b")
	firstA := fixture.snapshot(t, "global\n", []templating.RenderedEvent{eventA}, nil)
	b := fixture.snapshot(t, "global\n  daemon\n", []templating.RenderedEvent{eventB}, firstA)
	secondA := fixture.snapshot(t, "global\n  master-worker\n", []templating.RenderedEvent{eventA}, b)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.handleReconciliationCompleted(completedEvent(t, firstA))
	c.handleReconciliationCompleted(completedEvent(t, b))
	c.handleReconciliationCompleted(completedEvent(t, secondA))

	require.Len(t, recorder.events, 3)
	assert.Equal(t, []string{"route-a", "route-b", "route-a"}, []string{
		recorder.events[0].obj.(*corev1.ObjectReference).Name,
		recorder.events[1].obj.(*corev1.ObjectReference).Name,
		recorder.events[2].obj.(*corev1.ObjectReference).Name,
	})
}

type panicRecorder struct{}

func (*panicRecorder) Event(runtime.Object, string, string, string)          { panic("recorder failed") }
func (*panicRecorder) Eventf(runtime.Object, string, string, string, ...any) {}
func (*panicRecorder) AnnotatedEventf(runtime.Object, map[string]string, string, string, string, ...any) {
}

func TestComponentFailedEmissionDoesNotAdvanceDedup(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route"),
	}, nil)
	c := newTestComponent(t, &panicRecorder{})
	event := completedEvent(t, cycle)

	assert.PanicsWithValue(t, "recorder failed", func() {
		c.handleReconciliationCompleted(event)
	})
	recorder := &fakeRecorder{}
	c.recorder = recorder
	c.handleReconciliationCompleted(event)
	require.Len(t, recorder.events, 1)
}

func TestComponentConsumesOnlyCanonicalCompletedCarrier(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route"),
	}, nil)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)

	c.HandleEvent(&events.TemplateRenderedEvent{CycleSnapshot: cycle})
	assert.Empty(t, recorder.events)
	c.HandleEvent(completedEvent(t, cycle))
	require.Len(t, recorder.events, 1)
}

func TestComponentFollowerAndUninitializedRecorderDoNotEmit(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route"),
	}, nil)
	event := completedEvent(t, cycle)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)
	c.setLeader(false)
	c.handleReconciliationCompleted(event)
	assert.Empty(t, recorder.events)

	c.setLeader(true)
	c.recorder = nil
	assert.NotPanics(t, func() { c.handleReconciliationCompleted(event) })
	assert.NotPanics(t, func() { c.handleReconciliationCompleted(nil) })
	assert.Empty(t, recorder.events)
}

func TestComponentLeadershipLossInvalidatesEventDedup(t *testing.T) {
	fixture := newEventCycleFixture(t)
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		newRenderedEvent("route"),
	}, nil)
	recorder := &fakeRecorder{}
	c := newTestComponent(t, recorder)
	event := completedEvent(t, cycle)
	c.handleReconciliationCompleted(event)

	c.HandleEvent(&events.LostLeadershipEvent{})
	c.HandleEvent(&events.BecameLeaderEvent{})
	c.handleReconciliationCompleted(event)

	require.Len(t, recorder.events, 2)
}

func TestComponent_handleInstanceDeploymentFailed(t *testing.T) {
	endpoint := &dataplane.Endpoint{PodName: "haproxy-0", PodNamespace: "edge", PodUID: "uid-1"}

	t.Run("leader emits a Warning on the pod with the agent's message", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: true}
		c.handleInstanceDeploymentFailed(events.NewInstanceDeploymentFailedEvent(endpoint, "reload: [ALERT] parsing [haproxy.cfg:12]: unknown keyword", true))
		require.Len(t, rec.events, 1)
		ref, ok := rec.events[0].obj.(*corev1.ObjectReference)
		require.True(t, ok)
		assert.Equal(t, "Pod", ref.Kind)
		assert.Equal(t, "edge", ref.Namespace)
		assert.Equal(t, "haproxy-0", ref.Name)
		assert.Equal(t, corev1.EventTypeWarning, rec.events[0].etype)
		assert.Equal(t, applyFailedReason, rec.events[0].reason)
		assert.Contains(t, rec.events[0].message, "unknown keyword")
	})

	t.Run("follower emits nothing", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: false}
		c.handleInstanceDeploymentFailed(events.NewInstanceDeploymentFailedEvent(endpoint, "x", true))
		assert.Empty(t, rec.events)
	})

	t.Run("an endpoint that is not a pod is skipped", func(t *testing.T) {
		rec := &fakeRecorder{}
		c := &Component{recorder: rec, isLeader: true}
		c.handleInstanceDeploymentFailed(events.NewInstanceDeploymentFailedEvent("http://10.0.0.1:5555", "x", true))
		assert.Empty(t, rec.events)
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

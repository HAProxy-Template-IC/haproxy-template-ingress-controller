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

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type warningCycleFixture struct {
	outputAuthority *renderoutput.Authority
	cycleAuthority  *rendercycle.Authority
	artifacts       *renderartifact.Snapshot
	status          *templating.StatusPatchSnapshot
	resources       *templating.RenderedResourceSnapshot
}

func newWarningCycleFixture(t *testing.T) *warningCycleFixture {
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
	return &warningCycleFixture{
		outputAuthority: outputAuthority, cycleAuthority: cycleAuthority,
		artifacts: artifacts, status: status, resources: resources,
	}
}

func (f *warningCycleFixture) snapshot(
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

func warningCompletedEvent(t *testing.T, cycle *rendercycle.Snapshot) *events.ReconciliationCompletedEvent {
	t.Helper()
	occurrence, err := rendercycle.NewOccurrence(cycle)
	require.NoError(t, err)
	event, err := events.NewReconciliationCompletedEventWithCycle(0, occurrence)
	require.NoError(t, err)
	return event
}

func TestRenderWarningsLifecycle(t *testing.T) {
	metrics := NewMetrics(prometheus.NewRegistry())
	component := New(metrics, busevents.NewEventBus(16))
	fixture := newWarningCycleFixture(t)
	warning := func(name, eventType, reason string) templating.RenderedEvent {
		return templating.RenderedEvent{Namespace: "team", Name: name, APIVersion: "example.test/v1", Kind: "Widget", Type: eventType, Reason: reason, Message: "test message"}
	}
	cycle := fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		warning("one", templating.EventTypeWarning, "MissingInput"),
		warning("two", templating.EventTypeWarning, "MissingInput"),
		warning("three", templating.EventTypeWarning, "InvalidValue"),
		warning("four", templating.EventTypeNormal, "Ready"),
	}, nil)
	completed := warningCompletedEvent(t, cycle)
	component.handleEvent(completed)
	require.Equal(t, 0, testutil.CollectAndCount(metrics.RenderWarnings))
	component.handleEvent(events.NewBecameLeaderEvent("replica"))
	completed.Events = []templating.RenderedEvent{warning("forged", templating.EventTypeWarning, "Forged")}
	completed.EventSnapshot = &templating.RenderedEventSnapshot{}
	component.handleEvent(completed)
	require.Equal(t, 2, testutil.CollectAndCount(metrics.RenderWarnings))
	require.Equal(t, 2.0, testutil.ToFloat64(metrics.RenderWarnings.WithLabelValues("MissingInput")))
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.RenderWarnings.WithLabelValues("InvalidValue")))

	component.handleEvent(warningCompletedEvent(t, cycle))
	require.Equal(t, 2.0, testutil.ToFloat64(metrics.RenderWarnings.WithLabelValues("MissingInput")))
	invalid := events.NewReconciliationCompletedEvent(0, "", nil, nil)
	invalid.CycleSnapshot = cycle
	require.Error(t, component.updateRenderWarnings(invalid))
	require.Equal(t, 2, testutil.CollectAndCount(metrics.RenderWarnings))

	cycle = fixture.snapshot(t, "global\n", []templating.RenderedEvent{
		warning("two", templating.EventTypeWarning, "MissingInput"),
	}, cycle)
	component.handleEvent(warningCompletedEvent(t, cycle))
	require.Equal(t, 1, testutil.CollectAndCount(metrics.RenderWarnings))
	require.Equal(t, 1.0, testutil.ToFloat64(metrics.RenderWarnings.WithLabelValues("MissingInput")))

	component.handleEvent(events.NewLostLeadershipEvent("replica", "test"))
	require.Equal(t, 0, testutil.CollectAndCount(metrics.RenderWarnings))
	require.Nil(t, component.lastEvents)
	component.handleEvent(events.NewBecameLeaderEvent("replica"))
	component.handleEvent(warningCompletedEvent(t, cycle))
	require.Equal(t, 1, testutil.CollectAndCount(metrics.RenderWarnings))
	cycle = fixture.snapshot(t, "global\n", nil, cycle)
	component.handleEvent(warningCompletedEvent(t, cycle))
	require.Equal(t, 0, testutil.CollectAndCount(metrics.RenderWarnings))
}

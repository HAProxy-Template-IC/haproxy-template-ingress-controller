// Copyright 2026 Philipp Hossner
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

package deployer

import (
	"context"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// deployerBus is a started event bus with one subscription, which is what a
// deployment's assertions read.
type deployerBus struct {
	*busevents.EventBus
	Events <-chan busevents.Event
}

func newTestBus(t *testing.T) *deployerBus {
	t.Helper()
	bus := testutil.NewTestBus()
	published := bus.Subscribe("deployer-test", 200)
	bus.Start()
	return &deployerBus{EventBus: bus, Events: published}
}

// oneEndpoint is the single-pod fleet the scheduler tests dispatch against.
func oneEndpoint() []dataplane.Endpoint {
	return []dataplane.Endpoint{{URL: "http://localhost:5555", PodName: "haproxy-0", PodUID: "uid-0"}}
}

// depFor is a pending deployment targeting these endpoints.
func depFor(endpoints []dataplane.Endpoint) *scheduledDeployment {
	return &scheduledDeployment{
		occurrence: mustTestOccurrence("config", "test-plan", nil),
		endpoints:  endpoints,
		reason:     "pod_discovery",
	}
}

func exactTestPlan(id, config string) *renderplan.Plan {
	return &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		ID:            id,
		Sections: []renderplan.Section{{
			Kind:       renderplan.SectionKindCore,
			Name:       "test",
			TextDigest: renderplan.DigestString(config),
			Length:     len(config),
			Text:       config,
			TextKnown:  true,
		}},
		Files: []renderplan.File{{
			Path:           "haproxy.cfg",
			Kind:           renderplan.FileKindConfig,
			ReloadOnChange: true,
			Digest:         renderplan.DigestString(config),
			Size:           int64(len(config)),
			Content:        config,
			ContentKnown:   true,
		}},
	}
}

func primeRendered(s *DeploymentScheduler, config, _, planID string) *rendercycle.Occurrence {
	occurrence := mustTestOccurrence(config, planID, nil)
	s.mu.Lock()
	s.lastRenderedOccurrence = occurrence
	s.mu.Unlock()
	return occurrence
}

func primeValidated(s *DeploymentScheduler, config, _, planID string) *rendercycle.Occurrence {
	occurrence := mustTestOccurrence(config, planID, nil)
	s.mu.Lock()
	s.lastValidatedOccurrence = occurrence
	s.mu.Unlock()
	return occurrence
}

func scheduleExact(
	s *DeploymentScheduler,
	ctx context.Context,
	config string,
	endpoints []dataplane.Endpoint,
	reason, correlationID string,
) {
	planID := "test-plan:" + config
	s.scheduleOrQueueOccurrence(
		ctx, mustTestOccurrence(config, planID, nil), endpoints, reason, correlationID, true,
	)
}

// scheduledEvent builds the deploy event the per-pod handlers read their
// target identity, checksum and correlation from.
func scheduledEvent(runtimeConfigName, runtimeConfigNamespace, correlationID string) *events.DeploymentScheduledEvent {
	event, err := events.NewDeploymentScheduledEventWithCycle(
		mustTestOccurrence("config", "plan-1", nil), oneEndpoint(),
		runtimeConfigName, runtimeConfigNamespace, "config_validation", true,
		events.WithCorrelation(correlationID, correlationID),
	)
	if err != nil {
		panic(err)
	}
	return event
}

func renderGateForCompletion(
	tb testing.TB,
	completed *events.DeploymentCompletedEvent,
	ok, refused bool,
	message string,
) *events.RenderGateCompletedEvent {
	tb.Helper()
	occurrence, err := completed.RenderOccurrence()
	if err != nil {
		tb.Fatal(err)
	}
	event, err := events.NewRenderGateCompletedEventWithCycle(
		occurrence, ok, refused, true, message, !ok, 12,
	)
	if err != nil {
		tb.Fatal(err)
	}
	return event
}

func mustTestOccurrence(
	config, planID string,
	status *templating.StatusPatchSnapshot,
) *rendercycle.Occurrence {
	return mustOccurrenceFor(exactTestPlan(planID, config), config, &dataplane.AuxiliaryFiles{}, status)
}

func mustOccurrenceFor(
	plan *renderplan.Plan,
	config string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	status *templating.StatusPatchSnapshot,
) *rendercycle.Occurrence {
	plan.ComputeID()
	artifactAuthority := renderartifact.NewAuthority()
	artifacts, err := dataplane.BuildAuxiliaryFileSnapshot(
		artifactAuthority, nil, auxiliaryFiles,
	)
	if err != nil {
		panic(err)
	}
	planAuthority := renderplan.NewAuthority()
	outputAuthority, err := renderoutput.NewAuthority(planAuthority, artifactAuthority)
	if err != nil {
		panic(err)
	}
	cycleAuthority, err := rendercycle.NewAuthority(outputAuthority)
	if err != nil {
		panic(err)
	}
	if status == nil {
		status, err = templating.NewStatusPatchCollector().Snapshot()
		if err != nil {
			panic(err)
		}
	}
	renderedEvents, err := templating.NewEventCollector().Snapshot()
	if err != nil {
		panic(err)
	}
	resources, err := templating.NewRenderedResourceCollector().Snapshot()
	if err != nil {
		panic(err)
	}
	output, err := renderoutput.NewSnapshot(outputAuthority, config, plan, artifacts, nil)
	if err != nil {
		panic(err)
	}
	cycle, err := rendercycle.NewSnapshot(
		cycleAuthority, output, status, renderedEvents, resources, nil,
	)
	if err != nil {
		panic(err)
	}
	occurrence, err := rendercycle.NewOccurrence(cycle)
	if err != nil {
		panic(err)
	}
	return occurrence
}

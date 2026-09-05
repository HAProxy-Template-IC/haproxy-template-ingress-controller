// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
)

type renderOccurrenceAccessor interface {
	RenderOccurrence() (*rendercycle.Occurrence, error)
	AuthenticatedRenderIdentity() (*rendercycle.Snapshot, string, error)
}

func TestTemplateRenderedEventCreatesDistinctABAOccurrences(t *testing.T) {
	a := deploymentEventCycleFixture(t)
	b := deploymentEventCycleFixture(t)

	firstA, err := NewTemplateRenderedEventWithCycle(a.cycle, 1, "a-1", true)
	require.NoError(t, err)
	eventB, err := NewTemplateRenderedEventWithCycle(b.cycle, 2, "b", true)
	require.NoError(t, err)
	secondA, err := NewTemplateRenderedEventWithCycle(a.cycle, 3, "a-2", true)
	require.NoError(t, err)

	firstOccurrence := mustEventOccurrence(t, firstA)
	bOccurrence := mustEventOccurrence(t, eventB)
	secondOccurrence := mustEventOccurrence(t, secondA)
	assert.NotSame(t, firstOccurrence, bOccurrence)
	assert.NotSame(t, firstOccurrence, secondOccurrence)
	assert.NotEqual(t, firstA.RenderProof, eventB.RenderProof)
	assert.NotEqual(t, firstA.RenderProof, secondA.RenderProof)
	assert.Same(t, a.cycle, firstA.CycleSnapshot)
	assert.Same(t, a.cycle, secondA.CycleSnapshot)
}

func TestOccurrencePropagatesThroughEveryProductionEvent(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	templateEvent, err := NewTemplateRenderedEventWithOccurrence(
		fixture.occurrence, 1, "test", true,
	)
	require.NoError(t, err)
	reconciliation, err := NewReconciliationCompletedEventWithCycle(2, fixture.occurrence)
	require.NoError(t, err)
	resources, err := NewResourcesAppliedEventWithCycle(fixture.occurrence)
	require.NoError(t, err)
	gate, err := NewRenderGateCompletedEventWithCycle(
		fixture.occurrence, true, false, true, "", false, 3,
	)
	require.NoError(t, err)
	scheduled, err := NewDeploymentScheduledEventWithCycle(
		fixture.occurrence, nil, "runtime", "haptic", "test", true,
	)
	require.NoError(t, err)
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	completed, err := NewDeploymentCompletedEventWithCycle(result)
	require.NoError(t, err)
	skipped, err := NewDeploymentSkippedEventWithCycle(
		fixture.occurrence, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.NoError(t, err)

	accessors := map[string]renderOccurrenceAccessor{
		"template":       templateEvent,
		"reconciliation": reconciliation,
		"resources":      resources,
		"gate":           gate,
		"scheduled":      scheduled,
		"result":         result,
		"completed":      completed,
		"skipped":        skipped,
	}
	for name, accessor := range accessors {
		t.Run(name, func(t *testing.T) {
			occurrence := mustEventOccurrence(t, accessor)
			assert.Same(t, fixture.occurrence, occurrence)
			cycle, proof, identityErr := accessor.AuthenticatedRenderIdentity()
			require.NoError(t, identityErr)
			assert.Same(t, fixture.cycle, cycle)
			assert.NotEmpty(t, proof)
		})
	}
}

func TestPublicIdentityShadowMutationCannotPoisonOccurrence(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	foreign := deploymentEventCycleFixture(t)
	foreignProof, err := foreign.occurrence.Proof()
	require.NoError(t, err)

	templateEvent, err := NewTemplateRenderedEventWithOccurrence(
		fixture.occurrence, 1, "test", true,
	)
	require.NoError(t, err)
	templateEvent.CycleSnapshot = foreign.cycle
	templateEvent.OutputSnapshot = foreign.output
	templateEvent.RenderProof = foreignProof

	reconciliation, err := NewReconciliationCompletedEventWithCycle(2, fixture.occurrence)
	require.NoError(t, err)
	reconciliation.CycleSnapshot = foreign.cycle
	reconciliation.RenderProof = foreignProof

	resources, err := NewResourcesAppliedEventWithCycle(fixture.occurrence)
	require.NoError(t, err)
	resources.CycleSnapshot = foreign.cycle
	resources.RenderProof = foreignProof

	gate, err := NewRenderGateCompletedEventWithCycle(
		fixture.occurrence, true, false, true, "", false, 3,
	)
	require.NoError(t, err)
	gate.CycleSnapshot = foreign.cycle
	gate.OutputSnapshot = foreign.output
	gate.RenderProof = foreignProof

	scheduled, err := NewDeploymentScheduledEventWithCycle(
		fixture.occurrence, nil, "runtime", "haptic", "test", true,
	)
	require.NoError(t, err)
	scheduled.CycleSnapshot = foreign.cycle
	scheduled.OutputSnapshot = foreign.output
	scheduled.RenderProof = foreignProof

	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	result.CycleSnapshot = foreign.cycle
	result.OutputSnapshot = foreign.output
	result.RenderProof = foreignProof
	completed, err := NewDeploymentCompletedEventWithCycle(result)
	require.NoError(t, err)

	skipped, err := NewDeploymentSkippedEventWithCycle(
		fixture.occurrence, 1, SkipReasonConfigUnchanged, "pods",
	)
	require.NoError(t, err)
	skipped.CycleSnapshot = foreign.cycle
	skipped.OutputSnapshot = foreign.output
	skipped.RenderProof = foreignProof

	for name, accessor := range map[string]renderOccurrenceAccessor{
		"template": templateEvent, "reconciliation": reconciliation,
		"resources": resources, "gate": gate, "scheduled": scheduled,
		"result": result, "completed": completed, "skipped": skipped,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Same(t, fixture.occurrence, mustEventOccurrence(t, accessor))
			cycle, _, identityErr := accessor.AuthenticatedRenderIdentity()
			require.NoError(t, identityErr)
			assert.Same(t, fixture.cycle, cycle)
		})
	}
}

func TestProductionConstructorsRejectCopiedAndMissingOccurrences(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	copied := *fixture.occurrence

	constructors := map[string]func(*rendercycle.Occurrence) error{
		"template": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewTemplateRenderedEventWithOccurrence(occurrence, 0, "", true)
			return err
		},
		"reconciliation": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewReconciliationCompletedEventWithCycle(0, occurrence)
			return err
		},
		"resources": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewResourcesAppliedEventWithCycle(occurrence)
			return err
		},
		"gate": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewRenderGateCompletedEventWithCycle(
				occurrence, true, false, true, "", false, 0,
			)
			return err
		},
		"scheduled": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewDeploymentScheduledEventWithCycle(
				occurrence, nil, "", "", "", true,
			)
			return err
		},
		"result": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewDeploymentResultWithOccurrence(occurrence)
			return err
		},
		"skipped": func(occurrence *rendercycle.Occurrence) error {
			_, err := NewDeploymentSkippedEventWithCycle(
				occurrence, 0, SkipReasonConfigUnchanged, "",
			)
			return err
		},
	}
	for name, constructor := range constructors {
		t.Run(name+"/missing", func(t *testing.T) {
			require.Error(t, constructor(nil))
		})
		t.Run(name+"/copied", func(t *testing.T) {
			require.Error(t, constructor(&copied))
		})
	}
}

func TestEventAndResultCopiesRetainExactOccurrencePointer(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	templateEvent, err := NewTemplateRenderedEventWithOccurrence(
		fixture.occurrence, 1, "test", true,
	)
	require.NoError(t, err)
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)

	templateCopy := *templateEvent
	resultCopy := *result
	assert.Same(t, fixture.occurrence, mustEventOccurrence(t, &templateCopy))
	assert.Same(t, fixture.occurrence, mustEventOccurrence(t, &resultCopy))
}

func TestLegacyCompletionConstructorRejectsAuthenticatedResultDowngrade(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	result, err := NewDeploymentResultWithOccurrence(fixture.occurrence)
	require.NoError(t, err)
	require.PanicsWithValue(
		t,
		"authenticated deployment result requires NewDeploymentCompletedEventWithCycle",
		func() { NewDeploymentCompletedEvent(result) },
	)
}

func mustEventOccurrence(tb testing.TB, accessor renderOccurrenceAccessor) *rendercycle.Occurrence {
	tb.Helper()
	occurrence, err := accessor.RenderOccurrence()
	require.NoError(tb, err)
	return occurrence
}

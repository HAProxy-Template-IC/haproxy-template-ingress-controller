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

package deployer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func BenchmarkOccurrenceInspection(b *testing.B) {
	occurrence := mustTestOccurrence(strings.Repeat("#", 1<<20), "large-plan", nil)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := inspectOccurrence(occurrence); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOccurrenceMaterialization(b *testing.B) {
	occurrence := mustTestOccurrence(strings.Repeat("#", 1<<20), "large-plan", nil)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := materializeOccurrence(occurrence); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSchedulerRejectsLegacyRenderWithoutOccurrence(t *testing.T) {
	plan := exactTestPlan("legacy", "global\n")
	event := events.NewTemplateRenderedEvent(
		"global\n", nil, nil, nil, 0, 1, "config_change", "checksum",
		plan, plan.ID, true,
	)
	bus, logger := testutil.NewTestBusAndLogger()
	scheduler := newDeploymentScheduler(bus, logger, 0, time.Second)
	scheduler.handleTemplateRendered(context.Background(), event)
	assert.Nil(t, scheduler.lastRenderedOccurrence)
	assert.Nil(t, scheduler.state.pending)
}

func TestOutputEqualityDoesNotCollapseOccurrenceIdentity(t *testing.T) {
	occurrenceA := mustTestOccurrence("global\n", "plan-A", nil)
	identity, err := materializeOccurrence(occurrenceA)
	require.NoError(t, err)
	repeated, err := events.NewTemplateRenderedEventWithOccurrence(
		occurrenceA, 1, "config_change", true,
	)
	require.NoError(t, err)
	repeatedOccurrence, err := repeated.RenderOccurrence()
	require.NoError(t, err)
	assert.Same(t, occurrenceA, repeatedOccurrence)
	assert.Equal(t, identity.plan.ID, identity.planID)

	otherOccurrence := mustTestOccurrence("global\n", "plan-A", nil)
	assert.False(t, sameOccurrence(occurrenceA, otherOccurrence))
	assert.False(t, sameOccurrenceOutput(occurrenceA, otherOccurrence),
		"foreign authorities never authenticate as one output")
}

func TestValidatedPlanSetDistinguishesABARenderOccurrences(t *testing.T) {
	a1 := mustTestOccurrence("global\n# A\n", "plan-A", nil)
	b := mustTestOccurrence("global\n# B\n", "plan-B", nil)
	a2 := mustTestOccurrence("global\n# A\n", "plan-A", nil)
	set := newValidatedPlanSet()
	set.addOccurrence(a1)
	set.addOccurrence(b)
	set.addOccurrence(a2)
	a1Identity, err := materializeOccurrence(a1)
	require.NoError(t, err)

	assert.Equal(t, planReference{}, set.resolve(
		a1Identity.planID, "agent-proof", a1Identity.plan, mustTestOccurrence("global\n# A\n", "plan-A", nil),
	))
	assert.Equal(t, planReference{id: a1Identity.planID, proof: "agent-proof"}, set.resolve(
		a1Identity.planID, "agent-proof", a1Identity.plan, a1,
	))
}

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

package deployer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// TestDeployFailureDowngradesPendingRuntimeRawLane pins that a FAILED deploy
// invalidates the lane-classification baseline (lastDispatchedParsed): a pending
// render whose runtime-raw lane was frozen against the failed dispatch must NOT
// be applied via the silent runtime-raw lane — it must dispatch structurally so
// the pods' real (pre-failure) state is fully re-synced.
//
// This is the issue #76 incident choreography (Case 2 of the lane tests, but
// with the structural deploy FAILING): pre-fix, the parked runtime-raw pending
// dispatched authoritatively after the failure, restamped the pods' config
// version header over structural content the workers never loaded, and the
// armed fast retry then trusted the empty disk-diff — a silent 0-op "success"
// that left the new frontend parked unreloaded for 90s (conformance TCP
// listeners unreachable, GitLab issue #76).
func TestDeployFailureDowngradesPendingRuntimeRawLane(t *testing.T) {
	s, scheduledCh, applied, cancel := newLaneScheduler(t, 0)
	defer cancel()

	_, _, structural := laneRenders(t)

	// The fleet is already running the base config, so the partial apply in
	// step 2 has an activated baseline to patch. Without it the scheduler is at
	// cold start and correctly declines to apply anything (#112) — which is not
	// the choreography this test is about.
	s.schedulerMutex.Lock()
	s.lastActivatedConfig = fmt.Sprintf(laneConfigBase, "10.0.0.1:8080")
	s.schedulerMutex.Unlock()

	// 1. Dispatch a structural render (cold start → structural). The loop
	// publishes it, marks it in flight, and parks in awaitCompletion.
	s.scheduleOrQueue(context.Background(), "structural-config", nil, structural, oneEndpoint(),
		"structural-change", "corr-structural", nil, true, "checksum-structural")
	sd := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	require.Equal(t, "structural-config", sd.Config)

	// 2. While it is in flight, park a render whose diff vs the in-flight
	// structural baseline is purely runtime-eligible (address change only).
	// Its runtime subset partial-applies immediately (first `applied`), and the
	// pending stays parked with the frozen runtime-raw lane.
	structuralPlusAddr := parseLaneConfig(t, fmt.Sprintf(laneConfigBase, "10.0.0.2:8080")+laneStructuralExtra)
	s.scheduleOrQueue(context.Background(), "runtime-config", nil, structuralPlusAddr, oneEndpoint(),
		"endpoint-change", "corr-runtime", nil, true, "checksum-runtime")

	select {
	case <-applied:
		// The pre-interval partial apply of the runtime subset.
	case <-time.After(2 * time.Second):
		t.Fatal("the runtime subset was not partial-applied while the structural deploy was in flight")
	}
	s.schedulerMutex.Lock()
	require.NotNil(t, s.state.pending, "the runtime render must stay parked")
	require.Equal(t, laneRuntimeRaw, s.state.pending.lane, "premise: the pending's lane froze runtime-raw against the in-flight render")
	s.schedulerMutex.Unlock()

	// 3. The structural deploy comes back FULLY FAILED (the DPA 409 case). The
	// fix must drop the dispatch baseline and downgrade the parked pending to
	// the structural lane before the loop grabs it.
	s.handleDeploymentCompleted(events.NewDeploymentCompletedEvent(&events.DeploymentResult{
		Total: 2, Succeeded: 0, Failed: 2, ContentChecksum: "checksum-structural",
	}))

	// 4. POST-FIX: the parked render dispatches STRUCTURALLY — a real, visible
	// DeploymentScheduledEvent carrying the runtime render. PRE-FIX this times
	// out: the pending is consumed by the silent authoritative runtime-raw
	// dispatch instead (asserted below).
	sd2 := testutil.WaitForEvent[*events.DeploymentScheduledEvent](t, scheduledCh, testutil.LongTimeout)
	assert.Equal(t, "runtime-config", sd2.Config,
		"after a failed deploy the parked render must re-dispatch structurally (full sync), not runtime-raw")

	// PRE-FIX the authoritative runtime-raw apply fires here (a second `applied`
	// with the version-header restamp certifying never-loaded content as live).
	// POST-FIX the runtime-raw lane must stay silent after the failure.
	select {
	case <-applied:
		t.Fatal("the parked pending was applied via the runtime-raw lane after a FAILED deploy — " +
			"this restamps the version header over content the workers never loaded (issue #76)")
	case <-time.After(200 * time.Millisecond):
		// Expected: no authoritative runtime-raw apply.
	}
}

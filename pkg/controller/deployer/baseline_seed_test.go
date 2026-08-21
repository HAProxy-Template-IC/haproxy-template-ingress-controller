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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
)

// unreachable is a pod whose agent never answers: nothing listens on port 1, so
// every State read errors.
func unreachable(podName string) dataplane.Endpoint {
	return dataplane.Endpoint{URL: "http://127.0.0.1:1", PodName: podName, PodUID: podName + "-uid"}
}

// runningPlan returns a fresh fake agent already running plan, so its /v1/state
// reports that applied plan and the blob a cold-start read decodes.
func runningPlan(t *testing.T, bus *deployerBus) (agent *agenttest.Agent, planID string) {
	t.Helper()
	agent = agenttest.New(t)
	warm := createTestDeployer(bus.EventBus)
	plan, config, aux := renderFor("plan-1", "10.0.0.1", mapEntry)
	deployTo(t, warm, bus, plan, config, aux, "config_validation", agentEndpoint(agent, "haproxy-0"))
	return agent, plan.ID
}

// A first discovery that cannot reach any pod must not consume the per-term
// one-shot: the fleet's running plan is still adopted on a later discovery.
// Otherwise the first render describes servers no pod holds and the next apply
// moves every server — the churn seedBaseline exists to prevent.
func TestSeedBaseline_AllErroredDiscoveryDoesNotForfeitTheTerm(t *testing.T) {
	bus := newTestBus(t)
	good, planID := runningPlan(t, bus)

	c := createTestDeployer(bus.EventBus)
	sink := &recordingPlanSink{}
	c.ackedPlans = sink

	c.seedBaseline(context.Background(), []dataplane.Endpoint{unreachable("dead")})
	require.Empty(t, sink.plans, "an unreachable fleet adopts nothing")
	require.False(t, c.baselineSeeded.Load(),
		"an all-errored discovery must leave the one-shot unspent so a later one retries")

	c.seedBaseline(context.Background(), []dataplane.Endpoint{agentEndpoint(good, "haproxy-0")})
	require.Len(t, sink.plans, 1, "the next reachable discovery adopts the fleet's running plan")
	assert.Equal(t, planID, sink.plans[0].ID)
	require.True(t, c.baselineSeeded.Load(), "adopting a plan spends the one-shot")
}

// An empty pod set is not a fresh fleet — the store simply had not synced. It
// must not consume the one-shot either.
func TestSeedBaseline_EmptyDiscoveryDoesNotForfeitTheTerm(t *testing.T) {
	bus := newTestBus(t)
	good, planID := runningPlan(t, bus)

	c := createTestDeployer(bus.EventBus)
	sink := &recordingPlanSink{}
	c.ackedPlans = sink

	c.seedBaseline(context.Background(), nil)
	require.Empty(t, sink.plans)
	require.False(t, c.baselineSeeded.Load(), "an empty discovery must not spend the one-shot")

	c.seedBaseline(context.Background(), []dataplane.Endpoint{agentEndpoint(good, "haproxy-0")})
	require.Len(t, sink.plans, 1)
	assert.Equal(t, planID, sink.plans[0].ID)
}

// A reachable fleet where every pod reports no applied plan is genuinely fresh:
// there is nothing to preserve, so the one-shot is spent and a later discovery
// — even one carrying a plan — does not re-seed.
func TestSeedBaseline_ConfirmedFreshFleetLatchesAndDoesNotReseed(t *testing.T) {
	bus := newTestBus(t)
	good, _ := runningPlan(t, bus)
	fresh := agenttest.New(t) // never deployed to: reports no applied plan

	c := createTestDeployer(bus.EventBus)
	sink := &recordingPlanSink{}
	c.ackedPlans = sink

	c.seedBaseline(context.Background(), []dataplane.Endpoint{agentEndpoint(fresh, "haproxy-1")})
	require.Empty(t, sink.plans, "a fresh fleet has no plan to adopt")
	require.True(t, c.baselineSeeded.Load(),
		"a reachable fleet that all reports no plan is fresh — the one-shot is spent")

	c.seedBaseline(context.Background(), []dataplane.Endpoint{agentEndpoint(good, "haproxy-0")})
	assert.Empty(t, sink.plans, "the one-shot is not re-armed once the fleet was confirmed fresh")
}

// A partially-reachable set — one pod answers no plan, another is unreachable —
// is not confirmed fresh: the unreachable pod might hold the running plan, so
// the one-shot stays unspent and a later full discovery adopts it.
func TestSeedBaseline_PartialReachabilityRetries(t *testing.T) {
	bus := newTestBus(t)
	good, planID := runningPlan(t, bus)
	fresh := agenttest.New(t)

	c := createTestDeployer(bus.EventBus)
	sink := &recordingPlanSink{}
	c.ackedPlans = sink

	c.seedBaseline(context.Background(), []dataplane.Endpoint{
		agentEndpoint(fresh, "haproxy-1"),
		unreachable("dead"),
	})
	require.Empty(t, sink.plans)
	require.False(t, c.baselineSeeded.Load(),
		"a set with an unreachable pod is not confirmed fresh — retry")

	c.seedBaseline(context.Background(), []dataplane.Endpoint{agentEndpoint(good, "haproxy-0")})
	require.Len(t, sink.plans, 1)
	assert.Equal(t, planID, sink.plans[0].ID)
}

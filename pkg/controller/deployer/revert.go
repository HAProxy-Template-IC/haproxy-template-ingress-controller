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
	"errors"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

// handleRenderGateCompleted applies the render gate's verdict to the fleet.
//
// A pass names the plan every agent may promote its rollback baseline to, which
// travels on the next apply — the drift pass carries it within one interval even
// when nothing else changes. A refusal scopes a revert to the pods that took the
// plan without loading it.
func (c *Component) handleRenderGateCompleted(ctx context.Context, event *events.RenderGateCompletedEvent) {
	occurrence, err := gateEventOccurrence(event)
	if err != nil {
		return
	}
	if _, err := inspectOccurrence(occurrence); err != nil {
		return
	}
	if event.OK {
		c.SetValidatedOccurrence(occurrence)
		return
	}
	// Only HAProxy's own verdict is evidence about the config. A gate that
	// could not run says nothing about what the pods hold, and reverting on it
	// would undo a good config because a temp directory was unwritable.
	if !event.Refused {
		return
	}
	c.revertOccurrence(ctx, occurrence)
}

type refusedRender struct {
	occurrence *rendercycle.Occurrence
	planID     string
}

func (c *Component) revertOccurrence(
	ctx context.Context,
	occurrence *rendercycle.Occurrence,
) {
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return
	}
	c.revertExact(ctx, refusedRender{
		occurrence: occurrence, planID: identity.planID,
	})
}

func (c *Component) revertExact(ctx context.Context, refused refusedRender) {
	identity, err := inspectOccurrence(refused.occurrence)
	if err != nil || refused.planID == "" || identity.planID != refused.planID {
		return
	}
	endpoints := c.fleetSnapshot()
	if len(endpoints) == 0 {
		c.Logger().Warn("Render gate refused a plan but this controller has no fleet to revert", "plan", refused.planID)
		return
	}

	token := api.Token{LeaderEpoch: c.leaderEpoch(), RenderSeq: c.nextRenderSeq()}
	// A pod owned by a newer epoch ends the pass for every pod: this
	// controller is no longer the fleet's writer, and the leader that is will
	// run its own revert.
	revertCtx, standDown := context.WithCancel(ctx)
	defer standDown()

	var reverted, failed int
	var stoodDown bool
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConcurrentPods)
	for i := range endpoints {
		wg.Add(1)
		go func(endpoint *dataplane.Endpoint) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if revertCtx.Err() != nil {
				return
			}
			switch outcome := c.revertPodRender(revertCtx, endpoint, refused, token); outcome {
			case revertSkipped:
			case revertDone:
				mu.Lock()
				reverted++
				mu.Unlock()
			case revertFailed:
				mu.Lock()
				failed++
				mu.Unlock()
			case revertStoodDown:
				mu.Lock()
				stoodDown = true
				mu.Unlock()
				standDown()
			}
		}(&endpoints[i])
	}
	wg.Wait()

	if stoodDown {
		c.Logger().Warn("Abandoned the revert: a newer leader epoch owns the fleet",
			"plan", refused.planID, "reverted", reverted, "fleet", len(endpoints))
		return
	}
	c.Logger().Warn("Reverted the pods carrying a refused plan to their last known good set",
		"plan", refused.planID, "reverted", reverted, "failed", failed, "fleet", len(endpoints))
}

type revertOutcome int

const (
	revertSkipped revertOutcome = iota
	revertDone
	revertFailed
	// revertStoodDown: a newer leader epoch owns this pod, so this controller
	// is not the fleet's writer any more and the rest of the revert is not
	// its business.
	revertStoodDown
)

func (c *Component) revertPodRender(
	ctx context.Context,
	endpoint *dataplane.Endpoint,
	refused refusedRender,
	token api.Token,
) revertOutcome {
	client, err := c.clients.For(endpoint)
	if err != nil {
		c.Logger().Error("Cannot reach a pod to revert it", "pod", endpoint.PodName, "error", err)
		return revertFailed
	}
	state, err := client.State(ctx, false)
	if err != nil {
		c.Logger().Error("Cannot read a pod's state to decide on reverting it",
			"pod", endpoint.PodName, "error", err)
		return revertFailed
	}
	target := c.refusedRenderReference(endpoint, state, refused)
	if target.id == "" {
		return revertSkipped
	}

	result, err := client.Apply(ctx, &api.Manifest{
		IdentityVersion: api.ExactIdentityVersion,
		PlanID:          target.id,
		PlanProof:       target.proof,
		Token:           token,
		Mode:            api.ModeRevertLKG,
	}, nil, nil)
	if err != nil {
		var conflict *agentclient.ConflictError
		if errors.As(err, &conflict) && conflict.Conflict.Reason == conflictStaleEpoch {
			c.Logger().Error("A newer leader epoch owns the fleet, abandoning the revert",
				"pod", endpoint.PodName,
				"pod_epoch", conflict.Conflict.AppliedToken.LeaderEpoch,
				"controller_epoch", token.LeaderEpoch)
			return revertStoodDown
		}
		if errors.As(err, &conflict) && conflict.Conflict.Reason == conflictRevertTargetMismatch {
			return revertSkipped
		}
		c.Logger().Error("Reverting a pod to its last known good set failed",
			"pod", endpoint.PodName, "plan", refused.planID, "error", err)
		return revertFailed
	}
	if !result.OK {
		c.Logger().Error("A pod refused to revert to its last known good set",
			"pod", endpoint.PodName, "plan", refused.planID, "error", applyErrorMessage(result))
		return revertFailed
	}

	// The pod is no longer on a plan this controller composed against.
	c.invalidateBaseline(endpoint)
	c.Logger().Warn("Reverted a pod to its last known good set",
		"pod", endpoint.PodName, "refused_plan", refused.planID, "now_running", result.RunningPlanID)
	return revertDone
}

// carriesRefusedPlan reports whether a pod holds the refused plan in a state
// HAProxy has not proven loadable: on disk, or in the running worker's runtime
// state without a reload having read it. A pod whose running worker WAS started
// from the plan loaded it successfully and is left alone.
func (c *Component) refusedRenderReference(
	endpoint *dataplane.Endpoint,
	state *api.State,
	refused refusedRender,
) planReference {
	if state == nil || refused.planID == "" {
		return planReference{}
	}
	authority := podKey(endpoint)
	if c.roleCarriesRefusedRender(
		authority, state.RunningPlanID, state.RunningPlanProof, refused,
	) {
		return planReference{}
	}
	if c.roleCarriesRefusedRender(
		authority, state.AppliedPlanID, state.AppliedPlanProof, refused,
	) {
		return planReference{id: state.AppliedPlanID, proof: state.AppliedPlanProof}
	}
	if c.roleCarriesRefusedRender(
		authority, state.WorkerOpsPlanID, state.WorkerOpsPlanProof, refused,
	) {
		return planReference{id: state.WorkerOpsPlanID, proof: state.WorkerOpsPlanProof}
	}
	return planReference{}
}

func (c *Component) roleCarriesRefusedRender(
	authority, planID, planProof string,
	refused refusedRender,
) bool {
	return sameOccurrence(
		c.planOccurrence(authority, planID, planProof), refused.occurrence,
	)
}

func applyErrorMessage(result *api.ApplyResult) string {
	if result.Error == nil {
		return "the agent rejected the revert"
	}
	return result.Error.Stage + ": " + result.Error.Message
}

// recordFleet remembers the pods this controller wrote to, so a later revert
// knows where the refused plan can be.
func (c *Component) recordFleet(endpoints []dataplane.Endpoint) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.fleet = endpoints
}

func (c *Component) fleetSnapshot() []dataplane.Endpoint {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.fleet
}

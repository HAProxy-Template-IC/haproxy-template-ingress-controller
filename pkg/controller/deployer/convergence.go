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
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// maxAwaitingConvergence bounds the renders kept for a later observation. A
// pacing window holds a handful of coalesced dispatches; the oldest is dropped
// first, and a dropped render's status is written by the next converged one,
// whose patches cover it.
const maxAwaitingConvergence = 8

// awaitingRender is a render every pod accepted behind a paced reload. Its
// deployment could not report the fleet running it; the ACKs of a later
// deployment do, by naming it as their running plan.
type awaitingRender struct {
	planID          string
	contentChecksum string
	statusPatches   []templating.StatusPatch
}

// observeConvergence turns what the fleet reported running into the status
// its render's own deployment could not publish. Under continuous change a
// render's reload fires between two deployments, and the deployment that
// follows is dispatched before its own reload — so without this the deployed
// status would only ever be written when the change stops.
func (c *Component) observeConvergence(
	event *events.DeploymentScheduledEvent,
	podSetHash string,
	state *deploymentState,
) {
	total := len(event.Endpoints)
	running := state.fleetRunningPlan(total)
	converged := int(atomic.LoadInt32(&state.convergedCount)) == total
	pending := atomic.LoadInt32(&state.pendingReloads) > 0 && atomic.LoadInt32(&state.failureCount) == 0

	c.stateMu.Lock()
	var observed *awaitingRender
	switch {
	case converged:
		// This deployment's own completion writes the newest status; every
		// older render is covered by it.
		c.awaiting = c.awaiting[:0]
	case running != "":
		for i := range c.awaiting {
			if c.awaiting[i].planID != running {
				continue
			}
			entry := c.awaiting[i]
			observed = &entry
			c.awaiting = append(c.awaiting[:0], c.awaiting[i+1:]...)
			break
		}
	}
	if pending {
		c.awaiting = append(c.awaiting, awaitingRender{
			planID:          event.PlanID,
			contentChecksum: event.ContentChecksum,
			statusPatches:   event.StatusPatches,
		})
		if len(c.awaiting) > maxAwaitingConvergence {
			c.awaiting = append(c.awaiting[:0], c.awaiting[len(c.awaiting)-maxAwaitingConvergence:]...)
		}
	}
	c.stateMu.Unlock()

	if observed == nil {
		return
	}
	c.Logger().Debug("Fleet observed running a render whose reloads were pending; publishing its status",
		"plan", observed.planID,
		"observed_by", event.PlanID,
		"correlation_id", event.CorrelationID())
	c.EventBus().Publish(events.NewDeploymentSkippedEvent(
		total,
		events.SkipReasonReloadObserved,
		observed.contentChecksum,
		podSetHash,
		observed.statusPatches,
		events.PropagateCorrelation(event),
	))
}

func (c *Component) forgetAwaitingConvergence() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.awaiting = nil
}

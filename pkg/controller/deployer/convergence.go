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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
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
	occurrence *rendercycle.Occurrence
	planID     string
}

func (a *awaitingRender) matches(running runningRender) bool {
	return sameOccurrence(a.occurrence, running.occurrence)
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
	occurrence *rendercycle.Occurrence,
) {
	total := len(event.Endpoints)
	running := state.fleetRunningRender(total)
	converged := int(atomic.LoadInt32(&state.convergedCount)) == total
	pending := atomic.LoadInt32(&state.pendingReloads) > 0 && atomic.LoadInt32(&state.failureCount) == 0

	var next *awaitingRender
	if pending {
		identity, err := inspectOccurrence(occurrence)
		if err == nil {
			next = &awaitingRender{occurrence: occurrence, planID: identity.planID}
		}
	}
	observed := c.updateAwaitingConvergence(running, converged, next)
	if observed == nil {
		return
	}
	c.Logger().Debug("Fleet observed running a render whose reloads were pending; publishing its status",
		"plan", observed.planID,
		"observed_by", deploymentPlanID(event),
		"correlation_id", event.CorrelationID())
	c.publishObservedConvergence(event, total, podSetHash, observed)
}

func (c *Component) updateAwaitingConvergence(
	running runningRender,
	converged bool,
	next *awaitingRender,
) *awaitingRender {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	var observed *awaitingRender
	if converged {
		c.awaiting = c.awaiting[:0]
	} else if running.plan != nil {
		for i := range c.awaiting {
			if !c.awaiting[i].matches(running) {
				continue
			}
			entry := c.awaiting[i]
			observed = &entry
			c.awaiting = append(c.awaiting[:0], c.awaiting[i+1:]...)
			break
		}
	}
	if next != nil {
		c.awaiting = append(c.awaiting, *next)
		if len(c.awaiting) > maxAwaitingConvergence {
			c.awaiting = append(c.awaiting[:0], c.awaiting[len(c.awaiting)-maxAwaitingConvergence:]...)
		}
	}
	return observed
}

func (c *Component) publishObservedConvergence(
	event *events.DeploymentScheduledEvent,
	total int,
	podSetHash string,
	observed *awaitingRender,
) {
	skipped, err := events.NewDeploymentSkippedEventWithCycle(
		observed.occurrence, total, events.SkipReasonReloadObserved, podSetHash,
		events.PropagateCorrelation(event),
	)
	if err != nil {
		c.Logger().Error("Refusing to publish an unauthenticated convergence event", "error", err)
		return
	}
	c.EventBus().Publish(skipped)
}

func (c *Component) forgetAwaitingConvergence() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.awaiting = nil
}

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
	"fmt"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

type planCacheKey struct {
	authority string
	proof     string
}

// planCache binds each pod's agent-issued role proof to the exact plan this
// controller sent and the render occurrence the agent acknowledged.
type planCache struct {
	mu       sync.Mutex
	plans    map[planCacheKey]*cachedPlan
	unusable map[planCacheKey]struct{}
}

type cachedPlan struct {
	plan       *renderplan.Plan
	occurrence *rendercycle.Occurrence
}

func newPlanCache() *planCache {
	return &planCache{
		plans:    map[planCacheKey]*cachedPlan{},
		unusable: map[planCacheKey]struct{}{},
	}
}

// Bind records an agent-proved plan imported without a local render occurrence.
func (c *planCache) Bind(authority, id, proof string, plan *renderplan.Plan) bool {
	return c.bind(authority, id, proof, plan, nil)
}

func (c *planCache) BindOccurrence(
	authority, id, proof string,
	plan *renderplan.Plan,
	occurrence *rendercycle.Occurrence,
) error {
	identity, err := materializeOccurrence(occurrence)
	if err != nil {
		return err
	}
	if identity.planID != id || !exactPlan(identity.plan, plan) {
		return fmt.Errorf("render occurrence does not carry plan %s", id)
	}
	if !c.bind(authority, id, proof, plan, occurrence) {
		return fmt.Errorf("plan %s conflicts with the plan already bound to this role proof", id)
	}
	return nil
}

func (c *planCache) bind(
	authority, id, proof string,
	plan *renderplan.Plan,
	occurrence *rendercycle.Occurrence,
) bool {
	if authority == "" || id == "" || proof == "" || plan == nil || plan.ID != id ||
		!exactPlan(plan, plan) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := planCacheKey{authority: authority, proof: proof}
	if existing, ok := c.plans[key]; ok {
		if sameCachedPlan(existing, plan, occurrence) {
			return true
		}
		c.plans[key] = nil
		return false
	}
	owned := plan.Clone()
	if occurrence != nil {
		owned = plan
	}
	c.plans[key] = &cachedPlan{plan: owned, occurrence: occurrence}
	return true
}

func sameCachedPlan(
	existing *cachedPlan,
	plan *renderplan.Plan,
	occurrence *rendercycle.Occurrence,
) bool {
	if existing == nil || !exactPlan(existing.plan, plan) {
		return false
	}
	if existing.occurrence != nil || occurrence != nil {
		return sameOccurrence(existing.occurrence, occurrence)
	}
	return true
}

// Plan resolves a pod role only through its pod-scoped agent proof.
func (c *planCache) Plan(authority, id, proof string) *renderplan.Plan {
	if authority == "" || id == "" || proof == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.plans[planCacheKey{authority: authority, proof: proof}]
	if entry == nil || entry.plan == nil || entry.plan.ID != id {
		return nil
	}
	return entry.plan
}

// Occurrence resolves the render occurrence bound to a role proof. One that
// stopped authenticating is corruption, not an absent binding.
func (c *planCache) Occurrence(authority, id, proof string) (*rendercycle.Occurrence, error) {
	if authority == "" || id == "" || proof == "" {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.plans[planCacheKey{authority: authority, proof: proof}]
	if entry == nil || entry.plan == nil || entry.plan.ID != id || entry.occurrence == nil {
		return nil, nil
	}
	if err := entry.occurrence.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("cached render occurrence for plan %s: %w", id, err)
	}
	return entry.occurrence, nil
}

func (c *planCache) Baseline(authority string, state *api.State) *renderplan.Plan {
	if state == nil {
		return nil
	}
	return c.Plan(authority, state.AppliedPlanID, state.AppliedPlanProof)
}

// AdoptMeasured imports the plan a pod reports as a diff baseline, so a leader
// that never sent it composes ops instead of reloading the fleet.
//
// The blob on its own is not evidence: a controller that did not write it cannot
// vouch for its bytes. What makes it usable is the pod's freshly measured tree --
// every file the decoded plan declares has to be on disk at the same digest and
// size, the same evidence prepareContentProofs already trusts a file on. A plan
// the measurement cannot fully account for is no baseline at all; diffing
// against a guess would compose ops for a pod running something else.
//
// Callers must pass a state read with verify, or the digests are remembered
// rather than observed.
func (c *planCache) AdoptMeasured(authority string, state *api.State) *renderplan.Plan {
	if state == nil || authority == "" || state.AppliedPlanID == "" ||
		state.AppliedPlanProof == "" || len(state.AppliedPlan) == 0 {
		return nil
	}
	if plan := c.Plan(authority, state.AppliedPlanID, state.AppliedPlanProof); plan != nil {
		return plan
	}
	key := planCacheKey{authority: authority, proof: state.AppliedPlanProof}
	c.mu.Lock()
	_, refused := c.unusable[key]
	c.mu.Unlock()
	if refused {
		return nil
	}
	plan, err := planblob.Decode(state.AppliedPlan)
	if err != nil || plan.ID != state.AppliedPlanID ||
		plan.SchemaVersion != renderplan.SchemaVersion || !measuredHoldsPlan(state, plan) {
		c.mu.Lock()
		c.unusable[key] = struct{}{}
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, taken := c.plans[key]; taken {
		return nil
	}
	c.plans[key] = &cachedPlan{plan: plan}
	return plan
}

// measuredHoldsPlan reports whether the pod's measured tree accounts for every
// file the plan declares.
func measuredHoldsPlan(state *api.State, plan *renderplan.Plan) bool {
	if len(plan.Files) == 0 {
		return false
	}
	for i := range plan.Files {
		file := &plan.Files[i]
		at, present := state.Files[file.Path]
		if !present || at.Digest != file.Digest || at.Size != file.Size {
			return false
		}
	}
	return true
}

// Retain drops bindings for pods that left the fleet and role proofs they no
// longer report.
func (c *planCache) Retain(referenced []planCacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := make(map[planCacheKey]struct{}, len(referenced))
	for _, ref := range referenced {
		if ref.authority != "" && ref.proof != "" {
			keep[ref] = struct{}{}
		}
	}
	for ref := range c.plans {
		if _, wanted := keep[ref]; !wanted {
			delete(c.plans, ref)
		}
	}
	for ref := range c.unusable {
		if _, wanted := keep[ref]; !wanted {
			delete(c.unusable, ref)
		}
	}
}

func exactPlan(left, right *renderplan.Plan) bool {
	return left != nil && right != nil && left.ID == right.ID && renderplan.ExactlyEqual(left, right)
}

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

package templating

import (
	"errors"
	"fmt"

	projection "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

// StatusPatchProjectionPlan is an immutable persistent index of named projections.
type StatusPatchProjectionPlan struct {
	root      *projection.PlanRoot
	integrity *statusPatchProjectionPlanIntegrity
	seal      *StatusPatchProjectionPlan
}

// StatusPatchProjectionPlanEntry identifies one ordered projection within a conflict group.
type StatusPatchProjectionPlanEntry struct {
	Group      string
	Entry      string
	Projection *StatusPatchProjection
}

type statusPatchProjectionPlanIntegrity struct {
	owner *StatusPatchProjectionPlan
	root  *projection.PlanRoot
	seal  *statusPatchProjectionPlanIntegrity
}

// StatusPatchProjectionPlanReplay authenticates one plan for synchronous replay.
type StatusPatchProjectionPlanReplay struct {
	plan *StatusPatchProjectionPlan
	root *projection.PlanRoot
	seal *StatusPatchProjectionPlanReplay
}

type statusPatchProjectionPlanBinding struct {
	collector *StatusPatchCollector
	replay    *StatusPatchProjectionPlanReplay
	plan      *StatusPatchProjectionPlan
	root      *projection.PlanRoot
	seal      *statusPatchProjectionPlanBinding
}

// NewStatusPatchProjectionPlan returns an empty authenticated projection plan.
func NewStatusPatchProjectionPlan() *StatusPatchProjectionPlan {
	plan := &StatusPatchProjectionPlan{}
	plan.seal = plan
	root, err := projection.NewPlan(plan)
	if err != nil {
		panic(err)
	}
	plan.root = root
	plan.integrity = newStatusPatchProjectionPlanIntegrity(plan)
	return plan
}

// NewStatusPatchProjectionPlanFromEntries builds an authenticated plan atomically from ordered entries.
func NewStatusPatchProjectionPlanFromEntries(
	entries []StatusPatchProjectionPlanEntry,
) (*StatusPatchProjectionPlan, error) {
	plan := &StatusPatchProjectionPlan{}
	plan.seal = plan
	owned := make([]projection.PlanEntry, len(entries))
	for index := range entries {
		entry := &entries[index]
		if entry.Group == "" {
			return nil, fmt.Errorf("statusPatch projection plan entry %d has an empty group", index)
		}
		if entry.Projection == nil {
			return nil, fmt.Errorf("statusPatch projection plan entry %d group %q is nil", index, entry.Group)
		}
		if err := entry.Projection.ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf(
				"statusPatch projection plan entry %d group %q: %w", index, entry.Group, err,
			)
		}
		owned[index] = projection.PlanEntry{
			Group: entry.Group, Entry: entry.Entry,
			Root: entry.Projection.root, Owner: entry.Projection,
		}
	}
	root, err := projection.NewPlanFromEntries(plan, owned)
	if err != nil {
		return nil, fmt.Errorf("statusPatch projection plan: %w", err)
	}
	plan.root = root
	plan.integrity = newStatusPatchProjectionPlanIntegrity(plan)
	return plan, nil
}

// ReplaceGroup returns a fresh plan with group replaced, or the same plan when already exact.
func (p *StatusPatchProjectionPlan) ReplaceGroup(
	group string,
	statusProjection *StatusPatchProjection,
) (*StatusPatchProjectionPlan, error) {
	return p.ReplaceEntry(group, "", statusProjection)
}

// ReplaceEntry replaces one lexically ordered projection within a conflict group.
func (p *StatusPatchProjectionPlan) ReplaceEntry(
	group, entry string,
	statusProjection *StatusPatchProjection,
) (*StatusPatchProjectionPlan, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if group == "" {
		return nil, errors.New("statusPatch projection plan group is empty")
	}
	var root *projection.Root
	var rootOwner any
	if statusProjection != nil {
		if err := statusProjection.ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf("statusPatch projection plan group %q: %w", group, err)
		}
		root = statusProjection.root
		rootOwner = statusProjection
	}
	exact, err := p.root.ExactEntry(p, group, entry, root, rootOwner)
	if err != nil {
		return nil, fmt.Errorf("statusPatch projection plan group %q: %w", group, err)
	}
	if exact {
		return p, nil
	}
	next := &StatusPatchProjectionPlan{}
	next.seal = next
	next.root, err = p.root.ReplaceEntry(p, next, group, entry, root, rootOwner)
	if err != nil {
		return nil, fmt.Errorf("statusPatch projection plan group %q: %w", group, err)
	}
	next.integrity = newStatusPatchProjectionPlanIntegrity(next)
	return next, nil
}

func newStatusPatchProjectionPlanIntegrity(
	owner *StatusPatchProjectionPlan,
) *statusPatchProjectionPlanIntegrity {
	integrity := &statusPatchProjectionPlanIntegrity{owner: owner, root: owner.root}
	integrity.seal = integrity
	return integrity
}

// PrepareReplay validates the plan and returns an authenticated replay view.
func (p *StatusPatchProjectionPlan) PrepareReplay() (*StatusPatchProjectionPlanReplay, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return nil, err
	}
	replay := &StatusPatchProjectionPlanReplay{plan: p, root: p.root}
	replay.seal = replay
	return replay, nil
}

// ValidateAuthentication verifies the plan's exact private ownership chain.
func (p *StatusPatchProjectionPlan) ValidateAuthentication() error {
	if p == nil || p.seal != p || p.root == nil || p.integrity == nil ||
		p.integrity.seal != p.integrity || p.integrity.owner != p || p.integrity.root != p.root {
		return errors.New("statusPatch projection plan has invalid provenance")
	}
	if err := p.root.Validate(p); err != nil {
		return fmt.Errorf("statusPatch projection plan has invalid provenance: %w", err)
	}
	return nil
}

func (r *StatusPatchProjectionPlanReplay) valid() bool {
	return r != nil && r.seal == r && r.plan != nil && r.root != nil &&
		r.root == r.plan.root && r.plan.ValidateAuthentication() == nil
}

func (r *StatusPatchProjectionPlanReplay) targetCount() (int, error) {
	if !r.valid() {
		return 0, errors.New("statusPatch projection plan replay has invalid provenance")
	}
	return r.root.TargetCount(r.plan)
}

// Empty reports whether the plan contains no target resources.
func (r *StatusPatchProjectionPlanReplay) Empty() bool {
	count, err := r.targetCount()
	return err == nil && count == 0
}

func (r *StatusPatchProjectionPlanReplay) containsTarget(key statusPatchIdentity) (bool, error) {
	if !r.valid() {
		return false, errors.New("statusPatch projection plan replay has invalid provenance")
	}
	return r.root.ContainsTarget(r.plan, key.namespace, key.name, key.apiVersion, key.kind)
}

func (r *StatusPatchProjectionPlanReplay) validateLineage(patch *collectedStatusPatch) error {
	if !r.valid() {
		return errors.New("statusPatch projection plan replay has invalid provenance")
	}
	if patch == nil {
		return errors.New("statusPatch projection plan direct patch has invalid provenance")
	}
	return r.root.ValidateLineage(
		r.plan,
		patch.Namespace,
		patch.Name,
		patch.APIVersion,
		patch.Kind,
		patch.UID,
		patch.ResourceVersion,
	)
}

func (r *StatusPatchProjectionPlanReplay) visitPatches(
	visit func(*StatusPatchProjection, projection.PatchView) error,
) error {
	if !r.valid() {
		return errors.New("statusPatch projection plan replay has invalid provenance")
	}
	if visit == nil {
		return errors.New("statusPatch projection plan patch visitor is nil")
	}
	return r.root.VisitGroups(r.plan, func(group projection.PlanGroup) error {
		owner, ok := group.Owner.(*StatusPatchProjection)
		if !ok || owner == nil || owner.root != group.Root {
			return fmt.Errorf("statusPatch projection plan group %q has invalid provenance", group.Name)
		}
		if err := owner.ValidateAuthentication(); err != nil {
			return fmt.Errorf("statusPatch projection plan group %q: %w", group.Name, err)
		}
		return owner.visitPatches(visit)
	})
}

func exactStatusPatchProjectionPlanReplays(
	left, right *StatusPatchProjectionPlanReplay,
) bool {
	return left == nil && right == nil || left != nil && right != nil &&
		left.valid() && right.valid() && left.plan == right.plan && left.root == right.root
}

func (c *StatusPatchCollector) bindProjectionPlan(replay *StatusPatchProjectionPlanReplay) {
	binding := &statusPatchProjectionPlanBinding{
		collector: c, replay: replay, plan: replay.plan, root: replay.root,
	}
	binding.seal = binding
	c.projectionPlan = replay
	c.planBinding = binding
}

func (c *StatusPatchCollector) validProjectionPlanBinding() bool {
	return c != nil && c.projectionPlan != nil && c.planBinding != nil &&
		c.planBinding.seal == c.planBinding && c.planBinding.collector == c &&
		c.planBinding.replay == c.projectionPlan && c.planBinding.plan == c.projectionPlan.plan &&
		c.planBinding.root == c.projectionPlan.root && c.projectionPlan.valid()
}

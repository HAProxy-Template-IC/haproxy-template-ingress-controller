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

// StatusPatchProjection is an immutable compiled set of status-patch calls.
type StatusPatchProjection struct {
	root      *projection.Root
	integrity *statusPatchProjectionIntegrity
	seal      *StatusPatchProjection
}

type statusPatchProjectionIntegrity struct {
	owner *StatusPatchProjection
	root  *projection.Root
	seal  *statusPatchProjectionIntegrity
}

// StatusPatchProjectionReplay authenticates one projection for synchronous replay.
type StatusPatchProjectionReplay struct {
	projection *StatusPatchProjection
	root       *projection.Root
	seal       *StatusPatchProjectionReplay
}

// StatusPatchProjectionClaim identifies one target phase owned by a projection.
type StatusPatchProjectionClaim struct {
	Namespace       string
	Name            string
	APIVersion      string
	Kind            string
	UID             string
	ResourceVersion string
	Phase           string
}

// NewStatusPatchProjection compiles ordered status-patch calls into immutable storage.
func NewStatusPatchProjection(calls []StatusPatch) (*StatusPatchProjection, error) {
	result := &StatusPatchProjection{}
	result.seal = result
	inputs := make([]projection.InputPatch, len(calls))
	for index := range calls {
		call := &calls[index]
		for phase := range call.Variants {
			if err := validateStatusPatchPhase(phase); err != nil {
				return nil, fmt.Errorf("statusPatch call %d: %w", index, err)
			}
		}
		inputs[index] = projection.InputPatch{
			Namespace: call.Namespace, Name: call.Name, APIVersion: call.APIVersion, Kind: call.Kind,
			UID: call.UID, ResourceVersion: call.ResourceVersion, Variants: call.Variants,
			SourceTemplate: call.SourceTemplate, SourceLine: call.SourceLine,
		}
	}
	root, err := projection.New(result, inputs)
	if err != nil {
		return nil, fmt.Errorf("statusPatch projection: %w", err)
	}
	result.root = root
	result.integrity = newStatusPatchProjectionIntegrity(result)
	return result, nil
}

// NewStatusPatchProjectionGroup composes ordered immutable projections without copying their values.
func NewStatusPatchProjectionGroup(parts []*StatusPatchProjection) (*StatusPatchProjection, error) {
	result := &StatusPatchProjection{}
	result.seal = result
	owned := make([]projection.Part, len(parts))
	for index, part := range parts {
		if err := part.ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf("statusPatch projection part %d: %w", index, err)
		}
		owned[index] = projection.Part{Root: part.root, Owner: part}
	}
	root, err := projection.NewGroup(result, owned)
	if err != nil {
		return nil, fmt.Errorf("statusPatch projection group: %w", err)
	}
	result.root = root
	result.integrity = newStatusPatchProjectionIntegrity(result)
	return result, nil
}

func newStatusPatchProjectionIntegrity(owner *StatusPatchProjection) *statusPatchProjectionIntegrity {
	integrity := &statusPatchProjectionIntegrity{owner: owner, root: owner.root}
	integrity.seal = integrity
	return integrity
}

func validateStatusPatchPhase(phase string) error {
	switch phase {
	case statusPhaseRendered, statusPhaseDeployed, statusPhaseRenderFailed, statusPhaseDeployFailed:
		return nil
	default:
		return fmt.Errorf("invalid phase %q, must be one of: rendered, deployed, renderFailed, deployFailed", phase)
	}
}

// PrepareReplay validates the projection and returns an authenticated replay view.
func (p *StatusPatchProjection) PrepareReplay() (*StatusPatchProjectionReplay, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return nil, err
	}
	replay := &StatusPatchProjectionReplay{projection: p, root: p.root}
	replay.seal = replay
	return replay, nil
}

// ValidateAuthentication verifies the projection's private ownership seal.
func (p *StatusPatchProjection) ValidateAuthentication() error {
	if p == nil || p.seal != p || p.root == nil || p.integrity == nil ||
		p.integrity.seal != p.integrity || p.integrity.owner != p || p.integrity.root != p.root {
		return errors.New("projection has invalid provenance")
	}
	if err := p.root.Validate(p); err != nil {
		return fmt.Errorf("projection has invalid provenance: %w", err)
	}
	return nil
}

func (p *StatusPatchProjection) auditIntegrity() error {
	if err := p.ValidateAuthentication(); err != nil {
		return fmt.Errorf("projection failed its integrity check: %w", err)
	}
	return nil
}

func (r *StatusPatchProjectionReplay) valid() bool {
	return r != nil && r.seal == r && r.projection != nil && r.root != nil &&
		r.root == r.projection.root && r.projection.ValidateAuthentication() == nil
}

// Empty reports whether the projection contains no status-patch calls.
func (r *StatusPatchProjectionReplay) Empty() bool {
	if !r.valid() {
		return false
	}
	count, err := r.root.PatchCount(r.projection)
	return err == nil && count == 0
}

// VisitClaims visits every unique target phase in deterministic order.
func (r *StatusPatchProjectionReplay) VisitClaims(visit func(StatusPatchProjectionClaim) error) error {
	if !r.valid() {
		return errors.New("statusPatch projection replay has invalid provenance")
	}
	if visit == nil {
		return errors.New("statusPatch projection claim visitor is nil")
	}
	return r.projection.visitPatches(func(_ *StatusPatchProjection, patch projection.PatchView) error {
		metadata, err := patch.Metadata()
		if err != nil {
			return err
		}
		return patch.VisitPhases(func(phase projection.PhaseView) error {
			phaseName, err := phase.Name()
			if err != nil {
				return err
			}
			return visit(StatusPatchProjectionClaim{
				Namespace: metadata.Namespace, Name: metadata.Name, APIVersion: metadata.APIVersion,
				Kind: metadata.Kind, UID: metadata.UID, ResourceVersion: metadata.ResourceVersion,
				Phase: phaseName,
			})
		})
	})
}

// ReplayProjections merges authenticated projections without exposing their cached values.
func (c *StatusPatchCollector) ReplayProjections(replays []*StatusPatchProjectionReplay) error {
	for index, replay := range replays {
		if !replay.valid() {
			return fmt.Errorf("statusPatch projection replay %d has invalid provenance", index)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return errors.New("statusPatch: collector is sealed")
	}
	if c.projectionPlan != nil {
		return errors.New("statusPatch: collector already has a projection plan")
	}
	type lineage struct {
		uid             string
		resourceVersion string
	}
	lineages := make(map[statusPatchIdentity]lineage, len(c.patches))
	for key, patch := range c.patches {
		if patch == nil || patch.owner != c ||
			patch.lineageDigest != statusPatchLineageDigest(patch.UID, patch.ResourceVersion) {
			return errors.New("statusPatch: existing patch has invalid provenance")
		}
		lineages[key] = lineage{uid: patch.UID, resourceVersion: patch.ResourceVersion}
	}
	for _, replay := range replays {
		if err := replay.projection.visitPatches(func(_ *StatusPatchProjection, projected projection.PatchView) error {
			metadata, err := projected.Metadata()
			if err != nil {
				return err
			}
			key := newStatusPatchIdentity(metadata.Namespace, metadata.Name, metadata.APIVersion, metadata.Kind)
			candidate := lineage{uid: metadata.UID, resourceVersion: metadata.ResourceVersion}
			if existing, found := lineages[key]; found && existing != candidate {
				return fmt.Errorf("statusPatch: %s/%s has conflicting source lineage", metadata.Namespace, metadata.Name)
			}
			lineages[key] = candidate
			return nil
		}); err != nil {
			return err
		}
	}
	for _, replay := range replays {
		c.projections = append(c.projections, replay)
		if err := replay.projection.visitPatches(c.replayPatch); err != nil {
			return err
		}
	}
	return nil
}

// ReplayProjectionPlan stages an authenticated persistent plan without expanding it.
func (c *StatusPatchCollector) ReplayProjectionPlan(replay *StatusPatchProjectionPlanReplay) error {
	if c == nil {
		return errors.New("statusPatch: collector is nil")
	}
	if !replay.valid() {
		return errors.New("statusPatch projection plan replay has invalid provenance")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return errors.New("statusPatch: collector is sealed")
	}
	if c.projectionPlan != nil || len(c.projections) != 0 {
		return errors.New("statusPatch: collector already has cached projections")
	}
	for key, patch := range c.patches {
		if patch == nil || patch.owner != c ||
			patch.Namespace != key.namespace || patch.Name != key.name ||
			patch.APIVersion != key.apiVersion || patch.Kind != key.kind ||
			patch.lineageDigest != statusPatchLineageDigest(patch.UID, patch.ResourceVersion) {
			return errors.New("statusPatch: existing patch has invalid provenance")
		}
		if err := replay.validateLineage(patch); err != nil {
			return fmt.Errorf("statusPatch: %w", err)
		}
	}
	c.bindProjectionPlan(replay)
	return nil
}

func (c *StatusPatchCollector) replayPatch(
	owner *StatusPatchProjection,
	projected projection.PatchView,
) error {
	metadata, err := projected.Metadata()
	if err != nil {
		return err
	}
	key := newStatusPatchIdentity(metadata.Namespace, metadata.Name, metadata.APIVersion, metadata.Kind)
	patch := c.patches[key]
	if patch == nil {
		patch = &collectedStatusPatch{
			Namespace: metadata.Namespace, Name: metadata.Name, APIVersion: metadata.APIVersion, Kind: metadata.Kind,
			UID: metadata.UID, ResourceVersion: metadata.ResourceVersion,
			Variants: make(map[string]collectedStatusPatchVariant), owner: c,
		}
		patch.sourceDigest = statusPatchSourceDigest("", 0)
		patch.lineageDigest = statusPatchLineageDigest(metadata.UID, metadata.ResourceVersion)
		c.patches[key] = patch
		c.order = append(c.order, key)
	}
	if patch.SourceTemplate == "" && metadata.SourceTemplate != "" {
		patch.SourceTemplate = metadata.SourceTemplate
		patch.SourceLine = metadata.SourceLine
		patch.sourceDigest = statusPatchSourceDigest(metadata.SourceTemplate, metadata.SourceLine)
	}
	return projected.VisitPhases(func(phase projection.PhaseView) error {
		phaseName, err := phase.Name()
		if err != nil {
			return err
		}
		patch.Variants[phaseName] = collectedStatusPatchVariant{
			projected: phase, hasProjected: true, projection: owner, owner: c, sourcePatch: projected,
		}
		return nil
	})
}

func (p *StatusPatchProjection) visitPatches(
	visit func(*StatusPatchProjection, projection.PatchView) error,
) error {
	if err := p.ValidateAuthentication(); err != nil {
		return err
	}
	return p.root.Visit(p, func(projected projection.PatchView) error {
		owner, err := projected.Owner()
		if err != nil {
			return err
		}
		projectionOwner, ok := owner.(*StatusPatchProjection)
		if !ok || projectionOwner.ValidateAuthentication() != nil {
			return errors.New("statusPatch projection has invalid leaf provenance")
		}
		return visit(projectionOwner, projected)
	})
}

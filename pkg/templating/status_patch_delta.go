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
	"slices"
	"strings"

	projection "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

// ChangedPatchesForPhase materializes current patches whose contributions changed.
// A nil previous selects everything; removed contributions may expose older variants.
func (s *StatusPatchSnapshot) ChangedPatchesForPhase(previous *StatusPatchSnapshot, phase string) ([]StatusPatch, error) {
	if phase == "" {
		return nil, errors.New("statusPatch snapshot phase is empty")
	}
	if s == nil || s.collector == nil {
		return nil, errors.New("statusPatch snapshot has invalid provenance")
	}
	if previous == nil {
		return s.PatchesForPhase(phase)
	}
	if err := previous.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("statusPatch: previous snapshot: %w", err)
	}
	if previous == s {
		return nil, nil
	}
	s.collector.mu.RLock()
	defer s.collector.mu.RUnlock()
	if err := s.validateLocked(); err != nil {
		return nil, err
	}
	return s.collector.materializeChangedLocked(previous.collector, phase)
}

// materializeChangedLocked is materializeLocked over the patches that differ
// from previous, a frozen collector read without its lock like every previous
// snapshot is. Caller holds c.mu.
func (c *StatusPatchCollector) materializeChangedLocked(
	previous *StatusPatchCollector,
	phase string,
) ([]StatusPatch, error) {
	if err := c.validateMaterializeProvenanceLocked(); err != nil {
		return nil, err
	}
	changed := make(map[statusPatchIdentity]bool)
	for index, key := range c.order {
		patch := c.patches[key]
		if patch == nil || patch.Namespace != key.namespace || patch.Name != key.name ||
			patch.APIVersion != key.apiVersion || patch.Kind != key.kind || patch.owner != c {
			return nil, fmt.Errorf("statusPatch: patch %d has invalid provenance", index)
		}
		before := previous.patches[key]
		if before != nil && sameFrozenCollectedStatusPatch(patch, c, before, previous) {
			continue
		}
		// Unchanged patches retain their frozen predecessor's authenticated digests.
		if patch.sourceDigest != statusPatchSourceDigest(patch.SourceTemplate, patch.SourceLine) ||
			patch.lineageDigest != statusPatchLineageDigest(patch.UID, patch.ResourceVersion) {
			return nil, fmt.Errorf("statusPatch: patch %d has invalid provenance", index)
		}
		changed[key] = true
	}
	for _, key := range previous.order {
		if c.patches[key] == nil {
			changed[key] = true
		}
	}
	if err := c.markChangedProjectionsLocked(previous, changed); err != nil {
		return nil, err
	}
	return c.materializeSelectedLocked(changed, phase)
}

func (c *StatusPatchCollector) materializeSelectedLocked(changed map[statusPatchIdentity]bool, phase string) ([]StatusPatch, error) {
	result, err := c.materializeKeysLocked(changed, phase)
	if err != nil || c.projectionPlan == nil {
		return result, err
	}
	resultByKey := make(map[statusPatchIdentity]int, len(result))
	for index := range result {
		patch := &result[index]
		resultByKey[newStatusPatchIdentity(patch.Namespace, patch.Name, patch.APIVersion, patch.Kind)] = index
	}
	var projectedPatches []projection.PlanPatch
	for key := range changed {
		if err := c.projectionPlan.visitTargetPatches(key, func(patch projection.PlanPatch) error {
			projectedPatches = append(projectedPatches, patch)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(projectedPatches, func(left, right projection.PlanPatch) int {
		if order := strings.Compare(left.EntryKey, right.EntryKey); order != 0 {
			return order
		}
		return left.Position - right.Position
	})
	for _, patch := range projectedPatches {
		result, err = mergeProjectedStatusPatch(result, resultByKey, patch.View, phase)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// materializeKeysLocked materializes the phase variants of the collected
// patches in changed, in collector order. Caller holds c.mu.
func (c *StatusPatchCollector) materializeKeysLocked(
	changed map[statusPatchIdentity]bool,
	phase string,
) ([]StatusPatch, error) {
	result := make([]StatusPatch, 0, len(changed))
	for _, key := range c.order {
		if !changed[key] {
			continue
		}
		patch := c.patches[key]
		if patch == nil || patch.owner != c || patch.Namespace != key.namespace || patch.Name != key.name ||
			patch.APIVersion != key.apiVersion || patch.Kind != key.kind || !collectedStatusPatchDigestsValid(patch) {
			return nil, errors.New("statusPatch: selected patch has invalid provenance")
		}
		variants, err := c.materializePatchVariantsLocked(key, patch, phase)
		if err != nil {
			return nil, err
		}
		if len(variants) == 0 {
			continue
		}
		result = append(result, StatusPatch{
			Namespace: patch.Namespace, Name: patch.Name, APIVersion: patch.APIVersion, Kind: patch.Kind,
			UID: patch.UID, ResourceVersion: patch.ResourceVersion,
			Variants: variants, SourceTemplate: patch.SourceTemplate, SourceLine: patch.SourceLine,
		})
	}
	return result, nil
}

// Removed contributions can expose a direct patch or an earlier projected variant.
func (c *StatusPatchCollector) markChangedProjectionsLocked(
	previous *StatusPatchCollector,
	changed map[statusPatchIdentity]bool,
) error {
	if exactStatusPatchProjectionPlanReplays(c.projectionPlan, previous.projectionPlan) {
		return nil
	}
	if c.projectionPlan != nil && previous.projectionPlan != nil {
		return c.projectionPlan.visitChangedTargets(previous.projectionPlan, func(key statusPatchIdentity) error {
			changed[key] = true
			return nil
		})
	}
	plan := c.projectionPlan
	if plan == nil {
		plan = previous.projectionPlan
	}
	return plan.visitPatches(func(_ *StatusPatchProjection, projected projection.PatchView) error {
		key, err := projectedIdentity(projected)
		if err != nil {
			return err
		}
		changed[key] = true
		return nil
	})
}

// sameFrozenCollectedStatusPatch is exactCollectedStatusPatch for a current
// patch whose provenance the caller has just checked against a patch of a
// frozen collector, whose provenance was checked when it was sealed: the
// digests are compared as stored instead of being recomputed twice more per
// patch. A patch without lineage is never the same patch.
func sameFrozenCollectedStatusPatch(
	left *collectedStatusPatch,
	leftOwner *StatusPatchCollector,
	right *collectedStatusPatch,
	rightOwner *StatusPatchCollector,
) bool {
	if left.UID == "" || left.ResourceVersion == "" || right.owner != rightOwner ||
		left.UID != right.UID || left.ResourceVersion != right.ResourceVersion ||
		left.SourceTemplate != right.SourceTemplate || left.SourceLine != right.SourceLine ||
		left.sourceDigest != right.sourceDigest || left.lineageDigest != right.lineageDigest ||
		len(left.Variants) != len(right.Variants) {
		return false
	}
	for phase, leftVariant := range left.Variants {
		rightVariant, exists := right.Variants[phase]
		if !exists || !exactCollectedStatusPatchVariant(&leftVariant, leftOwner, &rightVariant, rightOwner) {
			return false
		}
	}
	return true
}

func projectedIdentity(projected projection.PatchView) (statusPatchIdentity, error) {
	metadata, err := projected.Metadata()
	if err != nil {
		return statusPatchIdentity{}, err
	}
	return newStatusPatchIdentity(metadata.Namespace, metadata.Name, metadata.APIVersion, metadata.Kind), nil
}

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
	"reflect"
	"slices"
)

// StatusPatchSnapshot is an authenticated immutable status-patch set.
type StatusPatchSnapshot struct {
	collector *StatusPatchCollector
	count     int
	seal      *StatusPatchSnapshot
}

// Snapshot seals the collector and reuses previous when every patch is exact.
func (c *StatusPatchCollector) Snapshot(previous ...*StatusPatchSnapshot) (*StatusPatchSnapshot, error) {
	if c == nil {
		return nil, errors.New("statusPatch: collector is nil")
	}
	if len(previous) > 1 {
		return nil, errors.New("statusPatch: more than one previous snapshot")
	}
	var prior *StatusPatchSnapshot
	if len(previous) == 1 && previous[0] != nil {
		if err := previous[0].ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf("statusPatch: previous snapshot: %w", err)
		}
		prior = previous[0]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot != nil {
		if err := c.snapshot.validateLocked(); err != nil {
			return nil, err
		}
		return c.snapshot, nil
	}
	for index, replay := range c.projections {
		if !replay.valid() {
			return nil, fmt.Errorf("statusPatch: cached projection %d has invalid provenance", index)
		}
	}
	if c.projectionPlan != nil && !c.validProjectionPlanBinding() {
		return nil, errors.New("statusPatch: cached projection plan has invalid provenance")
	}
	if len(c.order) != len(c.patches) {
		return nil, errors.New("statusPatch: collector ordering has invalid provenance")
	}
	if prior != nil && exactStatusPatchCollectors(c, prior.collector) {
		c.frozen = true
		c.snapshot = prior
		return prior, nil
	}
	count, err := c.targetCountLocked()
	if err != nil {
		return nil, err
	}
	snapshot := &StatusPatchSnapshot{collector: c, count: count}
	snapshot.seal = snapshot
	c.frozen = true
	c.snapshot = snapshot
	return snapshot, nil
}

func exactStatusPatchCollectors(current, previous *StatusPatchCollector) bool {
	if current == nil || previous == nil || !previous.frozen ||
		len(current.patches) != len(previous.patches) ||
		!slices.Equal(current.order, previous.order) ||
		len(current.projections) != len(previous.projections) ||
		!exactStatusPatchProjectionPlanReplays(current.projectionPlan, previous.projectionPlan) {
		return false
	}
	for index := range current.projections {
		left := current.projections[index]
		right := previous.projections[index]
		if !left.valid() || !right.valid() || left.projection != right.projection || left.root != right.root {
			return false
		}
	}
	for _, key := range current.order {
		left := current.patches[key]
		right := previous.patches[key]
		if !exactCollectedStatusPatch(left, current, right, previous, key) {
			return false
		}
	}
	return true
}

func exactCollectedStatusPatch(
	left *collectedStatusPatch,
	leftOwner *StatusPatchCollector,
	right *collectedStatusPatch,
	rightOwner *StatusPatchCollector,
	key statusPatchIdentity,
) bool {
	if left == nil || right == nil || left.owner != leftOwner || right.owner != rightOwner {
		return false
	}
	if !collectedStatusPatchMatchesKey(left, key) || !collectedStatusPatchMatchesKey(right, key) {
		return false
	}
	if !collectedStatusPatchDigestsValid(left) || !collectedStatusPatchDigestsValid(right) {
		return false
	}
	if left.UID != right.UID || left.ResourceVersion != right.ResourceVersion ||
		left.SourceTemplate != right.SourceTemplate || left.SourceLine != right.SourceLine ||
		left.sourceDigest != right.sourceDigest ||
		left.lineageDigest != right.lineageDigest ||
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

func collectedStatusPatchMatchesKey(patch *collectedStatusPatch, key statusPatchIdentity) bool {
	return patch.UID != "" && patch.ResourceVersion != "" &&
		patch.Namespace == key.namespace && patch.Name == key.name &&
		patch.APIVersion == key.apiVersion && patch.Kind == key.kind
}

func collectedStatusPatchDigestsValid(patch *collectedStatusPatch) bool {
	return patch.lineageDigest == statusPatchLineageDigest(patch.UID, patch.ResourceVersion) &&
		patch.sourceDigest == statusPatchSourceDigest(patch.SourceTemplate, patch.SourceLine)
}

func exactCollectedStatusPatchVariant(
	left *collectedStatusPatchVariant,
	leftOwner *StatusPatchCollector,
	right *collectedStatusPatchVariant,
	rightOwner *StatusPatchCollector,
) bool {
	if left.owner != leftOwner || right.owner != rightOwner ||
		left.hasDetached != right.hasDetached ||
		left.hasProjected != right.hasProjected ||
		left.projection != right.projection || !left.projected.Same(right.projected) {
		return false
	}
	if left.hasProjected {
		return left.projection != nil && left.projected.BelongsTo(left.sourcePatch) &&
			right.projected.BelongsTo(right.sourcePatch)
	}
	return left.hasDetached && reflect.DeepEqual(left.detached, right.detached)
}

// ValidateAuthentication verifies the snapshot's exact ownership chain.
func (s *StatusPatchSnapshot) ValidateAuthentication() error {
	if s == nil || s.collector == nil {
		return errors.New("statusPatch snapshot has invalid provenance")
	}
	s.collector.mu.Lock()
	defer s.collector.mu.Unlock()
	return s.validateLocked()
}

func (s *StatusPatchSnapshot) validateLocked() error {
	if s == nil || s.seal != s || s.collector == nil || !s.collector.frozen ||
		s.collector.snapshot != s ||
		len(s.collector.order) != len(s.collector.patches) {
		return errors.New("statusPatch snapshot has invalid provenance")
	}
	count, err := s.collector.targetCountLocked()
	if err != nil || s.count != count {
		return errors.New("statusPatch snapshot has invalid provenance")
	}
	return nil
}

func (c *StatusPatchCollector) targetCountLocked() (int, error) {
	if c.projectionPlan == nil {
		return len(c.patches), nil
	}
	if !c.validProjectionPlanBinding() {
		return 0, errors.New("statusPatch: cached projection plan has invalid provenance")
	}
	count, err := c.projectionPlan.targetCount()
	if err != nil {
		return 0, err
	}
	count += len(c.patches)
	for key := range c.patches {
		contained, containsErr := c.projectionPlan.containsTarget(key)
		if containsErr != nil {
			return 0, containsErr
		}
		if contained {
			count--
		}
	}
	return count, nil
}

// Len returns the number of target resources in an authenticated snapshot.
func (s *StatusPatchSnapshot) Len() (int, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return s.count, nil
}

// SameRoot reports exact authenticated snapshot identity.
func (s *StatusPatchSnapshot) SameRoot(other *StatusPatchSnapshot) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return s == other, nil
}

// ExactEqual compares every patch after detached materialization.
func (s *StatusPatchSnapshot) ExactEqual(other *StatusPatchSnapshot) (bool, error) {
	same, err := s.SameRoot(other)
	if err != nil || same {
		return same, err
	}
	if s.count != other.count {
		return false, nil
	}
	left, err := s.Patches()
	if err != nil {
		return false, err
	}
	right, err := other.Patches()
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(left, right), nil
}

// Patches returns a fully detached compatibility view.
func (s *StatusPatchSnapshot) Patches() ([]StatusPatch, error) {
	if s == nil || s.collector == nil {
		return nil, errors.New("statusPatch snapshot has invalid provenance")
	}
	s.collector.mu.Lock()
	defer s.collector.mu.Unlock()
	return s.materializeLocked("")
}

// PatchesForPhase returns detached patches containing only the requested phase.
func (s *StatusPatchSnapshot) PatchesForPhase(phase string) ([]StatusPatch, error) {
	if phase == "" {
		return nil, errors.New("statusPatch snapshot phase is empty")
	}
	if s == nil || s.collector == nil {
		return nil, errors.New("statusPatch snapshot has invalid provenance")
	}
	s.collector.mu.Lock()
	defer s.collector.mu.Unlock()
	return s.materializeLocked(phase)
}

func (s *StatusPatchSnapshot) materializeLocked(phase string) ([]StatusPatch, error) {
	if err := s.validateLocked(); err != nil {
		return nil, err
	}
	return s.collector.materializeLocked(phase)
}

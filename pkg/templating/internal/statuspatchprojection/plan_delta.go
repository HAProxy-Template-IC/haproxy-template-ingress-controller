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

package statuspatchprojection

import (
	"encoding/binary"
	"errors"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
)

// PlanPatch locates an immutable patch in the plan's full replay order.
type PlanPatch struct {
	Group    PlanGroup
	View     PatchView
	EntryKey string
	Position int
}

// VisitChangedTargets reports changed contributions, including removed targets.
func (p *PlanRoot) VisitChangedTargets(owner any, previous *PlanRoot, previousOwner any, visit func(Metadata) error) error {
	if err := p.Validate(owner); err != nil {
		return err
	}
	if err := previous.Validate(previousOwner); err != nil {
		return err
	}
	if visit == nil {
		return errors.New("plan target visitor is nil")
	}
	var visitErr error
	p.lineageRoot.WalkChanges(previous.lineageRoot, samePlanLineage, func(change persistenttree.Change[*planLineage]) bool {
		if change.BeforePresent {
			if visitErr = validatePlanLineage(change.Before); visitErr != nil {
				return true
			}
		}
		if change.AfterPresent {
			if visitErr = validatePlanLineage(change.After); visitErr != nil {
				return true
			}
		}
		var metadata Metadata
		metadata, visitErr = planTargetMetadata(change.Key)
		if visitErr == nil {
			visitErr = visit(metadata)
		}
		return visitErr != nil
	})
	return visitErr
}

func samePlanLineage(left, right *planLineage) bool {
	if validatePlanLineage(left) != nil || validatePlanLineage(right) != nil {
		return false
	}
	if left == right {
		return true
	}
	if left.uid != right.uid || left.resourceVersion != right.resourceVersion || left.groups.Len() != right.groups.Len() {
		return false
	}
	different := false
	left.groupsRoot.Walk(func(key []byte, group *planGroup) bool {
		other, exists := right.groupsRoot.Get(key)
		different = !exists || validatePlanGroup(group) != nil || validatePlanGroup(other) != nil ||
			group.root != other.root || !sameOwner(group.owner, other.owner)
		return different
	})
	return !different
}

// VisitTargetPatches visits every current contribution to one target, in replay order.
func (p *PlanRoot) VisitTargetPatches(owner any, target *Metadata, visit func(PlanPatch) error) error {
	if err := p.Validate(owner); err != nil {
		return err
	}
	if visit == nil {
		return errors.New("plan patch visitor is nil")
	}
	if target == nil {
		return errors.New("plan patch target is nil")
	}
	key := string(planTuple(target.Namespace, target.Name, target.APIVersion, target.Kind))
	lineage, found := p.lineages.Root().Get([]byte(key))
	if !found {
		return nil
	}
	if err := validatePlanLineage(lineage); err != nil {
		return err
	}
	var visitErr error
	lineage.groups.Root().Walk(func(_ []byte, group *planGroup) bool {
		visitErr = visitPlanGroupTarget(group, key, lineage, visit)
		return visitErr != nil
	})
	return visitErr
}

func visitPlanGroupTarget(group *planGroup, key string, lineage *planLineage, visit func(PlanPatch) error) error {
	if err := validatePlanGroup(group); err != nil {
		return err
	}
	index, found := slices.BinarySearchFunc(group.lineages, key, func(claim planLineageClaim, key string) int {
		return strings.Compare(claim.key, key)
	})
	if !found {
		return errors.New("plan target index has invalid provenance")
	}
	claim := &group.lineages[index]
	if claim.metadata.UID != lineage.uid || claim.metadata.ResourceVersion != lineage.resourceVersion ||
		string(planTuple(claim.metadata.Namespace, claim.metadata.Name, claim.metadata.APIVersion, claim.metadata.Kind)) != key {
		return errors.New("plan target lineage has invalid provenance")
	}
	emit := func(patch planIndexedPatch) error {
		metadata, err := patch.view.Metadata()
		if err != nil {
			return err
		}
		if metadata.Namespace != claim.metadata.Namespace || metadata.Name != claim.metadata.Name ||
			metadata.APIVersion != claim.metadata.APIVersion || metadata.Kind != claim.metadata.Kind ||
			metadata.UID != claim.metadata.UID || metadata.ResourceVersion != claim.metadata.ResourceVersion {
			return errors.New("plan target patch has invalid provenance")
		}
		return visit(PlanPatch{
			Group: PlanGroup{Name: group.name, Root: group.root, Owner: group.owner},
			View:  patch.view, EntryKey: group.key, Position: patch.position,
		})
	}
	if err := emit(claim.first); err != nil {
		return err
	}
	for _, patch := range claim.others {
		if err := emit(patch); err != nil {
			return err
		}
	}
	return nil
}

func planTargetMetadata(key string) (Metadata, error) {
	var parts [4]string
	for index := range parts {
		length, prefixBytes := binary.Uvarint([]byte(key))
		if prefixBytes <= 0 {
			return Metadata{}, errors.New("plan target key has invalid provenance")
		}
		key = key[prefixBytes:]
		if length > uint64(len(key)) {
			return Metadata{}, errors.New("plan target key has invalid provenance")
		}
		parts[index] = key[:length]
		key = key[length:]
	}
	if key != "" {
		return Metadata{}, errors.New("plan target key has invalid provenance")
	}
	return Metadata{Namespace: parts[0], Name: parts[1], APIVersion: parts[2], Kind: parts[3]}, nil
}

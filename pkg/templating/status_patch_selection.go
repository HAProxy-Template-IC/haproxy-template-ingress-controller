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

package templating

import "errors"

// StatusPatchTarget identifies a resource independently of its current patch.
type StatusPatchTarget struct {
	Namespace  string
	Name       string
	APIVersion string
	Kind       string
}

// Target identifies the resource addressed by p.
func (p *StatusPatch) Target() StatusPatchTarget {
	return StatusPatchTarget{Namespace: p.Namespace, Name: p.Name, APIVersion: p.APIVersion, Kind: p.Kind}
}

// PatchesForPhaseTargets materializes current patches for the selected targets.
// Targets absent from this snapshot or phase contribute no patch.
func (s *StatusPatchSnapshot) PatchesForPhaseTargets(phase string, targets []StatusPatchTarget) ([]StatusPatch, error) {
	if phase == "" {
		return nil, errors.New("statusPatch snapshot phase is empty")
	}
	if s == nil || s.collector == nil {
		return nil, errors.New("statusPatch snapshot has invalid provenance")
	}
	s.collector.mu.RLock()
	defer s.collector.mu.RUnlock()
	if err := s.validateLocked(); err != nil {
		return nil, err
	}
	if err := s.collector.validateMaterializeProvenanceLocked(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}
	selected := make(map[statusPatchIdentity]bool, len(targets))
	for _, target := range targets {
		selected[newStatusPatchIdentity(target.Namespace, target.Name, target.APIVersion, target.Kind)] = true
	}
	return s.collector.materializeSelectedLocked(selected, phase)
}

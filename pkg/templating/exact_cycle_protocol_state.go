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

import "errors"

type exactCycleEmptyProtocolState struct {
	kind     string
	authKind string
	seal     *exactCycleEmptyProtocolState
}

func newExactCycleEmptyProtocolState(kind string) *exactCycleEmptyProtocolState {
	state := &exactCycleEmptyProtocolState{kind: kind, authKind: kind}
	state.seal = state
	return state
}

func (s *exactCycleEmptyProtocolState) ValidateExactCycleProtocolState() error {
	if s == nil || s.seal != s || s.kind == "" || s.kind != s.authKind {
		return errors.New("exact cycle replay empty protocol state has invalid provenance")
	}
	return nil
}

func (s *exactCycleEmptyProtocolState) SameExactCycleProtocolState(
	current ExactCycleProtocolState,
) (bool, error) {
	if err := s.ValidateExactCycleProtocolState(); err != nil {
		return false, err
	}
	other, ok := current.(*exactCycleEmptyProtocolState)
	if !ok {
		return false, nil
	}
	if err := other.ValidateExactCycleProtocolState(); err != nil {
		return false, err
	}
	return s.kind == other.kind, nil
}

// ExactCycleProtocolState proves that this context starts empty and is owned by one render.
func (s *SharedContext) ExactCycleProtocolState() (ExactCycleProtocolState, error) {
	if s == nil {
		return nil, errors.New("shared context is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil || len(s.data) != 0 {
		return nil, errors.New("shared context is not fresh and empty")
	}
	return newExactCycleEmptyProtocolState(declShared), nil
}

// ExactCycleProtocolState proves that this collector starts empty and mutable.
func (c *StatusPatchCollector) ExactCycleProtocolState() (ExactCycleProtocolState, error) {
	if c == nil {
		return nil, errors.New("status patch collector is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.patches == nil || len(c.patches) != 0 || len(c.order) != 0 || len(c.projections) != 0 ||
		c.projectionPlan != nil || c.planBinding != nil || c.frozen || c.snapshot != nil {
		return nil, errors.New("status patch collector is not fresh and empty")
	}
	return newExactCycleEmptyProtocolState("statusPatchCollector"), nil
}

// ExactCycleProtocolState proves that this collector starts empty and mutable.
func (c *EventCollector) ExactCycleProtocolState() (ExactCycleProtocolState, error) {
	if c == nil {
		return nil, errors.New("event collector is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events == nil || len(c.events) != 0 || c.frozen || c.snapshot != nil {
		return nil, errors.New("event collector is not fresh and empty")
	}
	return newExactCycleEmptyProtocolState("recordEventCollector"), nil
}

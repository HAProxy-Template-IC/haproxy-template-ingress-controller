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

package rendercontext

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// PreparedPlanSnapshot is an immutable set of validated plan declarations.
type PreparedPlanSnapshot struct {
	sections *iradix.Tree[string]
	backends *iradix.Tree[PreparedPlanBackend]
	auth     preparedPlanSnapshotAuthentication
}

type preparedPlanSnapshotAuthentication struct {
	sections *iradix.Tree[string]
	backends *iradix.Tree[PreparedPlanBackend]
}

// NewPreparedPlanSnapshot creates an authenticated empty declaration set.
func NewPreparedPlanSnapshot() *PreparedPlanSnapshot {
	snapshot := &PreparedPlanSnapshot{
		sections: iradix.New[string](),
		backends: iradix.New[PreparedPlanBackend](),
	}
	snapshot.authenticate()
	return snapshot
}

// NewPreparedPlanSnapshotFromDeclarations creates an authenticated snapshot from validated declarations.
func NewPreparedPlanSnapshotFromDeclarations(
	profiles []PreparedPlanProfile,
	backends []*PreparedPlanBackend,
) (*PreparedPlanSnapshot, error) {
	type namedProfile struct {
		name string
		text string
	}
	type namedBackend struct {
		name     string
		prepared PreparedPlanBackend
	}

	preparedProfiles := make([]namedProfile, len(profiles))
	profileNames := make(map[string]struct{}, len(profiles))
	for index := range profiles {
		profile := profiles[index]
		if err := profile.Validate(); err != nil {
			return nil, fmt.Errorf("prepared plan snapshot profile %d: %w", index, err)
		}
		if _, duplicate := profileNames[profile.Name]; duplicate {
			return nil, fmt.Errorf("prepared plan snapshot repeats profile %q", profile.Name)
		}
		profileNames[profile.Name] = struct{}{}
		preparedProfiles[index] = namedProfile{name: profile.Name, text: profile.Text}
	}

	preparedBackends := make([]namedBackend, len(backends))
	backendNames := make(map[string]struct{}, len(backends))
	for index := range backends {
		backend := backends[index]
		if err := backend.Validate(); err != nil {
			return nil, fmt.Errorf("prepared plan snapshot backend %d: %w", index, err)
		}
		name := backend.Backend.Name
		if _, duplicate := backendNames[name]; duplicate {
			return nil, fmt.Errorf("prepared plan snapshot repeats backend %q", name)
		}
		backendNames[name] = struct{}{}
		preparedBackends[index] = namedBackend{name: name, prepared: backend.Clone()}
	}

	slices.SortFunc(preparedProfiles, func(left, right namedProfile) int {
		return strings.Compare(left.name, right.name)
	})
	slices.SortFunc(preparedBackends, func(left, right namedBackend) int {
		return strings.Compare(left.name, right.name)
	})

	sections := iradix.New[string]().Txn()
	for index := range preparedProfiles {
		profile := &preparedProfiles[index]
		sections.Insert(preparedSectionKey(renderplan.SectionKindProfile, profile.name), profile.text)
	}
	backendTree := iradix.New[PreparedPlanBackend]().Txn()
	for index := range preparedBackends {
		backend := &preparedBackends[index]
		sections.Insert(preparedSectionKey(renderplan.SectionKindBackend, backend.name), backend.prepared.Text)
		backendTree.Insert([]byte(backend.name), backend.prepared)
	}
	snapshot := &PreparedPlanSnapshot{sections: sections.Commit(), backends: backendTree.Commit()}
	snapshot.authenticate()
	return snapshot, nil
}

// WithProfile returns a snapshot containing profile.
func (s *PreparedPlanSnapshot) WithProfile(profile PreparedPlanProfile) (*PreparedPlanSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("prepared plan snapshot profile: %w", err)
	}
	sections, _, _ := s.sections.Insert(preparedSectionKey(renderplan.SectionKindProfile, profile.Name), profile.Text)
	updated := &PreparedPlanSnapshot{sections: sections, backends: s.backends}
	updated.authenticate()
	return updated, nil
}

// WithoutProfile returns a snapshot without name.
func (s *PreparedPlanSnapshot) WithoutProfile(name string) (*PreparedPlanSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	sections, _, changed := s.sections.Delete(preparedSectionKey(renderplan.SectionKindProfile, name))
	if !changed {
		return s, nil
	}
	updated := &PreparedPlanSnapshot{sections: sections, backends: s.backends}
	updated.authenticate()
	return updated, nil
}

// WithBackend returns a snapshot containing backend.
func (s *PreparedPlanSnapshot) WithBackend(backend *PreparedPlanBackend) (*PreparedPlanSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if err := backend.Validate(); err != nil {
		return nil, fmt.Errorf("prepared plan snapshot backend: %w", err)
	}
	detached := backend.Clone()
	name := detached.Backend.Name
	sections, _, _ := s.sections.Insert(preparedSectionKey(renderplan.SectionKindBackend, name), detached.Text)
	backends, _, _ := s.backends.Insert([]byte(name), detached)
	updated := &PreparedPlanSnapshot{sections: sections, backends: backends}
	updated.authenticate()
	return updated, nil
}

// WithoutBackend returns a snapshot without name.
func (s *PreparedPlanSnapshot) WithoutBackend(name string) (*PreparedPlanSnapshot, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, err
	}
	sections, _, sectionChanged := s.sections.Delete(preparedSectionKey(renderplan.SectionKindBackend, name))
	backends, _, backendChanged := s.backends.Delete([]byte(name))
	if !sectionChanged && !backendChanged {
		return s, nil
	}
	if sectionChanged != backendChanged {
		return nil, fmt.Errorf("prepared plan snapshot backend %q is incomplete", name)
	}
	updated := &PreparedPlanSnapshot{sections: sections, backends: backends}
	updated.authenticate()
	return updated, nil
}

// ValidateAuthentication rejects an unsealed or root-substituted snapshot.
func (s *PreparedPlanSnapshot) ValidateAuthentication() error {
	if s == nil || s.sections == nil || s.backends == nil {
		return errors.New("prepared plan snapshot is unavailable")
	}
	if s.auth.sections != s.sections || s.auth.backends != s.backends {
		return errors.New("prepared plan snapshot authentication seal does not match its roots")
	}
	return nil
}

func (s *PreparedPlanSnapshot) authenticate() {
	s.auth = preparedPlanSnapshotAuthentication{sections: s.sections, backends: s.backends}
}

func (s *PreparedPlanSnapshot) section(kind, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	return s.sections.Root().Get(preparedSectionKey(kind, name))
}

func (s *PreparedPlanSnapshot) backend(name string) (renderplan.Backend, bool, error) {
	if s == nil {
		return renderplan.Backend{}, false, nil
	}
	prepared, exists := s.backends.Root().Get([]byte(name))
	if !exists {
		return renderplan.Backend{}, false, nil
	}
	detached := prepared.Clone()
	detached.Backend.Body = normalizeStrings(detached.Body)
	detached.Backend.Comments = normalizeStrings(detached.Comments)
	detached.Backend.ContentKnown = true
	return detached.Backend, true, nil
}

func preparedSectionKey(kind, name string) []byte {
	return []byte(kind + "\x00" + name)
}

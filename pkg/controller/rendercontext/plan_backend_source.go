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
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// planBackendSource is the registry's renderplan.BackendSource: the prepared
// snapshot's backends under this render's declarations, each with the text
// digest of its assembled section. Its token names the prepared snapshot's
// log position and the declared names, which together bound what can differ
// from the snapshot a transition compares against.
type planBackendSource struct {
	prepared  *PreparedPlanSnapshot
	declared  map[string]renderplan.Backend
	assembled []renderplan.Section
	digests   map[string]string
	token     *planBackendToken
}

type planBackendToken struct {
	log      *preparedBackendLog
	declared []string
}

func (r *PlanRegistry) planBackendSource() (*planBackendSource, error) {
	source := &planBackendSource{
		prepared:  r.prepared,
		declared:  r.backends,
		assembled: r.assembled,
		token:     &planBackendToken{declared: make([]string, 0, len(r.backends))},
	}
	if r.prepared != nil {
		if err := r.prepared.ValidateAuthentication(); err != nil {
			return nil, err
		}
		source.token.log = r.prepared.log
	}
	for name := range r.backends {
		source.token.declared = append(source.token.declared, name)
		if r.prepared == nil {
			continue
		}
		existing, exists := r.prepared.backends.Root().Get([]byte(name))
		if !exists {
			continue
		}
		prepared, declared := preparedBackendRecord(&existing), r.backends[name]
		if !sameBackendRecordExact(&prepared, &declared) {
			return nil, fmt.Errorf("planRegistry.Backend: backend %q declared twice with different values", name)
		}
	}
	slices.Sort(source.token.declared)
	return source, nil
}

// preparedBackendRecord is the plan's view of a prepared backend: the sealed
// snapshot's slices are shared, since every consumer copies before it mutates.
func preparedBackendRecord(entry *PreparedPlanBackend) renderplan.Backend {
	backend := entry.Backend
	backend.Body = sharedStrings(entry.Body)
	backend.Comments = sharedStrings(entry.Comments)
	backend.ContentKnown = true
	return backend
}

func (s *planBackendSource) Token() any { return s.token }

func (s *planBackendSource) ChangedSince(token any) ([]string, bool) {
	base, ok := token.(*planBackendToken)
	if !ok || base == nil || base.log == nil || s.prepared == nil {
		return nil, false
	}
	names, ok := s.prepared.backendsChangedSince(base.log)
	if !ok {
		return nil, false
	}
	names = append(names, base.declared...)
	names = append(names, s.token.declared...)
	return names, true
}

// Len counts a name declared and prepared at once only once, as Walk visits it.
func (s *planBackendSource) Len() int {
	if s.prepared == nil {
		return len(s.declared)
	}
	length := s.prepared.backends.Len()
	root := s.prepared.backends.Root()
	for name := range s.declared {
		if _, prepared := root.Get([]byte(name)); !prepared {
			length++
		}
	}
	return length
}

func (s *planBackendSource) Get(name string) (renderplan.Backend, bool) {
	backend, exists := s.declared[name]
	if !exists {
		if s.prepared == nil {
			return renderplan.Backend{}, false
		}
		entry, found := s.prepared.backends.Root().Get([]byte(name))
		if !found {
			return renderplan.Backend{}, false
		}
		backend = preparedBackendRecord(&entry)
	}
	backend.TextDigest = s.textDigest(name)
	return backend, true
}

func (s *planBackendSource) Walk(visit func(string, renderplan.Backend) error) error {
	if s.prepared != nil {
		var err error
		s.prepared.backends.Root().Walk(func(key []byte, entry PreparedPlanBackend) bool {
			name := string(key)
			if _, declared := s.declared[name]; declared {
				return false
			}
			backend := preparedBackendRecord(&entry)
			backend.TextDigest = s.textDigest(name)
			err = visit(name, backend)
			return err != nil
		})
		if err != nil {
			return err
		}
	}
	for name := range s.declared {
		backend := s.declared[name]
		backend.TextDigest = s.textDigest(name)
		if err := visit(name, backend); err != nil {
			return err
		}
	}
	return nil
}

func (s *planBackendSource) textDigest(name string) string {
	if s.digests == nil {
		s.digests = make(map[string]string, len(s.assembled))
		for _, section := range s.assembled {
			if section.Kind == renderplan.SectionKindBackend {
				s.digests[section.Name] = section.TextDigest
			}
		}
	}
	return s.digests[name]
}

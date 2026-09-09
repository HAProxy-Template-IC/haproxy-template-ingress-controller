// Copyright 2025 Philipp Hossner
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

package deployplan

import (
	"maps"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// Index is what a diff looks up in a plan by name. A caller that holds a plan
// across deployments builds it once with IndexPlan; Diff builds its own for a
// plan that comes without one. It points into the plan, so it is only valid
// while the plan is left alone, which a shared or decoded plan is.
type Index struct {
	sections map[sectionKey]*renderplan.Section // every section but the backends
	files    map[string]*renderplan.File
	backends []backendEntry // backend sections in document order; a record without a section is not in the text
	byName   map[string]int // backend name → position in backends
}

// backendEntry pairs a backend section with its record, so a diff resolves a
// backend with one lookup instead of one per map it lives in. The record is a
// copy: a map value has no address, and one slice of them is one allocation.
type backendEntry struct {
	name      string
	section   *renderplan.Section
	record    renderplan.Backend
	described bool // the plan has a record for the section
}

// IndexPlan indexes p; nil for a nil plan.
func IndexPlan(p *renderplan.Plan) *Index {
	if p == nil {
		return nil
	}
	index := &Index{
		sections: make(map[sectionKey]*renderplan.Section, len(p.Sections)-len(p.Backends)),
		files:    fileIndex(p.Files),
		backends: make([]backendEntry, 0, len(p.Backends)),
		byName:   make(map[string]int, len(p.Backends)),
	}
	for i := range p.Sections {
		section := &p.Sections[i]
		if section.Kind != renderplan.SectionKindBackend {
			index.sections[sectionKey{section.Kind, section.Name}] = section
			continue
		}
		entry := backendEntry{name: section.Name, section: section}
		entry.record, entry.described = p.Backends[section.Name]
		index.byName[section.Name] = len(index.backends)
		index.backends = append(index.backends, entry)
	}
	return index
}

// backend is the entry for name, nil when the plan has no such section.
func (x *Index) backend(name string) *backendEntry {
	if at, ok := x.byName[name]; ok {
		return &x.backends[at]
	}
	return nil
}

func sortedMapNames(plans map[string]renderplan.Map) []string {
	return slices.Sorted(maps.Keys(plans))
}

func fileIndex(files []renderplan.File) map[string]*renderplan.File {
	index := make(map[string]*renderplan.File, len(files))
	for i := range files {
		index[files[i].Path] = &files[i]
	}
	return index
}

func serverIndex(servers []renderplan.Server) map[string]*renderplan.Server {
	index := make(map[string]*renderplan.Server, len(servers))
	for i := range servers {
		index[servers[i].Name] = &servers[i]
	}
	return index
}

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

// backendSections lists the plan's backends in the order its sections have
// them; a record without a section is not part of the configuration text.
func backendSections(p *renderplan.Plan) []string {
	names := make([]string, 0, len(p.Backends))
	for i := range p.Sections {
		if p.Sections[i].Kind == renderplan.SectionKindBackend {
			names = append(names, p.Sections[i].Name)
		}
	}
	return names
}

func sortedMapNames(plans map[string]renderplan.Map) []string {
	return slices.Sorted(maps.Keys(plans))
}

// sectionIndex indexes every section; each rule selects the kinds it owns.
func sectionIndex(sections []renderplan.Section) map[sectionKey]*renderplan.Section {
	index := make(map[sectionKey]*renderplan.Section, len(sections))
	for i := range sections {
		index[sectionKey{sections[i].Kind, sections[i].Name}] = &sections[i]
	}
	return index
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

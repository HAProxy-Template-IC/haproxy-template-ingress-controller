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
	"maps"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type renderAssemblyGeneration struct {
	owner          *RenderDocumentCache
	render         *renderDocumentGeneration
	document       rendercontent.Document
	assembled      rendercontent.Document
	parts          *renderAssemblyParts
	authority      *PlanTokenAuthority
	prepared       *PreparedPlanSnapshot
	directSections *renderAssemblyDirectSections
	sections       *renderAssemblySections
	auth           renderAssemblyAuthentication
	seal           *renderAssemblyGeneration
}

type renderAssemblyParts struct {
	values []rendercontent.Document
	seal   *renderAssemblyParts
}

type renderAssemblyDirectSections struct {
	values map[sectionKey]string
	seal   *renderAssemblyDirectSections
}

type renderAssemblySections struct {
	values []renderplan.Section
	seal   *renderAssemblySections
}

type renderAssemblyAuthentication struct {
	owner          *RenderDocumentCache
	render         *renderDocumentGeneration
	document       rendercontent.Document
	assembled      rendercontent.Document
	parts          *renderAssemblyParts
	authority      *PlanTokenAuthority
	prepared       *PreparedPlanSnapshot
	directSections *renderAssemblyDirectSections
	sections       *renderAssemblySections
}

func (s *RenderCacheSession) loadAssembly(
	document rendercontent.Document,
	registry *PlanRegistry,
	render *renderDocumentGeneration,
) (config string, sections []renderplan.Section, generation *renderAssemblyGeneration, hit bool, err error) {
	assembled, sections, generation, hit, err := s.loadAssemblyDocument(document, registry, render)
	if err != nil || !hit {
		return "", sections, generation, hit, err
	}
	config, err = assembled.String()
	if err != nil {
		return "", nil, nil, false, err
	}
	return config, sections, generation, true, nil
}

func (s *RenderCacheSession) loadAssemblyDocument(
	document rendercontent.Document,
	registry *PlanRegistry,
	render *renderDocumentGeneration,
) (assembled rendercontent.Document, sections []renderplan.Section, generation *renderAssemblyGeneration, hit bool, err error) {
	if s == nil {
		return rendercontent.Document{}, nil, nil, false, nil
	}
	if err := s.ensureOpen(); err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if err := document.ValidateAuthentication(); err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if render == nil {
		return rendercontent.Document{}, nil, nil, false, nil
	}
	if err := render.validate(s.owner); err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if render.document != document {
		return rendercontent.Document{}, nil, nil, false, errors.New("render assembly cache input belongs to another document")
	}
	proof, reusable, err := s.owner.currentPostProcessReuseProof(render.templateName)
	if err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if !reusable || render.proof != proof || s.base == nil || s.base.document != render {
		return rendercontent.Document{}, nil, nil, false, nil
	}
	generation = s.base.assembly
	if generation == nil {
		return rendercontent.Document{}, nil, nil, false, nil
	}
	if err := generation.validate(s.owner); err != nil {
		return rendercontent.Document{}, nil, nil, false, err
	}
	if generation.render != render || generation.document != document || generation.authority != registry.authority ||
		generation.prepared != registry.prepared || !maps.Equal(generation.directSections.values, registry.sections) {
		return rendercontent.Document{}, nil, nil, false, nil
	}
	return generation.assembled, slices.Clone(generation.sections.values), generation, true, nil
}

func (s *RenderCacheSession) prepareAssembly(
	document rendercontent.Document,
	registry *PlanRegistry,
	render *renderDocumentGeneration,
	config string,
	sections []renderplan.Section,
) (*renderAssemblyGeneration, bool, error) {
	assembled, err := renderDocumentFromString(config)
	if err != nil {
		return nil, false, err
	}
	parts := []rendercontent.Document(nil)
	if config != "" {
		parts = []rendercontent.Document{assembled}
	}
	return s.prepareAssemblyDocument(document, registry, render, assembled, parts, sections)
}

func (s *RenderCacheSession) prepareAssemblyDocument(
	document rendercontent.Document,
	registry *PlanRegistry,
	render *renderDocumentGeneration,
	assembled rendercontent.Document,
	parts []rendercontent.Document,
	sections []renderplan.Section,
) (*renderAssemblyGeneration, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, false, err
	}
	if err := document.ValidateAuthentication(); err != nil {
		return nil, false, err
	}
	if err := assembled.ValidateAuthentication(); err != nil {
		return nil, false, err
	}
	for _, part := range parts {
		if err := part.ValidateAuthentication(); err != nil {
			return nil, false, err
		}
	}
	if err := validateAssembledSectionPartition(assembled, sections); err != nil {
		return nil, false, err
	}
	if err := render.validate(s.owner); err != nil {
		return nil, false, err
	}
	if render.document != document {
		return nil, false, errors.New("render assembly cache input belongs to another document")
	}
	proof, reusable, err := s.owner.currentPostProcessReuseProof(render.templateName)
	if err != nil {
		return nil, false, err
	}
	if !reusable {
		return nil, false, nil
	}
	if render.proof != proof {
		return nil, false, errors.New("render assembly cache input has a stale post-process proof")
	}
	if err := registry.validateTokenAuthority(); err != nil {
		return nil, false, err
	}
	if registry.prepared != nil {
		if err := registry.prepared.ValidateAuthentication(); err != nil {
			return nil, false, err
		}
	}
	partIndex := &renderAssemblyParts{values: slices.Clone(parts)}
	partIndex.seal = partIndex
	directSectionIndex := &renderAssemblyDirectSections{values: maps.Clone(registry.sections)}
	directSectionIndex.seal = directSectionIndex
	sectionIndex := &renderAssemblySections{values: slices.Clone(sections)}
	sectionIndex.seal = sectionIndex
	generation := &renderAssemblyGeneration{
		owner:          s.owner,
		render:         render,
		document:       document,
		assembled:      assembled,
		parts:          partIndex,
		authority:      registry.authority,
		prepared:       registry.prepared,
		directSections: directSectionIndex,
		sections:       sectionIndex,
	}
	generation.auth = renderAssemblyAuthentication{
		owner:          generation.owner,
		render:         generation.render,
		document:       generation.document,
		assembled:      generation.assembled,
		parts:          generation.parts,
		authority:      generation.authority,
		prepared:       generation.prepared,
		directSections: generation.directSections,
		sections:       generation.sections,
	}
	generation.seal = generation
	s.assembly = generation
	return generation, true, nil
}

func validateAssembledSectionPartition(
	assembled rendercontent.Document,
	sections []renderplan.Section,
) error {
	assembledBytes, err := assembled.Bytes()
	if err != nil {
		return err
	}
	sectionBytes := 0
	for _, section := range sections {
		if !section.TextKnown || section.Length != len(section.Text) ||
			section.TextDigest != renderplan.DigestString(section.Text) {
			return errors.New("render assembly cache contains an invalid section")
		}
		if sectionBytes > assembledBytes || section.Length > assembledBytes-sectionBytes {
			return errors.New("render assembly cache sections do not partition its document")
		}
		sectionBytes += section.Length
	}
	if sectionBytes != assembledBytes {
		return errors.New("render assembly cache sections do not partition its document")
	}
	return nil
}

func (g *renderAssemblyGeneration) sealIntact(cache *RenderDocumentCache) bool {
	return g != nil && g.owner == cache && g.seal == g && g.render != nil && g.authority != nil &&
		g.parts != nil && g.parts.seal == g.parts && g.directSections != nil &&
		g.directSections.seal == g.directSections && g.sections != nil && g.sections.seal == g.sections
}

func (g *renderAssemblyGeneration) authenticationConsistent() bool {
	return g.auth.owner == g.owner && g.auth.render == g.render && g.auth.document == g.document &&
		g.auth.assembled == g.assembled && g.auth.parts == g.parts && g.auth.authority == g.authority &&
		g.auth.prepared == g.prepared && g.auth.directSections == g.directSections && g.auth.sections == g.sections
}

func (g *renderAssemblyGeneration) validate(cache *RenderDocumentCache) error {
	if !g.sealIntact(cache) || !g.authenticationConsistent() {
		return errors.New("render assembly cache generation is invalid")
	}
	if err := g.render.validate(cache); err != nil {
		return errors.New("render assembly cache contains an invalid render")
	}
	if g.render.document != g.document || g.render.proof == nil {
		return errors.New("render assembly cache render does not match its document")
	}
	if err := g.document.ValidateAuthentication(); err != nil {
		return errors.New("render assembly cache contains an invalid document")
	}
	if err := g.assembled.ValidateAuthentication(); err != nil {
		return errors.New("render assembly cache contains an invalid assembled document")
	}
	if err := g.authority.validate(); err != nil {
		return errors.New("render assembly cache contains an invalid token authority")
	}
	if g.prepared != nil {
		if err := g.prepared.ValidateAuthentication(); err != nil {
			return errors.New("render assembly cache contains an invalid prepared plan")
		}
	}
	return nil
}

func (s *RenderCacheSession) previousAssembly(
	registry *PlanRegistry,
) (*renderAssemblyGeneration, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, false, err
	}
	if s.base == nil || s.base.assembly == nil {
		return nil, false, nil
	}
	generation := s.base.assembly
	if err := generation.validate(s.owner); err != nil {
		return nil, false, err
	}
	if generation.authority != registry.authority {
		return nil, false, nil
	}
	return generation, true, nil
}

func renderDocumentFromString(text string) (rendercontent.Document, error) {
	var builder rendercontent.DocumentBuilder
	if _, err := builder.WriteString(text); err != nil {
		return rendercontent.Document{}, err
	}
	return builder.Build(nil)
}

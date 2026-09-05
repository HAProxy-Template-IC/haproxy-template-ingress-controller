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
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type renderPlanInputs struct {
	mapsMeta map[string]bool
	backends map[string]renderplan.Backend
	paths    *templating.PathResolver
}

type renderPlanGeneration struct {
	owner    *RenderDocumentCache
	assembly *renderAssemblyGeneration
	config   string
	aux      *dataplane.AuxiliaryFiles
	inputs   *renderPlanInputs
	plan     *renderplan.Plan
	identity *RenderPlanIdentity
	auth     renderPlanAuthentication
	seal     *renderPlanGeneration
}

type renderPlanAuthentication struct {
	owner    *RenderDocumentCache
	assembly *renderAssemblyGeneration
	aux      *dataplane.AuxiliaryFiles
	inputs   *renderPlanInputs
	plan     *renderplan.Plan
	identity *RenderPlanIdentity
}

// RenderPlanIdentity proves exact reuse of one immutable plan-cache generation.
type RenderPlanIdentity struct {
	owner      *RenderDocumentCache
	generation *renderPlanGeneration
	seal       *RenderPlanIdentity
}

// ValidateAuthentication rejects copied or substituted identities.
func (i *RenderPlanIdentity) ValidateAuthentication() error {
	if i == nil || i.seal != i || i.owner == nil || i.generation == nil || i.generation.identity != i {
		return errors.New("render plan identity is invalid")
	}
	if err := i.generation.validate(i.owner); err != nil {
		return errors.New("render plan identity has an invalid generation")
	}
	return nil
}

// SameRoot reports whether two identities name the same exact plan generation.
func (i *RenderPlanIdentity) SameRoot(other *RenderPlanIdentity) (bool, error) {
	if err := i.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return i == other, nil
}

func (s *RenderCacheSession) loadPlan(
	registry *PlanRegistry,
	config string,
	aux *dataplane.AuxiliaryFiles,
) (*renderplan.Plan, *RenderPlanIdentity, bool, error) {
	if s == nil || registry.assembly == nil {
		return nil, nil, false, nil
	}
	if err := s.ensureOpen(); err != nil {
		return nil, nil, false, err
	}
	if s.assembly != registry.assembly {
		return nil, nil, false, errors.New("render plan cache assembly does not match its session")
	}
	if s.base == nil || s.base.assembly != registry.assembly || s.base.plan == nil {
		return nil, nil, false, nil
	}
	generation := s.base.plan
	if err := generation.validate(s.owner); err != nil {
		return nil, nil, false, err
	}
	if generation.assembly != registry.assembly {
		return nil, nil, false, nil
	}
	if err := validatePlanAssembly(s.owner, registry, config); err != nil {
		return nil, nil, false, err
	}
	if generation.config != config {
		return nil, nil, false, errors.New("render plan cache assembly does not match its config")
	}
	if !maps.Equal(generation.inputs.mapsMeta, registry.mapsMeta) ||
		!reflect.DeepEqual(generation.inputs.backends, registry.backends) ||
		!reflect.DeepEqual(generation.inputs.paths, registry.paths) ||
		!dataplane.ContentEqual("", generation.aux, "", aux) {
		return nil, nil, false, nil
	}
	s.plan = generation
	return generation.plan.Clone(), generation.identity, true, nil
}

func (s *RenderCacheSession) storePlan(
	registry *PlanRegistry,
	config string,
	aux *dataplane.AuxiliaryFiles,
	plan *renderplan.Plan,
) (*RenderPlanIdentity, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if s.assembly != registry.assembly {
		return nil, errors.New("render plan cache assembly does not match its session")
	}
	if err := validatePlanAssembly(s.owner, registry, config); err != nil {
		return nil, err
	}
	if !renderplan.ExactlyEqual(plan, plan) {
		return nil, errors.New("render plan cache cannot retain an inexact plan")
	}
	inputs := &renderPlanInputs{
		mapsMeta: maps.Clone(registry.mapsMeta),
		backends: clonePlanInputBackends(registry.backends),
		paths:    clonePlanPathResolver(registry.paths),
	}
	ownedAux := dataplane.CloneAuxiliaryFiles(aux)
	if ownedAux == nil {
		ownedAux = &dataplane.AuxiliaryFiles{}
	}
	generation := &renderPlanGeneration{
		owner:    s.owner,
		assembly: registry.assembly,
		config:   config,
		aux:      ownedAux,
		inputs:   inputs,
		plan:     plan.Clone(),
	}
	identity := &RenderPlanIdentity{owner: s.owner, generation: generation}
	identity.seal = identity
	generation.identity = identity
	generation.auth = renderPlanAuthentication{
		owner:    s.owner,
		assembly: generation.assembly,
		aux:      generation.aux,
		inputs:   generation.inputs,
		plan:     generation.plan,
		identity: generation.identity,
	}
	generation.seal = generation
	s.plan = generation
	return identity, nil
}

func (g *renderPlanGeneration) validate(cache *RenderDocumentCache) error {
	if g == nil || g.owner != cache || g.seal != g || g.assembly == nil || g.aux == nil ||
		g.inputs == nil || g.plan == nil || g.identity == nil || g.auth.owner != g.owner ||
		g.auth.assembly != g.assembly || g.auth.aux != g.aux || g.auth.inputs != g.inputs ||
		g.auth.plan != g.plan || g.auth.identity != g.identity || g.identity.owner != cache ||
		g.identity.generation != g || g.identity.seal != g.identity {
		return errors.New("render plan cache generation is invalid")
	}
	return g.assembly.validate(cache)
}

func validatePlanAssembly(cache *RenderDocumentCache, registry *PlanRegistry, config string) error {
	if err := registry.validateTokenAuthority(); err != nil {
		return err
	}
	if err := registry.assembly.validate(cache); err != nil {
		return err
	}
	if registry.assembly.authority != registry.authority ||
		registry.assembly.prepared != registry.prepared ||
		!maps.Equal(registry.assembly.directSections.values, registry.sections) {
		return errors.New("render plan cache assembly does not match its registry")
	}
	assembled, err := registry.assembly.assembled.String()
	if err != nil {
		return err
	}
	if assembled != config {
		return errors.New("render plan cache assembly does not match its config")
	}
	return nil
}

func clonePlanInputBackends(source map[string]renderplan.Backend) map[string]renderplan.Backend {
	if source == nil {
		return nil
	}
	cloned := make(map[string]renderplan.Backend, len(source))
	for name := range source {
		backend := source[name]
		cloned[name] = clonePreparedBackendRecord(&backend)
	}
	return cloned
}

func clonePlanPathResolver(source *templating.PathResolver) *templating.PathResolver {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

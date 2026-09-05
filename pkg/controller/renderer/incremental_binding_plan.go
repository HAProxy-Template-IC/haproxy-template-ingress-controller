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

package renderer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalBindingPlan struct {
	bindings          []incrementalBinding
	byComponent       map[string][]incrementalBinding
	bySource          map[string][]incrementalComponent
	projectionSources map[string]struct{}
	props             map[string][]byte
	owners            map[string]incrementalComponent
}

type incrementalBindingCache struct {
	inputs *templating.IncrementalBindingInputSnapshot
	plan   *incrementalBindingPlan
}

func (s *incrementalRenderState) planBindings(
	ctx context.Context,
	baseContext map[string]any,
) (*incrementalBindingPlan, error) {
	plan, _, _, err := s.prepareBindingPlan(ctx, baseContext)
	return plan, err
}

func (s *incrementalRenderState) prepareBindingPlan(
	ctx context.Context,
	baseContext map[string]any,
) (*incrementalBindingPlan, *incrementalBindingCache, bool, error) {
	if len(s.bindingEntryPoints) == 0 {
		return s.staticBindingPlan, nil, true, nil
	}
	snapshotPlanner, authenticated := s.planner.(templating.IncrementalBindingSnapshotPlanner)
	if !authenticated {
		plan := cloneIncrementalBindingPlan(s.staticBindingPlan)
		plan, err := s.planDynamicBindings(ctx, baseContext, plan)
		return plan, nil, false, err
	}
	if s.snapshot != nil && s.snapshot.bindingCache != nil && snapshotPlanner.MatchIncrementalBindingInputs(
		s.bindingEntryPoints,
		baseContext,
		s.snapshot.bindingCache.inputs,
	) {
		cache := s.snapshot.bindingCache
		return cache.plan, cache, true, nil
	}
	inputs, err := snapshotPlanner.SnapshotIncrementalBindingInputs(s.bindingEntryPoints, baseContext)
	if err != nil {
		return nil, nil, false, err
	}
	plan := cloneIncrementalBindingPlan(s.staticBindingPlan)
	for _, name := range s.dynamicComponents {
		component := s.components[name]
		bindings, err := s.planComponentBindingsSnapshot(ctx, &component, snapshotPlanner, inputs)
		if err != nil {
			return nil, nil, false, err
		}
		if err := plan.addComponentBindings(&component, bindings); err != nil {
			return nil, nil, false, err
		}
	}
	plan.sort()
	cache := &incrementalBindingCache{inputs: inputs, plan: cloneIncrementalBindingPlan(plan)}
	return plan, cache, true, nil
}

func (s *incrementalRenderState) planDynamicBindings(
	ctx context.Context,
	baseContext map[string]any,
	plan *incrementalBindingPlan,
) (*incrementalBindingPlan, error) {
	for _, name := range s.dynamicComponents {
		component := s.components[name]
		bindings, err := s.planComponentBindings(ctx, &component, baseContext)
		if err != nil {
			return nil, err
		}
		if err := plan.addComponentBindings(&component, bindings); err != nil {
			return nil, err
		}
	}
	plan.sort()
	return plan, nil
}

func (s *incrementalRenderState) planComponentBindingsSnapshot(
	ctx context.Context,
	component *incrementalComponent,
	planner templating.IncrementalBindingSnapshotPlanner,
	inputs *templating.IncrementalBindingInputSnapshot,
) ([]incrementalBinding, error) {
	entryPoint := helpers.IncrementalBindingsEntryPointName(component.name)
	encoded, err := planner.RenderIncrementalBindingsSnapshot(ctx, entryPoint, inputs)
	if err != nil {
		return nil, fmt.Errorf("planning incremental component %q bindings: %w", component.name,
			remapIncrementalTemplateError(component.name, entryPoint, err))
	}
	bindings, err := decodeIncrementalBindings(component.name, encoded, s.config.WatchedResources)
	if err != nil {
		return nil, fmt.Errorf("planning incremental component %q bindings: %w", component.name, err)
	}
	return bindings, nil
}

func newIncrementalBindingPlan() *incrementalBindingPlan {
	return &incrementalBindingPlan{
		byComponent:       map[string][]incrementalBinding{},
		bySource:          map[string][]incrementalComponent{},
		projectionSources: map[string]struct{}{},
		props:             map[string][]byte{},
		owners:            map[string]incrementalComponent{},
	}
}

func cloneIncrementalBindingPlan(plan *incrementalBindingPlan) *incrementalBindingPlan {
	if plan == nil {
		return newIncrementalBindingPlan()
	}
	cloned := newIncrementalBindingPlan()
	cloned.bindings = cloneIncrementalBindings(plan.bindings)
	for component, bindings := range plan.byComponent {
		cloned.byComponent[component] = cloneIncrementalBindings(bindings)
	}
	for source, components := range plan.bySource {
		clonedComponents := make([]incrementalComponent, len(components))
		for index := range components {
			clonedComponents[index] = cloneIncrementalCarrierComponent(&components[index])
		}
		cloned.bySource[source] = clonedComponents
	}
	for source := range plan.projectionSources {
		cloned.projectionSources[source] = struct{}{}
	}
	for key, props := range plan.props {
		cloned.props[key] = slices.Clone(props)
	}
	for source := range plan.owners {
		owner := plan.owners[source]
		cloned.owners[source] = cloneIncrementalCarrierComponent(&owner)
	}
	return cloned
}

func cloneIncrementalBindings(bindings []incrementalBinding) []incrementalBinding {
	cloned := make([]incrementalBinding, len(bindings))
	for index, binding := range bindings {
		binding.props = slices.Clone(binding.props)
		binding.projection = cloneIncrementalResourceProjection(binding.projection)
		cloned[index] = binding
	}
	return cloned
}

func sortedComponentNames(components map[string]incrementalComponent) []string {
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (s *incrementalRenderState) planComponentBindings(
	ctx context.Context,
	component *incrementalComponent,
	bindingContext map[string]any,
) ([]incrementalBinding, error) {
	if component.source != "" {
		return []incrementalBinding{staticIncrementalBinding(component.name, component.source)}, nil
	}
	if s.planner == nil {
		return nil, errors.New("template engine has no incremental binding planner executor")
	}
	entryPoint := helpers.IncrementalBindingsEntryPointName(component.name)
	encoded, err := s.planner.RenderIncrementalBindings(ctx, entryPoint, bindingContext)
	if err != nil {
		return nil, fmt.Errorf("planning incremental component %q bindings: %w", component.name,
			remapIncrementalTemplateError(component.name, entryPoint, err))
	}
	bindings, err := decodeIncrementalBindings(component.name, encoded, s.config.WatchedResources)
	if err != nil {
		return nil, fmt.Errorf("planning incremental component %q bindings: %w", component.name, err)
	}
	return bindings, nil
}

func (p *incrementalBindingPlan) addComponentBindings(
	component *incrementalComponent,
	bindings []incrementalBinding,
) error {
	for _, binding := range bindings {
		key := string(bindingKey(binding.component, binding.source))
		if _, duplicate := p.props[key]; duplicate {
			return fmt.Errorf("incremental component %q repeats binding %q", component.name, binding.source)
		}
		binding.props = slices.Clone(binding.props)
		if component.resourceProjection {
			projection, err := decodeIncrementalResourceProjection(binding.props)
			if err != nil {
				return fmt.Errorf(
					"incremental component %q binding %q: %w",
					component.name,
					binding.source,
					err,
				)
			}
			binding.projection = projection
			p.projectionSources[binding.source] = struct{}{}
		}
		p.bindings = append(p.bindings, binding)
		p.byComponent[component.name] = append(p.byComponent[component.name], binding)
		if !component.resourceProjection {
			p.bySource[binding.source] = append(p.bySource[binding.source], *component)
		}
		p.props[key] = slices.Clone(binding.props)
		if !component.deriveResource {
			continue
		}
		if owner, exists := p.owners[binding.source]; exists && owner.name != component.name {
			return fmt.Errorf(
				"watched resource %q has multiple active deriveResource components %q and %q",
				binding.source,
				owner.name,
				component.name,
			)
		}
		p.owners[binding.source] = *component
	}
	return nil
}

func (p *incrementalBindingPlan) sort() {
	slices.SortFunc(p.bindings, compareIncrementalBindings)
	for name := range p.byComponent {
		slices.SortFunc(p.byComponent[name], compareIncrementalBindings)
	}
	for source := range p.bySource {
		slices.SortFunc(p.bySource[source], func(left, right incrementalComponent) int {
			return strings.Compare(left.name, right.name)
		})
	}
}

func compareIncrementalBindings(left, right incrementalBinding) int {
	if compared := strings.Compare(left.component, right.component); compared != 0 {
		return compared
	}
	return strings.Compare(left.source, right.source)
}

func sameIncrementalBindingPlans(left, right *incrementalBindingPlan) bool {
	if left == nil || right == nil || len(left.bindings) != len(right.bindings) {
		return left == right
	}
	for index := range left.bindings {
		leftBinding := &left.bindings[index]
		rightBinding := &right.bindings[index]
		if leftBinding.component != rightBinding.component || leftBinding.source != rightBinding.source ||
			!bytes.Equal(leftBinding.props, rightBinding.props) {
			return false
		}
	}
	return true
}

func (p *incrementalBindingPlan) required(base map[string]struct{}) map[string]struct{} {
	required := make(map[string]struct{}, len(base)+len(p.bySource)+len(p.projectionSources))
	for source := range base {
		required[source] = struct{}{}
	}
	for source := range p.bySource {
		required[source] = struct{}{}
	}
	for source := range p.projectionSources {
		required[source] = struct{}{}
	}
	return required
}

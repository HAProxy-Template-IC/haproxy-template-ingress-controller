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

package renderer

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func (s *incrementalRenderState) resourceProjectionDemandDrivenGroup(group string) bool {
	components := s.groups[group]
	if len(components) == 0 {
		return false
	}
	for index := range components {
		if !components[index].resourceProjection {
			return false
		}
	}
	return true
}

func (s *incrementalRenderState) resourceProjectionDemandDrivenClosure() map[string]bool {
	demandDriven := make(map[string]bool)
	for group := range s.groups {
		if s.resourceProjectionDemandDrivenGroup(group) {
			demandDriven[group] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for group, dependencies := range s.dependencies {
			if demandDriven[group] {
				continue
			}
			for _, dependency := range dependencies {
				if demandDriven[dependency] {
					demandDriven[group] = true
					changed = true
					break
				}
			}
		}
	}
	return demandDriven
}

func (r *incrementalRenderSession) executeResourceProjection(
	ctx context.Context,
	reader incremental.Reader,
	component *incrementalComponent,
	source, namespace, name string,
) ([]byte, error) {
	binding, _, err := incrementalResourceProjectionBindingForQuery(
		r.bindingPlan,
		component,
		source,
		namespace,
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("incremental component %q: %w", component.name, err)
	}
	_, encodedProps, _, found, err := r.decodeComponentInputWithEncoding(
		reader,
		bindingInputKey(component.name, source),
		component.name,
		"resource projection",
		true,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("incremental component %q resource projection binding disappeared", component.name)
	}
	if !bytes.Equal(encodedProps, binding.props) {
		return nil, incremental.ErrRevisionConflict
	}
	projection, err := decodeIncrementalResourceProjection(encodedProps)
	if err != nil {
		return nil, fmt.Errorf("incremental component %q resource projection: %w", component.name, err)
	}
	expectedNamespace, expectedName, ok := incrementalResourceProjectionIdentity(projection)
	if !ok || expectedNamespace != namespace || expectedName != name {
		return nil, errors.New("resource projection query does not match its binding")
	}
	items, _, err := r.decodeResourceInput(reader, &resourceInputSpec{
		resourceType: source,
		scope:        resourceInputGet,
		keys:         projection.Keys,
	})
	if err != nil {
		return nil, fmt.Errorf("incremental component %q resource projection: %w", component.name, err)
	}
	recorder, err := newIncrementalResourceProjectionRecorder(
		component,
		source,
		namespace,
		name,
		projection,
		items,
		r.publicationGeneration,
	)
	if err != nil {
		return nil, err
	}
	queryKey := r.registerComponentQuery(component, source, namespace, name)
	encoded, fresh, err := recorder.authenticatedResult(
		queryKey,
		component,
		source,
		namespace,
		name,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("incremental component %q result: %w", component.name, err)
	}
	if err := r.installFinalizedComponents(&finalizedIncrementalComponent{
		key:     queryKey,
		encoded: encoded,
		fresh:   fresh,
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(encoded), nil
}

func incrementalResourceProjectionBindingForQuery(
	plan *incrementalBindingPlan,
	component *incrementalComponent,
	source, namespace, name string,
) (incrementalBinding, *incrementalResourceProjection, error) {
	if plan == nil || component == nil || !component.resourceProjection ||
		namespace != incrementalResourceProjectionNamespace {
		return incrementalBinding{}, nil, errors.New("resource projection query has invalid provenance")
	}
	var matched incrementalBinding
	var projection *incrementalResourceProjection
	for _, binding := range plan.byComponent[component.name] {
		if binding.source != source {
			continue
		}
		candidate, err := incrementalResourceProjectionForBinding(binding)
		if err != nil {
			return incrementalBinding{}, nil, err
		}
		candidateNamespace, candidateName, ok := incrementalResourceProjectionIdentity(candidate)
		if !ok || candidateNamespace != namespace || candidateName != name {
			continue
		}
		if projection != nil {
			return incrementalBinding{}, nil, errors.New("resource projection query has multiple bindings")
		}
		matched = binding
		projection = candidate
	}
	if projection == nil {
		return incrementalBinding{}, nil, errors.New("resource projection query has no active binding")
	}
	return matched, projection, nil
}

func newIncrementalResourceProjectionRecorder(
	component *incrementalComponent,
	source, namespace, name string,
	projection *incrementalResourceProjection,
	items []any,
	generation *incrementalPublicationSnapshotGeneration,
) (*incrementalRecorder, error) {
	if component == nil || !component.resourceProjection || projection == nil || generation == nil {
		return nil, errors.New("resource projection result has invalid provenance")
	}
	if len(items) > 1 {
		return nil, fmt.Errorf(
			"incremental component %q resource projection %q matched %d resources; expected at most one",
			component.name,
			projection.Keys,
			len(items),
		)
	}
	recorder := &incrementalRecorder{
		publicationGeneration: generation,
		publicationGroup:      component.group,
		publicationOwner: incrementalGroupInstanceID{
			component: component.name,
			source:    source,
			namespace: namespace,
			name:      name,
		},
	}
	if len(items) == 0 {
		return recorder, nil
	}
	detached, err := templating.NewIncrementalDetachedValue(items[0])
	if err != nil {
		return nil, fmt.Errorf("detaching resource projection value: %w", err)
	}
	if projection.Rank == "" {
		recorder.PublishDetached(projection.Cell, projection.Key, detached)
	} else {
		recorder.PublishRankedDetached(projection.Cell, projection.Key, projection.Rank, detached)
	}
	return recorder, nil
}

func incrementalResourceProjectionQueryKey(
	component *incrementalComponent,
	binding incrementalBinding,
) (key incremental.QueryKey, namespace, name string, err error) {
	projection, err := incrementalResourceProjectionForBinding(binding)
	if err != nil {
		return incremental.QueryKey{}, "", "", err
	}
	namespace, name, ok := incrementalResourceProjectionIdentity(projection)
	if !ok {
		return incremental.QueryKey{}, "", "", errors.New("resource projection has invalid identity")
	}
	return componentQueryKey(component, binding.source, namespace, name), namespace, name, nil
}

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
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalColdSourceTransactionProjection uint8

const (
	incrementalColdSourceTransactionRaw incrementalColdSourceTransactionProjection = iota + 1
	incrementalColdSourceTransactionOwner
	incrementalColdSourceTransactionProjected
)

type incrementalColdSourceTransactionKey struct {
	source     string
	namespace  string
	name       string
	props      string
	projection incrementalColdSourceTransactionProjection
}

type incrementalColdSourceTransactionGroup struct {
	key      incrementalColdSourceTransactionKey
	children []int
}

type incrementalColdSourceTransactionChild struct {
	component *incrementalComponent
	item      incrementalColdCarrierPlannedItem
}

var errIncrementalColdSourceTransactionInvariant = errors.New(
	"incremental cold source transaction invariant failed",
)

func (r *incrementalRenderSession) coldSourceTransactionProjection(
	component *incrementalComponent,
	source string,
) (incrementalColdSourceTransactionProjection, error) {
	if r == nil || r.bindingPlan == nil || component == nil || component.name == "" || source == "" {
		return 0, errors.New("incremental cold source transaction projection is unavailable")
	}
	owner, projected := r.bindingPlan.owners[source]
	if !projected {
		if component.deriveResource {
			return 0, fmt.Errorf("incremental cold source transaction source %q has an unbound derive owner", source)
		}
		return incrementalColdSourceTransactionRaw, nil
	}
	if component.name == owner.name {
		if !component.deriveResource {
			return 0, fmt.Errorf("incremental cold source transaction source %q has an invalid derive owner", source)
		}
		return incrementalColdSourceTransactionOwner, nil
	}
	if component.deriveResource {
		return 0, fmt.Errorf("incremental cold source transaction source %q has conflicting derive owners", source)
	}
	return incrementalColdSourceTransactionProjected, nil
}

func flattenIncrementalColdSourceTransactionChildren(
	wave *incrementalColdCarrierPlannedWorkerWave,
) ([]incrementalColdSourceTransactionChild, error) {
	if wave == nil {
		return nil, errors.New("incremental cold source transaction wave is unavailable")
	}
	children := make([]incrementalColdSourceTransactionChild, 0)
	for laneIndex := range wave.lanes {
		lane := &wave.lanes[laneIndex]
		if lane.component == nil || lane.entryPoint == "" || lane.component.entryPoint != lane.entryPoint {
			return nil, errors.New("incremental cold source transaction lane has invalid provenance")
		}
		for itemIndex := range lane.items {
			item := lane.items[itemIndex]
			if !componentQueryKeyMatches(
				item.queryKey,
				lane.component,
				item.source,
				item.namespace,
				item.name,
			) {
				return nil, errors.New("incremental cold source transaction child has invalid provenance")
			}
			children = append(children, incrementalColdSourceTransactionChild{
				component: lane.component,
				item:      item,
			})
		}
	}
	return children, nil
}

func (r *incrementalRenderSession) coldSourceTransactionGroups(
	wave *incrementalColdCarrierPlannedWorkerWave,
) ([]incrementalColdSourceTransactionGroup, []incrementalColdSourceTransactionChild, error) {
	if r == nil || r.bindingPlan == nil || wave == nil {
		return nil, nil, errors.New("incremental cold source transaction plan is unavailable")
	}
	children, err := flattenIncrementalColdSourceTransactionChildren(wave)
	if err != nil {
		return nil, nil, err
	}
	if len(children) == 0 {
		return []incrementalColdSourceTransactionGroup{}, children, nil
	}
	byKey := make(map[incrementalColdSourceTransactionKey]*incrementalColdSourceTransactionGroup)
	for childIndex := range children {
		child := &children[childIndex]
		projection, err := r.coldSourceTransactionProjection(child.component, child.item.source)
		if err != nil {
			return nil, nil, err
		}
		props, found := r.bindingPlan.props[string(bindingKey(child.component.name, child.item.source))]
		if !found {
			return nil, nil, fmt.Errorf(
				"incremental cold source transaction component %q has no binding for %q",
				child.component.name,
				child.item.source,
			)
		}
		key := incrementalColdSourceTransactionKey{
			source: child.item.source, namespace: child.item.namespace, name: child.item.name,
			props: string(props), projection: projection,
		}
		group := byKey[key]
		if group == nil {
			group = &incrementalColdSourceTransactionGroup{key: key}
			byKey[key] = group
		}
		group.children = append(group.children, childIndex)
	}
	groups := make([]incrementalColdSourceTransactionGroup, 0, len(byKey))
	for _, group := range byKey {
		groups = append(groups, *group)
	}
	slices.SortFunc(groups, func(left, right incrementalColdSourceTransactionGroup) int {
		if compared := strings.Compare(left.key.source, right.key.source); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.key.namespace, right.key.namespace); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.key.name, right.key.name); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.key.props, right.key.props); compared != 0 {
			return compared
		}
		return int(left.key.projection) - int(right.key.projection)
	})
	return groups, children, nil
}

func (r *incrementalRenderSession) coldSourceTransactionWaves(
	worker *incrementalColdCarrierPlannedWorker,
) ([]templating.IncrementalComponentSourceTransactionWave, error) {
	if r == nil || worker == nil || len(worker.waves) == 0 {
		return nil, errors.New("incremental cold source transaction worker is empty")
	}
	waves := make([]templating.IncrementalComponentSourceTransactionWave, len(worker.waves))
	childBase := 0
	for waveIndex := range worker.waves {
		groups, children, err := r.coldSourceTransactionGroups(&worker.waves[waveIndex])
		if err != nil {
			return nil, fmt.Errorf("planning incremental cold source transaction wave %d: %w", waveIndex, err)
		}
		transactions := make([]templating.IncrementalComponentSourceTransaction, len(groups))
		for groupIndex := range groups {
			group := &groups[groupIndex]
			transactionChildren := make([]templating.IncrementalComponentSourceTransactionChild, len(group.children))
			for offset, childIndex := range group.children {
				if childIndex < 0 || childIndex >= len(children) {
					return nil, fmt.Errorf("incremental cold source transaction child %d is unavailable", childIndex)
				}
				transactionChildren[offset] = templating.IncrementalComponentSourceTransactionChild{
					TemplateName: children[childIndex].component.entryPoint,
					Index:        childBase + childIndex,
				}
			}
			transactions[groupIndex].Children = transactionChildren
		}
		waves[waveIndex].Transactions = transactions
		childBase += len(children)
	}
	if childBase == 0 {
		return nil, errors.New("incremental cold source transaction worker has no children")
	}
	return waves, nil
}

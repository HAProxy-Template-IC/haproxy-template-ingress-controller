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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalColdSourceTransactionsSplitPropsWithoutSplittingSourceAuthority(t *testing.T) {
	left := incrementalCarrierTestComponent(
		"left", "routes", "routes", nil, nil, false, false, false, false,
	)
	right := incrementalCarrierTestComponent(
		"right", "routes", "routes", nil, nil, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(left, right)
	plan := newIncrementalBindingPlan()
	plan.props[string(bindingKey(left.name, "routes"))] = []byte(`{"variant":"left"}`)
	plan.props[string(bindingKey(right.name, "routes"))] = []byte(`{"variant":"right"}`)
	session := &incrementalRenderSession{state: state, bindingPlan: plan}
	wave := &incrementalColdCarrierPlannedWorkerWave{lanes: []incrementalColdCarrierPlannedLane{
		incrementalColdSourceTransactionTestLane(&left, 0, "route"),
		incrementalColdSourceTransactionTestLane(&right, 1, "route"),
	}}

	groups, children, err := session.coldSourceTransactionGroups(wave)
	require.NoError(t, err)
	require.Len(t, children, 2)
	require.Len(t, groups, 2)
	assert.Equal(t, `{"variant":"left"}`, groups[0].key.props)
	assert.Equal(t, `{"variant":"right"}`, groups[1].key.props)
	assert.Equal(t, []int{0}, groups[0].children)
	assert.Equal(t, []int{1}, groups[1].children)

	leftShared, err := incrementalColdSourceTransactionSharedKeyFor(&groups[0])
	require.NoError(t, err)
	rightShared, err := incrementalColdSourceTransactionSharedKeyFor(&groups[1])
	require.NoError(t, err)
	assert.Equal(t, leftShared, rightShared)

	poisoned := *wave
	poisoned.lanes = append([]incrementalColdCarrierPlannedLane(nil), wave.lanes...)
	poisoned.lanes[1].items = append([]incrementalColdCarrierPlannedItem(nil), wave.lanes[1].items...)
	poisoned.lanes[1].items[0].queryKey = incremental.NewQueryKey("foreign")
	_, _, err = session.coldSourceTransactionGroups(&poisoned)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalColdSourceTransactionsHTTPRoute3000Shape(t *testing.T) {
	components := gatewayHTTPRouteCarrierComponents()
	state := newIncrementalCarrierTestState(components...)
	keys := make([]incremental.QueryKey, 0, 3000*13)
	for routeIndex := range 3000 {
		name := fmt.Sprintf("route-%04d", routeIndex)
		for _, component := range components[3:] {
			keys = append(keys, componentQueryKey(&component, "httproutes", "default", name))
		}
	}
	session, carrierPlan, graph := newIncrementalColdCarrierWaveTestPlan(t, state, keys)
	waves, err := session.planIncrementalColdCarrierWaves(carrierPlan, graph, 8)
	require.NoError(t, err)
	bindingPlan := newIncrementalBindingPlan()
	for _, component := range components[3:] {
		bindingPlan.props[string(bindingKey(component.name, "httproutes"))] = []byte("{}")
	}
	session.bindingPlan = bindingPlan

	rows := 0
	children := 0
	childrenPerRow := map[int]int{}
	rowsPerObject := make(map[string]int, 3000)
	propsPerObject := make(map[string]map[string]struct{}, 3000)
	rowsPerWave := make([]int, len(waves.waves))
	for workerIndex := range waves.workers {
		worker := &waves.workers[workerIndex]
		for waveIndex := range worker.waves {
			groups, descriptions, groupErr := session.coldSourceTransactionGroups(&worker.waves[waveIndex])
			require.NoError(t, groupErr)
			children += len(descriptions)
			rows += len(groups)
			rowsPerWave[waveIndex] += len(groups)
			for groupIndex := range groups {
				group := &groups[groupIndex]
				childrenPerRow[len(group.children)]++
				object := group.key.namespace + "/" + group.key.name
				rowsPerObject[object]++
				if propsPerObject[object] == nil {
					propsPerObject[object] = map[string]struct{}{}
				}
				propsPerObject[object][group.key.props] = struct{}{}
			}
		}
	}

	assert.Equal(t, 39000, children)
	assert.Equal(t, 9000, rows)
	assert.Equal(t, []int{0, 3000, 3000, 3000}, rowsPerWave)
	assert.Equal(t, map[int]int{1: 3000, 3: 3000, 9: 3000}, childrenPerRow)
	require.Len(t, rowsPerObject, 3000)
	for object, objectRows := range rowsPerObject {
		assert.Equal(t, 3, objectRows, object)
		assert.Equal(t, map[string]struct{}{"{}": {}}, propsPerObject[object], object)
	}
}

func TestIncrementalColdSourceTransactionsCoLocateHeterogeneousArbitraryCRDSource(t *testing.T) {
	session, wave := incrementalColdSourceTransactionHeterogeneousCRDWave()

	workersWithTarget := 0
	childrenWithTarget := 0
	for workerIndex := range wave.activeWorkers {
		lanes, err := incrementalColdCarrierExpectedWorkerWave(&wave, workerIndex)
		require.NoError(t, err)
		worker := incrementalColdCarrierPlannedWorkerWave{lanes: lanes}
		groups, _, err := session.coldSourceTransactionGroups(&worker)
		require.NoError(t, err)
		for groupIndex := range groups {
			group := &groups[groupIndex]
			if group.key.source != "widgets" || group.key.namespace != "default" ||
				group.key.name != "widget-150" {
				continue
			}
			workersWithTarget++
			childrenWithTarget += len(group.children)
		}
	}

	assert.Equal(t, 1, workersWithTarget)
	assert.Equal(t, 2, childrenWithTarget)
}

func incrementalColdSourceTransactionHeterogeneousCRDWave() (
	*incrementalRenderSession,
	incrementalColdCarrierPlannedWave,
) {
	wide := incrementalCarrierTestComponent(
		"wide", "wide", "widgets", nil, nil, false, false, false, false,
	)
	sparse := incrementalCarrierTestComponent(
		"sparse", "sparse", "widgets", nil, nil, false, false, false, false,
	)
	plan := newIncrementalBindingPlan()
	plan.props[string(bindingKey(wide.name, "widgets"))] = []byte("{}")
	plan.props[string(bindingKey(sparse.name, "widgets"))] = []byte("{}")
	session := &incrementalRenderSession{
		state:       newIncrementalCarrierTestState(wide, sparse),
		bindingPlan: plan,
	}
	wideLane := incrementalColdCarrierPlannedLane{entryPoint: wide.entryPoint, component: &wide}
	for index := range 200 {
		name := fmt.Sprintf("widget-%03d", index)
		wideLane.items = append(wideLane.items, incrementalColdCarrierPlannedItem{
			batchIndex: index,
			queryKey:   componentQueryKey(&wide, "widgets", "default", name),
			source:     "widgets",
			namespace:  "default",
			name:       name,
		})
	}
	sparseLane := incrementalColdCarrierPlannedLane{
		entryPoint: sparse.entryPoint,
		component:  &sparse,
		items: []incrementalColdCarrierPlannedItem{
			{
				batchIndex: 200,
				queryKey:   componentQueryKey(&sparse, "widgets", "default", "widget-150"),
				source:     "widgets",
				namespace:  "default",
				name:       "widget-150",
			},
			{
				batchIndex: 201,
				queryKey:   componentQueryKey(&sparse, "widgets", "default", "widget-199"),
				source:     "widgets",
				namespace:  "default",
				name:       "widget-199",
			},
		},
	}
	wave := incrementalColdCarrierPlannedWave{
		lanes:         []incrementalColdCarrierPlannedLane{wideLane, sparseLane},
		activeWorkers: 2,
	}
	return session, wave
}

func incrementalColdSourceTransactionTestLane(
	component *incrementalComponent,
	batchIndex int,
	name string,
) incrementalColdCarrierPlannedLane {
	return incrementalColdCarrierPlannedLane{
		entryPoint: component.entryPoint,
		component:  component,
		items: []incrementalColdCarrierPlannedItem{{
			batchIndex: batchIndex,
			queryKey:   componentQueryKey(component, "routes", "default", name),
			source:     "routes",
			namespace:  "default",
			name:       name,
		}},
	}
}

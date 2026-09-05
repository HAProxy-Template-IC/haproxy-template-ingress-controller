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
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalCarrierPlannerHTTPRoute3000Shape(t *testing.T) {
	components := gatewayHTTPRouteCarrierComponents()
	state := newIncrementalCarrierTestState(components...)
	queries, wantQueries := gatewayHTTPRouteCarrierQueries(components)

	forward, err := planIncrementalComponentCarriers(state, queries)
	require.NoError(t, err)
	reversedQueries := slices.Clone(queries)
	slices.Reverse(reversedQueries)
	plan, err := planIncrementalComponentCarriers(state, reversedQueries)
	require.NoError(t, err)
	require.Equal(t, forward, plan)

	require.Equal(t, 39000, plan.logicalQueries)
	require.Len(t, plan.groupStages, 4)
	for wave, wantGroups := range []int{7, 5, 1, 3} {
		assert.Equal(t, wave, plan.groupStages[wave].wave)
		assert.Len(t, plan.groupStages[wave].groups, wantGroups)
	}
	require.Len(t, plan.stages, 3)
	wantLaneNames := [][]string{
		{
			"backenditems-500-gateway-http",
			"gateway-backendtlspolicy-route-ancestors-100",
			"gateway-route-attachments-100-http",
			"gateway-route-candidates-100-http",
			"gateway-ssl-passthrough-100-http",
			"map-backend-service-200-gateway-http",
			"map-host-510-gateway-http",
			"map-weighted-backend-510-gateway-http",
			"status-patches-201-gateway-httproute",
		},
		{"gateway-route-analysis-100-http"},
		{
			"gateway-route-filter-maps-100-http",
			"gateway-route-frontend-100-http",
			"gateway-route-paths-100-http",
		},
	}
	assertGatewayHTTPRouteCarrierLanes(t, state, plan, wantQueries, wantLaneNames)
}

func gatewayHTTPRouteCarrierQueries(
	components []incrementalComponent,
) (queries []incrementalCarrierQuery, wantQueries map[incremental.QueryKey]incrementalCarrierQuery) {
	const routes = 3000
	queries = make([]incrementalCarrierQuery, 0, routes*13)
	wantQueries = make(map[incremental.QueryKey]incrementalCarrierQuery, routes*13)
	routeComponents := components[3:]
	for routeIndex := range routes {
		name := fmt.Sprintf("route-%04d", routeIndex)
		for componentIndex := range routeComponents {
			component := &routeComponents[componentIndex]
			queryKey := componentQueryKey(component, "httproutes", "default", name)
			query := incrementalCarrierQuery{
				batchIndex:    len(queries),
				queryKey:      queryKey,
				componentName: component.name,
				source:        "httproutes",
				namespace:     "default",
				name:          name,
			}
			queries = append(queries, query)
			wantQueries[queryKey] = query
		}
	}
	return queries, wantQueries
}

func assertGatewayHTTPRouteCarrierLanes(
	t *testing.T,
	state *incrementalRenderState,
	plan *incrementalCarrierPlan,
	wantQueries map[incremental.QueryKey]incrementalCarrierQuery,
	wantLaneNames [][]string,
) {
	t.Helper()
	seen := make(map[incremental.QueryKey]struct{}, len(wantQueries))
	physicalCarriers := 0
	logicalQueries := 0
	for stageIndex, stage := range plan.stages {
		require.Equal(t, stageIndex, stage.wave)
		require.Len(t, stage.carriers, 3000)
		require.Equal(t, 3000*len(wantLaneNames[stageIndex]), stage.logicalQueries)
		physicalCarriers += len(stage.carriers)
		logicalQueries += stage.logicalQueries
		for carrierIndex, carrier := range stage.carriers {
			require.Equal(t, "httproutes", carrier.source)
			require.Equal(t, "default", carrier.namespace)
			require.Equal(t, fmt.Sprintf("route-%04d", carrierIndex), carrier.name)
			require.Len(t, carrier.lanes, len(wantLaneNames[stageIndex]))
			laneNames := make([]string, 0, len(carrier.lanes))
			for _, lane := range carrier.lanes {
				require.NotNil(t, lane.component)
				laneNames = append(laneNames, lane.component.name)
				want, exists := wantQueries[lane.queryKey]
				require.True(t, exists, "unexpected logical query %q", lane.queryKey.Opaque())
				_, duplicate := seen[lane.queryKey]
				require.False(t, duplicate, "duplicate logical query %q", lane.queryKey.Opaque())
				seen[lane.queryKey] = struct{}{}
				assert.Equal(t, want.batchIndex, lane.batchIndex)
				wantComponent := state.components[want.componentName]
				assert.True(t, sameIncrementalCarrierComponent(&wantComponent, lane.component))
				assert.True(t, componentQueryKeyMatches(
					lane.queryKey,
					lane.component,
					carrier.source,
					carrier.namespace,
					carrier.name,
				))
			}
			assert.ElementsMatch(t, wantLaneNames[stageIndex], laneNames)
		}
	}
	assert.Equal(t, 9000, physicalCarriers)
	assert.Equal(t, 39000, logicalQueries)
	assert.Len(t, seen, 39000)
}

func planAndBindColdCarrierBatch(
	renderSession *incrementalRenderSession,
	batch incremental.ColdExactBatch,
	inputByQuery map[incremental.QueryKey]incremental.InputKey,
) (*incrementalCarrierPlan, error) {
	plan, planErr := renderSession.planColdComponentCarriers(batch)
	if planErr != nil {
		return nil, planErr
	}
	poisoned := plan.stages[0].carriers[0]
	poisoned.lanes = slices.Clone(poisoned.lanes)
	poisoned.lanes[0].batchIndex = (poisoned.lanes[0].batchIndex + 1) % batch.Len()
	if _, _, poisonErr := poisoned.bindColdQuery(batch, 0); poisonErr == nil {
		return nil, fmt.Errorf("carrier accepted a lane bound to another query frame")
	}
	poisoned = plan.stages[0].carriers[0]
	poisoned.name = "other"
	if _, _, poisonErr := poisoned.bindColdQuery(batch, 0); poisonErr == nil {
		return nil, fmt.Errorf("carrier accepted a lane bound to another resource")
	}
	if err := completeColdCarrierBatchQueries(plan, batch, inputByQuery); err != nil {
		return nil, err
	}
	return plan, nil
}

func completeColdCarrierBatchQueries(
	plan *incrementalCarrierPlan,
	batch incremental.ColdExactBatch,
	inputByQuery map[incremental.QueryKey]incremental.InputKey,
) error {
	for _, stage := range plan.stages {
		for _, carrier := range stage.carriers {
			if err := completeColdCarrierLaneQueries(carrier, batch, inputByQuery); err != nil {
				return err
			}
		}
	}
	return nil
}

func completeColdCarrierLaneQueries(
	carrier incrementalCarrier,
	batch incremental.ColdExactBatch,
	inputByQuery map[incremental.QueryKey]incremental.InputKey,
) error {
	for laneIndex := range carrier.lanes {
		lane, query, bindErr := carrier.bindColdQuery(batch, laneIndex)
		if bindErr != nil {
			return bindErr
		}
		value, found, inputErr := query.Input(inputByQuery[lane.queryKey])
		if inputErr != nil {
			return inputErr
		}
		if !found {
			return fmt.Errorf("query %q did not find its input", lane.queryKey.Opaque())
		}
		if _, completeErr := query.Complete(string(value)); completeErr != nil {
			return completeErr
		}
	}
	return nil
}

func TestIncrementalCarrierPlannerPreservesColdDependencyFrames(t *testing.T) {
	components := []incrementalComponent{
		incrementalCarrierTestComponent("producer", "producer", "routes", nil, nil, true, false, false, false),
		incrementalCarrierTestComponent("analysis", "analysis", "routes", []string{"producer"}, nil, true, false, false, false),
		incrementalCarrierTestComponent("consumer", "consumer", "routes", []string{"analysis"}, nil, false, true, false, false),
	}
	state := newIncrementalCarrierTestState(components...)
	keys := make([]incremental.QueryKey, len(components))
	inputs := make([]incremental.Input, len(components))
	inputByQuery := make(map[incremental.QueryKey]incremental.InputKey, len(components))
	definitions := make([]incremental.Definition, len(components))
	for index, component := range components {
		key := componentQueryKey(&component, "routes", "default", "route")
		inputKey := incremental.NewInputKey("input/" + component.name)
		keys[index] = key
		inputs[index] = incremental.Input{
			Key:      inputKey,
			Revision: incremental.NewRevision("revision/1"),
			Found:    true,
			Value:    []byte(component.name),
		}
		inputByQuery[key] = inputKey
		definitions[index] = incremental.Definition{
			Key: key,
			Run: func(context.Context, incremental.Reader) ([]byte, error) {
				return nil, fmt.Errorf("scalar execution is not expected")
			},
		}
	}
	graph, err := incremental.New(definitions...)
	require.NoError(t, err)
	cold, err := graph.BeginColdReset(inputs...)
	require.NoError(t, err)
	renderSession := &incrementalRenderSession{state: state}
	preflightKeys := slices.Clone(keys)
	slices.Reverse(preflightKeys)
	preflightPlan, err := renderSession.planColdComponentCarrierKeys(preflightKeys)
	require.NoError(t, err)
	var plan *incrementalCarrierPlan
	results, err := cold.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		var batchErr error
		plan, batchErr = planAndBindColdCarrierBatch(renderSession, batch, inputByQuery)
		return batchErr
	}, keys...)
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.NotNil(t, plan)
	assert.Equal(t, preflightPlan, plan)
	require.Len(t, plan.stages, 3)
	for index, stage := range plan.stages {
		assert.Equal(t, index, stage.wave)
		require.Len(t, stage.carriers, 1)
		require.Len(t, stage.carriers[0].lanes, 1)
	}
	require.NoError(t, cold.Commit(t.Context(), func(
		_ context.Context,
		observations []incremental.InputRevision,
	) (bool, error) {
		assert.Len(t, observations, 3)
		return true, nil
	}))

	warm, err := graph.Begin()
	require.NoError(t, err)
	t.Cleanup(warm.Abort)
	target := keys[1]
	require.NoError(t, warm.ApplyInputs(incremental.Input{
		Key:      inputByQuery[target],
		Revision: incremental.NewRevision("revision/2"),
		Found:    true,
		Value:    []byte("changed"),
	}))
	dirty, err := warm.DirtyQueries()
	require.NoError(t, err)
	assert.Equal(t, []incremental.QueryKey{target}, dirty)
}

func TestIncrementalCarrierPlannerKeyPreflightRejectsDuplicate(t *testing.T) {
	component := incrementalCarrierTestComponent(
		"component", "group", "routes", nil, nil, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(component)
	renderSession := &incrementalRenderSession{state: state}
	key := componentQueryKey(&component, "routes", "default", "route")
	_, err := renderSession.planColdComponentCarrierKeys([]incremental.QueryKey{key, key})
	require.ErrorContains(t, err, "is duplicated")
}

func TestIncrementalCarrierPlannerSeparatesSourceIdentities(t *testing.T) {
	component := incrementalCarrierTestComponent(
		"dynamic", "group", "", nil, nil, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(component)
	queries := make([]incrementalCarrierQuery, 0, 2)
	for index, source := range []string{"alpha", "beta"} {
		queries = append(queries, incrementalCarrierQuery{
			batchIndex:    index,
			queryKey:      componentQueryKey(&component, source, "default", "same-name"),
			componentName: component.name,
			source:        source,
			namespace:     "default",
			name:          "same-name",
		})
	}
	plan, err := planIncrementalComponentCarriers(state, queries)
	require.NoError(t, err)
	require.Len(t, plan.stages, 1)
	require.Len(t, plan.stages[0].carriers, 2)
	assert.Equal(t, "alpha", plan.stages[0].carriers[0].source)
	assert.Equal(t, "beta", plan.stages[0].carriers[1].source)
}

func TestIncrementalCarrierPlannerRetainsEmptyProducerWave(t *testing.T) {
	producer := incrementalCarrierTestComponent(
		"producer", "producer", "routes", nil, nil, true, false, false, false,
	)
	consumer := incrementalCarrierTestComponent(
		"consumer", "consumer", "routes", []string{"producer"}, nil, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(producer, consumer)
	plan, err := planIncrementalComponentCarriers(state, []incrementalCarrierQuery{
		incrementalCarrierTestQuery(&consumer),
	})
	require.NoError(t, err)
	require.Len(t, plan.groupStages, 2)
	assert.Equal(t, incrementalCarrierGroupStage{wave: 0, groups: []string{"producer"}}, plan.groupStages[0])
	assert.Equal(t, incrementalCarrierGroupStage{wave: 1, groups: []string{"consumer"}}, plan.groupStages[1])
	require.Len(t, plan.stages, 1)
	assert.Equal(t, 0, plan.stages[0].wave)
	require.Len(t, plan.stages[0].carriers, 1)
	require.Len(t, plan.stages[0].carriers[0].lanes, 1)
}

func TestIncrementalCarrierPlannerOwnsComponentMetadata(t *testing.T) {
	activationPath, err := templating.CompileExistenceJSONPath("$.metadata.name")
	require.NoError(t, err)
	producer := incrementalCarrierTestComponent(
		"producer", "producer", "routes", nil, nil, true, false, false, false,
	)
	consumer := incrementalCarrierTestComponent(
		"consumer", "consumer", "routes", []string{"producer"}, nil, false, false, false, false,
	)
	consumer.activationPaths = []templating.ExistenceJSONPath{activationPath}
	state := newIncrementalCarrierTestState(producer, consumer)
	plan, err := planIncrementalComponentCarriers(state, []incrementalCarrierQuery{
		incrementalCarrierTestQuery(&consumer),
	})
	require.NoError(t, err)
	laneComponent := plan.stages[0].carriers[0].lanes[0].component
	require.NotNil(t, laneComponent)

	poisoned := state.components[consumer.name]
	poisoned.consumes[0] = "poison"
	poisoned.activationPaths[0] = templating.ExistenceJSONPath{}
	assert.Equal(t, []string{"producer"}, laneComponent.consumes)
	assert.Equal(t, []templating.ExistenceJSONPath{activationPath}, laneComponent.activationPaths)
}

func TestIncrementalCarrierPlannerRejectsPoisonedTopologyAndQueries(t *testing.T) {
	tests := []struct {
		name    string
		state   func() *incrementalRenderState
		queries func(*incrementalRenderState) []incrementalCarrierQuery
		wantErr string
	}{
		{
			name: "unauthenticated missing optional dependency",
			state: func() *incrementalRenderState {
				return newIncrementalCarrierTestState(incrementalCarrierTestComponent(
					"consumer", "consumer", "routes", nil, []string{"missing"}, false, false, false, false,
				))
			},
			wantErr: `unavailable dependency "missing"`,
		},
		{
			name: "authenticated missing required dependency",
			state: func() *incrementalRenderState {
				state := newIncrementalCarrierTestState(incrementalCarrierTestComponent(
					"consumer", "consumer", "routes", []string{"missing"}, nil, false, false, false, false,
				))
				state.config.AbsentIncrementalGroups["missing"] = struct{}{}
				return state
			},
			wantErr: `unavailable dependency "missing"`,
		},
		{
			name: "dependency cycle",
			state: func() *incrementalRenderState {
				return newIncrementalCarrierTestState(
					incrementalCarrierTestComponent("left", "left", "routes", []string{"right"}, nil, true, false, false, false),
					incrementalCarrierTestComponent("right", "right", "routes", []string{"left"}, nil, true, false, false, false),
				)
			},
			wantErr: "dependency cycle",
		},
		{
			name: "dependency index mismatch",
			state: func() *incrementalRenderState {
				state := newIncrementalCarrierTestState(incrementalCarrierTestComponent(
					"component", "group", "routes", nil, nil, false, false, false, false,
				))
				state.dependencies["group"] = []string{"poison"}
				return state
			},
			wantErr: "dependency index does not match",
		},
		{
			name: "duplicate batch index",
			state: func() *incrementalRenderState {
				return newIncrementalCarrierTestState(
					incrementalCarrierTestComponent("left", "left", "routes", nil, nil, false, false, false, false),
					incrementalCarrierTestComponent("right", "right", "routes", nil, nil, false, false, false, false),
				)
			},
			queries: func(state *incrementalRenderState) []incrementalCarrierQuery {
				leftComponent := state.components["left"]
				rightComponent := state.components["right"]
				left := incrementalCarrierTestQuery(&leftComponent)
				right := incrementalCarrierTestQuery(&rightComponent)
				return []incrementalCarrierQuery{left, right}
			},
			wantErr: "batch index 0 is duplicated",
		},
		{
			name: "static source mismatch",
			state: func() *incrementalRenderState {
				return newIncrementalCarrierTestState(incrementalCarrierTestComponent(
					"component", "group", "routes", nil, nil, false, false, false, false,
				))
			},
			queries: func(state *incrementalRenderState) []incrementalCarrierQuery {
				component := state.components["component"]
				query := incrementalCarrierTestQuery(&component)
				query.source = "other"
				query.queryKey = componentQueryKey(&component, "other", query.namespace, query.name)
				return []incrementalCarrierQuery{query}
			},
			wantErr: `uses source "other", want "routes"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := test.state()
			var queries []incrementalCarrierQuery
			if test.queries != nil {
				queries = test.queries(state)
			}
			_, err := planIncrementalComponentCarriers(state, queries)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestIncrementalCarrierPlannerAcceptsAuthenticatedMissingOptionalDependency(t *testing.T) {
	component := incrementalCarrierTestComponent(
		"consumer", "consumer", "routes", nil, []string{"missing"}, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(component)
	state.config.AbsentIncrementalGroups["missing"] = struct{}{}
	plan, err := planIncrementalComponentCarriers(state, []incrementalCarrierQuery{
		incrementalCarrierTestQuery(&component),
	})
	require.NoError(t, err)
	require.Len(t, plan.stages, 1)
	require.Len(t, plan.stages[0].carriers, 1)
}

func gatewayHTTPRouteCarrierComponents() []incrementalComponent {
	return []incrementalComponent{
		incrementalCarrierTestComponent("fixture-host-port-scopes", "gateway-host-port-scopes", "gateways", nil, nil, true, false, false, false),
		incrementalCarrierTestComponent("fixture-host-listenersets", "gateway-host-listenersets", "listenersets", nil, nil, true, false, false, false),
		incrementalCarrierTestComponent("fixture-backend-tls", "gateway-backend-tls-policies", "backendtlspolicies", nil, nil, true, false, false, false),
		incrementalCarrierTestComponent("gateway-route-candidates-100-http", "gateway-route-candidates", "httproutes", []string{"gateway-host-port-scopes"}, []string{"gateway-host-listenersets"}, true, false, false, false),
		incrementalCarrierTestComponent("gateway-route-analysis-100-http", "gateway-route-analysis", "httproutes", []string{"gateway-route-candidates"}, nil, true, false, false, false),
		incrementalCarrierTestComponent("gateway-ssl-passthrough-100-http", "gateway-ssl-passthrough", "httproutes", nil, nil, true, false, false, false),
		incrementalCarrierTestComponent("backenditems-500-gateway-http", "gateway-backends", "", nil, []string{"gateway-backend-tls-policies"}, true, false, true, false),
		incrementalCarrierTestComponent("map-backend-service-200-gateway-http", "map-backend-service-gateway", "httproutes", nil, nil, false, false, false, false),
		incrementalCarrierTestComponent("map-host-510-gateway-http", "gateway-host-map", "httproutes", []string{"gateway-host-port-scopes"}, []string{"gateway-host-listenersets"}, false, false, false, false),
		incrementalCarrierTestComponent("gateway-route-paths-100-http", "gateway-route-paths", "httproutes", []string{"gateway-route-analysis"}, nil, true, true, false, false),
		incrementalCarrierTestComponent("map-weighted-backend-510-gateway-http", "map-weighted-backend-gateway", "httproutes", nil, nil, false, false, false, false),
		incrementalCarrierTestComponent("gateway-route-filter-maps-100-http", "gateway-route-filter-maps", "", []string{"gateway-route-analysis"}, nil, true, true, false, false),
		incrementalCarrierTestComponent("gateway-route-frontend-100-http", "gateway-route-frontend", "", []string{"gateway-route-analysis"}, nil, true, true, false, false),
		incrementalCarrierTestComponent("gateway-route-attachments-100-http", "gateway-route-attachments", "httproutes", nil, []string{"gateway-host-listenersets"}, true, false, false, false),
		incrementalCarrierTestComponent("status-patches-201-gateway-httproute", "gateway-httproute-status", "", nil, []string{"gateway-host-listenersets"}, false, false, false, true),
		incrementalCarrierTestComponent("gateway-backendtlspolicy-route-ancestors-100", "gateway-backendtlspolicy-route-ancestors", "httproutes", nil, nil, true, false, false, false),
	}
}

func incrementalCarrierTestComponent(
	name string,
	group string,
	source string,
	consumes []string,
	optionalConsumes []string,
	publishValue bool,
	recordEvent bool,
	backendPlan bool,
	statusPatch bool,
) incrementalComponent {
	return incrementalComponent{
		name:             name,
		entryPoint:       "entry/" + name,
		source:           source,
		group:            group,
		consumes:         slices.Clone(consumes),
		optionalConsumes: slices.Clone(optionalConsumes),
		publishValue:     publishValue,
		recordEvent:      recordEvent,
		backendPlan:      backendPlan,
		statusPatch:      statusPatch,
	}
}

func newIncrementalCarrierTestState(components ...incrementalComponent) *incrementalRenderState {
	state := &incrementalRenderState{
		components:   make(map[string]incrementalComponent, len(components)),
		groups:       make(map[string][]incrementalComponent),
		dependencies: make(map[string][]string),
		config: &config.Config{
			AbsentIncrementalGroups: map[string]struct{}{},
		},
	}
	dependencySets := make(map[string]map[string]struct{})
	for index := range components {
		component := &components[index]
		state.components[component.name] = cloneIncrementalCarrierComponent(component)
		state.groups[component.group] = append(state.groups[component.group], cloneIncrementalCarrierComponent(component))
		if dependencySets[component.group] == nil {
			dependencySets[component.group] = map[string]struct{}{}
		}
		for _, dependency := range append(slices.Clone(component.consumes), component.optionalConsumes...) {
			dependencySets[component.group][dependency] = struct{}{}
		}
	}
	for group := range state.groups {
		slices.SortFunc(state.groups[group], func(left, right incrementalComponent) int {
			if left.name < right.name {
				return -1
			}
			if left.name > right.name {
				return 1
			}
			return 0
		})
		for dependency := range dependencySets[group] {
			state.dependencies[group] = append(state.dependencies[group], dependency)
		}
		slices.Sort(state.dependencies[group])
	}
	return state
}

func incrementalCarrierTestQuery(component *incrementalComponent) incrementalCarrierQuery {
	return incrementalCarrierQuery{
		batchIndex:    0,
		queryKey:      componentQueryKey(component, "routes", "default", "route"),
		componentName: component.name,
		source:        "routes",
		namespace:     "default",
		name:          "route",
	}
}

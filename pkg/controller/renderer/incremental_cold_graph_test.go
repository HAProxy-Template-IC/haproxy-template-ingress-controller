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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestColdGraphSchedulerDrainsSmallPrerequisitesBeforeBulkWaves(t *testing.T) {
	components := gatewayHTTPRouteCarrierComponents()
	state := newIncrementalCarrierTestState(components...)
	plan, err := planIncrementalComponentCarriers(state, nil)
	require.NoError(t, err)
	groupOrder := make([]string, 0, len(state.groups))
	for _, stage := range plan.groupStages {
		groupOrder = append(groupOrder, stage.groups...)
	}
	keysByGroup := make(map[string][]incremental.QueryKey, len(state.groups))
	for _, component := range components[3:] {
		keysByGroup[component.group] = make([]incremental.QueryKey, 3000)
	}
	pending := make(map[string]struct{}, len(state.groups))
	for group := range state.groups {
		pending[group] = struct{}{}
	}
	completed := make(map[string]*incrementalGroupIndex, len(state.groups))
	session := &incrementalRenderSession{state: state}
	var stageSizes []int
	var stages [][]string
	for len(pending) > 0 {
		small, bulk := session.coldGraphReadyPartitions(groupOrder, pending, completed, keysByGroup)
		selected := small
		if len(selected) == 0 {
			selected = bulk
		}
		require.NotEmpty(t, selected)
		stages = append(stages, selected)
		stageSizes = append(stageSizes, len(selected))
		for _, group := range selected {
			completed[group] = newIncrementalGroupIndex()
			delete(pending, group)
		}
	}
	assert.Equal(t, []int{3, 9, 1, 3}, stageSizes)
	assert.ElementsMatch(t, []string{
		"gateway-backend-tls-policies",
		"gateway-host-listenersets",
		"gateway-host-port-scopes",
	}, stages[0])
}

func TestColdGraphAuthorityRejectsChangedProducerState(t *testing.T) {
	index := newIncrementalGroupIndex()
	session := &incrementalRenderSession{
		cold:         true,
		groupIndexes: map[string]*incrementalGroupIndex{"producer": index},
		groupReady:   map[string]bool{"producer": true},
	}
	pending, err := newIncrementalColdGraphAuthority(
		session,
		map[string]*incrementalGroupIndex{},
	)
	require.NoError(t, err)
	pendingCtx := context.WithValue(t.Context(), incrementalColdGraphContextKey{}, pending)
	assert.False(t, session.coldGraphProducerAuthorized(pendingCtx, "producer"))

	authority, err := newIncrementalColdGraphAuthority(
		session,
		map[string]*incrementalGroupIndex{"producer": index},
	)
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), incrementalColdGraphContextKey{}, authority)
	assert.True(t, session.coldGraphProducerAuthorized(ctx, "producer"))
	assert.False(t, session.coldGraphProducerAuthorized(ctx, "other"))
	poisoned := *authority
	poisonedCtx := context.WithValue(t.Context(), incrementalColdGraphContextKey{}, &poisoned)
	assert.False(t, session.coldGraphProducerAuthorized(poisonedCtx, "producer"))

	session.groupIndexes["producer"] = newIncrementalGroupIndex()
	assert.False(t, session.coldGraphProducerAuthorized(ctx, "producer"))
	session.groupIndexes["producer"] = index
	session.groupReady["producer"] = false
	assert.False(t, session.coldGraphProducerAuthorized(ctx, "producer"))
	session.groupReady["producer"] = true
	session.cold = false
	assert.False(t, session.coldGraphProducerAuthorized(ctx, "producer"))
}

func TestColdGraphReadyPartitionsUsesExactBulkBoundary(t *testing.T) {
	components := []incrementalComponent{
		incrementalCarrierTestComponent("empty", "empty", "routes", nil, nil, false, false, false, false),
		incrementalCarrierTestComponent("small", "small", "routes", nil, nil, false, false, false, false),
		incrementalCarrierTestComponent("bulk", "bulk", "routes", nil, nil, false, false, false, false),
	}
	state := newIncrementalCarrierTestState(components...)
	session := &incrementalRenderSession{state: state}
	pending := map[string]struct{}{"empty": {}, "small": {}, "bulk": {}}
	keysByGroup := map[string][]incremental.QueryKey{
		"small": make([]incremental.QueryKey, incrementalColdCarrierBulkGroupItems-1),
		"bulk":  make([]incremental.QueryKey, incrementalColdCarrierBulkGroupItems),
	}
	small, bulk := session.coldGraphReadyPartitions(
		[]string{"empty", "small", "bulk"},
		pending,
		map[string]*incrementalGroupIndex{},
		keysByGroup,
	)
	assert.Equal(t, []string{"empty", "small"}, small)
	assert.Equal(t, []string{"bulk"}, bulk)
}

func TestIncrementalColdCarrierWorkerLimitUsesEverySchedulerThread(t *testing.T) {
	tests := map[string]struct {
		gomaxprocs int
		want       int
	}{
		"invalid":        {gomaxprocs: 0, want: 1},
		"single core":    {gomaxprocs: 1, want: 1},
		"physical cores": {gomaxprocs: 8, want: 8},
		"SMT threads":    {gomaxprocs: 16, want: 16},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.want, incrementalColdCarrierWorkerLimit(test.gomaxprocs))
		})
	}
}

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
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

type countingPreparedPlanRegistrar struct {
	*rendercontext.PlanRegistry
	profiles map[string]int
}

func (r *countingPreparedPlanRegistrar) RegisterPreparedProfile(
	profile rendercontext.PreparedPlanProfile,
) (string, error) {
	r.profiles[profile.Name]++
	return r.PlanRegistry.RegisterPreparedProfile(profile)
}

func backendPlanResult(
	t *testing.T,
	record map[string]any,
	text string,
	formatToken func(string) string,
) incrementalComponentResult {
	t.Helper()
	plan := newIncrementalBackendPlanRecorder()
	token, err := plan.Backend(record, text)
	require.NoError(t, err)
	if formatToken != nil {
		token = formatToken(token)
	}
	recorder := &incrementalRecorder{plan: plan}
	result, err := recorder.result(token)
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&result))
	return result
}

func profiledBackendPlanResult(
	t *testing.T,
	mode, backendName, text string,
) (result incrementalComponentResult, profileName string) {
	t.Helper()
	plan := newIncrementalBackendPlanRecorder()
	profile, err := plan.Profile(map[string]any{"mode": mode})
	require.NoError(t, err)
	token, err := plan.Backend(map[string]any{
		"name":    backendName,
		"profile": profile,
		"mode":    mode,
	}, text)
	require.NoError(t, err)
	result, err = (&incrementalRecorder{plan: plan}).result(token)
	require.NoError(t, err)
	require.NoError(t, validateIncrementalInstanceResult(&result))
	return result, profile
}

func TestIncrementalBackendPlanLogicalTokenClassification(t *testing.T) {
	result := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", func(token string) string {
		return " \t" + strings.TrimSuffix(token, "\n") + "  \t\n"
	})
	require.Len(t, result.BackendPlanOutput, 1)
	require.NotNil(t, result.BackendPlanOutput[0].BackendCall)
	assert.Zero(t, *result.BackendPlanOutput[0].BackendCall)

	plan := newIncrementalBackendPlanRecorder()
	token, err := plan.Backend(map[string]any{"name": "be_app"}, "backend be_app\n")
	require.NoError(t, err)
	_, err = (&incrementalRecorder{plan: plan}).result(strings.TrimSuffix(token, "\n") + " forged\n")
	require.ErrorContains(t, err, "malformed token")
}

func TestIncrementalBackendPlanTokenIsDeterministicWhenObservedAsData(t *testing.T) {
	var baseline [sha256.Size]byte
	for range 20 {
		plan := newIncrementalBackendPlanRecorder()
		token, err := plan.Backend(map[string]any{"name": "be_app"}, "backend be_app\n")
		require.NoError(t, err)
		observed := sha256.Sum256([]byte(token))
		if baseline == ([sha256.Size]byte{}) {
			baseline = observed
		}
		assert.Equal(t, baseline, observed)
		assert.Equal(t, "# @haptic:incremental-backend:0:be_app@\n", token)
	}
}

func TestIncrementalBackendPlanFirstWinnerIsGlobalAndDeterministic(t *testing.T) {
	instances := []incrementalBackendPlanInstance{
		{
			group: "later-group",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "900-later", source: "routes", namespace: "default", name: "a",
				result: backendPlanResult(t, map[string]any{"name": "be_shared", "mode": "tcp"},
					"backend be_shared\n    mode tcp\n", nil),
			},
		},
		{
			group: "first-group",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "100-first", source: "routes", namespace: "default", name: "z",
				result: backendPlanResult(t, map[string]any{"name": "be_shared", "mode": "http"},
					"backend be_shared\n    mode http\n", nil),
			},
		},
	}

	registry := rendercontext.NewPlanRegistry(nil)
	outputs, err := replayIncrementalBackendPlans(instances, registry)
	require.NoError(t, err)
	assert.NotEmpty(t, outputs["first-group"]["100-first"])
	assert.Empty(t, outputs["later-group"]["900-later"])
	config, _, err := registry.Assemble(context.Background(), outputs["first-group"]["100-first"], nil)
	require.NoError(t, err)
	assert.Equal(t, "backend be_shared\n    mode http\n", config)
}

func TestIncrementalBackendPlanFirstWinnerUsesCanonicalIdentityOrder(t *testing.T) {
	tests := map[string]struct {
		earlier incrementalInstanceResult
		later   incrementalInstanceResult
	}{
		"component": {
			earlier: incrementalInstanceResult{component: "a", source: "z", namespace: "z", name: "z"},
			later:   incrementalInstanceResult{component: "b", source: "a", namespace: "a", name: "a"},
		},
		"source": {
			earlier: incrementalInstanceResult{component: "backends", source: "a", namespace: "z", name: "z"},
			later:   incrementalInstanceResult{component: "backends", source: "z", namespace: "a", name: "a"},
		},
		"namespace": {
			earlier: incrementalInstanceResult{component: "backends", source: "routes", namespace: "a", name: "z"},
			later:   incrementalInstanceResult{component: "backends", source: "routes", namespace: "z", name: "a"},
		},
		"name": {
			earlier: incrementalInstanceResult{component: "backends", source: "routes", namespace: "default", name: "a"},
			later:   incrementalInstanceResult{component: "backends", source: "routes", namespace: "default", name: "z"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			test.earlier.result = backendPlanResult(t, map[string]any{
				"name": "be_shared", "guid": "first",
			}, "backend be_shared\n    # first\n", nil)
			test.later.result = backendPlanResult(t, map[string]any{
				"name": "be_shared", "guid": "second",
			}, "backend be_shared\n    # second\n", nil)
			registry := rendercontext.NewPlanRegistry(nil)
			outputs, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{
				{group: "backends", incrementalInstanceResult: test.later},
				{group: "backends", incrementalInstanceResult: test.earlier},
			}, registry)
			require.NoError(t, err)
			var rendered strings.Builder
			seen := make(map[string]struct{})
			for _, component := range []string{test.earlier.component, test.later.component} {
				if _, exists := seen[component]; exists {
					continue
				}
				seen[component] = struct{}{}
				rendered.WriteString(outputs["backends"][component])
			}
			config, _, err := registry.Assemble(context.Background(), rendered.String(), nil)
			require.NoError(t, err)
			assert.Contains(t, config, "# first")
			assert.NotContains(t, config, "# second")
		})
	}
}

func TestIncrementalBackendPlanWinnerRetirementPromotesCachedCandidate(t *testing.T) {
	winner := incrementalBackendPlanInstance{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "a",
			result: backendPlanResult(t, map[string]any{"name": "be_shared", "mode": "http"},
				"backend be_shared\n    # a\n", nil),
		},
	}
	candidate := incrementalBackendPlanInstance{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "b",
			result: backendPlanResult(t, map[string]any{"name": "be_shared", "mode": "tcp"},
				"backend be_shared\n    # b\n", nil),
		},
	}

	firstRegistry := rendercontext.NewPlanRegistry(nil)
	first, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{candidate, winner}, firstRegistry)
	require.NoError(t, err)
	firstConfig, _, err := firstRegistry.Assemble(context.Background(), first["backends"]["backends"], nil)
	require.NoError(t, err)
	assert.Contains(t, firstConfig, "# a")

	secondRegistry := rendercontext.NewPlanRegistry(nil)
	second, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{candidate}, secondRegistry)
	require.NoError(t, err)
	secondConfig, _, err := secondRegistry.Assemble(context.Background(), second["backends"]["backends"], nil)
	require.NoError(t, err)
	assert.Contains(t, secondConfig, "# b")
	assert.NotEqual(t, first["backends"]["backends"], second["backends"]["backends"])
}

func TestIncrementalBackendPlanFreshTokensProduceStableConfig(t *testing.T) {
	result, profile := profiledBackendPlanResult(t, "http", "be_app", "backend be_app from profile\n")
	instances := []incrementalBackendPlanInstance{{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "route", result: result,
		},
	}}
	outputs := make([]string, 0, 2)
	configs := make([]string, 0, 2)
	for range 2 {
		registry := rendercontext.NewPlanRegistry(nil)
		materialized, err := replayIncrementalBackendPlans(instances, registry)
		require.NoError(t, err)
		output := materialized["backends"]["backends"]
		config, _, err := registry.Assemble(context.Background(), registry.ProfileGroup()+output, nil)
		require.NoError(t, err)
		outputs = append(outputs, output)
		configs = append(configs, config)
	}
	assert.NotEqual(t, outputs[0], outputs[1])
	assert.Equal(t, configs[0], configs[1])
	assert.Contains(t, configs[0], "defaults "+profile)
	assert.NotContains(t, configs[0], "@haptic:")
}

func TestIncrementalBackendPlanLosingProfileIsNotReplayed(t *testing.T) {
	winner, winningProfile := profiledBackendPlanResult(t, "http", "be_shared", "backend be_shared\n    # winner\n")
	loser, losingProfile := profiledBackendPlanResult(t, "tcp", "be_shared", "backend be_shared\n    # loser\n")
	instances := []incrementalBackendPlanInstance{
		{
			group: "backends",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "backends", source: "routes", namespace: "default", name: "b", result: loser,
			},
		},
		{
			group: "backends",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "backends", source: "routes", namespace: "default", name: "a", result: winner,
			},
		},
	}
	registry := rendercontext.NewPlanRegistry(nil)
	outputs, err := replayIncrementalBackendPlans(instances, registry)
	require.NoError(t, err)
	config, _, err := registry.Assemble(
		context.Background(), registry.ProfileGroup()+outputs["backends"]["backends"], nil,
	)
	require.NoError(t, err)
	assert.Contains(t, config, winningProfile)
	assert.NotContains(t, config, losingProfile)
	assert.Contains(t, config, "# winner")
	assert.NotContains(t, config, "# loser")
}

func TestIncrementalBackendPlanResolvesWinningProfileAcrossInstancesOnce(t *testing.T) {
	loser, profile := profiledBackendPlanResult(t, "http", "be_shared", "backend be_shared\n    # loser\n")
	duplicate, duplicateProfile := profiledBackendPlanResult(
		t, "http", "be_shared", "backend be_shared\n    # duplicate\n",
	)
	require.Equal(t, profile, duplicateProfile)
	winner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "profile": profile, "mode": "http",
	}, "backend be_shared\n    # winner\n", nil)
	instances := []incrementalBackendPlanInstance{
		{
			group: "backends",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "backends", source: "routes", namespace: "default", name: "c", result: duplicate,
			},
		},
		{
			group: "backends",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "backends", source: "routes", namespace: "default", name: "b", result: loser,
			},
		},
		{
			group: "backends",
			incrementalInstanceResult: incrementalInstanceResult{
				component: "backends", source: "routes", namespace: "default", name: "a", result: winner,
			},
		},
	}
	registry := &countingPreparedPlanRegistrar{
		PlanRegistry: rendercontext.NewPlanRegistry(nil),
		profiles:     make(map[string]int),
	}
	outputs, err := replayIncrementalBackendPlans(instances, registry)
	require.NoError(t, err)
	assert.Equal(t, 1, registry.profiles[profile])
	config, _, err := registry.Assemble(
		context.Background(), registry.ProfileGroup()+outputs["backends"]["backends"], nil,
	)
	require.NoError(t, err)
	assert.Contains(t, config, "defaults "+profile)
	assert.Contains(t, config, "# winner")
	assert.NotContains(t, config, "# loser")
	assert.NotContains(t, config, "# duplicate")
}

func TestIncrementalBackendPlanRejectsWinningUndeclaredProfile(t *testing.T) {
	winner := backendPlanResult(t, map[string]any{
		"name": "be_shared", "profile": "external-profile", "mode": "http",
	}, "backend be_shared from external-profile\n", nil)
	_, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "a", result: winner,
		},
	}}, rendercontext.NewPlanRegistry(nil))
	require.ErrorContains(t, err, `references undeclared profile "external-profile"`)
}

func TestIncrementalBackendPlanOperationOrderBreaksIdentityTies(t *testing.T) {
	plan := newIncrementalBackendPlanRecorder()
	first, err := plan.Backend(map[string]any{"name": "be_shared", "mode": "http"}, "backend be_shared\n    # first\n")
	require.NoError(t, err)
	second, err := plan.Backend(map[string]any{"name": "be_shared", "mode": "tcp"}, "backend be_shared\n    # second\n")
	require.NoError(t, err)
	result, err := (&incrementalRecorder{plan: plan}).result(first + second)
	require.NoError(t, err)
	instance := incrementalBackendPlanInstance{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "route", result: result,
		},
	}
	registry := rendercontext.NewPlanRegistry(nil)
	outputs, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{instance}, registry)
	require.NoError(t, err)
	config, _, err := registry.Assemble(context.Background(), outputs["backends"]["backends"], nil)
	require.NoError(t, err)
	assert.Contains(t, config, "# first")
	assert.NotContains(t, config, "# second")
}

func TestIncrementalBackendPlanRejectsCorruptedEffects(t *testing.T) {
	profiled, _ := profiledBackendPlanResult(t, "http", "be_app", "backend be_app\n")
	backendOnly := backendPlanResult(t, map[string]any{"name": "be_app"}, "backend be_app\n", nil)
	multiplePlan := newIncrementalBackendPlanRecorder()
	firstToken, err := multiplePlan.Backend(map[string]any{"name": "be_first"}, "backend be_first\n")
	require.NoError(t, err)
	secondToken, err := multiplePlan.Backend(map[string]any{"name": "be_second"}, "backend be_second\n")
	require.NoError(t, err)
	multiple, err := (&incrementalRecorder{plan: multiplePlan}).result(firstToken + secondToken)
	require.NoError(t, err)
	tests := map[string]struct {
		base   incrementalComponentResult
		poison func(*incrementalComponentResult)
	}{
		"policy": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlan[0].Policy = "last"
			},
		},
		"identity": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlan[0].Identity = "be_other"
			},
		},
		"declaration digest": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlan[0].Backend.Digest = "0000000000000000"
			},
		},
		"effect digest": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlanDigest = "0000000000000000"
			},
		},
		"profile owner": {
			base: profiled,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlan[0].Owners = []uint32{99}
			},
		},
		"profile owner removed": {
			base: profiled,
			poison: func(result *incrementalComponentResult) {
				result.BackendPlan[0].Owners = nil
			},
		},
		"output reference": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				invalid := uint32(99)
				result.BackendPlanOutput[0].BackendCall = &invalid
			},
		},
		"valid but redirected output reference": {
			base: multiple,
			poison: func(result *incrementalComponentResult) {
				redirected := uint32(1)
				result.BackendPlanOutput[0].BackendCall = &redirected
			},
		},
		"mixed persistent text": {
			base: backendOnly,
			poison: func(result *incrementalComponentResult) {
				result.Text = "poison"
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			result := cloneIndexedComponentResult(&test.base)
			test.poison(&result)
			require.Error(t, validateIncrementalInstanceResult(&result))
			_, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{{
				group: "backends",
				incrementalInstanceResult: incrementalInstanceResult{
					component: "backends", source: "routes", namespace: "default", name: "route", result: result,
				},
			}}, rendercontext.NewPlanRegistry(nil))
			require.Error(t, err)
		})
	}
}

func TestIncrementalBackendPlanRejectsPersistentNonlogicalOutput(t *testing.T) {
	result := incrementalComponentResult{Text: "poison"}
	_, err := replayIncrementalBackendPlans([]incrementalBackendPlanInstance{{
		group: "backends",
		incrementalInstanceResult: incrementalInstanceResult{
			component: "backends", source: "routes", namespace: "default", name: "route", result: result,
		},
	}}, rendercontext.NewPlanRegistry(nil))
	require.ErrorContains(t, err, "nonlogical output")
}

func TestIncrementalBackendPlanIndexDeepClonesNestedDeclarations(t *testing.T) {
	result := backendPlanResult(t, map[string]any{
		"name": "be_app",
		"servers": []any{map[string]any{
			"name":  "server-1",
			"extra": []any{map[string]any{"name": "check", "args": []any{"inter", "2s"}}},
		}},
		"body": []any{"    timeout server 5s"},
	}, "backend be_app\n", nil)
	instance := incrementalInstanceResult{
		component: "backends", source: "routes", namespace: "default", name: "route", result: result,
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	result.BackendPlan[0].Backend.Backend.Servers[0].Extra[0].Args[0] = "poison"
	result.BackendPlan[0].Backend.Body[0] = "poison"
	*result.BackendPlanOutput[0].BackendCall = 99

	_, indexed, exists := index.instances.Root().Minimum()
	require.True(t, exists)
	indexedResult, err := decodeIndexedGroupInstanceResult(&indexed)
	require.NoError(t, err)
	assert.Equal(t, "inter", indexedResult.BackendPlan[0].Backend.Backend.Servers[0].Extra[0].Args[0])
	assert.Equal(t, "    timeout server 5s", indexedResult.BackendPlan[0].Backend.Body[0])
	assert.Zero(t, *indexedResult.BackendPlanOutput[0].BackendCall)
	require.NoError(t, validateIncrementalInstanceResult(&indexedResult))
}

func TestIncrementalBackendPlanRootScopeIsMainOnly(t *testing.T) {
	component := incrementalComponent{name: "backends", backendPlan: true}
	require.NoError(t, validateIncrementalBackendPlanScope(&component, "haproxy.cfg"))
	require.ErrorContains(t, validateIncrementalBackendPlanScope(&component, "routes.map"), "haproxy.cfg")
}

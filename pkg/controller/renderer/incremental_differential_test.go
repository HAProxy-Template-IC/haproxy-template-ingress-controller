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
	"log/slog"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalRenderingMatchesModelAcrossResourceChurn(t *testing.T) {
	cfg := differentialIncrementalConfig()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	routes := k8sstore.NewMemoryStore(2)
	services := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes, "services": services})
	routeState := map[string]string{}
	serviceState := map[string]string{}
	random := rand.New(rand.NewPCG(187, 2026))
	transactions := rand.New(rand.NewPCG(187, 2027))
	mutations := map[string]int{}
	aborts := 0

	for step := range 120 {
		mutations[applyDifferentialMutation(t, random, routes, services, routeState, serviceState)]++
		expected := differentialExpectedOutput(routeState, serviceState)
		generation := service.incremental.graph.Generation()
		snapshot := service.incremental.snapshot

		result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err, "step %d", step)
		assert.Equal(t, expected, result.HAProxyConfig, "step %d", step)
		oracle := assertDifferentialColdOracle(t, cfg, engine, provider, result)
		assert.Equal(t, generation, service.incremental.graph.Generation(), "uncommitted generation at step %d", step)
		assert.Same(t, snapshot, service.incremental.snapshot, "uncommitted snapshot at step %d", step)

		if transactions.IntN(4) == 0 {
			aborts++
			result.InputTransaction.Abort()
			assert.Equal(t, generation, service.incremental.graph.Generation(), "aborted generation at step %d", step)
			assert.Same(t, snapshot, service.incremental.snapshot, "aborted snapshot at step %d", step)

			require.NoError(t, oracle.RetireIncrementalCache(), "retire oracle at step %d", step)
			result, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
			require.NoError(t, err, "retry at step %d", step)
			assert.Equal(t, expected, result.HAProxyConfig, "retry at step %d", step)
			oracle = assertDifferentialColdOracle(t, cfg, engine, provider, result)
			assert.Equal(t, generation, service.incremental.graph.Generation(), "retry generation at step %d", step)
			assert.Same(t, snapshot, service.incremental.snapshot, "retry snapshot at step %d", step)
		}

		require.NoError(t, result.InputTransaction.Commit(t.Context()), "step %d", step)
		waitForIncrementalCache(t, service)
		assert.Equal(
			t,
			incrementalDifferentialEffectSnapshot(t, oracle),
			incrementalDifferentialEffectSnapshot(t, service),
			"committed effect snapshot at step %d", step,
		)
		require.NoError(t, oracle.RetireIncrementalCache(), "retire oracle at step %d", step)
	}
	require.NotZero(t, mutations["add"])
	require.NotZero(t, mutations["update"])
	require.NotZero(t, mutations["delete"])
	require.NotZero(t, aborts)
}

// assertDifferentialColdOracle compares the warm render's observables against a
// fresh cold service and returns the oracle so the caller can compare committed
// effect snapshots after the warm transaction commits (the warm snapshot only
// advances at commit, so an earlier comparison would be off by one mutation).
func assertDifferentialColdOracle(
	t *testing.T,
	cfg *config.Config,
	engine templating.Engine,
	provider stores.StoreProvider,
	warm *RenderResult,
) *RenderService {
	t.Helper()
	fresh := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	cold := renderIncrementalSourceTransactionTestResult(t, fresh, provider)
	assertIncrementalSourceTransactionObservablesEqual(t, warm, cold)
	return fresh
}

type differentialIncrementalEffectSnapshot struct {
	results      []string
	groupOutputs []string
	events       []templating.RenderedEvent
	status       []incrementalStatusPatchCall
	publications []incrementalPublishedWinner
	http         []authenticatedIncrementalHTTPEffectTuple
	derived      []incrementalDerivedResource
}

func incrementalDifferentialEffectSnapshot(
	t *testing.T,
	service *RenderService,
) differentialIncrementalEffectSnapshot {
	t.Helper()
	service.incremental.mu.Lock()
	snapshot := service.incremental.snapshot
	service.incremental.mu.Unlock()
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))

	result := differentialIncrementalEffectSnapshot{
		derived: authenticatedIncrementalDerivedResources(t, service),
	}
	snapshot.results.Root().Walk(func(key []byte, root incremental.ExactValueRoot) bool {
		require.NoError(t, root.ValidateAuthentication())
		encoded, err := root.String()
		require.NoError(t, err)
		result.results = append(result.results, string(key)+"\x00"+encoded)
		return false
	})

	groups := make([]string, 0, len(snapshot.groupIndexes))
	for group := range snapshot.groupIndexes {
		groups = append(groups, group)
	}
	slices.Sort(groups)
	for _, group := range groups {
		index := snapshot.groupIndexes[group]
		require.NoError(t, index.validateAuthentication())
		components := slices.Clone(service.incremental.groups[group])
		slices.SortFunc(components, func(left, right incrementalComponent) int {
			return strings.Compare(left.name, right.name)
		})
		for position := range components {
			name := components[position].name
			result.groupOutputs = append(
				result.groupOutputs,
				group+"\x00"+name+"\x00"+mustIncrementalGroupOutput(t, index, name),
			)
		}
		events, err := index.renderedEvents()
		require.NoError(t, err)
		result.events = append(result.events, events...)
		status, err := index.statusPatchCalls()
		require.NoError(t, err)
		result.status = append(result.status, status...)
		publications, err := index.allPublishedWinners()
		require.NoError(t, err)
		result.publications = append(result.publications, publications...)
		if service.httpStoreComponent == nil {
			httpEffects, err := index.httpEffects()
			require.NoError(t, err)
			require.Empty(t, httpEffects)
		}
	}
	if service.httpStoreComponent != nil {
		result.http = canonicalIncrementalHTTPEffectTuples(t, service)
	}
	return result
}

func differentialIncrementalConfig() *config.Config {
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"services": {
				APIVersion: "example.test/v1", Resources: "services",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name: "routes", Requires: []string{"routes", "services"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{%%
var namespace = dig_string(item, "", "metadata", "namespace")
var name = dig_string(item, "", "metadata", "name")
var backend = dig_string(item, "", "spec", "backend")
var service = resources.services.GetSingle(namespace, backend)
if service != nil {
  show shared.Unique("routes", backend, name + "=" + dig_string(service, "", "spec", "value") + "\n")
}
%%}`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
}

func applyDifferentialMutation(
	t *testing.T,
	random *rand.Rand,
	routes, services *k8sstore.MemoryStore,
	routeState, serviceState map[string]string,
) string {
	t.Helper()
	routeName := fmt.Sprintf("route-%02d", random.IntN(18))
	serviceName := fmt.Sprintf("service-%02d", random.IntN(9))
	switch random.IntN(6) {
	case 0, 1:
		_, existed := routeState[routeName]
		routeState[routeName] = serviceName
		resource := incrementalTestResource("default", routeName, map[string]any{"backend": serviceName})
		if existed {
			require.NoError(t, routes.Update(resource, []string{"default", routeName}))
			return "update"
		}
		require.NoError(t, routes.Add(resource, []string{"default", routeName}))
		return "add"
	case 2:
		require.NoError(t, routes.Delete("default", routeName, []string{"default", routeName}))
		delete(routeState, routeName)
		return "delete"
	case 3, 4:
		value := fmt.Sprintf("value-%03d", random.IntN(50))
		_, existed := serviceState[serviceName]
		serviceState[serviceName] = value
		resource := incrementalTestResource("default", serviceName, map[string]any{"value": value})
		if existed {
			require.NoError(t, services.Update(resource, []string{"default", serviceName}))
			return "update"
		}
		require.NoError(t, services.Add(resource, []string{"default", serviceName}))
		return "add"
	case 5:
		require.NoError(t, services.Delete("default", serviceName, []string{"default", serviceName}))
		delete(serviceState, serviceName)
		return "delete"
	}
	panic("unreachable differential mutation")
}

func differentialExpectedOutput(routes, services map[string]string) string {
	names := make([]string, 0, len(routes))
	for name := range routes {
		names = append(names, name)
	}
	slices.Sort(names)
	seen := map[string]struct{}{}
	var output strings.Builder
	for _, name := range names {
		backend := routes[name]
		value, exists := services[backend]
		if _, duplicate := seen[backend]; !exists || duplicate {
			continue
		}
		seen[backend] = struct{}{}
		output.WriteString(name)
		output.WriteByte('=')
		output.WriteString(value)
		output.WriteByte('\n')
	}
	if output.Len() == 0 {
		return "\n"
	}
	return output.String()
}

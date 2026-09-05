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
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const multiMountFirstText = `{%%
recordEvent(item, "Mounted", "executed once")
show "first\n"
%%}`

const multiMountSecondText = `{%% show "second\n" %%}`

const multiMountPublishValue = `{%%
var name = item | dig_string("", "metadata", "name")
show shared.PublishRanked("values", source, name, name + "\n")
%%}`

func TestIncrementalGroupMountsTextThreeTimesAndEffectsOnce(t *testing.T) {
	for _, cold := range []bool{false, true} {
		mode := "incremental"
		if cold {
			mode = "cold"
		}
		t.Run(mode, func(t *testing.T) {
			cfg := multiMountConfig(
				`{{ render "100-first" }}{{ render "200-second" }}{{ render "100-first" }}{{ render "200-second" }}`,
				false,
			)
			cfg.Maps = map[string]config.MapFile{
				"third.map": {Template: `{{ render "100-first" }}{{ render "200-second" }}`},
			}
			service, engine, provider := newMultiMountService(t, cfg, true)
			result, err := renderMultiMount(t, cold, service, provider)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, "first\nsecond\nfirst\nsecond\n\n", result.HAProxyConfig)
			assert.Equal(t, "first\nsecond\n", multiMountMapContent(t, result, "third.map"))
			renderedEvents := requireRenderEvents(t, result)
			require.Len(t, renderedEvents, 1)
			assert.Equal(t, "Mounted", renderedEvents[0].Reason)
			assert.Equal(t, map[string]int{"firsts/first": 1, "seconds/second": 1}, engine.executionCounts())
			require.NoError(t, result.InputTransaction.Commit(t.Context()))
		})
	}
}

func TestIncrementalGroupRejectsInvalidSecondMount(t *testing.T) {
	tests := map[string]struct {
		root    string
		wantErr string
	}{
		"partial": {
			root:    `{{ render "100-first" }}{{ render "200-second" }}{{ render "100-first" }}`,
			wantErr: "1 trailing calls",
		},
		"reordered": {
			root:    `{{ render "100-first" }}{{ render "200-second" }}{{ render "200-second" }}{{ render "100-first" }}`,
			wantErr: "canonical order",
		},
	}
	for name, test := range tests {
		for _, cold := range []bool{false, true} {
			mode := "incremental"
			if cold {
				mode = "cold"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				cfg := multiMountConfig(test.root, false)
				service, _, provider := newMultiMountService(t, cfg, false)
				_, err := renderMultiMount(t, cold, service, provider)
				require.ErrorContains(t, err, test.wantErr)
			})
		}
	}
}

func TestIncrementalGroupSelectorRequiresCompletedMount(t *testing.T) {
	tests := map[string]struct {
		root    string
		wantErr string
	}{
		"after complete sequence": {
			root: `{{ render "100-first" }}{{ render "200-second" }}{{ incremental_ranked_fragments("mounts", "values") }}`,
		},
		"before first complete sequence": {
			root:    `{{ render "100-first" }}{{ incremental_ranked_fragments("mounts", "values") }}{{ render "200-second" }}`,
			wantErr: `must complete its canonical root call before selection`,
		},
		"during partial second sequence": {
			root:    `{{ render "100-first" }}{{ render "200-second" }}{{ render "100-first" }}{{ incremental_ranked_fragments("mounts", "values") }}{{ render "200-second" }}`,
			wantErr: `must complete its canonical root call before selection`,
		},
	}
	for name, test := range tests {
		for _, cold := range []bool{false, true} {
			mode := "incremental"
			if cold {
				mode = "cold"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				cfg := multiMountConfig(test.root, true)
				service, _, provider := newMultiMountService(t, cfg, false)
				result, err := renderMultiMount(t, cold, service, provider)
				if test.wantErr != "" {
					require.ErrorContains(t, err, test.wantErr)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, "first\nsecond\n\n", result.HAProxyConfig)
				require.NoError(t, result.InputTransaction.Commit(t.Context()))
			})
		}
	}
}

func TestIncrementalGroupMainMountAuthorizesAuxiliarySelector(t *testing.T) {
	for _, cold := range []bool{false, true} {
		mode := "incremental"
		if cold {
			mode = "cold"
		}
		t.Run(mode, func(t *testing.T) {
			cfg := multiMountConfig(`{{ render "100-first" }}{{ render "200-second" }}`, true)
			cfg.Maps = map[string]config.MapFile{
				"values.map": {Template: `{{ incremental_ranked_fragments("mounts", "values") }}`},
			}
			service, _, provider := newMultiMountService(t, cfg, false)
			result, err := renderMultiMount(t, cold, service, provider)
			require.NoError(t, err)
			assert.Equal(t, "first\nsecond\n", multiMountMapContent(t, result, "values.map"))
			require.NoError(t, result.InputTransaction.Commit(t.Context()))
		})
	}
}

func TestIncrementalGroupRejectsCrossAuxiliaryAuthorization(t *testing.T) {
	for _, cold := range []bool{false, true} {
		mode := "incremental"
		if cold {
			mode = "cold"
		}
		t.Run(mode, func(t *testing.T) {
			cfg := multiMountConfig("", true)
			cfg.Maps = map[string]config.MapFile{
				"producer.map": {Template: `{{ render "100-first" }}{{ render "200-second" }}`},
				"consumer.map": {Template: `{{ incremental_ranked_fragments("mounts", "values") }}`},
			}
			service, _, provider := newMultiMountService(t, cfg, false)
			_, err := renderMultiMount(t, cold, service, provider)
			require.ErrorContains(t, err, "must complete its canonical root call before selection")
		})
	}
}

func TestIncrementalGroupAuthorizationIsScopeLocal(t *testing.T) {
	components := []incrementalComponent{{name: "100-first"}, {name: "200-second"}}
	state := &incrementalRenderState{
		groups: map[string][]incrementalComponent{"mounts": components},
		config: &config.Config{},
	}

	t.Run("incremental", func(t *testing.T) {
		runtime := &incrementalRenderSession{
			state:        state,
			requested:    map[string]bool{"mounts": true},
			calls:        map[string][]incrementalCall{},
			scopedCalls:  map[string]map[string][]incrementalCall{},
			callStatuses: map[string]map[string]incrementalScopeCallStatus{},
		}
		runtime.calls, runtime.scopedCalls, runtime.callStatuses = recordIncrementalCall(runtime.calls, runtime.scopedCalls, runtime.callStatuses,
			"mounts", components, incrementalCall{
				scope: "haproxy.cfg", component: "100-first",
			})
		runtime.calls, runtime.scopedCalls, runtime.callStatuses = recordIncrementalCall(runtime.calls, runtime.scopedCalls, runtime.callStatuses,
			"mounts", components, incrementalCall{
				scope: "routes.map", component: "100-first",
			})
		runtime.calls, runtime.scopedCalls, runtime.callStatuses = recordIncrementalCall(runtime.calls, runtime.scopedCalls, runtime.callStatuses,
			"mounts", components, incrementalCall{
				scope: "haproxy.cfg", component: "200-second",
			})

		require.NoError(t, runtime.requireProducerGroupCall("mounts", "haproxy.cfg"))
		require.ErrorContains(t, runtime.requireProducerGroupCall("mounts", "routes.map"), "1 trailing calls")
		require.NoError(t, runtime.requireProducerGroupCall("mounts", "errors.http"))
		runtime.calls, runtime.scopedCalls, runtime.callStatuses = recordIncrementalCall(runtime.calls, runtime.scopedCalls, runtime.callStatuses,
			"mounts", components, incrementalCall{scope: "broken.map", component: "200-second"})
		require.ErrorContains(t, runtime.requireProducerGroupCall("mounts", "broken.map"), "canonical order")

		runtime.calls, runtime.scopedCalls, runtime.callStatuses = recordIncrementalCall(runtime.calls, runtime.scopedCalls, runtime.callStatuses,
			"mounts", components, incrementalCall{
				scope: "routes.map", component: "200-second",
			})
		require.NoError(t, runtime.requireProducerGroupCall("mounts", "routes.map"))

		auxOnly := &incrementalRenderSession{
			state: state, requested: map[string]bool{"mounts": true},
			calls: map[string][]incrementalCall{}, scopedCalls: map[string]map[string][]incrementalCall{},
			callStatuses: map[string]map[string]incrementalScopeCallStatus{},
		}
		require.ErrorContains(t, auxOnly.requireProducerGroupCall("mounts", "consumer.map"), "neither the current root")
		auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses = recordIncrementalCall(auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses,
			"mounts", components, incrementalCall{scope: "producer.map", component: "100-first"})
		auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses = recordIncrementalCall(auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses,
			"mounts", components, incrementalCall{scope: "producer.map", component: "200-second"})
		require.ErrorContains(t, auxOnly.requireProducerGroupCall("mounts", "consumer.map"), "neither the current root")
	})

	t.Run("cold", func(t *testing.T) {
		renderer := &coldIncrementalRenderer{
			state:        state,
			requested:    map[string]bool{"mounts": true},
			calls:        map[string][]incrementalCall{},
			scopedCalls:  map[string]map[string][]incrementalCall{},
			callStatuses: map[string]map[string]incrementalScopeCallStatus{},
		}
		renderer.calls, renderer.scopedCalls, renderer.callStatuses = recordIncrementalCall(renderer.calls, renderer.scopedCalls, renderer.callStatuses,
			"mounts", components, incrementalCall{
				scope: "haproxy.cfg", component: "100-first",
			})
		renderer.calls, renderer.scopedCalls, renderer.callStatuses = recordIncrementalCall(renderer.calls, renderer.scopedCalls, renderer.callStatuses,
			"mounts", components, incrementalCall{
				scope: "routes.map", component: "100-first",
			})
		renderer.calls, renderer.scopedCalls, renderer.callStatuses = recordIncrementalCall(renderer.calls, renderer.scopedCalls, renderer.callStatuses,
			"mounts", components, incrementalCall{
				scope: "haproxy.cfg", component: "200-second",
			})

		require.NoError(t, renderer.requireProducerGroupCall("mounts", "haproxy.cfg"))
		require.ErrorContains(t, renderer.requireProducerGroupCall("mounts", "routes.map"), "1 trailing calls")
		require.NoError(t, renderer.requireProducerGroupCall("mounts", "errors.http"))
		renderer.calls, renderer.scopedCalls, renderer.callStatuses = recordIncrementalCall(renderer.calls, renderer.scopedCalls, renderer.callStatuses,
			"mounts", components, incrementalCall{scope: "broken.map", component: "200-second"})
		require.ErrorContains(t, renderer.requireProducerGroupCall("mounts", "broken.map"), "canonical order")

		renderer.calls, renderer.scopedCalls, renderer.callStatuses = recordIncrementalCall(renderer.calls, renderer.scopedCalls, renderer.callStatuses,
			"mounts", components, incrementalCall{
				scope: "routes.map", component: "200-second",
			})
		require.NoError(t, renderer.requireProducerGroupCall("mounts", "routes.map"))

		auxOnly := &coldIncrementalRenderer{
			state: state, requested: map[string]bool{"mounts": true},
			calls: map[string][]incrementalCall{}, scopedCalls: map[string]map[string][]incrementalCall{},
			callStatuses: map[string]map[string]incrementalScopeCallStatus{},
		}
		require.ErrorContains(t, auxOnly.requireProducerGroupCall("mounts", "consumer.map"), "neither the current root")
		auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses = recordIncrementalCall(auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses,
			"mounts", components, incrementalCall{scope: "producer.map", component: "100-first"})
		auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses = recordIncrementalCall(auxOnly.calls, auxOnly.scopedCalls, auxOnly.callStatuses,
			"mounts", components, incrementalCall{scope: "producer.map", component: "200-second"})
		require.ErrorContains(t, auxOnly.requireProducerGroupCall("mounts", "consumer.map"), "neither the current root")
	})
}

func multiMountConfig(root string, publish bool) *config.Config {
	firstTemplate := multiMountFirstText
	secondTemplate := multiMountSecondText
	firstEffects := []config.IncrementalEffect{config.IncrementalEffectRecordEvent}
	var secondEffects []config.IncrementalEffect
	if publish {
		firstTemplate = multiMountPublishValue
		secondTemplate = multiMountPublishValue
		firstEffects = []config.IncrementalEffect{config.IncrementalEffectPublishValue}
		secondEffects = []config.IncrementalEffect{config.IncrementalEffectPublishValue}
	}
	return &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"firsts": {
				APIVersion: "example.test/v1", Resources: "firsts",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"seconds": {
				APIVersion: "example.test/v1", Resources: "seconds",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"100-first": {
				Name: "100-first", Requires: []string{"firsts"}, Template: firstTemplate,
				Incremental: &config.IncrementalTemplate{
					Source: "firsts", Group: "mounts", Effects: firstEffects,
				},
			},
			"200-second": {
				Name: "200-second", Requires: []string{"seconds"}, Template: secondTemplate,
				Incremental: &config.IncrementalTemplate{
					Source: "seconds", Group: "mounts", Effects: secondEffects,
				},
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: root + "\n"},
	}
}

func newMultiMountService(
	t *testing.T,
	cfg *config.Config,
	countExecutions bool,
) (*RenderService, *dynamicBindingCountingEngine, stores.StoreProvider) {
	t.Helper()
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := baseEngine
	var counting *dynamicBindingCountingEngine
	if countExecutions {
		counting = newDynamicBindingCountingEngine(t, baseEngine)
		engine = counting
	}
	firsts := k8sstore.NewMemoryStore(2)
	seconds := k8sstore.NewMemoryStore(2)
	require.NoError(t, firsts.Add(
		incrementalTestResource("default", "first", nil), []string{"default", "first"},
	))
	require.NoError(t, seconds.Add(
		incrementalTestResource("default", "second", nil), []string{"default", "second"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"firsts": firsts, "seconds": seconds,
	})
	return NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: slog.Default(),
	}), counting, provider
}

func renderMultiMount(
	t *testing.T,
	cold bool,
	service *RenderService,
	provider stores.StoreProvider,
) (*RenderResult, error) {
	t.Helper()
	if cold {
		return renderServiceStaticCold(t, service, provider)
	}
	return service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
}

func multiMountMapContent(t *testing.T, result *RenderResult, name string) string {
	t.Helper()
	for _, file := range requireAuxiliaryFiles(t, result).MapFiles {
		if file.Path == name {
			return file.Content
		}
	}
	t.Fatalf("map %q was not rendered", name)
	return ""
}

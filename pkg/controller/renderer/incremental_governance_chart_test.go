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
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const governanceChartSnippetName = "features-960-governance"

const governanceChartDerivedConsumer = `{%%
var namespace = item | dig_string("", "metadata", "namespace")
var name = item | dig_string("", "metadata", "name")
var current = resources.targets.GetSingle(namespace, name)
show "governed=" + (current | dig_string("<missing>", "metadata", "annotations", "governed")) +
  ",preserved=" + (current | dig_string("<missing>", "metadata", "annotations", "preserved")) + "\n"
%%}`

type governanceChartLibrary struct {
	TemplateSnippets map[string]governanceChartSnippet `yaml:"templateSnippets"`
}

type governanceChartSnippet struct {
	Template    string                      `yaml:"template"`
	Requires    []string                    `yaml:"requires"`
	Incremental *governanceChartIncremental `yaml:"incremental"`
}

type governanceChartIncremental struct {
	Source           string                     `yaml:"source"`
	BindingsTemplate string                     `yaml:"bindingsTemplate"`
	Group            string                     `yaml:"group"`
	Effects          []config.IncrementalEffect `yaml:"effects"`
}

func TestGovernanceChartDefaultHTTPSInvalidatesOnlyTLSBinding(t *testing.T) {
	snippet := loadGovernanceChartSnippet(t)
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: governanceChartContext(false),
		},
		WatchedResources: map[string]config.WatchedResource{
			"tlsTargets": {
				APIVersion: "example.test/v1",
				Resources:  "tlstargets",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
			"labelTargets": {
				APIVersion: "example.test/v1",
				Resources:  "labeltargets",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			governanceChartSnippetName: snippet,
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "features-960-governance" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})

	tlsTargets := k8sstore.NewMemoryStore(2)
	require.NoError(t, tlsTargets.Add(
		governanceChartResource("tls-route", nil),
		[]string{"default", "tls-route"},
	))
	labelTargets := k8sstore.NewMemoryStore(2)
	require.NoError(t, labelTargets.Add(
		governanceChartResource("label-route", map[string]any{"owner": "platform"}),
		[]string{"default", "label-route"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"tlsTargets":   tlsTargets,
		"labelTargets": labelTargets,
	})

	first := renderAndCommitGovernanceChart(t, service, provider)
	renderedEvents := requireRenderEvents(t, first)
	require.Len(t, renderedEvents, 1)
	assert.Equal(t, "tls-route", renderedEvents[0].Name)
	assert.Equal(t, "GovernanceViolation", renderedEvents[0].Reason)
	assert.Equal(t, map[string]int{"tlsTargets/tls-route": 1, "labelTargets/label-route": 1}, engine.executionCounts())

	component := service.incremental.components[governanceChartSnippetName]
	tlsQuery := componentQueryKey(&component, "tlsTargets", "default", "tls-route")
	labelQuery := componentQueryKey(&component, "labelTargets", "default", "label-route")
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(tlsQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(labelQuery).Executions)

	cfg.TemplatingSettings.ExtraContext = governanceChartContext(true)
	second := renderAndCommitGovernanceChart(t, service, provider)
	assert.Empty(t, requireRenderEvents(t, second))
	assert.Equal(t, map[string]int{"tlsTargets/tls-route": 2, "labelTargets/label-route": 1}, engine.executionCounts())
	assert.Equal(t, uint64(2), service.incremental.graph.Counters(tlsQuery).Executions)
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(labelQuery).Executions)
}

func TestGovernanceChartDerivesAnnotationsWithoutMutatingWatchedResources(t *testing.T) {
	fixture := newGovernanceChartDerivationFixture(t, "alpha")
	original := fixture.rawStore(t)

	added := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=alpha,preserved=original\n", added.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, added))
	assert.Equal(t, original, fixture.rawStore(t))
	fixture.assertDerivedAnnotation(t, "alpha")
	alphaRoot := fixture.authenticatedRoot(t, fixture.projectionQuery)
	fixture.authenticatedRoot(t, fixture.governanceQuery)
	fixture.authenticatedRoot(t, fixture.consumerQuery)

	beforeWarm := fixture.engine.executionCounts()
	warmAdded := fixture.renderAndCommit(t)
	assertCustomCRDObservableEqual(t, added, warmAdded)
	assert.Equal(t, original, fixture.rawStore(t))
	assert.Equal(t, beforeWarm, fixture.engine.executionCounts())
	warmAlphaRoot := fixture.authenticatedRoot(t, fixture.projectionQuery)
	sameRoot, err := alphaRoot.SameRoot(warmAlphaRoot)
	require.NoError(t, err)
	assert.True(t, sameRoot)

	fixture.setDefault("beta")
	changed := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=beta,preserved=original\n", changed.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, changed))
	assert.Equal(t, original, fixture.rawStore(t))
	fixture.assertDerivedAnnotation(t, "beta")
	betaRoot := fixture.authenticatedRoot(t, fixture.projectionQuery)
	sameRoot, err = alphaRoot.SameRoot(betaRoot)
	require.NoError(t, err)
	assert.False(t, sameRoot)
	assertCustomCRDObservableEqual(t, governanceChartDerivationColdOracle(t, "beta"), changed)

	beforeWarm = fixture.engine.executionCounts()
	warmChanged := fixture.renderAndCommit(t)
	assertCustomCRDObservableEqual(t, changed, warmChanged)
	assert.Equal(t, original, fixture.rawStore(t))
	assert.Equal(t, beforeWarm, fixture.engine.executionCounts())
	warmBetaRoot := fixture.authenticatedRoot(t, fixture.projectionQuery)
	sameRoot, err = betaRoot.SameRoot(warmBetaRoot)
	require.NoError(t, err)
	assert.True(t, sameRoot)

	assertGovernanceChartDerivationRemoval(t, fixture, original)
}

func assertGovernanceChartDerivationRemoval(
	t *testing.T,
	fixture *governanceChartDerivationFixture,
	original []byte,
) {
	t.Helper()
	fixture.setDefault("")
	removed := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=<missing>,preserved=original\n", removed.HAProxyConfig)
	assert.Empty(t, requireRenderEvents(t, removed))
	assert.Equal(t, original, fixture.rawStore(t))
	fixture.assertNoDerivedAnnotation(t)
	assertCustomCRDObservableEqual(t, governanceChartDerivationColdOracle(t, ""), removed)

	beforeWarm := fixture.engine.executionCounts()
	warmRemoved := fixture.renderAndCommit(t)
	assertCustomCRDObservableEqual(t, removed, warmRemoved)
	assert.Equal(t, original, fixture.rawStore(t))
	assert.Equal(t, beforeWarm, fixture.engine.executionCounts())
	fixture.assertNoDerivedAnnotation(t)
	_, projectionCached, err := fixture.service.incremental.graph.ExactValue(fixture.projectionQuery)
	require.NoError(t, err)
	assert.False(t, projectionCached)
}

func TestGovernanceDerivedSnapshotAuthenticatesSourceAndBindingLineage(t *testing.T) {
	fixture := newGovernanceChartDerivationFixture(t, "alpha")
	original := fixture.rawStore(t)
	alpha := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=alpha,preserved=original\n", alpha.HAProxyConfig)

	sourceInput := resourceInputKey(&resourceInputSpec{
		resourceType: "targets",
		scope:        resourceInputGet,
		keys:         []string{"default", "route"},
	})
	bindingInput := bindingInputKey(governanceChartSnippetName, "targets")
	ownerInput := deriveOwnerInputKey("targets")
	for _, key := range []incremental.InputKey{sourceInput, bindingInput, ownerInput} {
		require.NotEmpty(t, key.Opaque())
		assert.True(t, fixture.service.incremental.graph.HasInputDependents(key), key.Opaque())
	}
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.governanceQuery).Executions)

	fixture.setDefault("beta")
	beta := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=beta,preserved=original\n", beta.HAProxyConfig)
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(fixture.governanceQuery).Executions)

	fixture.setDefault("alpha")
	alphaAgain := fixture.renderAndCommit(t)
	assertCustomCRDObservableEqual(t, alpha, alphaAgain)
	assert.Equal(t, uint64(3), fixture.service.incremental.graph.Counters(fixture.governanceQuery).Executions)
	assert.Equal(t, original, fixture.rawStore(t))

	pending, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, pending.InputTransaction)
	assert.Equal(t, "governed=alpha,preserved=original\n", pending.HAProxyConfig)

	fixture.setDefault("beta")
	newer := fixture.renderAndCommit(t)
	assert.Equal(t, "governed=beta,preserved=original\n", newer.HAProxyConfig)
	require.ErrorIs(t, pending.InputTransaction.Commit(t.Context()), errRenderOutputGenerationSuperseded)
	fixture.assertDerivedAnnotation(t, "beta")
	assertCustomCRDObservableEqual(t, governanceChartDerivationColdOracle(t, "beta"), newer)
	assert.Equal(t, original, fixture.rawStore(t))
}

type governanceChartDerivationFixture struct {
	config          *config.Config
	service         *RenderService
	engine          *dynamicBindingCountingEngine
	targets         *k8sstore.MemoryStore
	provider        stores.StoreProvider
	governanceQuery incremental.QueryKey
	projectionQuery incremental.QueryKey
	consumerQuery   incremental.QueryKey
}

func newGovernanceChartDerivationFixture(t *testing.T, defaultValue string) *governanceChartDerivationFixture {
	t.Helper()
	snippets := map[string]config.TemplateSnippet{
		governanceChartSnippetName: loadGovernanceChartSnippet(t),
		"governance-derived-consumer": {
			Name:     "governance-derived-consumer",
			Requires: []string{"targets"},
			Incremental: &config.IncrementalTemplate{
				Source: "targets",
			},
			Template: governanceChartDerivedConsumer,
		},
	}
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: governanceChartDerivationContext(defaultValue),
		},
		WatchedResources: map[string]config.WatchedResource{
			"targets": {
				APIVersion: "example.test/v1",
				Resources:  "targets",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: snippets,
		HAProxyConfig: config.HAProxyConfig{
			Template: `{{ render "features-960-governance" }}{{ render "governance-derived-consumer" }}`,
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Kinds:  map[string]string{},
		Errors: map[string]error{},
	})
	baseEngine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil, declarations, helpers.EngineOptions{},
	)
	require.NoError(t, err)
	engine := newDynamicBindingCountingEngine(t, baseEngine)
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: slog.Default()})
	targets := k8sstore.NewMemoryStore(2)
	require.NoError(t, targets.Add(governanceChartDerivedResource(), []string{"default", "route"}))
	governance := service.incremental.components[governanceChartSnippetName]
	consumer := service.incremental.components["governance-derived-consumer"]
	return &governanceChartDerivationFixture{
		config:          cfg,
		service:         service,
		engine:          engine,
		targets:         targets,
		provider:        stores.NewRealStoreProvider(map[string]stores.Store{"targets": targets}),
		governanceQuery: componentQueryKey(&governance, "targets", "default", "route"),
		projectionQuery: derivedProjectionQueryKey("targets", "default", "route"),
		consumerQuery:   componentQueryKey(&consumer, "targets", "default", "route"),
	}
}

func governanceChartDerivationContext(defaultValue string) map[string]any {
	rule := map[string]any{
		"enabled":  false,
		"resource": "targets",
		"path":     "metadata.annotations.governed",
	}
	if defaultValue != "" {
		rule["enabled"] = true
		rule["default"] = defaultValue
	}
	return map[string]any{
		"governance": map[string]any{
			"enabled":          true,
			"exemptNamespaces": []any{},
			"rules":            map[string]any{"annotation-default": rule},
		},
	}
}

func governanceChartDerivedResource() map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        "route",
			"annotations": map[string]any{"preserved": "original"},
		},
		"spec": map[string]any{},
	}
}

func governanceChartDerivationColdOracle(t *testing.T, defaultValue string) *RenderResult {
	t.Helper()
	fixture := newGovernanceChartDerivationFixture(t, defaultValue)
	original := fixture.rawStore(t)
	result := fixture.renderAndCommit(t)
	assert.Equal(t, original, fixture.rawStore(t))
	return result
}

func (f *governanceChartDerivationFixture) setDefault(defaultValue string) {
	f.config.TemplatingSettings.ExtraContext = governanceChartDerivationContext(defaultValue)
}

func (f *governanceChartDerivationFixture) renderAndCommit(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func (f *governanceChartDerivationFixture) rawStore(t *testing.T) []byte {
	t.Helper()
	items, err := f.targets.List()
	require.NoError(t, err)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	return encoded
}

func (f *governanceChartDerivationFixture) authenticatedRoot(
	t *testing.T,
	query incremental.QueryKey,
) incremental.ExactValueRoot {
	t.Helper()
	root, found, err := f.service.incremental.graph.ExactValue(query)
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, root.ValidateAuthentication())
	require.NoError(t, f.service.incremental.graph.ValidateExactValue(query, root))
	require.NoError(t, f.service.incremental.graph.ValidateCommittedExactValue(query, root))
	return root
}

func (f *governanceChartDerivationFixture) assertDerivedAnnotation(t *testing.T, expected string) {
	t.Helper()
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	entry, found := f.service.incremental.snapshot.derived.Get(derivedKey(rendercontext.DerivedResourceIdentity{
		Resource: "targets", Namespace: "default", Name: "route",
	}))
	require.True(t, found)
	assert.Equal(t, rendercontext.DerivedResourceIdentity{
		Resource: "targets", Namespace: "default", Name: "route",
	}, entry.Identity)
	wantSource, err := json.Marshal(governanceChartDerivedResource())
	require.NoError(t, err)
	assert.Equal(t, string(wantSource), entry.Source)
	decoded, err := decodeResourceValue([]byte(entry.Value))
	require.NoError(t, err)
	resource, ok := decoded.(map[string]any)
	require.True(t, ok)
	metadata, ok := resource["metadata"].(map[string]any)
	require.True(t, ok)
	annotations, ok := metadata["annotations"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"governed": expected, "preserved": "original"}, annotations)
	view := rendercontext.NewDerivedResourceView()
	materialized := entry.materialize()
	require.NoError(t, view.Replay(&materialized))
}

func (f *governanceChartDerivationFixture) assertNoDerivedAnnotation(t *testing.T) {
	t.Helper()
	f.service.incremental.mu.Lock()
	defer f.service.incremental.mu.Unlock()
	_, found := f.service.incremental.snapshot.derived.Get(derivedKey(rendercontext.DerivedResourceIdentity{
		Resource: "targets", Namespace: "default", Name: "route",
	}))
	assert.False(t, found)
}

func loadGovernanceChartSnippet(t *testing.T) config.TemplateSnippet {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts", "governance", "library.yaml")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var library governanceChartLibrary
	require.NoError(t, yaml.Unmarshal(content, &library))
	chartSnippet, exists := library.TemplateSnippets[governanceChartSnippetName]
	require.True(t, exists)
	require.NotNil(t, chartSnippet.Incremental)
	return config.TemplateSnippet{
		Name:     governanceChartSnippetName,
		Template: chartSnippet.Template,
		Requires: chartSnippet.Requires,
		Incremental: &config.IncrementalTemplate{
			Source:           chartSnippet.Incremental.Source,
			BindingsTemplate: chartSnippet.Incremental.BindingsTemplate,
			Group:            chartSnippet.Incremental.Group,
			Effects:          chartSnippet.Incremental.Effects,
		},
	}
}

func governanceChartContext(defaultHTTPS bool) map[string]any {
	return map[string]any{
		"ingressDefaultHTTPS": defaultHTTPS,
		"governance": map[string]any{
			"enabled":          true,
			"exemptNamespaces": []any{},
			"rules": map[string]any{
				"tls": map[string]any{
					"enabled":     true,
					"resource":    "tlsTargets",
					"satisfiedBy": "tls",
					"enforcement": "audit",
				},
				"label": map[string]any{
					"enabled":     true,
					"resource":    "labelTargets",
					"path":        "metadata.labels.owner",
					"required":    true,
					"enforcement": "audit",
				},
			},
		},
	}
}

func governanceChartResource(name string, labels map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Example",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
			"labels":    labels,
		},
		"spec": map[string]any{},
	}
}

func renderAndCommitGovernanceChart(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	return result
}

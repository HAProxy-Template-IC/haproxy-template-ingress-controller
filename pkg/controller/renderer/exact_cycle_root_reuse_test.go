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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type rootReuseFixture struct {
	cfg      *config.Config
	service  *RenderService
	provider stores.StoreProvider
	hosts    *k8sstore.MemoryStore
	paths    *k8sstore.MemoryStore
}

// Two map roots over two independent groups, plus a general file that reads
// nothing incremental and a map that records an effect.
func newRootReuseFixture(t *testing.T) *rootReuseFixture {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"hosts": {
				APIVersion: "example.test/v1", Resources: "hosts",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
			"paths": {
				APIVersion: "example.test/v1", Resources: "paths",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"host-lines": {
				Name: "host-lines", Requires: []string{"hosts"},
				Incremental: &config.IncrementalTemplate{Source: "hosts", Group: "hosts"},
				Template:    `{{ item | dig_string("", "metadata", "name") }} {{ item | dig_string("", "spec", "value") }}` + "\n",
			},
			"path-lines": {
				Name: "path-lines", Requires: []string{"paths"},
				Incremental: &config.IncrementalTemplate{Source: "paths", Group: "paths"},
				Template:    `{{ item | dig_string("", "metadata", "name") }} {{ item | dig_string("", "spec", "value") }}` + "\n",
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `hosts:
{{ render "host-lines" }}paths:
{{ render "path-lines" }}`},
		Maps: map[string]config.MapFile{
			"hosts.map":  {Template: `{{ render "host-lines" }}`},
			"paths.map":  {Template: `{{ render "path-lines" }}`},
			"status.map": {Template: `{{ render "path-lines" }}{% statusPatch(resources.hosts.GetSingle("default", "h2"), map[string]any{"rendered": map[string]any{"value": "yes"}}) %}`},
		},
		Files: map[string]config.GeneralFile{
			"static.txt": {Template: `constant`},
		},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	_, logger := testutil.NewTestBusAndLogger()
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: logger})
	require.NotNil(t, service.exactCycleProgram)
	hosts := k8sstore.NewMemoryStore(2)
	paths := k8sstore.NewMemoryStore(2)
	for _, name := range []string{"h1", "h2"} {
		require.NoError(t, hosts.Add(
			incrementalTestResource("default", name, map[string]any{"value": "v1"}), []string{"default", name},
		))
	}
	require.NoError(t, paths.Add(
		incrementalTestResource("default", "p1", map[string]any{"value": "v1"}), []string{"default", "p1"},
	))
	return &rootReuseFixture{
		cfg: cfg, service: service, hosts: hosts, paths: paths,
		provider: stores.NewRealStoreProvider(map[string]stores.Store{"hosts": hosts, "paths": paths}),
	}
}

func (f *rootReuseFixture) render(t *testing.T) *RenderResult {
	t.Helper()
	result, err := f.service.Render(t.Context(), f.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, f.service)
	return result
}

func auxContents(t *testing.T, result *RenderResult) map[string]string {
	t.Helper()
	files, err := result.MaterializeAuxiliaryFiles()
	require.NoError(t, err)
	contents := map[string]string{}
	for _, file := range files.MapFiles {
		contents[file.Path] = file.Content
	}
	for _, file := range files.GeneralFiles {
		contents[file.Path] = file.Content
	}
	return contents
}

func TestAuxiliaryRootsReuseTheirOutputWhenTheyObservedNothingNew(t *testing.T) {
	fixture := newRootReuseFixture(t)
	fixture.render(t)
	warm := fixture.render(t)
	require.Equal(t, "replay", warm.CacheState)
	require.Zero(t, warm.RootsReused, "a replayed cycle executes no root at all")

	require.NoError(t, fixture.hosts.Update(
		incrementalTestResource("default", "h1", map[string]any{"value": "v2"}), []string{"default", "h1"},
	))
	changedHost := fixture.render(t)
	// paths.map observed the same path lines, static.txt observes nothing;
	// hosts.map observed new host lines, and status.map records an effect.
	assert.Equal(t, 2, changedHost.RootsReused)
	assert.Contains(t, auxContents(t, changedHost)["hosts.map"], "h1 v2")

	require.NoError(t, fixture.paths.Update(
		incrementalTestResource("default", "p1", map[string]any{"value": "v2"}), []string{"default", "p1"},
	))
	changedPath := fixture.render(t)
	assert.Equal(t, 2, changedPath.RootsReused)
	contents := auxContents(t, changedPath)
	assert.Contains(t, contents["paths.map"], "p1 v2")
	assert.Contains(t, contents["hosts.map"], "h1 v2")

	oracle := newRootReuseFixture(t)
	require.NoError(t, oracle.hosts.Update(
		incrementalTestResource("default", "h1", map[string]any{"value": "v2"}), []string{"default", "h1"},
	))
	require.NoError(t, oracle.paths.Update(
		incrementalTestResource("default", "p1", map[string]any{"value": "v2"}), []string{"default", "p1"},
	))
	cold := oracle.render(t)
	assert.Equal(t, cold.HAProxyConfig, changedPath.HAProxyConfig)
	assert.Equal(t, auxContents(t, cold), contents)
}

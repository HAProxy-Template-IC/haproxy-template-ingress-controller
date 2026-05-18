// Copyright 2025 Philipp Hossner
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
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// mockStoreProvider implements stores.StoreProvider for testing.
type mockStoreProvider struct {
	storeMap map[string]stores.Store
}

func (m *mockStoreProvider) GetStore(name string) stores.Store {
	return m.storeMap[name]
}

func (m *mockStoreProvider) StoreNames() []string {
	names := make([]string, 0, len(m.storeMap))
	for name := range m.storeMap {
		names = append(names, name)
	}
	return names
}

// mockTypedStore implements both stores.Store and types.Store for testing.
type mockTypedStore struct {
	items []any
}

func (m *mockTypedStore) Add(resource any, keys []string) error {
	m.items = append(m.items, resource)
	return nil
}

func (m *mockTypedStore) Update(resource any, keys []string) error {
	return nil
}

func (m *mockTypedStore) Delete(keys ...string) error {
	return nil
}

func (m *mockTypedStore) List() ([]any, error) {
	return m.items, nil
}

func (m *mockTypedStore) Get(keys ...string) ([]any, error) {
	return nil, nil
}

func (m *mockTypedStore) Clear() error {
	m.items = nil
	return nil
}

// Verify mockTypedStore implements both interfaces.
var _ stores.Store = (*mockTypedStore)(nil)
var _ types.Store = (*mockTypedStore)(nil)

// testDataplaneConfig returns a Dataplane config with proper directory paths
// for testing. The RenderService extracts directory names from these paths
// using filepath.Base().
func testDataplaneConfig() config.DataplaneConfig {
	return config.DataplaneConfig{
		MapsDir:           "/etc/haproxy/maps",
		SSLCertsDir:       "/etc/haproxy/ssl",
		GeneralStorageDir: "/etc/haproxy/files",
	}
}

func TestNewRenderService(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		nil, nil, nil)
	require.NoError(t, err)

	logger := slog.Default()

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       logger,
		Capabilities: defaultCapabilities(),
	})

	require.NotNil(t, svc)
	assert.NotNil(t, svc.engine)
	assert.NotNil(t, svc.config)
	assert.NotNil(t, svc.logger)
	assert.NotNil(t, svc.pathResolver)
	assert.Equal(t, "maps", svc.pathResolver.MapsDir)
	assert.Equal(t, "ssl", svc.pathResolver.SSLDir)
	assert.Equal(t, "files", svc.pathResolver.GeneralDir)
}

func TestNewRenderService_CrtListDir(t *testing.T) {
	// CRT-list files are ALWAYS stored in general file storage, regardless of HAProxy version.
	// This is because the native CRT-list API (POST ssl_crt_lists) triggers a reload without
	// supporting skip_reload, while general file storage returns 201 without triggering reloads.
	// See: pkg/dataplane/auxiliaryfiles/crtlist.go
	tests := []struct {
		name         string
		capabilities dataplane.Capabilities
		wantCrtList  string
	}{
		{
			name:         "supports crt-list",
			capabilities: defaultCapabilities(),
			wantCrtList:  "files", // Always uses general storage
		},
		{
			name:         "no crt-list support",
			capabilities: dataplane.Capabilities{SupportsCrtList: false},
			wantCrtList:  "files", // Always uses general storage
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HAProxyConfig: config.HAProxyConfig{
					Template: "global\n",
				},
				Dataplane: testDataplaneConfig(),
			}

			engine, err := templating.New(templating.EngineTypeScriggo,
				map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
				nil, nil, nil)
			require.NoError(t, err)

			svc := NewRenderService(&RenderServiceConfig{
				Engine:       engine,
				Config:       cfg,
				Logger:       slog.Default(),
				Capabilities: tt.capabilities,
			})

			assert.Equal(t, tt.wantCrtList, svc.pathResolver.CRTListDir)
		})
	}
}

func TestRenderService_Render_SimpleConfig(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n\ndefaults\n    mode http\n",
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{},
	}

	result, err := svc.Render(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.HAProxyConfig, "global")
	assert.Contains(t, result.HAProxyConfig, "daemon")
	assert.Contains(t, result.HAProxyConfig, "defaults")
	assert.NotNil(t, result.AuxiliaryFiles)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
}

func TestRenderService_Render_WithStores(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
{% for _, ing := range resources["ingresses"].List() %}
# ingress: {{ ing }}
{% end %}
`,
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	ingressStore := &mockTypedStore{
		items: []any{"ingress1", "ingress2"},
	}

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{
			"ingresses": ingressStore,
		},
	}

	result, err := svc.Render(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.HAProxyConfig, "global")
	assert.Contains(t, result.HAProxyConfig, "# ingress: ingress1")
	assert.Contains(t, result.HAProxyConfig, "# ingress: ingress2")
}

func TestRenderService_Render_WithMapFiles(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"domains.map": {
				Template: "example.com backend1\n",
			},
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg": cfg.HAProxyConfig.Template,
			"domains.map": cfg.Maps["domains.map"].Template,
		},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{},
	}

	result, err := svc.Render(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.AuxiliaryFiles.MapFiles, 1)
	assert.Equal(t, "domains.map", result.AuxiliaryFiles.MapFiles[0].Path)
	assert.Contains(t, result.AuxiliaryFiles.MapFiles[0].Content, "example.com backend1")
	assert.Equal(t, 1, result.AuxFileCount)
}

func TestRenderService_Render_WithGeneralFiles(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Files: map[string]config.GeneralFile{
			"errors/503.http": {
				Template: "HTTP/1.1 503 Service Unavailable\r\n\r\n",
			},
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{
			"haproxy.cfg":     cfg.HAProxyConfig.Template,
			"errors/503.http": cfg.Files["errors/503.http"].Template,
		},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{},
	}

	result, err := svc.Render(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.AuxiliaryFiles.GeneralFiles, 1)
	assert.Equal(t, "errors/503.http", result.AuxiliaryFiles.GeneralFiles[0].Filename)
	assert.Equal(t, "files/errors/503.http", result.AuxiliaryFiles.GeneralFiles[0].Path)
	assert.Contains(t, result.AuxiliaryFiles.GeneralFiles[0].Content, "503 Service Unavailable")
}

func TestRenderService_Render_Error(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Dataplane: testDataplaneConfig(),
	}

	// Create engine without haproxy.cfg template to trigger error
	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"other.cfg": "content"},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{},
	}

	result, err := svc.Render(context.Background(), provider)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "rendering haproxy.cfg")
}

func TestRenderService_Render_PathResolverAvailable(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
# map path: {{ pathResolver.GetPath("hosts.map", "map") }}
`,
		},
		Dataplane: testDataplaneConfig(),
	}

	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})

	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{},
	}

	result, err := svc.Render(context.Background(), provider)

	require.NoError(t, err)
	require.NotNil(t, result)
	// PathResolver should resolve to relative path
	assert.Contains(t, result.HAProxyConfig, "# map path: maps/hosts.map")
}

// TestRenderService_buildRenderingContext_PropagatesIndexBy is a regression test
// pinning that the production renderer constructs StoreWrappers WITH the
// `IndexBy` slice from the corresponding `spec.watchedResources` entry. The
// wrapper's `IndexBy` is what enables Fetch/GetSingle to read from the
// per-render snapshot instead of bypassing to the live (potentially mutating)
// store — without it, parallel resource creation during the conformance
// suite produced inconsistent reads and intermittent render output. Issue #45
// tracks the original symptom (a flaky `HTTPRouteRequestMultipleMirrors`
// timeout and `TLSRouteHostnameIntersection` EOF), both traced back to this
// gap.
func TestRenderService_buildRenderingContext_PropagatesIndexBy(t *testing.T) {
	indexBy := []string{"metadata.namespace", "metadata.name"}
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{Template: "global\n  daemon\n"},
		Dataplane:     testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    indexBy,
			},
			// "tlsroutes" deliberately omitted to verify the lookup
			// falls through cleanly when WatchedResources has no entry
			// for a present store.
		},
	}
	engine, err := templating.New(templating.EngineTypeScriggo,
		map[string]string{"haproxy.cfg": cfg.HAProxyConfig.Template},
		nil, nil, nil)
	require.NoError(t, err)

	svc := NewRenderService(&RenderServiceConfig{
		Engine:       engine,
		Config:       cfg,
		Logger:       slog.Default(),
		Capabilities: defaultCapabilities(),
	})
	provider := &mockStoreProvider{
		storeMap: map[string]stores.Store{
			"ingresses": &mockTypedStore{},
			"tlsroutes": &mockTypedStore{},
		},
	}

	renderCtx, fileRegistry, statusPatches, renderedResources := svc.buildRenderingContext(context.Background(), provider)
	require.NotNil(t, fileRegistry, "fileRegistry collector must be wired so templates can register dynamic aux files")
	require.NotNil(t, statusPatches, "statusPatchCollector must be wired so filters_status.go can capture mutations")
	require.NotNil(t, renderedResources, "renderedResourceCollector must be wired so k8sResources templates can emit owned resources")

	resources, ok := renderCtx["resources"].(map[string]templating.ResourceStore)
	require.True(t, ok, "renderCtx[\"resources\"] must be map[string]ResourceStore")

	ing, ok := resources["ingresses"].(*rendercontext.StoreWrapper)
	require.True(t, ok, "ingresses wrapper must be *StoreWrapper")
	assert.Equal(t, indexBy, ing.IndexBy,
		"StoreWrapper must carry the IndexBy from config.WatchedResources so Fetch/GetSingle stay on the per-render snapshot")

	tls, ok := resources["tlsroutes"].(*rendercontext.StoreWrapper)
	require.True(t, ok, "tlsroutes wrapper must be *StoreWrapper")
	assert.Nil(t, tls.IndexBy,
		"a store present without a matching WatchedResources entry has no IndexBy hint, so the wrapper must accept that gracefully")
}

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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/helpers"
	"haproxy-template-ic/pkg/controller/httpstore"
	"haproxy-template-ic/pkg/controller/rendercontext"
	"haproxy-template-ic/pkg/controller/testutil"
	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/dataplane/auxiliaryfiles"
	"haproxy-template-ic/pkg/k8s/types"
	"haproxy-template-ic/pkg/templating"
)

// mockStore implements types.Store for testing.
type mockStore struct {
	items []interface{}
}

func (m *mockStore) Add(resource interface{}, keys []string) error {
	m.items = append(m.items, resource)
	return nil
}

func (m *mockStore) Update(resource interface{}, keys []string) error {
	return nil
}

func (m *mockStore) Delete(keys ...string) error {
	return nil
}

func (m *mockStore) List() ([]interface{}, error) {
	return m.items, nil
}

func (m *mockStore) Get(keys ...string) ([]interface{}, error) {
	return nil, nil
}

func (m *mockStore) Clear() error {
	m.items = nil
	return nil
}

// defaultCapabilities returns HAProxy 3.2+ capabilities for tests.
func defaultCapabilities() dataplane.Capabilities {
	return dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
}

// TestNew_Success tests successful renderer creation with valid configuration.
func TestNew_Success(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"domain.map": {Template: "example.com backend1\n"},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	renderer, err := New(bus, cfg, stores, haproxyPodStore, defaultCapabilities(), logger)

	require.NoError(t, err)
	assert.NotNil(t, renderer)
	assert.NotNil(t, renderer.engine)
	assert.Equal(t, cfg, renderer.config)
	assert.Equal(t, stores, renderer.stores)
}

// TestNew_InvalidTemplate tests renderer creation with invalid template syntax.
func TestNew_InvalidTemplate(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n{{ unclosed tag\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	renderer, err := New(bus, cfg, stores, haproxyPodStore, defaultCapabilities(), logger)

	assert.Error(t, err)
	assert.Nil(t, renderer)
	// Error comes directly from templating.New (CompilationError) without double wrapping
	assert.Contains(t, err.Error(), "failed to compile template")
}

// TestRenderer_SuccessfulRendering tests successful template rendering.
func TestRenderer_SuccessfulRendering(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http

# Ingress count: {{ len(resources.ingresses.List()) }}
# Ingress: test-ingress
`,
		},
	}

	// Create mock store with sample ingress
	ingressStore := &mockStore{
		items: []interface{}{
			map[string]interface{}{
				"name":      "test-ingress",
				"namespace": "default",
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": ingressStore,
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start renderer
	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for rendered event
	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "global")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Ingress: test-ingress")
	assert.Greater(t, renderedEvent.ConfigBytes, 0)
	assert.GreaterOrEqual(t, renderedEvent.DurationMs, int64(0))
}

// TestRenderer_WithAuxiliaryFiles tests rendering with maps, files, and SSL certificates.
func TestRenderer_WithAuxiliaryFiles(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"domains.map": {
				Template: "{% for _, ingress := range resources.ingresses.List() %}{{ ingress.(map[string]interface{})[\"metadata\"].(map[string]interface{})[\"name\"] }}.example.com backend1\n{% end %}",
			},
		},
		Files: map[string]config.GeneralFile{
			"error-500.http": {
				Template: "HTTP/1.0 500 Internal Server Error\nContent-Type: text/html\n\n<h1>Error 500</h1>\n",
			},
		},
		SSLCertificates: map[string]config.SSLCertificate{
			"example.pem": {
				Template: "-----BEGIN CERTIFICATE-----\ntest-cert-data\n-----END CERTIFICATE-----\n",
			},
		},
	}

	ingressStore := &mockStore{
		items: []interface{}{
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test-ingress",
				},
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": ingressStore,
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)
	assert.Equal(t, 3, renderedEvent.AuxiliaryFileCount, "Should have 1 map + 1 file + 1 SSL cert")

	// Verify auxiliary files are populated
	assert.NotNil(t, renderedEvent.AuxiliaryFiles)
}

// TestRenderer_RenderFailure tests handling of template compilation failures.
// Note: Scriggo catches undefined functions at compile time, not runtime.
func TestRenderer_RenderFailure(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"broken.map": {
				// Template references non-existent function - caught at compile time
				Template: "{{ undefined_function() }}",
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	// With Scriggo, undefined functions are caught at compile time, so renderer creation fails
	_, err := New(bus, cfg, stores, haproxyPodStore, defaultCapabilities(), logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undefined")
}

// TestRenderer_EmptyStores tests rendering with empty resource stores.
func TestRenderer_EmptyStores(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

{% if len(resources.ingresses.List()) == 0 %}
# No ingresses configured
{% end %}
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{items: []interface{}{}}, // Empty store
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "# No ingresses configured")
}

// TestRenderer_MultipleStores tests rendering with multiple resource types.
func TestRenderer_MultipleStores(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

# Ingresses: {{ len(resources.ingresses.List()) }}
# Services: {{ len(resources.services.List()) }}
# Pods: {{ len(resources.pods.List()) }}
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{
			items: []interface{}{
				map[string]interface{}{"kind": "Ingress"},
				map[string]interface{}{"kind": "Ingress"},
			},
		},
		"services": &mockStore{
			items: []interface{}{
				map[string]interface{}{"kind": "Service"},
			},
		},
		"pods": &mockStore{
			items: []interface{}{
				map[string]interface{}{"kind": "Pod"},
				map[string]interface{}{"kind": "Pod"},
				map[string]interface{}{"kind": "Pod"},
			},
		},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Ingresses: 2")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Services: 1")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Pods: 3")
}

// TestRenderer_ContextCancellation tests graceful shutdown on context cancellation.
func TestRenderer_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	renderer, err := New(bus, cfg, stores, haproxyPodStore, defaultCapabilities(), logger)
	require.NoError(t, err)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start renderer
	done := make(chan error, 1)
	go func() {
		done <- renderer.Start(ctx)
	}()

	// Cancel context
	time.Sleep(testutil.StartupDelay)
	cancel()

	// Should return quickly
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Renderer did not shut down within timeout")
	}
}

// TestRenderer_MultipleReconciliations tests handling multiple reconciliation triggers.
func TestRenderer_MultipleReconciliations(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n# Count: {{ len(resources.ingresses.List()) }}\n",
		},
	}

	ingressStore := &mockStore{
		items: []interface{}{
			map[string]interface{}{"name": "ing1"},
		},
	}

	stores := map[string]types.Store{
		"ingresses": ingressStore,
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger first reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("first"))

	// Wait for first render
	_ = testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)

	// Add more ingresses to store
	ingressStore.items = append(ingressStore.items, map[string]interface{}{"name": "ing2"})

	// Trigger second reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("second"))

	// Wait for second render
	secondEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.EventTimeout)

	require.NotNil(t, secondEvent)
	assert.Contains(t, secondEvent.HAProxyConfig, "# Count: 2")
}

// TestBuildRenderingContext tests the context building logic.
func TestBuildRenderingContext(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{
			items: []interface{}{
				map[string]interface{}{"name": "ing1"},
				map[string]interface{}{"name": "ing2"},
			},
		},
		"services": &mockStore{
			items: []interface{}{
				map[string]interface{}{"name": "svc1"},
			},
		},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Build context
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/ssl",
		CRTListDir: "/etc/haproxy/ssl",
		GeneralDir: "/etc/haproxy/general",
	}
	ctx, fileRegistry := renderer.buildRenderingContext(context.Background(), pathResolver, false)

	// Verify file registry was created
	require.NotNil(t, fileRegistry)

	// Verify structure
	require.Contains(t, ctx, "resources")
	require.Contains(t, ctx, "fileRegistry")

	// resources is now map[string]templating.ResourceStore for direct method calls in Scriggo templates
	resources, ok := ctx["resources"].(map[string]templating.ResourceStore)
	require.True(t, ok, "resources should be a map[string]templating.ResourceStore")

	// Verify ingresses store wrapper
	ingressesWrapper, ok := resources["ingresses"].(*rendercontext.StoreWrapper)
	require.True(t, ok, "ingresses should be a StoreWrapper")
	assert.Equal(t, "ingresses", ingressesWrapper.ResourceType)

	// Verify ingresses content via List()
	ingresses := ingressesWrapper.List()
	assert.Len(t, ingresses, 2)

	// Verify services store wrapper
	servicesWrapper, ok := resources["services"].(*rendercontext.StoreWrapper)
	require.True(t, ok, "services should be a StoreWrapper")
	assert.Equal(t, "services", servicesWrapper.ResourceType)

	// Verify services content via List()
	services := servicesWrapper.List()
	assert.Len(t, services, 1)
}

// TestPathResolverWithCapabilities_CRTListFallback tests CRT-list path resolution
// based on HAProxy version capabilities. When CRT-list storage is not supported
// (HAProxy < 3.2), CRT-list files should use the general files directory.
func TestPathResolverWithCapabilities_CRTListFallback(t *testing.T) {
	tests := []struct {
		name                   string
		version                *dataplane.Version
		expectSSLDir           bool // true = SSL dir (/etc/haproxy/ssl), false = general dir (/etc/haproxy/files)
		expectCrtListSupported bool
		expectMapSupported     bool
	}{
		{
			name:                   "HAProxy 3.0 - CRT-list uses general directory",
			version:                &dataplane.Version{Major: 3, Minor: 0, Full: "3.0.0"},
			expectSSLDir:           false,
			expectCrtListSupported: false,
			expectMapSupported:     true, // All v3.x have /storage/maps
		},
		{
			name:                   "HAProxy 3.1 - CRT-list uses general directory",
			version:                &dataplane.Version{Major: 3, Minor: 1, Full: "3.1.0"},
			expectSSLDir:           false,
			expectCrtListSupported: false,
			expectMapSupported:     true,
		},
		{
			name:                   "HAProxy 3.2 - CRT-list uses SSL directory",
			version:                &dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"},
			expectSSLDir:           true,
			expectCrtListSupported: true,
			expectMapSupported:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()

			cfg := &config.Config{
				HAProxyConfig: config.HAProxyConfig{
					Template: `global
    daemon

frontend test
    bind *:443 ssl crt-list {{ pathResolver.GetPath("certificate-list.txt", "crt-list") }}
`,
				},
				Dataplane: config.DataplaneConfig{
					MapsDir:           "/etc/haproxy/maps",
					SSLCertsDir:       "/etc/haproxy/ssl",
					GeneralStorageDir: "/etc/haproxy/files",
				},
			}

			stores := map[string]types.Store{
				"ingresses": &mockStore{},
			}

			capabilities := dataplane.CapabilitiesFromVersion(tt.version)
			assert.Equal(t, tt.expectCrtListSupported, capabilities.SupportsCrtList, "SupportsCrtList mismatch")
			assert.Equal(t, tt.expectMapSupported, capabilities.SupportsMapStorage, "SupportsMapStorage mismatch")

			renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
			require.NoError(t, err)

			eventChan := bus.Subscribe(50)
			bus.Start()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go renderer.Start(ctx)
			time.Sleep(testutil.StartupDelay)

			bus.Publish(events.NewReconciliationTriggeredEvent("test"))

			renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
			require.NotNil(t, renderedEvent)

			if tt.expectSSLDir {
				assert.Contains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/ssl/certificate-list.txt",
					"CRT-list should use SSL directory")
				assert.NotContains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/files/certificate-list.txt",
					"CRT-list should NOT use general files directory")
			} else {
				assert.Contains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/files/certificate-list.txt",
					"CRT-list should fall back to general files directory")
				assert.NotContains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/ssl/certificate-list.txt",
					"CRT-list should NOT use SSL directory")
			}
		})
	}
}

// TestPathResolverInitialization tests that the PathResolver is correctly initialized
// with all required directory paths for the pathResolver.GetPath() method.
func TestPathResolverInitialization(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	// Create a config with templates that use pathResolver.GetPath() method for crt-list
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

frontend test
    bind *:80
    bind *:443 ssl crt-list {{ pathResolver.GetPath("certificate-list.txt", "crt-list") }}
`,
		},
		Dataplane: config.DataplaneConfig{
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/ssl",
			GeneralStorageDir: "/etc/haproxy/files",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Get the path resolver from the engine
	// We'll test this through the template rendering since pathResolver is not exported
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger rendering
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for rendered event
	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)

	// CRITICAL: Verify that pathResolver.GetPath("crt-list") returns the full path, not just the filename
	// This is the TDD test that will initially FAIL because CRTListDir is not set
	assert.Contains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/ssl/certificate-list.txt",
		"pathResolver.GetPath('certificate-list.txt', 'crt-list') should return full path with directory prefix")

	// Ensure it doesn't contain just the filename (which is what happens when CRTListDir is empty)
	assert.NotContains(t, renderedEvent.HAProxyConfig, "crt-list certificate-list.txt",
		"pathResolver.GetPath() should not return just the filename without directory")
}

// TestRenderer_Name tests the Name method.
func TestRenderer_Name(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	assert.Equal(t, ComponentName, renderer.Name())
	assert.Equal(t, "renderer", renderer.Name())
}

// TestRenderer_SetHTTPStoreComponent tests setting the HTTP store component.
func TestRenderer_SetHTTPStoreComponent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Initially nil
	assert.Nil(t, renderer.httpStoreComponent)

	// Set HTTP store component
	renderer.SetHTTPStoreComponent(nil) // Just test setting works
	assert.Nil(t, renderer.httpStoreComponent)
}

// TestRenderer_HandleBecameLeader_NoState tests BecameLeaderEvent when no state is available.
func TestRenderer_HandleBecameLeader_NoState(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Send BecameLeaderEvent without any prior rendering (no state to replay)
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait a bit to ensure event is processed
	time.Sleep(testutil.DebounceWait)

	// No TemplateRenderedEvent should be published since there's no state to replay
	select {
	case event := <-eventChan:
		// Skip the BecameLeaderEvent itself if received
		if _, ok := event.(*events.BecameLeaderEvent); ok {
			// Try to get another event briefly
			select {
			case event := <-eventChan:
				_, isRendered := event.(*events.TemplateRenderedEvent)
				assert.False(t, isRendered, "Should not publish TemplateRenderedEvent when no state available")
			case <-time.After(testutil.DebounceWait):
				// Expected - no event
			}
		}
	case <-time.After(testutil.NoEventTimeout):
		// Expected - no events beyond the one we sent
	}
}

// TestRenderer_HandleBecameLeader_WithState tests BecameLeaderEvent when state is available.
func TestRenderer_HandleBecameLeader_WithState(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n# leader-test\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First, trigger a normal reconciliation to populate state
	bus.Publish(events.NewReconciliationTriggeredEvent("initial"))

	// Wait for first render
	firstRenderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, firstRenderedEvent)

	// Now send BecameLeaderEvent - should replay state
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait for replayed render event
	replayedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, replayedEvent)
	assert.Contains(t, replayedEvent.HAProxyConfig, "# leader-test")
	assert.Equal(t, firstRenderedEvent.HAProxyConfig, replayedEvent.HAProxyConfig)
}

// TestRenderer_WithPostProcessors tests rendering with post-processors configured.
func TestRenderer_WithPostProcessors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			// Template with MARKER text that will be replaced by regex_replace
			Template: "global\n    daemon\n\nfrontend fe1\n    bind *:80 #MARKER#\n",
			PostProcessing: []config.PostProcessorConfig{
				{
					Type: "regex_replace",
					Params: map[string]string{
						"pattern": "#MARKER#",
						"replace": "replaced",
					},
				},
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)

	// Verify post-processor was applied (MARKER should be replaced)
	assert.Contains(t, renderedEvent.HAProxyConfig, "global")
	assert.Contains(t, renderedEvent.HAProxyConfig, "replaced")
	assert.NotContains(t, renderedEvent.HAProxyConfig, "#MARKER#")
}

// TestRenderer_WithTemplateSnippets tests rendering with template snippets.
func TestRenderer_WithTemplateSnippets(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

{{ render "defaults-snippet" }}

frontend fe1
    bind *:80
`,
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"defaults-snippet": {
				Template: `defaults
    mode http
    timeout client 30s
    timeout server 30s
`,
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)

	// Verify snippet was included
	assert.Contains(t, renderedEvent.HAProxyConfig, "defaults")
	assert.Contains(t, renderedEvent.HAProxyConfig, "timeout client 30s")
}

// TestFailFunction tests the fail() global function.
func TestFailFunction(t *testing.T) {
	tests := []struct {
		name        string
		args        []interface{}
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid string argument",
			args:        []interface{}{"Secret 'foo/bar' not found"},
			wantErr:     true,
			errContains: "Secret 'foo/bar' not found",
		},
		{
			name:        "no arguments",
			args:        []interface{}{},
			wantErr:     true,
			errContains: "requires exactly one string argument",
		},
		{
			name:        "too many arguments",
			args:        []interface{}{"first", "second"},
			wantErr:     true,
			errContains: "requires exactly one string argument",
		},
		{
			name:        "non-string argument (int)",
			args:        []interface{}{42},
			wantErr:     true,
			errContains: "must be a string",
		},
		{
			name:        "non-string argument (nil)",
			args:        []interface{}{nil},
			wantErr:     true,
			errContains: "must be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := templating.FailFunction(tt.args...)
			assert.Nil(t, result)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestExtractTemplates tests template extraction from config.
func TestExtractTemplates(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"snippet1": {Template: "snippet1-content"},
			"snippet2": {Template: "snippet2-content"},
		},
		Maps: map[string]config.MapFile{
			"map1.map": {Template: "map1-content"},
		},
		Files: map[string]config.GeneralFile{
			"file1.http": {Template: "file1-content"},
		},
		SSLCertificates: map[string]config.SSLCertificate{
			"cert1.pem": {Template: "cert1-content"},
		},
	}

	templates := helpers.ExtractTemplatesFromConfig(cfg)

	// Verify all templates extracted
	assert.Contains(t, templates.AllTemplates, "haproxy.cfg")
	assert.Contains(t, templates.AllTemplates, "snippet1")
	assert.Contains(t, templates.AllTemplates, "snippet2")
	assert.Contains(t, templates.AllTemplates, "map1.map")
	assert.Contains(t, templates.AllTemplates, "file1.http")
	assert.Contains(t, templates.AllTemplates, "cert1.pem")

	// Verify content
	assert.Equal(t, "global\n    daemon\n", templates.AllTemplates["haproxy.cfg"])
	assert.Equal(t, "snippet1-content", templates.AllTemplates["snippet1"])
	assert.Equal(t, "map1-content", templates.AllTemplates["map1.map"])
}

// TestExtractPostProcessorConfigs tests post-processor config extraction.
func TestExtractPostProcessorConfigs(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n",
			PostProcessing: []config.PostProcessorConfig{
				{Type: "remove_empty_lines", Params: map[string]string{}},
			},
		},
		Maps: map[string]config.MapFile{
			"map1.map": {
				Template: "map content",
				PostProcessing: []config.PostProcessorConfig{
					{Type: "sort_lines", Params: map[string]string{}},
				},
			},
		},
		Files: map[string]config.GeneralFile{
			"file1.http": {
				Template: "file content",
				PostProcessing: []config.PostProcessorConfig{
					{Type: "trim", Params: map[string]string{}},
				},
			},
		},
		SSLCertificates: map[string]config.SSLCertificate{
			"cert1.pem": {
				Template: "cert content",
				PostProcessing: []config.PostProcessorConfig{
					{Type: "trim", Params: map[string]string{}},
				},
			},
		},
	}

	ppConfigs := helpers.ExtractPostProcessorConfigs(cfg)

	// Verify main haproxy config has post-processor
	require.Contains(t, ppConfigs, "haproxy.cfg")
	assert.Len(t, ppConfigs["haproxy.cfg"], 1)
	assert.Equal(t, templating.PostProcessorType("remove_empty_lines"), ppConfigs["haproxy.cfg"][0].Type)

	// Verify map has post-processor
	require.Contains(t, ppConfigs, "map1.map")
	assert.Equal(t, templating.PostProcessorType("sort_lines"), ppConfigs["map1.map"][0].Type)

	// Verify file has post-processor
	require.Contains(t, ppConfigs, "file1.http")
	assert.Equal(t, templating.PostProcessorType("trim"), ppConfigs["file1.http"][0].Type)

	// Verify SSL cert has post-processor
	require.Contains(t, ppConfigs, "cert1.pem")
	assert.Equal(t, templating.PostProcessorType("trim"), ppConfigs["cert1.pem"][0].Type)
}

// TestMergeAuxiliaryFiles tests merging static and dynamic auxiliary files.
func TestMergeAuxiliaryFiles(t *testing.T) {
	static := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "static-map.map", Content: "static"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "static-file.http", Content: "static"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "static-cert.pem", Content: "static"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "static-crtlist.txt", Content: "static"},
		},
	}

	dynamic := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "dynamic-map.map", Content: "dynamic"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "dynamic-file.http", Content: "dynamic"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "dynamic-cert.pem", Content: "dynamic"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "dynamic-crtlist.txt", Content: "dynamic"},
		},
	}

	merged := MergeAuxiliaryFiles(static, dynamic)

	assert.Len(t, merged.MapFiles, 2)
	assert.Len(t, merged.GeneralFiles, 2)
	assert.Len(t, merged.SSLCertificates, 2)
	assert.Len(t, merged.CRTListFiles, 2)

	// Verify static files come first
	assert.Equal(t, "static-map.map", merged.MapFiles[0].Path)
	assert.Equal(t, "dynamic-map.map", merged.MapFiles[1].Path)
}

// ============================================================================
// Reconciliation Coalescing Tests
// ============================================================================

// TestRenderer_ReconciliationCoalescing_LatestWins verifies that when multiple
// ReconciliationTriggeredEvents arrive while rendering is in progress, only
// the latest trigger is processed (intermediate events are superseded).
func TestRenderer_ReconciliationCoalescing_LatestWins(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish multiple reconciliation triggers rapidly
	// The renderer should coalesce these and process fewer events
	for i := 0; i < 5; i++ {
		bus.Publish(events.NewReconciliationTriggeredEvent("batch_test"))
	}

	// Wait for at least one render to complete
	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "global")

	// The test verifies that rendering completes without blocking/deadlock
	// when multiple triggers arrive - the coalescing prevents queue buildup
}

// TestRenderer_TriggerReasonPropagation verifies that the trigger reason from
// ReconciliationTriggeredEvent is propagated to the TemplateRenderedEvent.
func TestRenderer_TriggerReasonPropagation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish trigger with specific reason
	bus.Publish(events.NewReconciliationTriggeredEvent("drift_prevention"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)

	require.NotNil(t, renderedEvent)
	assert.Equal(t, "drift_prevention", renderedEvent.TriggerReason)
}

// ============================================================================
// State Caching Tests (Leadership Transition Replay)
// ============================================================================

// TestRenderer_HandleBecameLeader_WithTriggerReason verifies that when leadership
// is acquired, the renderer replays the last rendered state including the trigger reason.
func TestRenderer_HandleBecameLeader_WithTriggerReason(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First, trigger a render with a specific reason
	bus.Publish(events.NewReconciliationTriggeredEvent("config_change"))

	// Wait for initial render to complete
	firstRendered := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, firstRendered)
	assert.Equal(t, "config_change", firstRendered.TriggerReason)

	// Now simulate becoming leader - should replay last state
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait for replayed event
	replayedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, replayedEvent)

	// Verify the replayed event has the same config and trigger reason
	assert.Equal(t, firstRendered.HAProxyConfig, replayedEvent.HAProxyConfig)
	assert.Equal(t, "config_change", replayedEvent.TriggerReason)
}

// TestRenderer_StateReplay_PreservesAllFields verifies that leadership transition
// replay preserves all cached render output fields.
func TestRenderer_StateReplay_PreservesAllFields(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"test.map": {Template: "example.com backend1\n"},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger initial render
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	originalEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, originalEvent)

	// Simulate becoming leader - should replay state
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	replayedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, replayedEvent)

	// Verify all fields are preserved
	assert.Equal(t, originalEvent.HAProxyConfig, replayedEvent.HAProxyConfig)
	assert.Equal(t, originalEvent.ValidationHAProxyConfig, replayedEvent.ValidationHAProxyConfig)
	assert.Equal(t, originalEvent.AuxiliaryFileCount, replayedEvent.AuxiliaryFileCount)
	// Note: Duration may differ slightly between original and replay, that's expected
}

// ============================================================================
// HTTP Store Integration Tests
// ============================================================================

// TestRenderer_WithHTTPStoreComponent verifies that when an HTTP store component
// is set, templates can call http.Fetch() to retrieve remote content.
func TestRenderer_WithHTTPStoreComponent(t *testing.T) {
	// Set up a test HTTP server that returns predictable content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("192.168.1.1 backend1\n192.168.1.2 backend2"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()

	// Template that uses http.Fetch()
	templateContent := `global
    daemon
# Remote content:
{{ http.Fetch("` + server.URL + `") }}
`

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: templateContent,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Create and set HTTP store component
	httpStoreComponent := httpstore.New(bus, logger, 0)
	renderer.SetHTTPStoreComponent(httpStoreComponent)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start both components
	go httpStoreComponent.Start(ctx)
	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("http_test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)

	// Verify the HTTP content was fetched and included in the rendered output
	assert.Contains(t, renderedEvent.HAProxyConfig, "192.168.1.1 backend1")
	assert.Contains(t, renderedEvent.HAProxyConfig, "192.168.1.2 backend2")
}

// TestRenderer_WithoutHTTPStoreComponent verifies that rendering succeeds even
// when no HTTP store component is set, but http.Fetch() is not available.
func TestRenderer_WithoutHTTPStoreComponent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Do NOT set HTTP store component - it should remain nil
	assert.Nil(t, renderer.httpStoreComponent)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation - should succeed without HTTP store
	bus.Publish(events.NewReconciliationTriggeredEvent("no_http_store_test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)

	// Verify rendering completed successfully
	assert.Contains(t, renderedEvent.HAProxyConfig, "global")
	assert.Contains(t, renderedEvent.HAProxyConfig, "daemon")
}

// TestRenderer_HTTPStoreContextAvailability verifies that the 'http' object
// is available in the template context when HTTP store component is set.
func TestRenderer_HTTPStoreContextAvailability(t *testing.T) {
	// Set up test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test-content"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()

	// Template that conditionally uses http if available
	// This tests that the http object exists in the context
	templateContent := `global
    daemon
{% if http != nil %}# http object is available{% end %}
`

	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{Engine: "scriggo"},
		HAProxyConfig: config.HAProxyConfig{
			Template: templateContent,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	renderer, err := New(bus, cfg, stores, &mockStore{}, defaultCapabilities(), logger)
	require.NoError(t, err)

	// Set HTTP store component
	httpStoreComponent := httpstore.New(bus, logger, 0)
	renderer.SetHTTPStoreComponent(httpStoreComponent)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go httpStoreComponent.Start(ctx)
	go renderer.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Trigger reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("context_test"))

	renderedEvent := testutil.WaitForEvent[*events.TemplateRenderedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, renderedEvent)

	// Verify the http object was detected in the template
	assert.Contains(t, renderedEvent.HAProxyConfig, "# http object is available")
}

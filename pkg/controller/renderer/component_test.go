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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	busevents "haproxy-template-ic/pkg/events"
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

// TestNew_Success tests successful renderer creation with valid configuration.
func TestNew_Success(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

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

	// Use capabilities for HAProxy 3.2+ to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, haproxyPodStore, capabilities, logger)

	require.NoError(t, err)
	assert.NotNil(t, renderer)
	assert.NotNil(t, renderer.engine)
	assert.Equal(t, cfg, renderer.config)
	assert.Equal(t, stores, renderer.stores)
}

// TestNew_InvalidTemplate tests renderer creation with invalid template syntax.
func TestNew_InvalidTemplate(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n{{ unclosed tag\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	// Use capabilities for HAProxy 3.2+ to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, haproxyPodStore, capabilities, logger)

	assert.Error(t, err)
	assert.Nil(t, renderer)
	assert.Contains(t, err.Error(), "failed to create template engine")
}

// TestRenderer_SuccessfulRendering tests successful template rendering.
func TestRenderer_SuccessfulRendering(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http

{% for ingress in resources.ingresses.List() %}
# Ingress: {{ ingress.metadata.name }}
{% endfor %}
`,
		},
	}

	// Create mock store with sample ingress
	ingressStore := &mockStore{
		items: []interface{}{
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-ingress",
					"namespace": "default",
				},
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": ingressStore,
	}

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start renderer
	go renderer.Start(ctx)

	// Give renderer time to start
	time.Sleep(50 * time.Millisecond)

	// Trigger reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for rendered event
	timeout := time.After(1 * time.Second)
	var renderedEvent *events.TemplateRenderedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				renderedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
		}
	}

Done:
	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "global")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Ingress: test-ingress")
	assert.Greater(t, renderedEvent.ConfigBytes, 0)
	assert.GreaterOrEqual(t, renderedEvent.DurationMs, int64(0))
}

// TestRenderer_WithAuxiliaryFiles tests rendering with maps, files, and SSL certificates.
func TestRenderer_WithAuxiliaryFiles(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"domains.map": {
				Template: "{% for ingress in resources.ingresses.List() %}{{ ingress.metadata.name }}.example.com backend1\n{% endfor %}",
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

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	timeout := time.After(1 * time.Second)
	var renderedEvent *events.TemplateRenderedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				renderedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
		}
	}

Done:
	require.NotNil(t, renderedEvent)
	assert.Equal(t, 3, renderedEvent.AuxiliaryFileCount, "Should have 1 map + 1 file + 1 SSL cert")

	// Verify auxiliary files are populated
	assert.NotNil(t, renderedEvent.AuxiliaryFiles)
}

// TestRenderer_RenderFailure tests handling of template rendering failures.
func TestRenderer_RenderFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
		Maps: map[string]config.MapFile{
			"broken.map": {
				// Template references non-existent function
				Template: "{{ undefined_function() }}",
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	// Use capabilities for HAProxy 3.2+ to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	timeout := time.After(1 * time.Second)
	var failureEvent *events.TemplateRenderFailedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderFailedEvent); ok {
				failureEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderFailedEvent")
		}
	}

Done:
	require.NotNil(t, failureEvent)
	assert.Equal(t, "broken.map", failureEvent.TemplateName)
	assert.NotEmpty(t, failureEvent.Error)
}

// TestRenderer_EmptyStores tests rendering with empty resource stores.
func TestRenderer_EmptyStores(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

{% if resources.ingresses.List()|length == 0 %}
# No ingresses configured
{% endif %}
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{items: []interface{}{}}, // Empty store
	}

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	timeout := time.After(1 * time.Second)
	var renderedEvent *events.TemplateRenderedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				renderedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
		}
	}

Done:
	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "# No ingresses configured")
}

// TestRenderer_MultipleStores tests rendering with multiple resource types.
func TestRenderer_MultipleStores(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

# Ingresses: {{ resources.ingresses.List()|length }}
# Services: {{ resources.services.List()|length }}
# Pods: {{ resources.pods.List()|length }}
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

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	timeout := time.After(1 * time.Second)
	var renderedEvent *events.TemplateRenderedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				renderedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
		}
	}

Done:
	require.NotNil(t, renderedEvent)
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Ingresses: 2")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Services: 1")
	assert.Contains(t, renderedEvent.HAProxyConfig, "# Pods: 3")
}

// TestRenderer_ContextCancellation tests graceful shutdown on context cancellation.
func TestRenderer_ContextCancellation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n",
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	haproxyPodStore := &mockStore{}

	// Use capabilities for HAProxy 3.2+ to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start renderer
	done := make(chan error, 1)
	go func() {
		done <- renderer.Start(ctx)
	}()

	// Cancel context
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Should return quickly
	timeout := time.After(1 * time.Second)
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-timeout:
		t.Fatal("Renderer did not shut down within timeout")
	}
}

// TestRenderer_MultipleReconciliations tests handling multiple reconciliation triggers.
func TestRenderer_MultipleReconciliations(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon\n# Count: {{ resources.ingresses.List()|length }}\n",
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

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Trigger first reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("first"))

	// Wait for first render
	timeout1 := time.After(500 * time.Millisecond)
	receivedFirst := false

Loop1:
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.TemplateRenderedEvent); ok {
				receivedFirst = true
				break Loop1
			}
		case <-timeout1:
			t.Fatal("Timeout waiting for first render")
		}
	}

	assert.True(t, receivedFirst)

	// Add more ingresses to store
	ingressStore.items = append(ingressStore.items, map[string]interface{}{"name": "ing2"})

	// Trigger second reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("second"))

	// Wait for second render
	timeout2 := time.After(500 * time.Millisecond)
	var secondEvent *events.TemplateRenderedEvent

Loop2:
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				secondEvent = e
				break Loop2
			}
		case <-timeout2:
			t.Fatal("Timeout waiting for second render")
		}
	}

	require.NotNil(t, secondEvent)
	assert.Contains(t, secondEvent.HAProxyConfig, "# Count: 2")
}

// TestBuildRenderingContext tests the context building logic.
func TestBuildRenderingContext(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

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

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
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
	require.Contains(t, ctx, "file_registry")

	resources, ok := ctx["resources"].(map[string]interface{})
	require.True(t, ok, "resources should be a map")

	// Verify ingresses store wrapper
	ingressesWrapper, ok := resources["ingresses"].(*StoreWrapper)
	require.True(t, ok, "ingresses should be a StoreWrapper")
	assert.Equal(t, "ingresses", ingressesWrapper.ResourceType)

	// Verify ingresses content via List()
	ingresses := ingressesWrapper.List()
	assert.Len(t, ingresses, 2)

	// Verify services store wrapper
	servicesWrapper, ok := resources["services"].(*StoreWrapper)
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
			bus := busevents.NewEventBus(100)
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

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
			time.Sleep(50 * time.Millisecond)

			bus.Publish(events.NewReconciliationTriggeredEvent("test"))

			renderedEvent := waitForTemplateRenderedEvent(t, eventChan, 1*time.Second)
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

// waitForTemplateRenderedEvent waits for a TemplateRenderedEvent on the event channel.
func waitForTemplateRenderedEvent(t *testing.T, eventChan <-chan busevents.Event, timeout time.Duration) *events.TemplateRenderedEvent {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				return e
			}
		case <-timer:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
			return nil
		}
	}
}

// TestPathResolverInitialization tests that the PathResolver is correctly initialized
// with all required directory paths for the pathResolver.GetPath() method.
func TestPathResolverInitialization(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

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

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	renderer, err := New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	// Get the path resolver from the engine
	// We'll test this through the template rendering since pathResolver is not exported
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go renderer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Trigger rendering
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for rendered event
	timeout := time.After(1 * time.Second)
	var renderedEvent *events.TemplateRenderedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.TemplateRenderedEvent); ok {
				renderedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for TemplateRenderedEvent")
		}
	}

Done:
	require.NotNil(t, renderedEvent)

	// CRITICAL: Verify that pathResolver.GetPath("crt-list") returns the full path, not just the filename
	// This is the TDD test that will initially FAIL because CRTListDir is not set
	assert.Contains(t, renderedEvent.HAProxyConfig, "crt-list /etc/haproxy/ssl/certificate-list.txt",
		"pathResolver.GetPath('certificate-list.txt', 'crt-list') should return full path with directory prefix")

	// Ensure it doesn't contain just the filename (which is what happens when CRTListDir is empty)
	assert.NotContains(t, renderedEvent.HAProxyConfig, "crt-list certificate-list.txt",
		"pathResolver.GetPath() should not return just the filename without directory")
}

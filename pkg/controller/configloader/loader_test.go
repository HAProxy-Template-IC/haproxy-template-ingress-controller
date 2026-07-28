package configloader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestConfigLoaderComponent_ProcessCRD(t *testing.T) {
	// Create CRD resource
	crd := &v1alpha1.HAProxyTemplateConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "haproxy-haptic.org/v1alpha1",
			Kind:       "HAProxyTemplateConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "default",
			ResourceVersion: "12345",
		},
		Spec: v1alpha1.HAProxyTemplateConfigSpec{
			CredentialsSecretRef: v1alpha1.SecretReference{
				Name: "haproxy-creds",
			},
			PodSelector: v1alpha1.PodSelector{
				MatchLabels: map[string]string{
					"app": "haproxy",
				},
			},
			HAProxyConfig: v1alpha1.HAProxyConfig{
				Template: "global\n  daemon",
			},
		},
	}

	// Convert to unstructured
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(crd)
	require.NoError(t, err)
	unstructuredCRD := &unstructured.Unstructured{Object: unstructuredMap}

	// Create event bus and loader
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, []string{"test-config"}, nil, logger)

	// Subscribe to events and start
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish ConfigResourceChangedEvent with CRD
	bus.Publish(events.NewConfigResourceChangedEvent(unstructuredCRD))

	// Wait for ConfigParsedEvent
	parsedEvent := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "test-config=12345", parsedEvent.Version)
	assert.NotNil(t, parsedEvent.Config)
}

func TestConfigLoaderComponent_UnsupportedResourceType(t *testing.T) {
	// Create unsupported resource (e.g., Deployment)
	deployment := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":            "test-deployment",
				"namespace":       "default",
				"resourceVersion": "11111",
			},
		},
	}

	// Create event bus and loader
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, []string{"test-deployment"}, nil, logger)

	// Subscribe to events and start
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish ConfigResourceChangedEvent with unsupported resource
	bus.Publish(events.NewConfigResourceChangedEvent(deployment))

	// Should not receive ConfigParsedEvent
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)
}

func TestConfigLoaderComponent_Stop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, []string{"test-config"}, nil, logger)
	bus.Start()

	done := make(chan struct{})
	go func() {
		loader.Start(context.Background())
		close(done)
	}()

	// Give loader time to start
	time.Sleep(testutil.DebounceWait)

	// Stop the loader
	loader.Stop()

	// Loader should exit gracefully
	select {
	case <-done:
		// Success - loader stopped
	case <-time.After(testutil.LongTimeout):
		t.Fatal("Timeout waiting for loader to stop")
	}
}

func TestConfigLoaderComponent_InvalidResourceType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, []string{"test-config"}, nil, logger)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish ConfigResourceChangedEvent with non-*unstructured.Unstructured type
	invalidResource := map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
	}
	bus.Publish(events.NewConfigResourceChangedEvent(invalidResource))

	// Should not receive ConfigParsedEvent
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)
}

func TestConfigLoaderComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, []string{"test-config"}, nil, logger)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish a different event type
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Should not receive ConfigParsedEvent
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

// configResource builds a HAProxyTemplateConfig carrying one snippet, enough to
// tell merged output apart by source.
func configResource(name, resourceVersion, snippetName, snippetBody string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
		"metadata": map[string]any{
			"name":            name,
			"namespace":       "default",
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"podSelector":   map[string]any{"matchLabels": map[string]any{"app": "haproxy"}},
			"haproxyConfig": map[string]any{"template": "global\n  daemon"},
			"templateSnippets": map[string]any{
				snippetName: map[string]any{"template": snippetBody},
			},
		},
	}}
}

// snippets type-asserts the event's `any` Config (typed as any on the event to
// avoid a circular import) down to the snippet map under test.
func snippets(t *testing.T, parsed *events.ConfigParsedEvent) map[string]coreconfig.TemplateSnippet {
	t.Helper()
	cfg, ok := parsed.Config.(*coreconfig.Config)
	require.True(t, ok, "ConfigParsedEvent.Config should be *config.Config, got %T", parsed.Config)
	return cfg.TemplateSnippets
}

func startLoader(t *testing.T, names []string) (bus *busevents.EventBus, published <-chan busevents.Event) {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, names, nil, logger)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	t.Cleanup(cancel)
	go loader.Start(ctx)
	time.Sleep(testutil.DebounceWait)

	return bus, eventChan
}

// Each config arrives on its own watcher, so the loader must hold what it has
// seen and stay quiet until the set is complete — a partial merge would drop
// whichever library had not arrived yet.
func TestConfigLoaderComponent_WaitsForEveryConfiguredSource(t *testing.T) {
	bus, eventChan := startLoader(t, []string{"lib-base", "operator-config"})

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("lib-base", "1", "from-base", "base")))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("operator-config", "1", "from-operator", "operator")))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, parsed.Config)
	assert.Contains(t, snippets(t, parsed), "from-base")
	assert.Contains(t, snippets(t, parsed), "from-operator")
}

// A later config wins, and a change to one member re-merges against the held
// copies of the others rather than waiting for them to be re-sent.
func TestConfigLoaderComponent_RemergesOnSingleSourceChange(t *testing.T) {
	bus, eventChan := startLoader(t, []string{"lib-base", "operator-config"})

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("lib-base", "1", "shared", "from base")))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("operator-config", "1", "shared", "from operator")))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "from operator", snippets(t, parsed)["shared"].Template)
	assert.Equal(t, "lib-base=1,operator-config=1", parsed.Version)

	// Only the library changes. The operator's copy is replayed from the held
	// set, so its override must still win.
	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("lib-base", "2", "shared", "from base v2")))

	parsed = testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "from operator", snippets(t, parsed)["shared"].Template)

	// The version has to move even though the primary config did not — the
	// reinit guard compares it for equality and would otherwise drop the change.
	assert.Equal(t, "lib-base=2,operator-config=1", parsed.Version)
}

func TestConfigLoaderComponent_IgnoresUnconfiguredConfig(t *testing.T) {
	bus, eventChan := startLoader(t, []string{"operator-config"})

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("someone-elses-config", "1", "stray", "stray")))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("operator-config", "1", "mine", "mine")))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.NotContains(t, snippets(t, parsed), "stray")
	assert.Contains(t, snippets(t, parsed), "mine")
}

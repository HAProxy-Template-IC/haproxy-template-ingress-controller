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
			Generation:      7,
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
	loader := NewConfigLoaderComponent(bus, "test-config", logger)

	// Subscribe to events and start
	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// The loader needs a snippets snapshot before it can decide the set is
	// complete; this config references none, so an empty one suffices.
	bus.Publish(events.NewLibrarySetChangedEvent(nil))
	bus.Publish(events.NewConfigResourceChangedEvent(unstructuredCRD))

	// Wait for ConfigParsedEvent
	parsedEvent := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "test-config=7", parsedEvent.Version,
		"the composite version keys on generation, so a status write cannot look like a config change")
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
	loader := NewConfigLoaderComponent(bus, "test-deployment", logger)

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
	loader := NewConfigLoaderComponent(bus, "test-config", logger)
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
	loader := NewConfigLoaderComponent(bus, "test-config", logger)

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
	loader := NewConfigLoaderComponent(bus, "test-config", logger)

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
func configResource(name, snippetName, snippetBody string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
		"metadata": map[string]any{
			"name":            name,
			"namespace":       "default",
			"resourceVersion": "1",
			"generation":      int64(1),
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

const (
	testConfigName  = "operator-config"
	testLibraryName = "lib-base"
)

func startLoader(t *testing.T) (bus *busevents.EventBus, published <-chan busevents.Event) {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, testConfigName, logger)

	eventChan := bus.Subscribe("test-sub", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	t.Cleanup(cancel)
	go loader.Start(ctx)
	time.Sleep(testutil.DebounceWait)

	return bus, eventChan
}

// libraryResource builds a HAProxyTemplateLibrary carrying one snippet.
func libraryResource(resourceVersion, revision, snippetName, snippetBody string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateLibrary",
		"metadata": map[string]any{
			"name":            testLibraryName,
			"namespace":       "default",
			"resourceVersion": resourceVersion,
			"generation":      int64(1),
		},
		"spec": map[string]any{
			"revision": revision,
			"templateSnippets": map[string]any{
				snippetName: map[string]any{"template": snippetBody},
			},
		},
	}}
}

// configWithRefs builds a HAProxyTemplateConfig carrying one snippet of its own
// plus an ordered list of snippet references.
func configWithRefs(snippetName, snippetBody string, refs ...[2]string) *unstructured.Unstructured {
	config := configResource(testConfigName, snippetName, snippetBody)
	entries := make([]any, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, map[string]any{"name": ref[0], "revision": ref[1]})
	}
	if len(entries) > 0 {
		spec, _ := config.Object["spec"].(map[string]any)
		spec["libraryRefs"] = entries
	}
	return config
}

// A referenced snippets object that has not arrived leaves the set incomplete.
// Rendering anyway would silently drop a library rather than fail.
func TestConfigLoaderComponent_WaitsForReferencedLibrary(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent(nil))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("from-operator", "operator", [2]string{testLibraryName, "rev-1"})))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)

	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("1", "rev-1", "from-base", "base"),
	}))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, parsed.Config)
	assert.Contains(t, snippets(t, parsed), "from-base")
	assert.Contains(t, snippets(t, parsed), "from-operator")
}

// The revision is the half-applied-set detector: a snippets object present but
// stamped differently from what the config expects must not be rendered.
func TestConfigLoaderComponent_HoldsOnRevisionMismatch(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("1", "rev-OLD", "from-base", "base"),
	}))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("mine", "mine", [2]string{testLibraryName, "rev-NEW"})))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)

	// The writer finishes applying the set: the snippet catches up.
	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("2", "rev-NEW", "from-base", "base v2"),
	}))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "base v2", snippets(t, parsed)["from-base"].Template)
}

// Editing a snippet's content in place leaves spec.revision alone, so the
// reference still resolves and the edit takes effect. Verifying a content hash
// would break exactly this.
func TestConfigLoaderComponent_InPlaceLibraryEditStillRenders(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("1", "rev-1", "shared", "original"),
	}))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("mine", "mine", [2]string{testLibraryName, "rev-1"})))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "original", snippets(t, parsed)["shared"].Template)

	// kubectl edit: content changes, revision does not.
	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("2", "rev-1", "shared", "hand-edited"),
	}))

	parsed = testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "hand-edited", snippets(t, parsed)["shared"].Template)
}

// Deleting a referenced snippet must stop rendering rather than silently drop
// the library. A snapshot without it is how the deletion arrives.
func TestConfigLoaderComponent_HoldsWhenReferencedLibraryDeleted(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("1", "rev-1", "from-base", "base"),
	}))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("mine", "mine", [2]string{testLibraryName, "rev-1"})))
	testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)

	bus.Publish(events.NewLibrarySetChangedEvent(nil))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)
}

// The config's own inline content wins over every referenced snippet, so the
// object an operator edits is the override point regardless of ref order.
func TestConfigLoaderComponent_ConfigOverridesReferencedLibraries(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent([]any{
		libraryResource("1", "rev-1", "shared", "from base"),
	}))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("shared", "from operator", [2]string{testLibraryName, "rev-1"})))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "from operator", snippets(t, parsed)["shared"].Template)
	assert.Equal(t, "lib-base=1,operator-config=1", parsed.Version)
}

func TestConfigLoaderComponent_IgnoresUnconfiguredConfig(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewLibrarySetChangedEvent(nil))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("someone-elses-config", "stray", "stray")))
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource("operator-config", "mine", "mine")))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.NotContains(t, snippets(t, parsed), "stray")
	assert.Contains(t, snippets(t, parsed), "mine")
}

// The multi-object Watcher stores indexer.ConvertedResource — a plain
// map[string]any. Only SingleWatcher hands out *unstructured.Unstructured, and
// every other test here feeds that, which is why they all passed while the
// controller in a real cluster never rendered: the loader type-asserted, the
// assertion failed, and it discarded every snapshot while logging that it was
// still waiting for one.
func TestConfigLoaderComponent_AcceptsPlainMapLibraries(t *testing.T) {
	bus, eventChan := startLoader(t)

	library := libraryResource("1", "rev-1", "from-library", "library")

	bus.Publish(events.NewLibrarySetChangedEvent([]any{library.Object}))
	bus.Publish(events.NewConfigResourceChangedEvent(
		configWithRefs("mine", "mine", [2]string{testLibraryName, "rev-1"})))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, parsed.Config)
	assert.Contains(t, snippets(t, parsed), "from-library",
		"a library delivered as map[string]any must merge exactly like one delivered as Unstructured")
}

// A config that references nothing must render without ever seeing a library
// snapshot. In a cluster with no libraries — or without the CRD installed at
// all — that watch may never sync, and requiring a snapshot there meant the
// controller never reconciled: acceptance saw it as "reconciliation_total is
// 0, controller still initializing".
func TestConfigLoaderComponent_RendersWithoutLibrariesWhenNoneReferenced(t *testing.T) {
	bus, eventChan := startLoader(t)

	bus.Publish(events.NewConfigResourceChangedEvent(
		configResource(testConfigName, "inline", "self-contained")))

	parsed := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.LongTimeout)
	require.NotNil(t, parsed.Config)
	assert.Contains(t, snippets(t, parsed), "inline")
}

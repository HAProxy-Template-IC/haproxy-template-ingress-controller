package configloader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/apis/haproxytemplate/v1alpha1"
	"haptic/pkg/controller/events"
	"haptic/pkg/controller/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestConfigLoaderComponent_ProcessCRD(t *testing.T) {
	// Create CRD resource
	crd := &v1alpha1.HAProxyTemplateConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "haproxy-template-ic.gitlab.io/v1alpha1",
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
	loader := NewConfigLoaderComponent(bus, logger)

	// Subscribe to events and start
	eventChan := bus.Subscribe(10)
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
	assert.Equal(t, "12345", parsedEvent.Version)
	assert.NotNil(t, parsedEvent.Config)
}

func TestConfigLoaderComponent_UnsupportedResourceType(t *testing.T) {
	// Create unsupported resource (e.g., Deployment)
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":            "test-deployment",
				"namespace":       "default",
				"resourceVersion": "11111",
			},
		},
	}

	// Create event bus and loader
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, logger)

	// Subscribe to events and start
	eventChan := bus.Subscribe(10)
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
	loader := NewConfigLoaderComponent(bus, logger)
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
	loader := NewConfigLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish ConfigResourceChangedEvent with non-*unstructured.Unstructured type
	invalidResource := map[string]interface{}{
		"apiVersion": "haproxy-template-ic.gitlab.io/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
	}
	bus.Publish(events.NewConfigResourceChangedEvent(invalidResource))

	// Should not receive ConfigParsedEvent
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)
}

func TestConfigLoaderComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	loader := NewConfigLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.VeryLongTimeout)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(testutil.DebounceWait)

	// Publish a different event type
	bus.Publish(events.NewControllerStartedEvent("v1", "v2"))

	// Should not receive ConfigParsedEvent
	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

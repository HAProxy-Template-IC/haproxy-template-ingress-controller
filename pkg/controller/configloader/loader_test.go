package configloader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"

	"log/slog"

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
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	loader := NewConfigLoaderComponent(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(10)
	bus.Start()

	// Start loader in background
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go loader.Start(ctx)

	// Give loader time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Publish ConfigResourceChangedEvent with CRD
	bus.Publish(events.NewConfigResourceChangedEvent(unstructuredCRD))

	// Wait for ConfigParsedEvent (skip the original ConfigResourceChangedEvent)
	timeout := time.After(1 * time.Second)
	var gotParsedEvent bool
	for !gotParsedEvent {
		select {
		case event := <-eventChan:
			if parsedEvent, ok := event.(*events.ConfigParsedEvent); ok {
				assert.Equal(t, "12345", parsedEvent.Version)
				// Verify config was parsed correctly
				assert.NotNil(t, parsedEvent.Config)
				gotParsedEvent = true
			}
			// Skip other events (like the original ConfigResourceChangedEvent)
		case <-timeout:
			t.Fatal("Timeout waiting for ConfigParsedEvent")
		}
	}
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
	bus := busevents.NewEventBus(100)
	logger := slog.Default()
	loader := NewConfigLoaderComponent(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(10)
	bus.Start()

	// Start loader in background
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go loader.Start(ctx)

	// Publish ConfigResourceChangedEvent with unsupported resource
	bus.Publish(events.NewConfigResourceChangedEvent(deployment))

	// Should not receive ConfigParsedEvent (loader logs error but doesn't publish it)
	// We'll receive the original ConfigResourceChangedEvent, but no ConfigParsedEvent
	timeout := time.After(500 * time.Millisecond)
	var gotParsedEvent bool
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ConfigParsedEvent); ok {
				gotParsedEvent = true
			}
			// Ignore other events (like the original ConfigResourceChangedEvent)
		case <-timeout:
			// Expected - no ConfigParsedEvent for unsupported resource
			assert.False(t, gotParsedEvent, "Should not receive ConfigParsedEvent for unsupported resource")
			return
		}
	}
}

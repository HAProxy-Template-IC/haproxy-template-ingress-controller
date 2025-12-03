package configloader

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/pkg/controller/conversion"
	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/core/config"
	busevents "haproxy-template-ic/pkg/events"
)

// ConfigLoaderComponent subscribes to ConfigResourceChangedEvent and parses config data.
//
// This component is responsible for:
// - Converting HAProxyTemplateConfig CRD Spec to config.Config
// - Publishing ConfigParsedEvent for successfully parsed configs
// - Logging errors for conversion failures
//
// Architecture:
// This is a pure event-driven component with no knowledge of watchers or
// Kubernetes. It simply reacts to ConfigResourceChangedEvent and produces
// ConfigParsedEvent.
type ConfigLoaderComponent struct {
	bus    *busevents.EventBus
	logger *slog.Logger
	stopCh chan struct{}
}

// NewConfigLoaderComponent creates a new ConfigLoader component.
//
// Parameters:
//   - bus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *ConfigLoaderComponent ready to start
func NewConfigLoaderComponent(bus *busevents.EventBus, logger *slog.Logger) *ConfigLoaderComponent {
	return &ConfigLoaderComponent{
		bus:    bus,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins processing events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// It should typically be run in a goroutine.
//
// Example:
//
//	go component.Start(ctx)
func (c *ConfigLoaderComponent) Start(ctx context.Context) {
	eventCh := c.bus.Subscribe(50)

	c.logger.Info("ConfigLoader component started")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("ConfigLoader component stopped", "reason", ctx.Err())
			return
		case <-c.stopCh:
			c.logger.Info("ConfigLoader component stopped")
			return
		case event := <-eventCh:
			if configEvent, ok := event.(*events.ConfigResourceChangedEvent); ok {
				c.processConfigChange(configEvent)
			}
		}
	}
}

// Stop gracefully stops the component.
func (c *ConfigLoaderComponent) Stop() {
	close(c.stopCh)
}

// processConfigChange handles a ConfigResourceChangedEvent by parsing the config resource.
func (c *ConfigLoaderComponent) processConfigChange(event *events.ConfigResourceChangedEvent) {
	// Extract unstructured resource
	resource, ok := event.Resource.(*unstructured.Unstructured)
	if !ok {
		c.logger.Error("ConfigResourceChangedEvent contains invalid resource type",
			"expected", "*unstructured.Unstructured",
			"got", fmt.Sprintf("%T", event.Resource))
		return
	}

	// Get resourceVersion for tracking
	version := resource.GetResourceVersion()

	// Detect resource type from apiVersion and kind
	apiVersion := resource.GetAPIVersion()
	kind := resource.GetKind()

	c.logger.Debug("Processing config resource change",
		"apiVersion", apiVersion,
		"kind", kind,
		"version", version)

	// Validate resource type
	if apiVersion != "haproxy-template-ic.gitlab.io/v1alpha1" || kind != "HAProxyTemplateConfig" {
		c.logger.Error("Unsupported resource type for config",
			"apiVersion", apiVersion,
			"kind", kind,
			"version", version)
		return
	}

	// Process CRD
	cfg, templateConfig, err := c.processCRD(resource)

	if err != nil {
		c.logger.Error("Failed to process config resource",
			"error", err,
			"apiVersion", apiVersion,
			"kind", kind,
			"version", version)
		return
	}

	c.logger.Info("Configuration processed successfully",
		"apiVersion", apiVersion,
		"kind", kind,
		"version", version)

	// Publish ConfigParsedEvent with both parsed config and original CRD
	// Note: SecretVersion will be empty here - it gets populated later when
	// the ValidationCoordinator correlates with credentials
	parsedEvent := events.NewConfigParsedEvent(cfg, templateConfig, version, "")
	c.bus.Publish(parsedEvent)
}

// processCRD converts a HAProxyTemplateConfig CRD to config.Config and returns both.
//
// Returns:
//   - *config.Config: Parsed configuration for validation and rendering
//   - *v1alpha1.HAProxyTemplateConfig: Original CRD for metadata (name, namespace, UID)
//   - error: Conversion failure
func (c *ConfigLoaderComponent) processCRD(resource *unstructured.Unstructured) (*config.Config, *v1alpha1.HAProxyTemplateConfig, error) {
	return conversion.ParseCRD(resource)
}

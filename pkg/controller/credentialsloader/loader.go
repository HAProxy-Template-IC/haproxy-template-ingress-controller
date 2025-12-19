package credentialsloader

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/core/config"
	busevents "haproxy-template-ic/pkg/events"
)

// CredentialsLoaderComponent subscribes to SecretResourceChangedEvent and parses Secret data.
//
// This component is responsible for:
// - Extracting credentials from Secret resources
// - Parsing Secret data into config.Credentials structures
// - Publishing CredentialsUpdatedEvent for successfully loaded credentials
// - Publishing CredentialsInvalidEvent for invalid credentials
//
// Architecture:
// This is a pure event-driven component with no knowledge of watchers or
// Kubernetes. It simply reacts to SecretResourceChangedEvent and produces
// CredentialsUpdatedEvent or CredentialsInvalidEvent.
type CredentialsLoaderComponent struct {
	eventBus *busevents.EventBus
	logger   *slog.Logger
	stopCh   chan struct{}
}

// NewCredentialsLoaderComponent creates a new CredentialsLoader component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *CredentialsLoaderComponent ready to start
func NewCredentialsLoaderComponent(eventBus *busevents.EventBus, logger *slog.Logger) *CredentialsLoaderComponent {
	return &CredentialsLoaderComponent{
		eventBus: eventBus,
		logger:   logger,
		stopCh:   make(chan struct{}),
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
func (c *CredentialsLoaderComponent) Start(ctx context.Context) {
	eventCh := c.eventBus.Subscribe(50)

	c.logger.Info("CredentialsLoader component started")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("CredentialsLoader component stopped", "reason", ctx.Err())
			return
		case <-c.stopCh:
			c.logger.Info("CredentialsLoader component stopped")
			return
		case event := <-eventCh:
			if secretEvent, ok := event.(*events.SecretResourceChangedEvent); ok {
				c.processSecretChange(secretEvent)
			}
		}
	}
}

// Stop gracefully stops the component.
func (c *CredentialsLoaderComponent) Stop() {
	close(c.stopCh)
}

// processSecretChange handles a SecretResourceChangedEvent by parsing the Secret.
func (c *CredentialsLoaderComponent) processSecretChange(event *events.SecretResourceChangedEvent) {
	// Extract unstructured resource
	resource, ok := event.Resource.(*unstructured.Unstructured)
	if !ok {
		c.logger.Error("SecretResourceChangedEvent contains invalid resource type",
			"expected", "*unstructured.Unstructured",
			"got", fmt.Sprintf("%T", event.Resource))
		return
	}

	// Get resourceVersion for tracking
	version := resource.GetResourceVersion()

	c.logger.Debug("Processing Secret change", "version", version)

	// Extract Secret data
	// Note: Secret data is stored as base64-encoded strings in the Kubernetes API,
	// but when accessed through unstructured, it's already decoded
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		c.logger.Error("Failed to extract Secret data field",
			"error", err,
			"version", version)
		c.eventBus.Publish(events.NewCredentialsInvalidEvent(version, fmt.Sprintf("failed to extract Secret data: %v", err)))
		return
	}
	if !found {
		c.logger.Error("Secret has no data field", "version", version)
		c.eventBus.Publish(events.NewCredentialsInvalidEvent(version, "Secret has no data field"))
		return
	}

	// Convert map[string]interface{} to map[string][]byte
	// In unstructured resources, Secret data values are strings
	data := make(map[string][]byte)
	for key, value := range dataRaw {
		if strValue, ok := value.(string); ok {
			data[key] = []byte(strValue)
		} else {
			c.logger.Error("Secret data contains non-string value",
				"key", key,
				"type", fmt.Sprintf("%T", value),
				"version", version)
			c.eventBus.Publish(events.NewCredentialsInvalidEvent(version, fmt.Sprintf("Secret data key '%s' has invalid type", key)))
			return
		}
	}

	// Load the credentials
	creds, err := config.LoadCredentials(data)
	if err != nil {
		c.logger.Error("Failed to load credentials from Secret",
			"error", err,
			"version", version)
		c.eventBus.Publish(events.NewCredentialsInvalidEvent(version, err.Error()))
		return
	}

	c.logger.Info("Credentials loaded successfully", "version", version)

	// Publish CredentialsUpdatedEvent
	c.eventBus.Publish(events.NewCredentialsUpdatedEvent(creds, version))
}

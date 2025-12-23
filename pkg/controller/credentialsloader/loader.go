package credentialsloader

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haptic/pkg/controller/events"
	"haptic/pkg/core/config"
	busevents "haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "credentialsloader"

	// EventBufferSize is the size of the event subscription buffer.
	// Size 50: Low-volume component (~1 event per secret change).
	EventBufferSize = 50
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
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger    *slog.Logger
	stopCh    chan struct{}
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
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &CredentialsLoaderComponent{
		eventBus:  eventBus,
		eventChan: eventChan,
		logger:    logger.With("component", ComponentName),
		stopCh:    make(chan struct{}),
	}
}

// Start begins processing events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// The component is already subscribed to the EventBus (subscription happens in constructor).
// Returns nil on graceful shutdown.
//
// Example:
//
//	go component.Start(ctx)
func (c *CredentialsLoaderComponent) Start(ctx context.Context) error {
	c.logger.Debug("credentials loader starting")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("CredentialsLoader shutting down", "reason", ctx.Err())
			return nil
		case <-c.stopCh:
			c.logger.Info("CredentialsLoader shutting down")
			return nil
		case event := <-c.eventChan:
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

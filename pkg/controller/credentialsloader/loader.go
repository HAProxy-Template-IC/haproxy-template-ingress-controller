package credentialsloader

import (
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceloader"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "credentialsloader"

	// EventBufferSize is the size of the event subscription buffer.
	// Low-volume component (~1 event per secret change).
	EventBufferSize = busevents.StandardSubscriberBuffer
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
	*resourceloader.BaseLoader
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
	c := &CredentialsLoaderComponent{}
	c.BaseLoader = resourceloader.NewBaseLoader(
		eventBus, logger, ComponentName, EventBufferSize, c,
		events.EventTypeSecretResourceChanged,
	)
	return c
}

// ProcessEvent handles a single event from the EventBus.
func (c *CredentialsLoaderComponent) ProcessEvent(event busevents.Event) {
	if secretEvent, ok := event.(*events.SecretResourceChangedEvent); ok {
		c.processSecretChange(secretEvent)
	}
}

// processSecretChange handles a SecretResourceChangedEvent by parsing the Secret.
func (c *CredentialsLoaderComponent) processSecretChange(event *events.SecretResourceChangedEvent) {
	resource, ok := c.AssertUnstructured("SecretResourceChangedEvent", event.Resource)
	if !ok {
		return
	}

	// Get resourceVersion for tracking
	version := resource.GetResourceVersion()

	c.Logger().Debug("Processing Secret change", "version", version)

	// Extract Secret data
	// Note: Secret data is stored as base64-encoded strings in the Kubernetes API.
	// When accessed through unstructured, the values are still base64-encoded strings
	// and must be decoded.
	dataRaw, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		c.failInvalid(version, "Failed to extract Secret data field",
			fmt.Sprintf("extracting Secret data: %v", err), "error", err)
		return
	}
	if !found {
		c.failInvalid(version, "Secret has no data field", "Secret has no data field")
		return
	}

	// Parse Secret data (handles base64 decoding)
	data, err := config.ParseSecretData(dataRaw)
	if err != nil {
		c.failInvalid(version, "Failed to parse Secret data", err.Error(), "error", err)
		return
	}

	// Load the credentials
	creds, err := config.LoadCredentials(data)
	if err != nil {
		c.failInvalid(version, "Failed to load credentials from Secret", err.Error(), "error", err)
		return
	}

	c.Logger().Info("Credentials loaded successfully", "version", version)

	// Publish CredentialsUpdatedEvent
	c.EventBus().Publish(events.NewCredentialsUpdatedEvent(creds, version))
}

// failInvalid logs an error and publishes a CredentialsInvalidEvent. logMsg is
// the structured-log message; reason is the user-visible message embedded in
// the event. logFields are extra key/value pairs added to the log entry
// alongside "version".
func (c *CredentialsLoaderComponent) failInvalid(version, logMsg, reason string, logFields ...any) {
	c.Logger().Error(logMsg, append(logFields, "version", version)...)
	c.EventBus().Publish(events.NewCredentialsInvalidEvent(version, reason))
}

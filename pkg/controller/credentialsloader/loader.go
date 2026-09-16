package credentialsloader

import (
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
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
// - Logging invalid credentials while retaining the previous valid value
//
// Architecture:
// This is a pure event-driven component with no knowledge of watchers or
// Kubernetes. It reacts to SecretResourceChangedEvent and produces
// CredentialsUpdatedEvent when parsing succeeds.
type CredentialsLoaderComponent struct {
	*component.Base
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
	c.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{events.EventTypeSecretResourceChanged},
	})
	return c
}

// HandleEvent handles a single event from the EventBus.
func (c *CredentialsLoaderComponent) HandleEvent(event busevents.Event) {
	if secretEvent, ok := event.(*events.SecretResourceChangedEvent); ok {
		c.processSecretChange(secretEvent)
	}
}

// processSecretChange handles a SecretResourceChangedEvent by parsing the Secret.
func (c *CredentialsLoaderComponent) processSecretChange(event *events.SecretResourceChangedEvent) {
	resource, err := helpers.AsUnstructured(event.Resource)
	if err != nil {
		c.Logger().Error("SecretResourceChangedEvent contains invalid resource", "error", err)
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
			"error", fmt.Errorf("extracting Secret data: %w", err))
		return
	}
	if !found {
		c.failInvalid(version, "Secret has no data field")
		return
	}

	// Parse Secret data (handles base64 decoding)
	data, err := config.ParseSecretData(dataRaw)
	if err != nil {
		c.failInvalid(version, "Failed to parse Secret data", "error", err)
		return
	}

	creds, err := config.LoadCredentials(data)
	if err != nil {
		c.failInvalid(version, "Failed to load credentials from Secret", "error", err)
		return
	}

	c.Logger().Info("Credentials loaded successfully", "version", version)

	c.EventBus().Publish(events.NewCredentialsUpdatedEvent(creds, version))
}

// failInvalid logs an invalid Secret while retaining the previous credentials.
func (c *CredentialsLoaderComponent) failInvalid(version, logMsg string, logFields ...any) {
	c.Logger().Error(logMsg, append(logFields, "version", version)...)
}

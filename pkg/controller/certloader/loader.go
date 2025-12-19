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

package certloader

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
)

// CertLoaderComponent subscribes to CertResourceChangedEvent and extracts TLS certificate data.
//
// This component is responsible for:
// - Extracting TLS certificate data from Secret resources
// - Validating certificate keys exist (tls.crt, tls.key)
// - Publishing CertParsedEvent for successfully extracted certificates
// - Logging errors for invalid or missing certificate data
//
// Architecture:
// This is a pure event-driven component with no knowledge of watchers or
// Kubernetes. It simply reacts to CertResourceChangedEvent and produces
// CertParsedEvent.
type CertLoaderComponent struct {
	eventBus *busevents.EventBus
	logger   *slog.Logger
	stopCh   chan struct{}
}

// NewCertLoaderComponent creates a new CertLoader component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *CertLoaderComponent ready to start
func NewCertLoaderComponent(eventBus *busevents.EventBus, logger *slog.Logger) *CertLoaderComponent {
	return &CertLoaderComponent{
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
func (c *CertLoaderComponent) Start(ctx context.Context) {
	eventCh := c.eventBus.Subscribe(50)

	c.logger.Info("CertLoader component started")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("CertLoader component stopped", "reason", ctx.Err())
			return
		case <-c.stopCh:
			c.logger.Info("CertLoader component stopped")
			return
		case event := <-eventCh:
			if certEvent, ok := event.(*events.CertResourceChangedEvent); ok {
				c.processCertChange(certEvent)
			}
		}
	}
}

// Stop gracefully stops the component.
func (c *CertLoaderComponent) Stop() {
	close(c.stopCh)
}

// processCertChange handles a CertResourceChangedEvent by extracting certificate data from the Secret.
func (c *CertLoaderComponent) processCertChange(event *events.CertResourceChangedEvent) {
	// Extract unstructured resource
	resource, ok := event.Resource.(*unstructured.Unstructured)
	if !ok {
		c.logger.Error("CertResourceChangedEvent contains invalid resource type",
			"expected", "*unstructured.Unstructured",
			"got", fmt.Sprintf("%T", event.Resource))
		return
	}

	// Get resourceVersion for tracking
	version := resource.GetResourceVersion()

	c.logger.Debug("Processing Secret change for webhook certificates", "version", version)

	// Extract Secret data
	data, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		c.logger.Error("Failed to extract Secret data field",
			"error", err,
			"version", version)
		return
	}
	if !found {
		c.logger.Error("Secret has no data field", "version", version)
		return
	}

	// Extract tls.crt and tls.key (standard Kubernetes TLS Secret keys)
	tlsCertBase64, ok := data["tls.crt"]
	if !ok {
		c.logger.Error("Secret data missing 'tls.crt' key", "version", version)
		return
	}

	tlsKeyBase64, ok := data["tls.key"]
	if !ok {
		c.logger.Error("Secret data missing 'tls.key' key", "version", version)
		return
	}

	// Decode base64 data
	tlsCertPEM, err := decodeBase64SecretValue(tlsCertBase64)
	if err != nil {
		c.logger.Error("Failed to decode tls.crt from base64",
			"error", err,
			"version", version)
		return
	}

	tlsKeyPEM, err := decodeBase64SecretValue(tlsKeyBase64)
	if err != nil {
		c.logger.Error("Failed to decode tls.key from base64",
			"error", err,
			"version", version)
		return
	}

	c.logger.Info("Webhook certificates extracted successfully",
		"version", version,
		"cert_size", len(tlsCertPEM),
		"key_size", len(tlsKeyPEM))

	// Publish CertParsedEvent
	parsedEvent := events.NewCertParsedEvent(tlsCertPEM, tlsKeyPEM, version)
	c.eventBus.Publish(parsedEvent)
}

// decodeBase64SecretValue decodes a base64-encoded Secret value.
//
// Secret data values can be either strings (for base64-encoded) or byte slices.
func decodeBase64SecretValue(value interface{}) ([]byte, error) {
	switch v := value.(type) {
	case string:
		// Decode base64
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64: %w", err)
		}
		return decoded, nil
	case []byte:
		// Already decoded (shouldn't happen with unstructured, but handle it)
		return v, nil
	default:
		return nil, fmt.Errorf("unexpected Secret data value type: %T", value)
	}
}

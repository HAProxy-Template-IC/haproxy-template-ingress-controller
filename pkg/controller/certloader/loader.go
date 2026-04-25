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
	"encoding/base64"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourceloader"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "certloader"

	// EventBufferSize is the size of the event subscription buffer.
	// Low-volume component (~1 event per certificate change).
	EventBufferSize = busevents.StandardSubscriberBuffer
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
	*resourceloader.BaseLoader
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
	c := &CertLoaderComponent{}
	c.BaseLoader = resourceloader.NewBaseLoader(
		eventBus, logger, ComponentName, EventBufferSize, c,
		events.EventTypeCertResourceChanged,
	)
	return c
}

// ProcessEvent handles a single event from the EventBus.
func (c *CertLoaderComponent) ProcessEvent(event busevents.Event) {
	if certEvent, ok := event.(*events.CertResourceChangedEvent); ok {
		c.processCertChange(certEvent)
	}
}

// processCertChange handles a CertResourceChangedEvent by extracting certificate data from the Secret.
func (c *CertLoaderComponent) processCertChange(event *events.CertResourceChangedEvent) {
	resource, ok := c.AssertUnstructured("CertResourceChangedEvent", event.Resource)
	if !ok {
		return
	}

	// Get resourceVersion for tracking
	version := resource.GetResourceVersion()

	c.Logger().Debug("Processing Secret change for webhook certificates", "version", version)

	// Extract Secret data
	data, found, err := unstructured.NestedMap(resource.Object, "data")
	if err != nil {
		c.Logger().Error("Failed to extract Secret data field",
			"error", err,
			"version", version)
		return
	}
	if !found {
		c.Logger().Error("Secret has no data field", "version", version)
		return
	}

	// Extract and decode tls.crt and tls.key (standard Kubernetes TLS Secret keys).
	tlsCertPEM, ok := c.decodeSecretKey(data, "tls.crt", version)
	if !ok {
		return
	}
	tlsKeyPEM, ok := c.decodeSecretKey(data, "tls.key", version)
	if !ok {
		return
	}

	c.Logger().Info("Webhook certificates extracted successfully",
		"version", version,
		"cert_size", len(tlsCertPEM),
		"key_size", len(tlsKeyPEM))

	// Publish CertParsedEvent
	parsedEvent := events.NewCertParsedEvent(tlsCertPEM, tlsKeyPEM, version)
	c.EventBus().Publish(parsedEvent)
}

// decodeSecretKey looks up key in the Secret's data map and base64-decodes its
// value, logging a typed error and returning ok=false if either step fails so
// the caller can simply early-return on the boolean.
func (c *CertLoaderComponent) decodeSecretKey(data map[string]any, key, version string) ([]byte, bool) {
	rawValue, ok := data[key]
	if !ok {
		c.Logger().Error("Secret data missing '"+key+"' key", "version", version)
		return nil, false
	}
	decoded, err := decodeBase64SecretValue(rawValue)
	if err != nil {
		c.Logger().Error("Failed to decode "+key+" from base64",
			"error", err,
			"version", version)
		return nil, false
	}
	return decoded, true
}

// decodeBase64SecretValue decodes a base64-encoded Secret value.
//
// Secret data values can be either strings (for base64-encoded) or byte slices.
func decodeBase64SecretValue(value any) ([]byte, error) {
	switch v := value.(type) {
	case string:
		// Decode base64
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("decoding base64: %w", err)
		}
		return decoded, nil
	case []byte:
		// Already decoded (shouldn't happen with unstructured, but handle it)
		return v, nil
	default:
		return nil, fmt.Errorf("unexpected Secret data value type: %T", value)
	}
}

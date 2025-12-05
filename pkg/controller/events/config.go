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

package events

import (
	"fmt"
	"time"
)

// -----------------------------------------------------------------------------
// Configuration Events.
// -----------------------------------------------------------------------------

// ConfigParsedEvent is published when the configuration ConfigMap/Secret has been.
// successfully parsed into a Config structure.
//
// This event does not mean the config is valid - only that it could be parsed.
// Validation occurs in a subsequent step.
type ConfigParsedEvent struct {
	// Config contains the parsed configuration.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to their expected config type.
	Config interface{}

	// TemplateConfig is the original HAProxyTemplateConfig CRD.
	// Type: interface{} to avoid circular dependencies.
	// Needed by ConfigPublisher to extract Kubernetes metadata (name, namespace, UID).
	TemplateConfig interface{}

	// Version is the resourceVersion of the ConfigMap.
	Version string

	// SecretVersion is the resourceVersion of the credentials Secret.
	SecretVersion string

	timestamp time.Time
}

// NewConfigParsedEvent creates a new ConfigParsedEvent.
func NewConfigParsedEvent(config, templateConfig interface{}, version, secretVersion string) *ConfigParsedEvent {
	return &ConfigParsedEvent{
		Config:         config,
		TemplateConfig: templateConfig,
		Version:        version,
		SecretVersion:  secretVersion,
		timestamp:      time.Now(),
	}
}

func (e *ConfigParsedEvent) EventType() string    { return EventTypeConfigParsed }
func (e *ConfigParsedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigValidationRequest is published to request validation of a parsed config.
//
// This is a Request event used in the scatter-gather pattern. Multiple validators
// (basic, template, jsonpath) will respond with ConfigValidationResponse events.
type ConfigValidationRequest struct {
	reqID string

	// Config contains the configuration to validate.
	Config interface{}

	// Version is the resourceVersion being validated.
	Version string

	timestamp time.Time
}

// NewConfigValidationRequest creates a new ConfigValidationRequest.
func NewConfigValidationRequest(config interface{}, version string) *ConfigValidationRequest {
	return &ConfigValidationRequest{
		reqID:     fmt.Sprintf("config-validation-%s-%d", version, time.Now().UnixNano()),
		Config:    config,
		Version:   version,
		timestamp: time.Now(),
	}
}

func (e *ConfigValidationRequest) EventType() string    { return EventTypeConfigValidationRequest }
func (e *ConfigValidationRequest) Timestamp() time.Time { return e.timestamp }
func (e *ConfigValidationRequest) RequestID() string    { return e.reqID }

// ConfigValidationResponse is sent by validators in response to ConfigValidationRequest.
//
// This is a Response event used in the scatter-gather pattern. The ValidationCoordinator
// collects all responses and determines if the config is valid overall.
type ConfigValidationResponse struct {
	reqID     string
	responder string

	// ValidatorName identifies which validator produced this response (basic, template, jsonpath).
	ValidatorName string

	// Valid is true if this validator found no errors.
	Valid bool

	// Errors contains validation error messages, empty if Valid is true.
	Errors []string

	timestamp time.Time
}

// NewConfigValidationResponse creates a new ConfigValidationResponse.
// Performs defensive copy of the errors slice.
func NewConfigValidationResponse(requestID, validatorName string, valid bool, errors []string) *ConfigValidationResponse {
	// Defensive copy of errors slice
	var errorsCopy []string
	if len(errors) > 0 {
		errorsCopy = make([]string, len(errors))
		copy(errorsCopy, errors)
	}

	return &ConfigValidationResponse{
		reqID:         requestID,
		responder:     validatorName,
		ValidatorName: validatorName,
		Valid:         valid,
		Errors:        errorsCopy,
		timestamp:     time.Now(),
	}
}

func (e *ConfigValidationResponse) EventType() string    { return EventTypeConfigValidationResponse }
func (e *ConfigValidationResponse) Timestamp() time.Time { return e.timestamp }
func (e *ConfigValidationResponse) RequestID() string    { return e.reqID }
func (e *ConfigValidationResponse) Responder() string    { return e.responder }

// ConfigValidatedEvent is published when all validators have confirmed the config is valid.
//
// After receiving this event, the controller proceeds to start resource watchers.
// with the validated configuration.
type ConfigValidatedEvent struct {
	Config interface{}

	// TemplateConfig is the original HAProxyTemplateConfig CRD.
	// Type: interface{} to avoid circular dependencies.
	// Needed by ConfigPublisher to extract Kubernetes metadata (name, namespace, UID).
	TemplateConfig interface{}

	Version       string
	SecretVersion string
	timestamp     time.Time
}

// NewConfigValidatedEvent creates a new ConfigValidatedEvent.
func NewConfigValidatedEvent(config, templateConfig interface{}, version, secretVersion string) *ConfigValidatedEvent {
	return &ConfigValidatedEvent{
		Config:         config,
		TemplateConfig: templateConfig,
		Version:        version,
		SecretVersion:  secretVersion,
		timestamp:      time.Now(),
	}
}

func (e *ConfigValidatedEvent) EventType() string    { return EventTypeConfigValidated }
func (e *ConfigValidatedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigInvalidEvent is published when config validation fails.
//
// The controller will continue running with the previous valid config and wait.
// for the next ConfigMap update.
type ConfigInvalidEvent struct {
	Version string

	// ValidationErrors maps validator names to their error messages.
	ValidationErrors map[string][]string

	timestamp time.Time
}

// NewConfigInvalidEvent creates a new ConfigInvalidEvent.
// Performs defensive copy of the validation errors map and its slice values.
func NewConfigInvalidEvent(version string, validationErrors map[string][]string) *ConfigInvalidEvent {
	// Defensive copy of map with slice values
	errorsCopy := make(map[string][]string, len(validationErrors))
	for k, v := range validationErrors {
		if len(v) > 0 {
			vCopy := make([]string, len(v))
			copy(vCopy, v)
			errorsCopy[k] = vCopy
		} else {
			errorsCopy[k] = nil
		}
	}

	return &ConfigInvalidEvent{
		Version:          version,
		ValidationErrors: errorsCopy,
		timestamp:        time.Now(),
	}
}

func (e *ConfigInvalidEvent) EventType() string    { return EventTypeConfigInvalid }
func (e *ConfigInvalidEvent) Timestamp() time.Time { return e.timestamp }

// ConfigResourceChangedEvent is published when the ConfigMap resource is added, updated, or deleted.
//
// This is a low-level event published directly by the SingleWatcher callback in the controller package.
// The ConfigLoaderComponent subscribes to this event and handles parsing.
type ConfigResourceChangedEvent struct {
	// Resource contains the raw ConfigMap resource.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to *unstructured.Unstructured or *corev1.ConfigMap.
	Resource interface{}

	timestamp time.Time
}

// NewConfigResourceChangedEvent creates a new ConfigResourceChangedEvent.
func NewConfigResourceChangedEvent(resource interface{}) *ConfigResourceChangedEvent {
	return &ConfigResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *ConfigResourceChangedEvent) EventType() string    { return EventTypeConfigResourceChanged }
func (e *ConfigResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

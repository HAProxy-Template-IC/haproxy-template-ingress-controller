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

import "time"

// -----------------------------------------------------------------------------
// Template Events.
// -----------------------------------------------------------------------------

// TemplateRenderedEvent is published when template rendering completes successfully.
//
// This event carries two versions of the rendered HAProxy configuration:
// - Production version with absolute paths for deployment to HAProxy pods
// - Validation version with temp directory paths for controller validation
//
// Both configurations are rendered from the same templates but with different PathResolver instances.
//
// The event also carries two versions of auxiliary files:
// - Production version with accepted HTTP content only (for deployment)
// - Validation version with pending HTTP content (for testing new content before promotion).
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
type TemplateRenderedEvent struct {
	// HAProxyConfig is the rendered main HAProxy configuration for production deployment.
	// Contains absolute paths like /etc/haproxy/maps/host.map for HAProxy pods.
	HAProxyConfig string

	// ValidationHAProxyConfig is the rendered configuration for controller validation.
	// Contains temp directory paths matching ValidationPaths for isolated validation.
	ValidationHAProxyConfig string

	// ValidationPaths specifies temp directories where auxiliary files should be written for validation.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to dataplane.ValidationPaths.
	ValidationPaths interface{}

	// AuxiliaryFiles contains all rendered auxiliary files (maps, certificates, general files)
	// for production deployment. Uses accepted HTTP content only.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	AuxiliaryFiles interface{}

	// ValidationAuxiliaryFiles contains auxiliary files for validation.
	// Uses pending HTTP content if available (for testing new content before promotion).
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	ValidationAuxiliaryFiles interface{}

	// Metrics for observability
	ConfigBytes           int   // Size of HAProxyConfig (production)
	ValidationConfigBytes int   // Size of ValidationHAProxyConfig
	AuxiliaryFileCount    int   // Number of auxiliary files
	DurationMs            int64 // Total rendering duration (both configs)

	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewTemplateRenderedEvent creates a new TemplateRenderedEvent.
// Performs defensive copy of the haproxyConfig strings.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewTemplateRenderedEvent(...,
//	    events.PropagateCorrelation(triggeredEvent))
func NewTemplateRenderedEvent(
	haproxyConfig string,
	validationHAProxyConfig string,
	validationPaths interface{},
	auxiliaryFiles interface{},
	validationAuxiliaryFiles interface{},
	auxFileCount int,
	durationMs int64,
	opts ...CorrelationOption,
) *TemplateRenderedEvent {
	// Calculate config sizes
	configBytes := len(haproxyConfig)
	validationConfigBytes := len(validationHAProxyConfig)

	return &TemplateRenderedEvent{
		HAProxyConfig:            haproxyConfig,
		ValidationHAProxyConfig:  validationHAProxyConfig,
		ValidationPaths:          validationPaths,
		AuxiliaryFiles:           auxiliaryFiles,
		ValidationAuxiliaryFiles: validationAuxiliaryFiles,
		ConfigBytes:              configBytes,
		ValidationConfigBytes:    validationConfigBytes,
		AuxiliaryFileCount:       auxFileCount,
		DurationMs:               durationMs,
		timestamp:                time.Now(),
		Correlation:              NewCorrelation(opts...),
	}
}

func (e *TemplateRenderedEvent) EventType() string    { return EventTypeTemplateRendered }
func (e *TemplateRenderedEvent) Timestamp() time.Time { return e.timestamp }

// TemplateRenderFailedEvent is published when template rendering fails.
//
// This event propagates the correlation ID from ReconciliationTriggeredEvent.
type TemplateRenderFailedEvent struct {
	// TemplateName is the name of the template that failed to render.
	TemplateName string

	// Error is the error message.
	Error string

	// StackTrace provides additional debugging context.
	StackTrace string

	timestamp time.Time

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewTemplateRenderFailedEvent creates a new TemplateRenderFailedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the triggering event:
//
//	event := events.NewTemplateRenderFailedEvent(name, err, stackTrace,
//	    events.PropagateCorrelation(triggeredEvent))
func NewTemplateRenderFailedEvent(templateName, err, stackTrace string, opts ...CorrelationOption) *TemplateRenderFailedEvent {
	return &TemplateRenderFailedEvent{
		TemplateName: templateName,
		Error:        err,
		StackTrace:   stackTrace,
		timestamp:    time.Now(),
		Correlation:  NewCorrelation(opts...),
	}
}

func (e *TemplateRenderFailedEvent) EventType() string    { return EventTypeTemplateRenderFailed }
func (e *TemplateRenderFailedEvent) Timestamp() time.Time { return e.timestamp }

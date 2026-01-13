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

package configchange

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// StatusUpdaterComponentName is the unique identifier for this component.
	StatusUpdaterComponentName = "status-updater"

	// StatusUpdaterEventBufferSize is the size of the event subscription buffer.
	StatusUpdaterEventBufferSize = 50
)

// StatusUpdater updates HAProxyTemplateConfig status based on validation results.
//
// This component subscribes to ConfigValidatedEvent, ConfigInvalidEvent, and
// ValidationFailedEvent, updating the HAProxyTemplateConfig CRD status to reflect
// validation state. Users can then see validation errors via `kubectl describe haproxytemplateconfig`.
//
// Architecture:
// This is an event adapter that bridges validation events to Kubernetes API updates.
// It uses the generated typed client for HAProxyTemplateConfig status updates.
//
// The component handles two types of validation:
// - Config validation (Stage 1): Template syntax, JSONPath expressions, etc.
// - HAProxy validation (Stage 4): Rendered config syntax check with haproxy -c.
type StatusUpdater struct {
	crdClient versioned.Interface
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger    *slog.Logger
	stopCh    chan struct{}

	// Cached config reference for HAProxy validation events
	// (ValidationFailedEvent doesn't include the HAProxyTemplateConfig reference)
	mu              sync.RWMutex
	configNamespace string
	configName      string
}

// NewStatusUpdater creates a new StatusUpdater.
//
// Parameters:
//   - crdClient: Kubernetes client for HAProxyTemplateConfig CRD
//   - eventBus: EventBus to subscribe to validation events
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *StatusUpdater ready to start
func NewStatusUpdater(
	crdClient versioned.Interface,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *StatusUpdater {
	// Subscribe to only the event types we handle during construction (before EventBus.Start())
	// This ensures proper startup synchronization and reduces buffer pressure
	eventChan := eventBus.SubscribeTypes(StatusUpdaterComponentName, StatusUpdaterEventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeConfigInvalid,
		events.EventTypeValidationFailed,
	)

	return &StatusUpdater{
		crdClient: crdClient,
		eventBus:  eventBus,
		eventChan: eventChan,
		logger:    logger.With("component", StatusUpdaterComponentName),
		stopCh:    make(chan struct{}),
	}
}

// Name returns the component name for lifecycle management.
func (u *StatusUpdater) Name() string {
	return StatusUpdaterComponentName
}

// Start begins processing validation events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// Returns nil on graceful shutdown.
//
// Example:
//
//	go updater.Start(ctx)
func (u *StatusUpdater) Start(ctx context.Context) error {
	u.logger.Debug("status updater starting")

	for {
		select {
		case <-ctx.Done():
			u.logger.Info("StatusUpdater shutting down", "reason", ctx.Err())
			return nil
		case <-u.stopCh:
			u.logger.Info("StatusUpdater shutting down")
			return nil
		case event := <-u.eventChan:
			switch e := event.(type) {
			case *events.ConfigValidatedEvent:
				u.handleConfigValidated(ctx, e)
			case *events.ConfigInvalidEvent:
				u.handleConfigInvalid(ctx, e)
			case *events.ValidationFailedEvent:
				u.handleHAProxyValidationFailed(ctx, e)
			}
		}
	}
}

// Stop gracefully stops the component.
func (u *StatusUpdater) Stop() {
	close(u.stopCh)
}

// handleConfigValidated updates CRD status to reflect successful validation.
func (u *StatusUpdater) handleConfigValidated(ctx context.Context, event *events.ConfigValidatedEvent) {
	// Skip synthetic bootstrap events
	if event.Version == "initial" {
		return
	}

	// Extract the HAProxyTemplateConfig from the event
	htc, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		u.logger.Debug("ConfigValidatedEvent does not contain HAProxyTemplateConfig, skipping status update",
			"type", fmt.Sprintf("%T", event.TemplateConfig))
		return
	}

	// Cache config reference for HAProxy validation events
	u.mu.Lock()
	u.configNamespace = htc.Namespace
	u.configName = htc.Name
	u.mu.Unlock()

	// Get the latest version of the resource
	current, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(htc.Namespace).
		Get(ctx, htc.Name, metav1.GetOptions{})
	if err != nil {
		u.logger.Warn("Failed to get HAProxyTemplateConfig for status update",
			"namespace", htc.Namespace,
			"name", htc.Name,
			"error", err)
		return
	}

	// Update status fields
	now := metav1.NewTime(time.Now())
	current.Status.LastValidated = &now
	current.Status.ValidationStatus = "Valid"
	current.Status.ValidationMessage = "Configuration validated successfully"
	current.Status.ValidationErrors = nil // Clear any previous errors

	// Update status
	_, err = u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(current.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		u.logger.Warn("Failed to update HAProxyTemplateConfig status",
			"namespace", current.Namespace,
			"name", current.Name,
			"error", err)
		return
	}

	u.logger.Debug("Updated HAProxyTemplateConfig status to Valid",
		"namespace", current.Namespace,
		"name", current.Name,
		"version", event.Version)
}

// handleConfigInvalid updates CRD status to reflect validation failure.
func (u *StatusUpdater) handleConfigInvalid(ctx context.Context, event *events.ConfigInvalidEvent) {
	// Extract the HAProxyTemplateConfig from the event
	htc, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		u.logger.Debug("ConfigInvalidEvent does not contain HAProxyTemplateConfig, skipping status update",
			"type", fmt.Sprintf("%T", event.TemplateConfig))
		return
	}

	// Cache config reference for HAProxy validation events
	u.mu.Lock()
	u.configNamespace = htc.Namespace
	u.configName = htc.Name
	u.mu.Unlock()

	// Get the latest version of the resource
	current, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(htc.Namespace).
		Get(ctx, htc.Name, metav1.GetOptions{})
	if err != nil {
		u.logger.Warn("Failed to get HAProxyTemplateConfig for status update",
			"namespace", htc.Namespace,
			"name", htc.Name,
			"error", err)
		return
	}

	// Flatten validation errors from all validators
	var allErrors []string
	for _, errors := range event.ValidationErrors {
		allErrors = append(allErrors, errors...)
	}

	// Update status fields
	now := metav1.NewTime(time.Now())
	current.Status.LastValidated = &now
	current.Status.ValidationStatus = "Invalid"
	current.Status.ValidationMessage = fmt.Sprintf("%d validation error(s)", len(allErrors))
	current.Status.ValidationErrors = allErrors

	// Update status
	_, err = u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(current.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		u.logger.Warn("Failed to update HAProxyTemplateConfig status",
			"namespace", current.Namespace,
			"name", current.Name,
			"error", err)
		return
	}

	u.logger.Debug("Updated HAProxyTemplateConfig status to Invalid",
		"namespace", current.Namespace,
		"name", current.Name,
		"version", event.Version,
		"error_count", len(allErrors))
}

// handleHAProxyValidationFailed updates CRD status when HAProxy validation fails.
// This handles the case where config validation passes (template syntax OK) but
// the rendered config fails HAProxy's syntax check (haproxy -c).
func (u *StatusUpdater) handleHAProxyValidationFailed(ctx context.Context, event *events.ValidationFailedEvent) {
	// Get cached config reference (set during handleConfigValidated/handleConfigInvalid)
	u.mu.RLock()
	configNamespace := u.configNamespace
	configName := u.configName
	u.mu.RUnlock()

	if configName == "" || configNamespace == "" {
		u.logger.Debug("No cached config reference, skipping HAProxy validation status update")
		return
	}

	// Get the latest version of the resource
	current, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(configNamespace).
		Get(ctx, configName, metav1.GetOptions{})
	if err != nil {
		u.logger.Warn("Failed to get HAProxyTemplateConfig for status update",
			"namespace", configNamespace,
			"name", configName,
			"error", err)
		return
	}

	// Update status fields
	now := metav1.NewTime(time.Now())
	current.Status.LastValidated = &now
	current.Status.ValidationStatus = "Invalid"
	current.Status.ValidationMessage = "HAProxy configuration validation failed"
	current.Status.ValidationErrors = event.Errors

	// Update status
	_, err = u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(current.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		u.logger.Warn("Failed to update HAProxyTemplateConfig status",
			"namespace", current.Namespace,
			"name", current.Name,
			"error", err)
		return
	}

	u.logger.Debug("Updated HAProxyTemplateConfig status to Invalid (HAProxy validation)",
		"namespace", current.Namespace,
		"name", current.Name,
		"error_count", len(event.Errors))
}

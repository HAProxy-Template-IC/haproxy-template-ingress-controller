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
	StatusUpdaterEventBufferSize = busevents.StandardSubscriberBuffer

	// Validation status values written to HAProxyTemplateConfig.Status.ValidationStatus.
	statusValid   = "Valid"
	statusInvalid = "Invalid"
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
	if event.Version == syntheticBootstrapVersion {
		return
	}

	// Extract the HAProxyTemplateConfig from the event
	htc, ok := event.TemplateConfig.(*v1alpha1.HAProxyTemplateConfig)
	if !ok {
		u.logger.Debug("ConfigValidatedEvent does not contain HAProxyTemplateConfig, skipping status update",
			"type", fmt.Sprintf("%T", event.TemplateConfig))
		return
	}

	u.cacheConfigRef(htc.Namespace, htc.Name)

	u.applyStatus(ctx, htc.Namespace, htc.Name,
		func(status *v1alpha1.HAProxyTemplateConfigStatus) {
			now := metav1.NewTime(time.Now())
			status.LastValidated = &now
			status.ValidationStatus = statusValid
			status.ValidationMessage = "Configuration validated successfully"
			status.ValidationErrors = nil // Clear any previous errors
		},
		"Updated HAProxyTemplateConfig status to Valid",
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

	u.cacheConfigRef(htc.Namespace, htc.Name)

	// Flatten validation errors from all validators
	var allErrors []string
	for _, errors := range event.ValidationErrors {
		allErrors = append(allErrors, errors...)
	}

	u.applyStatus(ctx, htc.Namespace, htc.Name,
		func(status *v1alpha1.HAProxyTemplateConfigStatus) {
			now := metav1.NewTime(time.Now())
			status.LastValidated = &now
			status.ValidationStatus = statusInvalid
			status.ValidationMessage = fmt.Sprintf("%d validation error(s)", len(allErrors))
			status.ValidationErrors = allErrors
		},
		"Updated HAProxyTemplateConfig status to Invalid",
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

	u.applyStatus(ctx, configNamespace, configName,
		func(status *v1alpha1.HAProxyTemplateConfigStatus) {
			now := metav1.NewTime(time.Now())
			status.LastValidated = &now
			status.ValidationStatus = statusInvalid
			status.ValidationMessage = "HAProxy configuration validation failed"
			status.ValidationErrors = event.Errors
		},
		"Updated HAProxyTemplateConfig status to Invalid (HAProxy validation)",
		"error_count", len(event.Errors))
}

// cacheConfigRef remembers the namespace/name of the HAProxyTemplateConfig so
// HAProxy validation events (which don't carry a CRD reference) can still find
// the right resource to update.
func (u *StatusUpdater) cacheConfigRef(namespace, name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.configNamespace = namespace
	u.configName = name
}

// applyStatus fetches the named HAProxyTemplateConfig, applies mutate to its
// Status, and writes it back via UpdateStatus. Failures are logged at WARN and
// swallowed (status updates are best-effort). On success, successMsg is logged
// at DEBUG with namespace/name plus any extra logFields the caller supplies.
func (u *StatusUpdater) applyStatus(
	ctx context.Context,
	namespace, name string,
	mutate func(*v1alpha1.HAProxyTemplateConfigStatus),
	successMsg string,
	logFields ...any,
) {
	current, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		u.logger.Warn("Failed to get HAProxyTemplateConfig for status update",
			"namespace", namespace,
			"name", name,
			"error", err)
		return
	}

	mutate(&current.Status)

	if _, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(current.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{}); err != nil {
		u.logger.Warn("Failed to update HAProxyTemplateConfig status",
			"namespace", current.Namespace,
			"name", current.Name,
			"error", err)
		return
	}

	u.logger.Debug(successMsg,
		append(logFields, "namespace", current.Namespace, "name", current.Name)...)
}

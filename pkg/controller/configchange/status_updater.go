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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	clientsetscheme "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/scheme"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

const (
	// StatusUpdaterComponentName is the unique identifier for this component.
	StatusUpdaterComponentName = "status-updater"

	// StatusUpdaterEventBufferSize is the size of the event subscription buffer.
	StatusUpdaterEventBufferSize = busevents.StandardSubscriberBuffer

	// Validation status values written to HAProxyTemplateConfig.Status.ValidationStatus.
	statusValid   = "Valid"
	statusInvalid = "Invalid"

	// eventSourceComponent identifies this controller as the source of emitted
	// Kubernetes Events, so they appear under `kubectl describe haproxytemplateconfig`.
	eventSourceComponent = "haptic-controller"
	// Event reasons (CamelCase per Kubernetes convention).
	eventReasonValidationFailed = "ValidationFailed"
	eventReasonValidated        = "Validated"
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
	*component.Base

	crdClient versioned.Interface

	// kubeClient, broadcaster and recorder emit Kubernetes Events on the
	// HAProxyTemplateConfig so failures surface in `kubectl describe` /
	// `kubectl get events`, not just in CRD status and controller logs. The
	// broadcaster's lifecycle is owned by Start/Stop.
	kubeClient  kubernetes.Interface
	broadcaster record.EventBroadcaster
	recorder    record.EventRecorder

	// ctx is the event-loop context captured by Start. Handlers run only on the
	// loop goroutine and use it for Kubernetes API calls so status writes abort
	// on shutdown.
	ctx context.Context

	// Cached config reference for HAProxy validation events
	// (ValidationFailedEvent doesn't include the HAProxyTemplateConfig reference)
	mu              sync.RWMutex
	configNamespace string
	configName      string
	// lastEmittedStatus is the validation status of the most recent emitted
	// Event, so a Normal "Validated" Event fires only on recovery
	// (Invalid -> Valid), not on every routine successful validation.
	lastEmittedStatus string
}

// NewStatusUpdater creates a new StatusUpdater.
//
// Parameters:
//   - crdClient: typed client for HAProxyTemplateConfig CRD status updates
//   - kubeClient: core client used to emit Kubernetes Events on the CRD
//   - eventBus: EventBus to subscribe to validation events
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *StatusUpdater ready to start
func NewStatusUpdater(
	crdClient versioned.Interface,
	kubeClient kubernetes.Interface,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *StatusUpdater {
	u := &StatusUpdater{
		crdClient:  crdClient,
		kubeClient: kubeClient,
	}

	// Subscribe to only the event types we handle during construction (before
	// EventBus.Start()) for proper startup synchronization and reduced buffer
	// pressure. component.Base supplies the subscribe + dispatch loop with
	// panic recovery and a sync.Once-guarded Stop, so this component no longer
	// hand-rolls a stopCh or its own select loop.
	u.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       StatusUpdaterComponentName,
		BufferSize: StatusUpdaterEventBufferSize,
		Handler:    u,
		EventTypes: []string{
			events.EventTypeConfigValidated,
			events.EventTypeConfigInvalid,
			events.EventTypeValidationFailed,
		},
	})

	return u
}

// Start captures the loop context for handlers and runs the embedded
// component.Base event loop until the context is cancelled or Stop is called.
// Returns nil on graceful shutdown.
func (u *StatusUpdater) Start(ctx context.Context) error {
	u.ctx = ctx
	// Build the Event recorder and forward recorded Events to the API server for
	// the lifetime of the loop. Done here (not in the constructor) so the
	// broadcaster goroutine only exists while the component is actually running.
	u.broadcaster = record.NewBroadcaster()
	u.recorder = u.broadcaster.NewRecorder(clientsetscheme.Scheme, corev1.EventSource{Component: eventSourceComponent})
	u.broadcaster.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: u.kubeClient.CoreV1().Events("")})
	defer u.broadcaster.Shutdown()
	return u.Base.Start(ctx)
}

// HandleEvent implements component.EventHandler, routing each validation event
// to the matching status-update handler using the loop context captured by
// Start.
func (u *StatusUpdater) HandleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		u.handleConfigValidated(u.ctx, e)
	case *events.ConfigInvalidEvent:
		u.handleConfigInvalid(u.ctx, e)
	case *events.ValidationFailedEvent:
		u.handleHAProxyValidationFailed(u.ctx, e)
	}
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
		u.Logger().Debug("ConfigValidatedEvent does not contain HAProxyTemplateConfig, skipping status update",
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
		u.Logger().Debug("ConfigInvalidEvent does not contain HAProxyTemplateConfig, skipping status update",
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
		u.Logger().Debug("No cached config reference, skipping HAProxy validation status update")
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
		u.Logger().Warn("Failed to get HAProxyTemplateConfig for status update",
			"namespace", namespace,
			"name", name,
			"error", err)
		return
	}

	mutate(&current.Status)

	if _, err := u.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(current.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{}); err != nil {
		u.Logger().Warn("Failed to update HAProxyTemplateConfig status",
			"namespace", current.Namespace,
			"name", current.Name,
			"error", err)
		return
	}

	u.emitStatusEvent(current)

	u.Logger().Debug(successMsg,
		append(logFields, "namespace", current.Namespace, "name", current.Name)...)
}

// emitStatusEvent records a Kubernetes Event reflecting the persisted validation
// status: a Warning whenever the config is Invalid (the recorder aggregates
// repeats), and a single Normal only on recovery (Invalid -> Valid) so routine
// successful validations don't spam events.
func (u *StatusUpdater) emitStatusEvent(htc *v1alpha1.HAProxyTemplateConfig) {
	if u.recorder == nil {
		return
	}
	u.mu.Lock()
	prev := u.lastEmittedStatus
	u.lastEmittedStatus = htc.Status.ValidationStatus
	u.mu.Unlock()

	switch htc.Status.ValidationStatus {
	case statusInvalid:
		u.recorder.Event(htc, corev1.EventTypeWarning, eventReasonValidationFailed, validationEventMessage(&htc.Status))
	case statusValid:
		if prev == statusInvalid {
			u.recorder.Event(htc, corev1.EventTypeNormal, eventReasonValidated, "HAProxyTemplateConfig is valid again")
		}
	}
}

// validationEventMessage renders a concise Event message from the status: the
// first validation error (plus a "+N more" hint), or the status message when no
// per-error detail is available.
func validationEventMessage(status *v1alpha1.HAProxyTemplateConfigStatus) string {
	if len(status.ValidationErrors) == 0 {
		return status.ValidationMessage
	}
	msg := status.ValidationErrors[0]
	if len(status.ValidationErrors) > 1 {
		msg = fmt.Sprintf("%s (+%d more)", msg, len(status.ValidationErrors)-1)
	}
	return msg
}

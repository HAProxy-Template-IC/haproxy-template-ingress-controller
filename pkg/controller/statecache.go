// Copyright 2025 Philipp Hossner.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at.
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software.
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and.
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Pipeline status constants.
const (
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	statusPending   = "pending"
	statusSkipped   = "skipped"
	statusPartial   = "partial"
)

// StateCache caches controller state by subscribing to events.
//
// This component implements the debug.StateProvider interface and provides
// thread-safe access to the controller's internal state for debug purposes.
//
// It subscribes to key events and updates its cached state accordingly:
//   - ConfigValidatedEvent → updates config cache
//   - CredentialsUpdatedEvent → updates credentials cache
//   - TemplateRenderedEvent → updates rendered config cache
//   - ReconciliationTriggeredEvent → updates pipeline trigger state
//   - ValidationStartedEvent/CompletedEvent/FailedEvent → updates validation state
//   - DeploymentStartedEvent/CompletedEvent → updates deployment state
//   - InstanceDeploymentFailedEvent → tracks failed endpoints
type StateCache struct {
	eventBus        *busevents.EventBus
	resourceWatcher *resourcewatcher.ResourceWatcherComponent
	logger          *slog.Logger
	eventChan       <-chan busevents.Event

	// Cached state (thread-safe)
	mu                   sync.RWMutex
	currentConfig        *coreconfig.Config
	currentConfigVersion string
	currentCreds         *coreconfig.Credentials
	currentCredsVersion  string
	lastRendered         string
	lastRenderedTime     time.Time
	lastAuxFiles         *dataplane.AuxiliaryFiles
	lastAuxFilesTime     time.Time

	// Pipeline status (new fields for debug endpoints)
	lastTriggerReason string
	lastTriggerTime   time.Time

	// Rendering status
	renderStatus     string // "succeeded" | "failed"
	renderError      string
	renderTime       time.Time
	renderDurationMs int64

	// Validation status
	validationStatus     string // "succeeded" | "failed" | "pending"
	validationErrors     []string
	validationWarnings   []string
	validationTime       time.Time
	validationDurationMs int64

	// Last validated config (only updated on success)
	lastValidatedConfig string
	lastValidatedTime   time.Time

	// Deployment status
	deploymentStatus     string // "succeeded" | "failed" | "skipped" | "pending"
	deploymentReason     string // why skipped (e.g., "validation_failed")
	deploymentTime       time.Time
	deploymentDurationMs int64
	endpointsTotal       int
	endpointsSucceeded   int
	endpointsFailed      int
	failedEndpoints      []debug.FailedEndpoint
}

// Compile-time check that StateCache implements debug.StateProvider interface.
var _ debug.StateProvider = (*StateCache)(nil)

// NewStateCache creates a new state cache component.
//
// The StateCache subscribes to the EventBus in the constructor (before EventBus.Start())
// to ensure proper startup synchronization and receive all buffered startup events.
//
// Usage:
//
//	stateCache := NewStateCache(eventBus, resourceWatcher, logger)
//	go stateCache.Start(ctx)  // Process events in background
//	eventBus.Start()          // Release buffered events
func NewStateCache(eventBus *busevents.EventBus, resourceWatcher *resourcewatcher.ResourceWatcherComponent, logger *slog.Logger) *StateCache {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes("state-cache", 100,
		events.EventTypeConfigValidated,
		events.EventTypeCredentialsUpdated,
		events.EventTypeTemplateRendered,
		events.EventTypeTemplateRenderFailed,
		events.EventTypeReconciliationTriggered,
		events.EventTypeValidationStarted,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeDeploymentStarted,
		events.EventTypeDeploymentCompleted,
		events.EventTypeInstanceDeploymentFailed,
	)

	return &StateCache{
		eventBus:        eventBus,
		resourceWatcher: resourceWatcher,
		logger:          logger,
		eventChan:       eventChan,
	}
}

// Start begins processing events from the EventBus.
//
// The component is already subscribed to the EventBus (subscription happens in NewStateCache()),
// so this method only processes events. This method blocks until the context is cancelled.
func (sc *StateCache) Start(ctx context.Context) error {
	for {
		select {
		case event := <-sc.eventChan:
			sc.handleEvent(event)

		case <-ctx.Done():
			return nil
		}
	}
}

// handleEvent processes events and updates cached state.
func (sc *StateCache) handleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		sc.handleConfigValidated(e)
	case *events.CredentialsUpdatedEvent:
		sc.handleCredentialsUpdated(e)
	case *events.TemplateRenderedEvent:
		sc.handleTemplateRendered(e)
	case *events.TemplateRenderFailedEvent:
		sc.handleTemplateRenderFailed(e)
	case *events.ReconciliationTriggeredEvent:
		sc.handleReconciliationTriggered(e)
	case *events.ValidationStartedEvent:
		sc.handleValidationStarted(e)
	case *events.ValidationCompletedEvent:
		sc.handleValidationCompleted(e)
	case *events.ValidationFailedEvent:
		sc.handleValidationFailed(e)
	case *events.DeploymentStartedEvent:
		sc.handleDeploymentStarted(e)
	case *events.DeploymentCompletedEvent:
		sc.handleDeploymentCompleted(e)
	case *events.InstanceDeploymentFailedEvent:
		sc.handleInstanceDeploymentFailed(e)
	}
}

func (sc *StateCache) handleConfigValidated(e *events.ConfigValidatedEvent) {
	if cfg, ok := e.Config.(*coreconfig.Config); ok {
		sc.mu.Lock()
		sc.currentConfig = cfg
		sc.currentConfigVersion = e.Version
		sc.mu.Unlock()

		// Update log level dynamically if configured in ConfigMap
		// Empty Level means use LOG_LEVEL env var (don't change)
		if cfg.Logging.Level != "" {
			oldLevel := logging.GetLevel()
			logging.SetLevel(cfg.Logging.Level)
			newLevel := logging.GetLevel()
			if oldLevel != newLevel {
				sc.logger.Info("Log level updated from config",
					"old_level", oldLevel,
					"new_level", newLevel)
			}
		}
	} else {
		sc.logger.Error("type assertion failed for ConfigValidatedEvent config",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", e.Config))
	}
}

func (sc *StateCache) handleCredentialsUpdated(e *events.CredentialsUpdatedEvent) {
	if creds, ok := e.Credentials.(*coreconfig.Credentials); ok {
		sc.mu.Lock()
		sc.currentCreds = creds
		sc.currentCredsVersion = e.SecretVersion
		sc.mu.Unlock()
	} else {
		sc.logger.Error("type assertion failed for CredentialsUpdatedEvent credentials",
			"expected", "*coreconfig.Credentials",
			"got", fmt.Sprintf("%T", e.Credentials))
	}
}

func (sc *StateCache) handleTemplateRendered(e *events.TemplateRenderedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.lastRendered = e.HAProxyConfig
	sc.lastRenderedTime = time.Now()
	sc.renderStatus = statusSucceeded
	sc.renderError = ""
	sc.renderTime = e.Timestamp()

	if e.AuxiliaryFiles != nil {
		sc.lastAuxFiles = e.AuxiliaryFiles
		sc.lastAuxFilesTime = time.Now()
	}
}

func (sc *StateCache) handleTemplateRenderFailed(e *events.TemplateRenderFailedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.renderStatus = statusFailed
	sc.renderError = e.Error
	sc.renderTime = e.Timestamp()
}

func (sc *StateCache) handleReconciliationTriggered(e *events.ReconciliationTriggeredEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.lastTriggerReason = e.Reason
	sc.lastTriggerTime = e.Timestamp()
	// Reset pipeline state for new reconciliation
	sc.renderStatus = ""
	sc.validationStatus = ""
	sc.deploymentStatus = ""
	sc.failedEndpoints = nil
}

func (sc *StateCache) handleValidationStarted(e *events.ValidationStartedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.validationStatus = statusPending
	sc.validationTime = e.Timestamp()
	sc.validationErrors = nil
	sc.validationWarnings = nil
}

func (sc *StateCache) handleValidationCompleted(e *events.ValidationCompletedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.validationStatus = statusSucceeded
	sc.validationTime = e.Timestamp()
	sc.validationDurationMs = e.DurationMs
	sc.validationWarnings = e.Warnings
	sc.validationErrors = nil
	// Update last validated config
	sc.lastValidatedConfig = sc.lastRendered
	sc.lastValidatedTime = e.Timestamp()
}

func (sc *StateCache) handleValidationFailed(e *events.ValidationFailedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.validationStatus = statusFailed
	sc.validationTime = e.Timestamp()
	sc.validationDurationMs = e.DurationMs
	sc.validationErrors = e.Errors
	// Mark deployment as skipped due to validation failure
	sc.deploymentStatus = statusSkipped
	sc.deploymentReason = "validation_failed"
}

func (sc *StateCache) handleDeploymentStarted(e *events.DeploymentStartedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.deploymentStatus = statusPending
	sc.deploymentTime = e.Timestamp()
	sc.endpointsTotal = len(e.Endpoints)
	sc.endpointsSucceeded = 0
	sc.endpointsFailed = 0
	sc.failedEndpoints = nil
	sc.deploymentReason = ""
}

func (sc *StateCache) handleDeploymentCompleted(e *events.DeploymentCompletedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if e.Failed > 0 && e.Succeeded == 0 {
		sc.deploymentStatus = statusFailed
	} else if e.Failed > 0 {
		sc.deploymentStatus = statusPartial
	} else {
		sc.deploymentStatus = statusSucceeded
	}
	sc.deploymentTime = e.Timestamp()
	sc.deploymentDurationMs = e.DurationMs
	sc.endpointsTotal = e.Total
	sc.endpointsSucceeded = e.Succeeded
	sc.endpointsFailed = e.Failed
}

func (sc *StateCache) handleInstanceDeploymentFailed(e *events.InstanceDeploymentFailedEvent) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	var endpointURL string
	if stringer, ok := e.Endpoint.(fmt.Stringer); ok {
		endpointURL = stringer.String()
	} else {
		endpointURL = fmt.Sprintf("%v", e.Endpoint)
	}
	sc.failedEndpoints = append(sc.failedEndpoints, debug.FailedEndpoint{
		URL:   endpointURL,
		Error: e.Error,
	})
}

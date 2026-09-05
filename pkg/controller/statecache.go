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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
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
//   - RenderGateCompletedEvent/ValidationFailedEvent → updates validation state
//   - DeploymentStartedEvent/CompletedEvent → updates deployment state
//   - InstanceDeploymentFailedEvent → tracks failed endpoints
type StateCache struct {
	*component.Base

	resourceWatcher *resourcewatcher.ResourceWatcherComponent

	// Cached state (thread-safe)
	mu                   sync.RWMutex
	currentConfig        *coreconfig.Config
	currentConfigVersion string
	activeLogLevel       string
	currentCreds         *coreconfig.Credentials
	currentCredsVersion  string
	lastRendered         string
	lastRenderedPlanID   string
	lastRenderProof      string
	lastRenderOccurrence *rendercycle.Occurrence
	lastRenderedTime     time.Time
	lastAuxFiles         *dataplane.AuxiliaryFiles
	lastOutputSnapshot   *renderoutput.Snapshot
	lastCycleSnapshot    *rendercycle.Snapshot
	lastAuxFilesTime     time.Time

	// Pipeline status (new fields for debug endpoints)
	lastTriggerReason string
	lastTriggerTime   time.Time

	// Rendering status
	renderStatus     string // "succeeded" | "failed"
	renderError      string
	renderTime       time.Time
	renderDurationMs int64

	validationStatus     string // "succeeded" | "failed" | "pending"
	validationErrors     []string
	validationWarnings   []string
	validationTime       time.Time
	validationDurationMs int64
	// validationPlanID names the render the verdict judged, which is not always
	// the newest one: the gate also answers for plans pods still run.
	validationPlanID string

	// Last validated config (only updated on success)
	lastValidatedConfig string
	lastValidatedTime   time.Time

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
	sc := &StateCache{
		resourceWatcher: resourceWatcher,
	}
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps.
	// Use typed subscription to only receive events we handle (reduces buffer pressure).
	sc.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       "state-cache",
		BufferSize: 100,
		Handler:    sc,
		EventTypes: []string{
			events.EventTypeConfigValidated,
			events.EventTypeCredentialsUpdated,
			events.EventTypeTemplateRendered,
			events.EventTypeTemplateRenderFailed,
			events.EventTypeReconciliationTriggered,
			events.EventTypeRenderGateCompleted,
			events.EventTypeValidationFailed,
			events.EventTypeDeploymentStarted,
			events.EventTypeDeploymentCompleted,
			events.EventTypeInstanceDeploymentFailed,
		},
	})
	return sc
}

// HandleEvent implements component.EventHandler: it processes events and
// updates cached state.
func (sc *StateCache) HandleEvent(event busevents.Event) {
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
	case *events.RenderGateCompletedEvent:
		sc.handleRenderGateCompleted(e)
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
	cfg, ok := e.Config.(*coreconfig.Config)
	if !ok {
		sc.Logger().Error("Type assertion failed for ConfigValidatedEvent config",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", e.Config))
		return
	}

	targetLogLevel := cfg.Logging.Level
	sc.mu.Lock()
	sc.currentConfig = cfg
	sc.currentConfigVersion = e.Version
	if e.ActiveSnapshotRestore {
		if targetLogLevel == "" {
			targetLogLevel = sc.activeLogLevel
		}
	} else if e.CandidateGeneration == 0 {
		sc.activeLogLevel = targetLogLevel
		if sc.activeLogLevel == "" {
			sc.activeLogLevel = logging.GetLevel()
		}
	}
	sc.mu.Unlock()

	// Empty Level means use LOG_LEVEL; an active restore uses the captured level.
	if targetLogLevel != "" {
		oldLevel := logging.GetLevel()
		logging.SetLevel(targetLogLevel)
		newLevel := logging.GetLevel()
		if oldLevel != newLevel {
			sc.Logger().Info("Log level updated from config",
				"old_level", oldLevel,
				"new_level", newLevel)
		}
	}
}

func (sc *StateCache) handleCredentialsUpdated(e *events.CredentialsUpdatedEvent) {
	creds, ok := e.Credentials.(*coreconfig.Credentials)
	if !ok {
		sc.Logger().Error("Type assertion failed for CredentialsUpdatedEvent credentials",
			"expected", "*coreconfig.Credentials",
			"got", fmt.Sprintf("%T", e.Credentials))
		return
	}

	sc.mu.Lock()
	sc.currentCreds = creds
	sc.currentCredsVersion = e.SecretVersion
	sc.mu.Unlock()
}

func (sc *StateCache) handleTemplateRendered(e *events.TemplateRenderedEvent) {
	occurrence, err := e.RenderOccurrence()
	if err != nil {
		sc.Logger().Error("Rejected rendered event without authenticated occurrence", "error", err)
		return
	}
	cycleSnapshot, err := occurrence.Snapshot()
	if err != nil {
		sc.Logger().Error("Rejected invalid rendered occurrence", "error", err)
		return
	}
	outputSnapshot, err := cycleSnapshot.OutputSnapshot()
	if err != nil {
		sc.Logger().Error("Rejected invalid rendered cycle", "error", err)
		return
	}
	config, err := outputSnapshot.Config()
	if err != nil {
		sc.Logger().Error("Rejected invalid rendered output", "error", err)
		return
	}
	planID, err := outputSnapshot.PlanID()
	if err != nil {
		sc.Logger().Error("Rejected invalid rendered output plan", "error", err)
		return
	}
	renderProof, err := occurrence.Proof()
	if err != nil {
		sc.Logger().Error("Rejected invalid rendered occurrence proof", "error", err)
		return
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.lastRendered = config
	sc.lastRenderedPlanID = planID
	sc.lastRenderProof = renderProof
	sc.lastRenderOccurrence = occurrence
	sc.lastRenderedTime = time.Now()
	sc.renderStatus = statusSucceeded
	sc.renderError = ""
	sc.renderTime = e.Timestamp()
	sc.renderDurationMs = e.DurationMs

	sc.lastCycleSnapshot = cycleSnapshot
	sc.lastOutputSnapshot = outputSnapshot
	sc.lastAuxFiles = nil
	sc.lastAuxFilesTime = time.Now()
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

// handleRenderGateCompleted records the render gate's verdict. It arrives after
// the render was dispatched (the gate runs off the wall clock), so a failure
// describes a config the fleet may already hold and is being reverted from.
//
// The gate also answers for superseded plans that pods still run. Those
// verdicts are real, but they are not statements about the current render, so
// the debug view reports which plan each one judged and only promotes the
// cached config when the verdict names the render this cache holds.
func (sc *StateCache) handleRenderGateCompleted(e *events.RenderGateCompletedEvent) {
	occurrence, err := e.RenderOccurrence()
	if err != nil {
		sc.Logger().Error("Rejected render-gate event without authenticated occurrence", "error", err)
		return
	}
	cycleSnapshot, err := occurrence.Snapshot()
	if err != nil {
		sc.Logger().Error("Rejected invalid render-gate occurrence", "error", err)
		return
	}
	outputSnapshot, err := cycleSnapshot.OutputSnapshot()
	if err != nil {
		sc.Logger().Error("Rejected invalid render-gate cycle", "error", err)
		return
	}
	verdictPlanID, err := outputSnapshot.PlanID()
	if err != nil {
		sc.Logger().Error("Rejected invalid render-gate output", "error", err)
		return
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	describesCurrentRender := false
	if sc.lastRenderOccurrence != nil {
		describesCurrentRender, err = sc.lastRenderOccurrence.Same(occurrence)
		if err != nil {
			sc.Logger().Error("Rejected invalid render-gate occurrence identity", "error", err)
			return
		}
	}
	sc.validationTime = e.Timestamp()
	sc.validationDurationMs = e.DurationMs
	sc.validationWarnings = nil
	sc.validationPlanID = verdictPlanID
	if !e.OK {
		sc.validationStatus = statusFailed
		sc.validationErrors = []string{e.Message}
		return
	}
	sc.validationStatus = statusSucceeded
	sc.validationErrors = nil
	if !describesCurrentRender {
		return
	}
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
	sc.endpointsTotal = e.EndpointCount
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

	endpointURL := fmt.Sprint(e.Endpoint)
	sc.failedEndpoints = append(sc.failedEndpoints, debug.FailedEndpoint{
		URL:   endpointURL,
		Error: e.Error,
	})
}

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

// Package validator implements validation components for HAProxy configuration.
//
// The HAProxyValidatorComponent validates rendered HAProxy configurations
// using a two-phase approach: syntax validation (client-native parser) and
// semantic validation (haproxy binary with -c flag).
package validator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// HAProxyValidatorComponentName is the unique identifier for this component.
	HAProxyValidatorComponentName = "haproxy-validator"

	// HAProxyValidatorEventBufferSize is the size of the event subscription buffer.
	// Size 50: Medium-volume component (validation events during reconciliation).
	HAProxyValidatorEventBufferSize = 50
)

// HAProxyValidatorComponent validates rendered HAProxy configurations.
//
// It subscribes to TemplateRenderedEvent and BecameLeaderEvent, validates the configuration using
// dataplane.ValidateConfiguration(), and publishes validation result events
// for the next phase (deployment).
//
// Validation is performed in two phases:
//  1. Syntax validation using client-native parser
//  2. Semantic validation using haproxy binary (-c flag)
//
// The component caches the last validation result to support:
// - State replay during leadership transitions (when new leader-only components start subscribing).
// - Skipping re-validation of identical failed configs (reduces log spam).
type HAProxyValidatorComponent struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger    *slog.Logger

	// State protected by mutex (for leadership transition replay and config caching)
	mu                       sync.RWMutex
	lastValidationSucceeded  bool
	lastValidationWarnings   []string
	lastValidationDurationMs int64
	lastCorrelationID        string // Correlation ID from triggering event
	lastTriggerReason        string // Reason from ReconciliationTriggeredEvent (e.g., "drift_prevention")
	hasValidationResult      bool

	// Config hash cache to skip re-validating identical failed configs
	// This prevents log spam when the same broken config is re-rendered repeatedly
	lastConfigHash       string
	lastValidationErrors []string
}

// NewHAProxyValidator creates a new HAProxy validator component.
//
// The validator extracts validation paths from TemplateRenderedEvent, which are created
// per-render by the Renderer component for isolated validation.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to events and publishing results
//   - logger: Structured logger for component logging
//
// Returns:
//   - A new HAProxyValidatorComponent instance ready to be started
func NewHAProxyValidator(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) *HAProxyValidatorComponent {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes(HAProxyValidatorComponentName, HAProxyValidatorEventBufferSize,
		events.EventTypeTemplateRendered,
		events.EventTypeBecameLeader,
	)

	return &HAProxyValidatorComponent{
		eventBus:  eventBus,
		eventChan: eventChan,
		logger:    logger,
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (v *HAProxyValidatorComponent) Name() string {
	return HAProxyValidatorComponentName
}

// Start begins the validator's event loop.
//
// This method blocks until the context is cancelled or an error occurs.
// The component is already subscribed to the EventBus (subscription happens in NewHAProxyValidator()),
// so this method only processes events:
//   - TemplateRenderedEvent: Starts HAProxy configuration validation
//   - BecameLeaderEvent: Replays last validation state for new leader-only components
//
// The component runs until the context is cancelled, at which point it
// performs cleanup and returns.
//
// Parameters:
//   - ctx: Context for cancellation and lifecycle management
//
// Returns:
//   - nil when context is cancelled (graceful shutdown)
//   - Error only in exceptional circumstances
func (v *HAProxyValidatorComponent) Start(ctx context.Context) error {
	v.logger.Debug("HAProxy validator starting")

	for {
		select {
		case event := <-v.eventChan:
			v.handleEvent(event)

		case <-ctx.Done():
			v.logger.Info("HAProxy Validator shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (v *HAProxyValidatorComponent) handleEvent(event busevents.Event) {
	switch ev := event.(type) {
	case *events.TemplateRenderedEvent:
		v.handleTemplateRendered(ev)

	case *events.BecameLeaderEvent:
		v.handleBecameLeader(ev)
	}
}

// handleTemplateRendered validates the rendered HAProxy configuration.
// Propagates correlation ID and trigger reason from the triggering event to validation events.
func (v *HAProxyValidatorComponent) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	startTime := time.Now()
	correlationID := event.CorrelationID()
	triggerReason := event.TriggerReason

	// Extract validation paths first to get TempDir for cleanup.
	// The renderer creates a temp directory for each render cycle, and the validator
	// is responsible for cleaning it up after validation completes.
	validationPaths, ok := event.GetValidationPaths()
	if !ok {
		v.publishValidationFailure(
			[]string{"failed to extract validation paths from event"},
			time.Since(startTime).Milliseconds(),
			correlationID,
			triggerReason,
			"", // Don't cache infrastructure errors
		)
		return
	}

	// Defer cleanup of the validation temp directory.
	// This ensures cleanup happens regardless of how the function exits (success, failure, or cache skip).
	// The renderer creates the temp directory but delegates cleanup to the validator to prevent
	// race conditions where cleanup runs before async validation.
	defer v.cleanupValidationTempDir(validationPaths.TempDir)

	// Compute config hash to detect identical configs
	configHash := v.computeConfigHash(event.HAProxyConfig)

	// Check if this is the same config that already failed validation
	// If so, skip re-validation to reduce log spam and publish cached failure
	v.mu.RLock()
	if configHash == v.lastConfigHash && !v.lastValidationSucceeded && v.hasValidationResult {
		cachedErrors := v.lastValidationErrors
		v.mu.RUnlock()

		v.logger.Debug("Skipping validation for unchanged failed config",
			"config_hash", configHash[:16],
			"correlation_id", correlationID)

		// Publish validation started event (for consistency)
		v.eventBus.Publish(events.NewValidationStartedEvent(
			events.PropagateCorrelation(event),
		))

		// Publish cached failure with current trigger context
		v.publishValidationFailure(
			cachedErrors,
			0, // Duration is 0 since we skipped validation
			correlationID,
			triggerReason,
			"", // Don't re-cache the same hash
		)
		return
	}
	v.mu.RUnlock()

	// Publish validation started event with correlation
	v.eventBus.Publish(events.NewValidationStartedEvent(
		events.PropagateCorrelation(event),
	))

	// Extract validation auxiliary files from event
	// These contain pending HTTP content (for testing new content before promotion)
	// Uses typed accessor method for compile-time type safety
	auxiliaryFiles, ok := event.GetValidationAuxiliaryFiles()
	if !ok {
		v.publishValidationFailure(
			[]string{"failed to extract validation auxiliary files from event"},
			time.Since(startTime).Milliseconds(),
			correlationID,
			triggerReason,
			"", // Don't cache infrastructure errors
		)
		return
	}

	// Validate configuration using validation config and paths from event
	// Use ValidationHAProxyConfig (rendered with temp paths) instead of HAProxyConfig (production paths)
	// Pass nil version to use default v3.0 schema (safest for validation)
	// Use permissive validation (skipDNSValidation=true) to prevent blocking when DNS fails at runtime
	err := dataplane.ValidateConfiguration(event.ValidationHAProxyConfig, auxiliaryFiles, validationPaths, nil, true)
	if err != nil {
		// Simplify error message for user-facing output
		// Keep full error in logs for debugging
		simplified := dataplane.SimplifyValidationError(err)

		v.logger.Error("HAProxy configuration validation failed",
			"error", simplified,
			"correlation_id", correlationID)

		v.publishValidationFailure(
			[]string{simplified},
			time.Since(startTime).Milliseconds(),
			correlationID,
			triggerReason,
			configHash, // Cache this config hash to skip re-validation
		)
		return
	}

	// Validation succeeded
	durationMs := time.Since(startTime).Milliseconds()

	// Cache validation result for leadership transition replay
	// Clear config hash cache since validation succeeded (new successful config)
	v.mu.Lock()
	v.lastValidationSucceeded = true
	v.lastValidationWarnings = []string{} // No warnings
	v.lastValidationDurationMs = durationMs
	v.lastCorrelationID = correlationID
	v.lastTriggerReason = triggerReason
	v.hasValidationResult = true
	v.lastConfigHash = ""        // Clear cache - new valid config
	v.lastValidationErrors = nil // Clear cached errors
	v.mu.Unlock()

	v.eventBus.Publish(events.NewValidationCompletedEvent(
		[]string{}, // No warnings
		durationMs,
		triggerReason,
		events.PropagateCorrelation(event),
	))
}

// handleBecameLeader handles BecameLeaderEvent by re-publishing the last validation result.
//
// This ensures DeploymentScheduler (which starts subscribing only after becoming leader)
// receives the current validation state, even if validation occurred before leadership was acquired.
//
// This prevents the "late subscriber problem" where leader-only components miss events
// that were published before they started subscribing.
func (v *HAProxyValidatorComponent) handleBecameLeader(_ *events.BecameLeaderEvent) {
	v.mu.RLock()
	hasResult := v.hasValidationResult
	succeeded := v.lastValidationSucceeded
	warnings := v.lastValidationWarnings
	durationMs := v.lastValidationDurationMs
	correlationID := v.lastCorrelationID
	triggerReason := v.lastTriggerReason
	v.mu.RUnlock()

	if !hasResult {
		v.logger.Debug("Became leader but no validation result available yet, skipping state replay")
		return
	}

	if succeeded {
		v.logger.Debug("Became leader, re-publishing last validation result (success) for DeploymentScheduler",
			"warnings", len(warnings),
			"duration_ms", durationMs,
			"correlation_id", correlationID,
			"trigger_reason", triggerReason)

		// Re-publish with original correlation ID so the deployment can be traced
		v.eventBus.Publish(events.NewValidationCompletedEvent(
			warnings,
			durationMs,
			triggerReason,
			events.WithCorrelation(correlationID, correlationID),
		))
	} else {
		v.logger.Debug("Became leader, last validation failed, skipping state replay")
		// Note: We only replay ValidationCompletedEvent (success), not ValidationFailedEvent.
		// DeploymentScheduler only acts on successful validation, so replaying failures
		// would be unnecessary and could cause confusion.
	}
}

// publishValidationFailure publishes a validation failure event and caches the failure state.
// If configHash is provided (non-empty), the hash and errors are cached to skip re-validation
// of identical failed configs (reduces log spam).
func (v *HAProxyValidatorComponent) publishValidationFailure(errors []string, durationMs int64, correlationID, triggerReason, configHash string) {
	// Cache validation failure for leadership transition state
	v.mu.Lock()
	v.lastValidationSucceeded = false
	v.lastValidationDurationMs = durationMs
	v.lastCorrelationID = correlationID
	v.lastTriggerReason = triggerReason
	v.hasValidationResult = true
	// Cache config hash and errors to skip re-validation of identical failed configs
	if configHash != "" {
		v.lastConfigHash = configHash
		v.lastValidationErrors = errors
	}
	v.mu.Unlock()

	v.eventBus.Publish(events.NewValidationFailedEvent(
		errors,
		durationMs,
		triggerReason,
		events.WithCorrelation(correlationID, correlationID),
	))
}

// computeConfigHash computes a SHA256 hash of the HAProxy config content.
// Used to detect identical configs for caching purposes.
func (v *HAProxyValidatorComponent) computeConfigHash(config string) string {
	hash := sha256.Sum256([]byte(config))
	return hex.EncodeToString(hash[:])
}

// cleanupValidationTempDir removes the validation temp directory.
// Called via defer in handleTemplateRendered to ensure cleanup happens regardless of validation outcome.
// Logs a warning on failure but does not return an error since cleanup is best-effort.
func (v *HAProxyValidatorComponent) cleanupValidationTempDir(tempDir string) {
	if tempDir == "" {
		return
	}

	if err := os.RemoveAll(tempDir); err != nil {
		v.logger.Warn("Failed to clean up validation temp directory",
			"path", tempDir,
			"error", err)
	}
}

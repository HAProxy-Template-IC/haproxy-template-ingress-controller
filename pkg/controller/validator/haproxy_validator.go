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
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/coalesce"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"
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

	// State replay for leadership transitions (only successful validations)
	validationReplayer *leadership.StateReplayer[*events.ValidationCompletedEvent]

	// State protected by mutex (for config caching)
	mu                      sync.RWMutex
	lastValidationSucceeded bool
	hasValidationResult     bool

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
		eventBus:           eventBus,
		eventChan:          eventChan,
		logger:             logger,
		validationReplayer: leadership.NewStateReplayer[*events.ValidationCompletedEvent](eventBus),
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

// handleTemplateRendered implements "latest wins" coalescing for template rendered events.
//
// When multiple coalescible TemplateRenderedEvents arrive while validation is in progress,
// intermediate events are superseded - only the latest pending event is processed.
// This prevents queue backlog where validation can't keep up with high-frequency renders.
//
// Non-coalescible events (e.g., from drift_prevention) are always processed and never skipped.
//
// Uses the centralized coalesce.DrainLatest utility for consistent behavior across components.
func (v *HAProxyValidatorComponent) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	// Process current event
	v.performValidation(event)

	// After validation completes, drain the event channel for any pending coalescible events.
	// Since the event loop is single-threaded, events buffer in eventChan while performValidation executes.
	// We process only the latest coalescible event, handling other event types normally.
	for {
		latest, supersededCount := coalesce.DrainLatest[*events.TemplateRenderedEvent](
			v.eventChan,
			v.handleEvent, // Handle non-coalescible and other event types
		)
		if latest == nil {
			return
		}

		if supersededCount > 0 {
			v.logger.Debug("Coalesced template rendered events",
				"superseded_count", supersededCount,
				"processing", latest.CorrelationID())
		}
		v.performValidation(latest)
	}
}

// performValidation validates the rendered HAProxy configuration.
// Propagates correlation ID and trigger reason from the triggering event to validation events.
// This method is called by handleTemplateRendered after coalescing logic.
//
// The validator creates its own temp directory for validation. The config uses relative paths
// (maps/, ssl/, files/) that work with HAProxy's `default-path config` directive.
func (v *HAProxyValidatorComponent) performValidation(event *events.TemplateRenderedEvent) {
	startTime := time.Now()
	correlationID := event.CorrelationID()
	triggerReason := event.TriggerReason

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

	auxiliaryFiles := event.AuxiliaryFiles

	// Create isolated temp directory for validation
	// The config uses relative paths (maps/, ssl/, files/) that we create as subdirectories
	tempDir, err := os.MkdirTemp("", "haproxy-validation-*")
	if err != nil {
		v.publishValidationFailure(
			[]string{"failed to create temp directory: " + err.Error()},
			time.Since(startTime).Milliseconds(),
			correlationID,
			triggerReason,
			"", // Don't cache infrastructure errors
		)
		return
	}

	// Ensure cleanup happens regardless of validation outcome
	defer v.cleanupValidationTempDir(tempDir)

	// Build validation paths matching the relative subdirectories used in the config.
	// CRTListDir uses "files" because CRT-list files are always stored in general
	// file storage to avoid triggering HAProxy reloads (see pkg/dataplane/auxiliaryfiles/crtlist.go).
	validationPaths := &dataplane.ValidationPaths{
		TempDir:           tempDir,
		MapsDir:           filepath.Join(tempDir, "maps"),
		SSLCertsDir:       filepath.Join(tempDir, "ssl"),
		CRTListDir:        filepath.Join(tempDir, "files"),
		GeneralStorageDir: filepath.Join(tempDir, "files"),
		ConfigFile:        filepath.Join(tempDir, "haproxy.cfg"),
	}

	// Validate configuration using the single HAProxy config
	// Config uses relative paths that match the validation subdirectories
	// Pass nil version to use default v3.0 schema (safest for validation)
	// Use permissive validation (skipDNSValidation=true) to prevent blocking when DNS fails at runtime
	parsedConfig, err := dataplane.ValidateConfiguration(event.HAProxyConfig, auxiliaryFiles, validationPaths, nil, true)
	if err != nil && !errors.Is(err, dataplane.ErrValidationCacheHit) {
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
	// ErrValidationCacheHit means validation was skipped because config was already validated
	// parsedConfig will be nil in this case - caller should use parser cache if needed

	// Validation succeeded
	durationMs := time.Since(startTime).Milliseconds()

	// Clear config hash cache since validation succeeded (new successful config)
	v.mu.Lock()
	v.lastValidationSucceeded = true
	v.hasValidationResult = true
	v.lastConfigHash = ""        // Clear cache - new valid config
	v.lastValidationErrors = nil // Clear cached errors
	v.mu.Unlock()

	completedEvent := events.NewValidationCompletedEvent(
		[]string{}, // No warnings
		durationMs,
		triggerReason,
		parsedConfig,
		event.Coalescible(),
		events.PropagateCorrelation(event),
	)

	// Cache successful validation for leadership transition replay
	v.validationReplayer.Cache(completedEvent)

	v.eventBus.Publish(completedEvent)
}

// handleBecameLeader handles BecameLeaderEvent by re-publishing the last validation result.
//
// This ensures DeploymentScheduler (which starts subscribing only after becoming leader)
// receives the current validation state, even if validation occurred before leadership was acquired.
//
// This prevents the "late subscriber problem" where leader-only components miss events
// that were published before they started subscribing.
func (v *HAProxyValidatorComponent) handleBecameLeader(_ *events.BecameLeaderEvent) {
	if !v.validationReplayer.HasState() {
		v.logger.Debug("Became leader but no validation result available yet, skipping state replay")
		return
	}

	// StateReplayer only caches successful validations (Cache is called only on success)
	v.logger.Debug("Became leader, re-publishing last validation result (success) for DeploymentScheduler")
	v.validationReplayer.Replay()
}

// publishValidationFailure publishes a validation failure event and caches the failure state.
// If configHash is provided (non-empty), the hash and errors are cached to skip re-validation
// of identical failed configs (reduces log spam).
func (v *HAProxyValidatorComponent) publishValidationFailure(errs []string, durationMs int64, correlationID, triggerReason, configHash string) {
	// Cache validation failure state (not replayed on leadership transition)
	v.mu.Lock()
	v.lastValidationSucceeded = false
	v.hasValidationResult = true
	// Cache config hash and errors to skip re-validation of identical failed configs
	if configHash != "" {
		v.lastConfigHash = configHash
		v.lastValidationErrors = errs
	}
	v.mu.Unlock()

	v.eventBus.Publish(events.NewValidationFailedEvent(
		errs,
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

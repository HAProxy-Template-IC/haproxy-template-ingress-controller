package configchange

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "configchange-handler"

	// EventBufferSize is the size of the event subscription buffer.
	// Size 50: Moderate-volume component handling config and validation events.
	EventBufferSize = 50

	// DefaultReinitDebounceInterval is the default time to wait after the last config
	// change before signaling controller reinitialization. This allows rapid CRD updates
	// to be coalesced, ensuring templates are fully rendered before reinitialization starts.
	DefaultReinitDebounceInterval = 500 * time.Millisecond
)

// ConfigChangeHandler coordinates configuration validation and detects config changes.
//
// This component has two main responsibilities:
//
//  1. Validation Coordination: Subscribes to ConfigParsedEvent, sends ConfigValidationRequest,
//     collects responses from validators using scatter-gather pattern, and publishes
//     ConfigValidatedEvent or ConfigInvalidEvent.
//
//  2. Change Detection: Subscribes to ConfigValidatedEvent and signals the controller to
//     reinitialize via the configChangeCh channel with debouncing to coalesce rapid changes.
//
// Debouncing behavior:
// When multiple CRD config changes arrive in rapid succession, the handler debounces the
// reinitialization signal. This ensures all pending renders complete before reinitialization
// starts, preventing the race condition where reinitialization cancels in-progress renders.
//
// Architecture:
// This component bridges the gap between configuration parsing and validation, and between
// validation and controller reinitialization. It uses the scatter-gather pattern from the
// event bus for coordinating validation across multiple validators.
type ConfigChangeHandler struct {
	eventBus       *busevents.EventBus
	eventChan      <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	logger         *slog.Logger
	configChangeCh chan<- *coreconfig.Config
	validators     []string
	stopCh         chan struct{}

	// State caching for leader election replay (prevents "late subscriber problem")
	mu                       sync.RWMutex
	lastConfigValidatedEvent *events.ConfigValidatedEvent
	hasValidatedConfig       bool

	// Debouncing for reinitialization signals
	// Coalesces rapid CRD config changes to prevent reinitialization from interrupting renders
	debounceInterval time.Duration
	debounceTimer    *time.Timer
	pendingConfig    *coreconfig.Config

	// Initial config version tracking to prevent infinite reinitialization loop
	// When a new iteration starts, CRDWatcher triggers onAdd for the existing CRD,
	// publishing ConfigValidatedEvent with the same version as the initial config.
	// Without tracking this version, ConfigChangeHandler would trigger reinitialization
	// for the bootstrap event, creating an infinite loop.
	initialConfigVersion string
}

// NewConfigChangeHandler creates a new ConfigChangeHandler.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//   - configChangeCh: Channel to signal controller reinitialization with validated config
//   - validators: List of expected validator names (e.g., ["basic", "template", "jsonpath"])
//   - debounceInterval: Time to wait after last config change before triggering reinitialization.
//     Use 0 for default (500ms).
//
// Returns:
//   - *ConfigChangeHandler ready to start
func NewConfigChangeHandler(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	configChangeCh chan<- *coreconfig.Config,
	validators []string,
	debounceInterval time.Duration,
) *ConfigChangeHandler {
	if debounceInterval <= 0 {
		debounceInterval = DefaultReinitDebounceInterval
	}

	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &ConfigChangeHandler{
		eventBus:         eventBus,
		eventChan:        eventChan,
		logger:           logger.With("component", ComponentName),
		configChangeCh:   configChangeCh,
		validators:       validators,
		stopCh:           make(chan struct{}),
		debounceInterval: debounceInterval,
		debounceTimer:    nil,
		pendingConfig:    nil,
	}
}

// SetInitialConfigVersion sets the initial config version to prevent reinitialization
// on bootstrap ConfigValidatedEvent.
//
// This must be called after fetching the initial config but before CRDWatcher starts.
// When CRDWatcher's informer triggers onAdd for the existing CRD, it publishes a
// ConfigValidatedEvent with the same version. Without this tracking, that event would
// trigger reinitialization, creating an infinite loop.
//
// Parameters:
//   - version: The resourceVersion from the initial CRD fetch
func (h *ConfigChangeHandler) SetInitialConfigVersion(version string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.initialConfigVersion = version
	h.logger.Debug("Set initial config version for bootstrap skip",
		"version", version)
}

// Start begins processing events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// The component is already subscribed to the EventBus (subscription happens in constructor).
// Returns nil on graceful shutdown.
//
// Example:
//
//	go handler.Start(ctx)
func (h *ConfigChangeHandler) Start(ctx context.Context) error {
	h.logger.Debug("config change handler starting", "validators", h.validators)

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("ConfigChangeHandler shutting down", "reason", ctx.Err())
			h.cleanup()
			return nil
		case <-h.stopCh:
			h.logger.Info("ConfigChangeHandler shutting down")
			h.cleanup()
			return nil
		case <-h.getDebounceTimerChan():
			// Debounce timer expired - send pending config
			h.sendPendingConfig()
		case event := <-h.eventChan:
			switch e := event.(type) {
			case *events.ConfigParsedEvent:
				h.handleConfigParsed(ctx, e)
			case *events.ConfigValidatedEvent:
				h.handleConfigValidated(e)
			case *events.BecameLeaderEvent:
				h.handleBecameLeader(e)
			}
		}
	}
}

// Stop gracefully stops the component.
func (h *ConfigChangeHandler) Stop() {
	h.stopDebounceTimer()
	close(h.stopCh)
}

// cleanup performs cleanup when the component is shutting down.
func (h *ConfigChangeHandler) cleanup() {
	h.stopDebounceTimer()
}

// handleConfigParsed coordinates validation for a parsed config using scatter-gather pattern.
func (h *ConfigChangeHandler) handleConfigParsed(ctx context.Context, event *events.ConfigParsedEvent) {
	// If no validators are configured, skip validation and immediately publish validated event
	if len(h.validators) == 0 {
		h.logger.Debug("No validators configured, skipping validation", "version", event.Version)

		validatedEvent := events.NewConfigValidatedEvent(
			event.Config,
			event.TemplateConfig,
			event.Version,
			event.SecretVersion,
		)

		// Cache the event for leadership transition replay
		h.mu.Lock()
		h.lastConfigValidatedEvent = validatedEvent
		h.hasValidatedConfig = true
		h.mu.Unlock()

		h.eventBus.Publish(validatedEvent)
		return
	}

	h.logger.Info("Coordinating config validation", "version", event.Version)

	// Create validation request
	req := events.NewConfigValidationRequest(event.Config, event.Version)

	// Send request and wait for responses using scatter-gather
	// Timeout is set to 10 seconds based on expected validation performance:
	// - Small configs (10 templates, 5 JSONPaths): ~50-100ms
	// - Medium configs (100 templates, 20 JSONPaths): ~200-500ms
	// - Large configs (1000 templates, 100 JSONPaths): ~2-5 seconds
	// The 10s timeout provides adequate headroom even for very large configs
	// or systems under high CPU pressure. If validation consistently approaches
	// this timeout, consider investigating performance bottlenecks.
	result, err := h.eventBus.Request(ctx, req, busevents.RequestOptions{
		Timeout:            10 * time.Second,
		ExpectedResponders: h.validators,
	})

	if err != nil {
		h.logger.Error("Config validation request failed",
			"error", err,
			"version", event.Version)
		// Publish invalid event
		h.eventBus.Publish(events.NewConfigInvalidEvent(event.Version, map[string][]string{
			"coordinator": {err.Error()},
		}))
		return
	}

	// Collect validation errors
	validationErrors := make(map[string][]string)
	allValid := true

	for _, resp := range result.Responses {
		validationResp, ok := resp.(*events.ConfigValidationResponse)
		if !ok {
			h.logger.Warn("Received non-ConfigValidationResponse",
				"type", fmt.Sprintf("%T", resp))
			continue
		}

		if !validationResp.Valid {
			allValid = false
			validationErrors[validationResp.ValidatorName] = validationResp.Errors
		}
	}

	// Check for missing responders
	if len(result.Errors) > 0 {
		allValid = false
		validationErrors["coordinator"] = result.Errors
	}

	if allValid {
		h.logger.Info("Config validation succeeded", "version", event.Version)

		validatedEvent := events.NewConfigValidatedEvent(
			event.Config,
			event.TemplateConfig,
			event.Version,
			event.SecretVersion,
		)

		// Cache the event for leadership transition replay
		h.mu.Lock()
		h.lastConfigValidatedEvent = validatedEvent
		h.hasValidatedConfig = true
		h.mu.Unlock()

		// Publish validated event
		h.eventBus.Publish(validatedEvent)
	} else {
		h.logger.Warn("Config validation failed",
			"version", event.Version,
			"error_count", len(validationErrors))
		// Publish invalid event
		h.eventBus.Publish(events.NewConfigInvalidEvent(event.Version, validationErrors))
	}
}

// handleConfigValidated signals controller reinitialization when config is validated.
//
// Reinitialization signals are debounced to coalesce rapid CRD config changes.
// This prevents the race condition where reinitialization interrupts in-progress renders,
// ensuring all config changes are fully rendered before reinitialization starts.
func (h *ConfigChangeHandler) handleConfigValidated(event *events.ConfigValidatedEvent) {
	// Cache the event for leadership transition replay
	// Read initialConfigVersion while holding the lock
	h.mu.Lock()
	h.lastConfigValidatedEvent = event
	h.hasValidatedConfig = true
	initialVersion := h.initialConfigVersion
	h.mu.Unlock()

	// Ignore initial bootstrap events:
	// 1. Version "initial" - synthetic bootstrap events
	// 2. Version matches initialConfigVersion - CRDWatcher.onAdd for existing CRD at iteration start
	// Without this check, the CRDWatcher bootstrap event would trigger reinitialization,
	// creating an infinite loop: start → onAdd → ConfigValidatedEvent → reinit → start → ...
	if event.Version == "initial" {
		h.logger.Debug("Ignoring synthetic bootstrap ConfigValidatedEvent (version='initial')")
		return
	}
	if initialVersion != "" && event.Version == initialVersion {
		h.logger.Debug("Ignoring bootstrap ConfigValidatedEvent (matches initial config version)",
			"version", event.Version)
		return
	}

	// Extract the config
	cfg, ok := event.Config.(*coreconfig.Config)
	if !ok {
		h.logger.Error("ConfigValidatedEvent contains invalid config type",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", event.Config))
		return
	}

	// Store config and reset debounce timer
	// The timer callback will send the config after the debounce interval
	h.pendingConfig = cfg

	h.logger.Debug("Config validated, reinitialization debounced",
		"version", event.Version)

	h.resetDebounceTimer()
}

// resetDebounceTimer resets the debounce timer to the configured interval.
func (h *ConfigChangeHandler) resetDebounceTimer() {
	if h.debounceTimer == nil {
		// Create timer on first use
		h.debounceTimer = time.NewTimer(h.debounceInterval)
	} else {
		// Stop and drain existing timer before resetting
		if !h.debounceTimer.Stop() {
			// Timer already fired, drain the channel
			select {
			case <-h.debounceTimer.C:
			default:
			}
		}
		h.debounceTimer.Reset(h.debounceInterval)
	}
}

// stopDebounceTimer stops the debounce timer if it's running.
func (h *ConfigChangeHandler) stopDebounceTimer() {
	if h.debounceTimer != nil {
		if !h.debounceTimer.Stop() {
			// Timer already fired, drain the channel
			select {
			case <-h.debounceTimer.C:
			default:
			}
		}
	}
	h.pendingConfig = nil
}

// getDebounceTimerChan returns the debounce timer's channel or a nil channel
// if the timer hasn't been created yet.
//
// This allows the select statement to work correctly - a nil channel blocks forever,
// which is the desired behavior when there's no active debounce timer.
func (h *ConfigChangeHandler) getDebounceTimerChan() <-chan time.Time {
	if h.debounceTimer == nil {
		return nil
	}
	return h.debounceTimer.C
}

// sendPendingConfig sends the pending config to the controller.
// This is called after the debounce interval expires, ensuring rapid config changes are coalesced.
func (h *ConfigChangeHandler) sendPendingConfig() {
	cfg := h.pendingConfig
	h.pendingConfig = nil

	if cfg == nil {
		// No pending config (e.g., already sent or cleared)
		return
	}

	h.logger.Info("Signaling controller reinitialization after debounce")

	// Signal controller to reinitialize
	// Use non-blocking send to avoid deadlock if channel is full
	select {
	case h.configChangeCh <- cfg:
		h.logger.Debug("Reinitialization signal sent")
	default:
		h.logger.Warn("Failed to send reinitialization signal: channel full")
	}
}

// handleBecameLeader handles BecameLeaderEvent by re-publishing the last validated config.
//
// This ensures ConfigPublisher (which starts subscribing only after becoming leader)
// receives the current validated config state, even if validation occurred before leadership
// was acquired.
//
// This prevents the "late subscriber problem" where leader-only components miss events
// that were published before they started subscribing.
func (h *ConfigChangeHandler) handleBecameLeader(_ *events.BecameLeaderEvent) {
	h.mu.RLock()
	hasState := h.hasValidatedConfig
	validatedEvent := h.lastConfigValidatedEvent
	h.mu.RUnlock()

	if !hasState {
		h.logger.Debug("Became leader but no validated config available yet, skipping state replay")
		return
	}

	h.logger.Debug("Became leader, re-publishing last validated config for leader-only components",
		"config_version", validatedEvent.Version,
		"secret_version", validatedEvent.SecretVersion)

	// Re-publish the last validated event to ensure new leader-only components receive it
	h.eventBus.Publish(validatedEvent)
}

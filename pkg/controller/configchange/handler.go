package configchange

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timers"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "configchange-handler"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate-volume component handling config and validation events.
	EventBufferSize = busevents.StandardSubscriberBuffer
)

// DefaultReinitDebounceInterval is the default time to wait after the last config
// change before signaling controller reinitialization. This allows rapid CRD updates
// to be coalesced, ensuring templates are fully rendered before reinitialization starts.
// Reuses the lenient per-watcher debounce default (types.DefaultDebounceInterval, 2s):
// CRD config edits are operator-initiated and tolerate a couple of seconds of
// coalescing, like other structural changes.
var DefaultReinitDebounceInterval = types.DefaultDebounceInterval

// syntheticBootstrapVersion is the literal version string webhook.go
// stamps on the placeholder ConfigValidatedEvent and
// CredentialsUpdatedEvent published during iteration startup so
// subscribers (discovery, status_updater, etc.) get a known-good
// kick before the real watcher onAdd. Multiple handlers in this
// package check against it to skip the synthetic path; centralised
// here so a future rename can't desync the checks.
const syntheticBootstrapVersion = "initial"

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

	// State replay for leadership transitions (prevents "late subscriber problem")
	configReplayer *leadership.StateReplayer[*events.ConfigValidatedEvent]

	// Debouncing for reinitialization signals
	// Coalesces rapid CRD config changes to prevent reinitialization from interrupting renders
	debounceInterval time.Duration
	debounceTimer    timers.SafeTimer
	pendingConfig    *coreconfig.Config

	// Mutex for initialConfigVersion and reinitializationEnabled
	mu sync.RWMutex

	// Initial config version tracking to prevent infinite reinitialization loop
	// When a new iteration starts, CRDWatcher triggers onAdd for the existing CRD,
	// publishing ConfigValidatedEvent with the same version as the initial config.
	// Without tracking this version, ConfigChangeHandler would trigger reinitialization
	// for the bootstrap event, creating an infinite loop.
	initialConfigVersion string

	// Initial credentials Secret version tracked the same way, for the same
	// reason. The credentialsloader emits CredentialsUpdatedEvent on every
	// Secret resync — including the bootstrap event the watcher fires the
	// moment it observes the existing Secret at iteration startup. Filtering
	// by version means we only signal reinitialization when the Secret
	// content actually changed (rotation), not when the watcher just
	// re-observes the same Secret.
	initialCredentialsVersion string

	// reinitializationEnabled controls whether ConfigValidatedEvents trigger reinitialization.
	// During startup, multiple ConfigValidatedEvents can occur:
	// 1. Synthetic event (version="initial") - always skipped
	// 2. Watcher event from OnSyncComplete - skipped during bootstrap
	// All events are skipped until EnableReinitialization() is called after startup completes.
	// Note: CRDWatcher uses generation-based filtering, so status-only updates don't
	// trigger ConfigValidatedEvents in the first place.
	reinitializationEnabled bool
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

	// Subscribe to only the event types we handle during construction (before EventBus.Start())
	// This ensures proper startup synchronization and reduces buffer pressure.
	// CredentialsUpdated and CertParsed are subscribed here so that a
	// rotation of either Secret triggers iteration restart through the
	// same configChangeCh path the CRD already uses — there's no parallel
	// "reload loop"; everything funnels through ConfigChangeHandler.
	eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeConfigParsed,
		events.EventTypeConfigValidated,
		events.EventTypeBecameLeader,
		events.EventTypeCredentialsUpdated,
	)

	return &ConfigChangeHandler{
		eventBus:         eventBus,
		eventChan:        eventChan,
		logger:           logger.With("component", ComponentName),
		configChangeCh:   configChangeCh,
		validators:       validators,
		configReplayer:   leadership.NewStateReplayer[*events.ConfigValidatedEvent](eventBus),
		stopCh:           make(chan struct{}),
		debounceInterval: debounceInterval,
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

// SetInitialCredentialsVersion records the resourceVersion of the
// credentials Secret as observed at iteration startup, so the bootstrap
// CredentialsUpdatedEvent (fired by the watcher's initial onAdd) doesn't
// trigger a redundant reinitialization loop.
func (h *ConfigChangeHandler) SetInitialCredentialsVersion(version string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.initialCredentialsVersion = version
	h.logger.Debug("Set initial credentials Secret version for bootstrap skip",
		"version", version)
}

// EnableReinitialization enables the reinitialization signaling mechanism.
//
// This must be called after controller startup is complete to allow config changes
// to trigger reinitialization. During startup, ConfigValidatedEvents occur that
// should NOT trigger reinitialization. Calling this method signals that the startup
// phase is complete and future config changes should trigger reinitialization.
//
// Note: CRDWatcher uses generation-based filtering, so status-only updates (which
// don't increment generation) never trigger ConfigValidatedEvents in the first place.
func (h *ConfigChangeHandler) EnableReinitialization() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reinitializationEnabled = true
	h.logger.Debug("Reinitialization signaling enabled (startup complete)")
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
		case <-h.debounceTimer.Chan():
			h.debounceTimer.Fired()
			h.sendPendingConfig()
		case event := <-h.eventChan:
			switch e := event.(type) {
			case *events.ConfigParsedEvent:
				h.handleConfigParsed(ctx, e)
			case *events.ConfigValidatedEvent:
				h.handleConfigValidated(e)
			case *events.BecameLeaderEvent:
				h.handleBecameLeader(e)
			case *events.CredentialsUpdatedEvent:
				h.handleSecretRotation("credentials", e.SecretVersion, &h.initialCredentialsVersion)
			}
		}
	}
}

// Stop gracefully stops the component.
func (h *ConfigChangeHandler) Stop() {
	h.debounceTimer.Stop()
	h.pendingConfig = nil
	close(h.stopCh)
}

// cleanup performs cleanup when the component is shutting down.
func (h *ConfigChangeHandler) cleanup() {
	h.debounceTimer.Stop()
	h.pendingConfig = nil
}

// handleConfigParsed coordinates validation for a parsed config using scatter-gather pattern.
func (h *ConfigChangeHandler) handleConfigParsed(ctx context.Context, event *events.ConfigParsedEvent) {
	// If no validators are configured, skip validation and immediately publish validated event
	if len(h.validators) == 0 {
		h.logger.Debug("No validators configured, skipping validation", "version", event.Version)
		h.publishValidated(event)
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
		// Publish invalid event with TemplateConfig reference for status updates
		h.eventBus.Publish(events.NewConfigInvalidEvent(event.Version, event.TemplateConfig, map[string][]string{
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
		h.publishValidated(event)
	} else {
		h.logger.Error("Config validation failed",
			"version", event.Version,
			"error_count", len(validationErrors),
			"validation_errors", validationErrors)
		// Publish invalid event with TemplateConfig reference for status updates
		h.eventBus.Publish(events.NewConfigInvalidEvent(event.Version, event.TemplateConfig, validationErrors))
	}
}

// publishValidated builds a ConfigValidatedEvent from the parsed event,
// caches it for leadership-transition replay, and publishes it. Used by both
// the no-validators short-circuit and the all-valid branch of
// handleConfigParsed.
func (h *ConfigChangeHandler) publishValidated(event *events.ConfigParsedEvent) {
	validatedEvent := events.NewConfigValidatedEvent(
		event.Config,
		event.TemplateConfig,
		event.Version,
		event.SecretVersion,
	)
	h.configReplayer.Cache(validatedEvent)
	h.eventBus.Publish(validatedEvent)
}

// handleConfigValidated signals controller reinitialization when config is validated.
//
// Reinitialization signals are debounced to coalesce rapid CRD config changes.
// This prevents the race condition where reinitialization interrupts in-progress renders,
// ensuring all config changes are fully rendered before reinitialization starts.
func (h *ConfigChangeHandler) handleConfigValidated(event *events.ConfigValidatedEvent) {
	// Always cache the event for leadership transition replay
	h.configReplayer.Cache(event)

	// Skip synthetic bootstrap events (version="initial") - these don't trigger reinitialization
	if event.Version == syntheticBootstrapVersion {
		h.logger.Debug("Ignoring synthetic bootstrap ConfigValidatedEvent (version='initial')")
		return
	}

	// Read reinitialization state
	h.mu.RLock()
	reinitEnabled := h.reinitializationEnabled
	initialVersion := h.initialConfigVersion
	h.mu.RUnlock()

	// During startup (before EnableReinitialization is called), skip all events.
	// Multiple events occur during startup (watcher sync) that should not trigger
	// reinitialization. Note: CRDWatcher uses generation-based filtering, so status-only
	// updates never trigger ConfigValidatedEvents in the first place.
	if !reinitEnabled {
		h.logger.Debug("Ignoring ConfigValidatedEvent (reinitialization disabled during startup)",
			"version", event.Version)
		return
	}

	// Version-based check as safety fallback (e.g., if SetInitialConfigVersion was called)
	if initialVersion != "" && event.Version == initialVersion {
		h.logger.Debug("Ignoring ConfigValidatedEvent (matches initial config version)",
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

	h.debounceTimer.Reset(h.debounceInterval)
}

// handleSecretRotation reacts to a Secret-rotation event (credentials or
// webhook-cert) by signalling iteration restart through the same
// configChangeCh path used for CRD changes. The bootstrap event the
// watcher fires when it first observes the Secret is filtered out by
// comparing against the initial version recorded at iteration startup.
//
// Because the iteration restart re-runs fetchAndValidateInitialConfig,
// the new iteration loads the rotated Secret from the API server before
// any component (notably the webhook server) starts up — there's no
// hot-rotation in any individual component. This mirrors how CRD changes
// flow through the same channel.
func (h *ConfigChangeHandler) handleSecretRotation(kind, version string, initialVersion *string) {
	// Skip synthetic bootstrap events (version="initial"). webhook.go
	// publishes a placeholder CredentialsUpdatedEvent("initial") during
	// iteration startup so components subscribing to credentials state
	// (discovery, etc.) get a known-good kick before the real
	// CredentialsUpdatedEvent from the watcher's onAdd arrives. The
	// synthetic carries the literal string "initial" which never
	// matches the real Secret resourceVersion recorded via
	// SetInitialCredentialsVersion, so without this skip it would slip
	// past the bootstrap-match check below and trigger an iteration
	// restart ~1s after every startup. Mirrors the same check in
	// handleConfigValidated (line 363) — same root cause, same fix.
	//
	// Root cause for issue #46: that spurious restart raced
	// UpdateBlocklistAndRestart in the HTTP-store invalid-update
	// acceptance test. When the new iteration's empty HTTPStore ran
	// its first fetch, the blocklist server had already swapped to
	// invalid content; the live-fetch path cached invalid as accepted
	// (because Fetch stores initial-fetch results directly as accepted
	// with no validation), then HAProxy semantic validation rejected
	// the rendered config, and the test's debug-endpoint query saw
	// "no files rendered yet".
	if version == syntheticBootstrapVersion {
		h.logger.Debug("Ignoring synthetic bootstrap " + kind + " event (version='initial')")
		return
	}

	h.mu.RLock()
	reinitEnabled := h.reinitializationEnabled
	bootstrap := *initialVersion
	h.mu.RUnlock()

	if !reinitEnabled {
		h.logger.Debug("Ignoring "+kind+" Secret rotation (reinitialization disabled during startup)",
			"version", version)
		return
	}
	if bootstrap != "" && version == bootstrap {
		h.logger.Debug("Ignoring "+kind+" Secret bootstrap event (matches initial version)",
			"version", version)
		return
	}

	cfg, ok := h.configReplayer.Get()
	if !ok || cfg == nil {
		h.logger.Warn("Cannot signal reinitialization for "+kind+" rotation: no validated config cached",
			"version", version)
		return
	}
	parsed, ok := cfg.Config.(*coreconfig.Config)
	if !ok {
		h.logger.Error("Cached config event has unexpected type",
			"kind", kind,
			"got", fmt.Sprintf("%T", cfg.Config))
		return
	}

	h.logger.Info("Secret rotation detected; debouncing iteration restart",
		"kind", kind,
		"version", version)
	h.pendingConfig = parsed
	h.debounceTimer.Reset(h.debounceInterval)
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
	event, ok := h.configReplayer.Get()
	if !ok {
		h.logger.Debug("Became leader but no validated config available yet, skipping state replay")
		return
	}

	h.logger.Debug("Became leader, re-publishing last validated config for leader-only components",
		"config_version", event.Version,
		"secret_version", event.SecretVersion)

	h.configReplayer.Replay()
}

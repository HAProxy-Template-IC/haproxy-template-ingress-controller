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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "configchange-handler"

	// EventBufferSize is the size of the event subscription buffer.
	// Moderate-volume component handling config and validation events.
	EventBufferSize = busevents.StandardSubscriberBuffer
)

// The config-validation scatter-gather timeout is computed per request from
// validator.SuiteValidationEnvelope(len(cfg.ValidationTests)) — the
// validationtests validator's suite-size-scaled run budget plus a fixed
// bootstrap/setup/slack margin — so the envelope is strictly LARGER than the
// validator's own worst case for ANY suite size, and that validator always
// self-reports its result (pass / "invalid: did not complete") rather than
// being declared a missing responder. Config-load only: rare,
// operator-initiated, never on the reconciliation hot path, so a budget well
// above the sub-second structural validators is correct (#77).

// DefaultReinitDebounceInterval is the default time to wait after the last config
// change before signaling controller reinitialization. This allows rapid CRD updates
// to be coalesced, ensuring templates are fully rendered before reinitialization starts.
// Stays lenient where the per-watcher default (types.DefaultDebounceInterval) is
// near zero: reinit tears down and rebuilds every informer, so coalescing a burst
// of operator-initiated CRD edits is worth the added seconds.
const DefaultReinitDebounceInterval = 2 * time.Second

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
	configChangeCh chan *ReloadRequest
	validators     []string

	// State replay for leadership transitions (prevents "late subscriber problem")
	configReplayer *leadership.StateReplayer[*events.ConfigValidatedEvent]

	// Debouncing for reinitialization signals
	// Coalesces rapid CRD config changes to prevent reinitialization from interrupting renders
	debounceInterval time.Duration
	debounceTimer    timers.SafeTimer
	pendingReload    *ReloadRequest

	// Async-validation single-flight state, owned by the Start loop goroutine
	// (validationDone is the only cross-goroutine member and is a channel).
	//
	// The validation scatter-gather can take tens of seconds (the
	// validationtests validator runs the config's entire embedded suite), so
	// running it inline in the event loop starves every other subscribed
	// event for its whole duration. The decisive victim is BecameLeaderEvent:
	// its state replay is what hands the last validated config to leader-only
	// components (notably the config-publisher, which subscribes only on
	// leadership). Observed in issue #55: a leadership acquisition landed
	// while a post-reinitialization validation was in flight, the replay sat
	// queued behind the blocked loop, and the publisher dropped every
	// HAProxyCfg publish with "missing cached state" until validation
	// finished (~15s) — blowing the e2e convergence budget.
	//
	// Validation therefore runs in a spawned goroutine while the loop keeps
	// dispatching side events. Single-flight keeps ConfigValidatedEvents
	// strictly ordered: at most one validation is in flight, and a parsed
	// event arriving meanwhile waits in queuedParsed (latest wins — a
	// superseded config is never validated).
	validationInFlight  bool
	queuedParsed        *validationCandidate
	validationDone      chan validationOutcome
	candidateGeneration uint64
	startupReplay       chan struct{}
	effectiveReload     chan struct{}

	// Mutex for initialConfigVersion and reinitializationEnabled
	mu sync.RWMutex

	// effectiveResolver, when set (SetEffectiveResolver), transforms a parsed
	// config into the EFFECTIVE config before validation: watched-resource
	// candidate versions resolved against live discovery, features whose
	// optional resources are unavailable stripped. Validators must judge
	// exactly what a reinitialized iteration would load — validating the raw
	// config would reject configs whose stripped snippets reference
	// unavailable resources. Nil (tests, callers without discovery) means
	// identity.
	effectiveResolver func(*coreconfig.Config) (*ResolvedConfig, error)

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

	// reinitializationEnabled separates bootstrap echoes from concurrent updates.
	// Exact startup versions are ignored; newer versions are queued for replay.
	reinitializationEnabled bool

	// Changes newer than the fetched startup versions are replayed after startup.
	pendingStartupConfig      *ValidatedSnapshot
	pendingStartupCredentials *events.CredentialsUpdatedEvent

	currentCredentials        *coreconfig.Credentials
	currentCredentialsVersion string
	activeSnapshot            *ValidatedSnapshot
	acceptedCandidate         *ValidatedSnapshot
	activeReplay              *events.ConfigValidatedEvent
	credentialsDirty          bool
}

type validationCandidate struct {
	generation uint64
	event      *events.ConfigParsedEvent
}

type validationOutcome struct {
	candidate        *validationCandidate
	rawConfig        *coreconfig.Config
	resolved         *ResolvedConfig
	valid            bool
	validationErrors map[string][]string
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
	configChangeCh chan *ReloadRequest,
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
		debounceInterval: debounceInterval,
		// Capacity 1 suffices: single-flight means at most one validation
		// goroutine has a completion to signal, so the send never blocks.
		validationDone:  make(chan validationOutcome, 1),
		startupReplay:   make(chan struct{}, 1),
		effectiveReload: make(chan struct{}, 1),
	}
}

// RequestEffectiveReload asks the handler to restart from its accepted raw
// config after resolving API versions again.
func (h *ConfigChangeHandler) RequestEffectiveReload() {
	select {
	case h.effectiveReload <- struct{}{}:
	default:
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

// SetEffectiveResolver installs the effective-config transformation applied
// to every parsed config before validation (see the field doc). Like
// SetInitialConfigVersion, this must be called after construction and before
// the CRD watcher starts delivering events.
func (h *ConfigChangeHandler) SetEffectiveResolver(resolve func(*coreconfig.Config) (*ResolvedConfig, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.effectiveResolver = resolve
}

// SetInitialSnapshot records the configuration and credentials owned by the
// running iteration. It must run before reinitialization is enabled.
func (h *ConfigChangeHandler) SetInitialSnapshot(snapshot *ValidatedSnapshot) {
	h.mu.Lock()
	h.activeSnapshot = cloneSnapshot(snapshot)
	h.acceptedCandidate = nil
	h.credentialsDirty = false
	if snapshot == nil {
		h.activeReplay = nil
		h.mu.Unlock()
		return
	}
	h.initialConfigVersion = snapshot.ConfigVersion
	h.initialCredentialsVersion = snapshot.CredentialsVersion
	h.currentCredentials = snapshot.Credentials
	h.currentCredentialsVersion = snapshot.CredentialsVersion
	activeReplay := events.NewConfigValidatedEvent(
		snapshot.Config, snapshot.TemplateConfig, snapshot.ConfigVersion, snapshot.CredentialsVersion)
	activeReplay.Sources = append([]events.ConfigSourceRef(nil), snapshot.Sources...)
	h.activeReplay = activeReplay
	h.mu.Unlock()
	h.configReplayer.Cache(activeReplay)
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
	h.reinitializationEnabled = true
	pendingConfig := h.pendingStartupConfig
	pendingCredentials := h.pendingStartupCredentials
	h.pendingStartupConfig = nil
	h.mu.Unlock()

	h.logger.Debug("Reinitialization signaling enabled (startup complete)")
	if pendingConfig != nil || pendingCredentials != nil {
		h.startupReplay <- struct{}{}
	}
}

// Start begins processing events from the EventBus.
//
// This method blocks until the context is canceled.
// The component is already subscribed to the EventBus (subscription happens in constructor).
// Returns nil on graceful shutdown.
//
// Example:
//
//	go handler.Start(ctx)
func (h *ConfigChangeHandler) Start(ctx context.Context) error {
	h.logger.Debug("Config change handler starting", "validators", h.validators)

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("ConfigChangeHandler shutting down", "reason", ctx.Err())
			// Join an in-flight validation before returning: the goroutine
			// publishes and caches on completion, and must not outlive the
			// handler's Start (the canceled ctx makes the scatter-gather
			// return promptly, so this drain is bounded).
			if h.validationInFlight {
				<-h.validationDone
				h.validationInFlight = false
			}
			h.cleanup()
			return nil
		case <-h.debounceTimer.Chan():
			h.debounceTimer.Fired()
			h.drainQueuedEvents()
			h.sendPendingReload()
			h.startQueuedValidation(ctx)
		case outcome := <-h.validationDone:
			h.validationInFlight = false
			h.drainQueuedEvents()
			h.applyValidationOutcome(outcome)
			h.startQueuedValidation(ctx)
		case <-h.startupReplay:
			if event := h.takePendingStartupCredentials(); event != nil {
				h.dispatchSideEvent(event)
			}
			if snapshot := h.acceptedCandidateSnapshot(); snapshot != nil {
				h.scheduleReload(snapshot, ReloadReasonConfig)
			}
		case <-h.effectiveReload:
			h.drainQueuedEvents()
			h.retirePendingReload()
			if reload := h.effectiveReloadRequest(); reload != nil {
				h.sendReload(reload)
			}
		case event := <-h.eventChan:
			// Validation stays off-loop so leadership replay remains responsive.
			if parsed, ok := event.(*events.ConfigParsedEvent); ok {
				h.recordParsed(parsed)
				h.startQueuedValidation(ctx)
			} else {
				h.dispatchSideEvent(event)
			}
		}
	}
}

// cleanup performs cleanup when the component is shutting down.
func (h *ConfigChangeHandler) cleanup() {
	h.debounceTimer.Stop()
	h.pendingReload = nil
}

// recordParsed assigns an authority generation and retires any restart armed
// by an older candidate. The newest parked candidate wins.
func (h *ConfigChangeHandler) recordParsed(event *events.ConfigParsedEvent) {
	h.candidateGeneration++
	h.mu.Lock()
	retiredCandidate := h.acceptedCandidate != nil
	h.acceptedCandidate = nil
	h.pendingStartupConfig = nil
	activeReplay := h.activeReplay
	credentialsReload := h.credentialsReloadSnapshotLocked()
	reinitializationEnabled := h.reinitializationEnabled
	h.mu.Unlock()
	if activeReplay != nil {
		if retiredCandidate {
			activeReplay = newActiveSnapshotRestore(activeReplay)
		}
		h.configReplayer.Cache(activeReplay)
		if retiredCandidate {
			h.eventBus.Publish(activeReplay)
		}
	}
	h.reviseQueuedReloadAfterCandidateRetired()
	if credentialsReload != nil && reinitializationEnabled {
		h.pendingReload = &ReloadRequest{
			Snapshot: credentialsReload,
			Reasons:  ReloadReasonCredentials,
		}
		if h.debounceTimer.Chan() == nil {
			h.debounceTimer.Reset(h.debounceInterval)
		}
	} else {
		h.retirePendingReload()
	}
	if h.queuedParsed != nil {
		h.logger.Debug("Coalescing superseded config-parsed event",
			"skipped_version", h.queuedParsed.event.Version, "newer_version", event.Version)
	}
	h.queuedParsed = &validationCandidate{generation: h.candidateGeneration, event: event}
}

func (h *ConfigChangeHandler) startQueuedValidation(ctx context.Context) {
	if h.validationInFlight || h.queuedParsed == nil {
		return
	}
	candidate := h.queuedParsed
	h.queuedParsed = nil
	h.validationInFlight = true
	go func() {
		h.validationDone <- h.validateCandidate(ctx, candidate)
	}()
}

// validateCandidate performs the long scatter-gather without publishing. The
// event loop applies its result only while the candidate generation is current.
func (h *ConfigChangeHandler) validateCandidate(ctx context.Context, candidate *validationCandidate) validationOutcome {
	event := candidate.event
	outcome := validationOutcome{candidate: candidate}

	// Resolve the effective config BEFORE validation so the validators judge
	// exactly what a reinitialized iteration would load. A resolution failure
	// (a required resource with no served version) is reported like any other
	// validation failure — the current config keeps running.
	h.mu.RLock()
	resolve := h.effectiveResolver
	h.mu.RUnlock()
	if parsed, ok := event.Config.(*coreconfig.Config); ok {
		outcome.rawConfig = parsed
		outcome.resolved = &ResolvedConfig{Config: parsed}
	}
	if resolve != nil && outcome.rawConfig != nil {
		resolved, err := resolve(outcome.rawConfig)
		if err != nil {
			outcome.validationErrors = map[string][]string{
				"effective-config": {err.Error()},
			}
			return outcome
		}
		if resolved == nil || resolved.Config == nil {
			outcome.validationErrors = map[string][]string{
				"effective-config": {"effective-config resolver returned no config"},
			}
			return outcome
		}
		outcome.resolved = resolved
	}
	configToValidate := event.Config
	if outcome.resolved != nil {
		configToValidate = outcome.resolved.Config
	}

	// If no validators are configured, skip validation and immediately publish validated event
	if len(h.validators) == 0 {
		outcome.valid = true
		return outcome
	}

	h.logger.Info("Coordinating config validation", "version", event.Version)

	// Create validation request
	req := events.NewConfigValidationRequest(configToValidate, event.Version)

	// Send request and wait for responses using scatter-gather.
	// The structural / template-syntax / JSONPath validators are sub-second even
	// for large configs. The long pole is the validationtests validator, which
	// runs the config's entire embedded suite (engine build + every test's
	// `haproxy -c`) — seconds for the bundled chart, potentially more for a very
	// large config. The timeout is sized so a slow-but-valid config load is NOT
	// false-rejected (a timed-out responder aggregates to "invalid", which would
	// refuse a perfectly good config); config loads are rare and operator-
	// initiated, never on the reconciliation hot path. If validation consistently
	// approaches this, investigate the validationtests suite / apiserver latency.
	// Derived from the SAME formula as the validationtests validator's run
	// budget so the envelope stays strictly larger for any suite size — the
	// validator must always self-report before this deadline (#77). A non-
	// *coreconfig.Config payload (unit-test stub) gets the zero-suite floor.
	suiteSize := 0
	if parsed, ok := configToValidate.(*coreconfig.Config); ok {
		suiteSize = len(parsed.ValidationTests)
	}
	result, err := h.eventBus.Request(ctx, req, busevents.RequestOptions{
		Timeout:            validator.SuiteValidationEnvelope(suiteSize),
		ExpectedResponders: h.validators,
	})

	if err != nil {
		outcome.validationErrors = map[string][]string{
			"coordinator": {err.Error()},
		}
		return outcome
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
		outcome.valid = true
		return outcome
	}
	outcome.validationErrors = validationErrors
	return outcome
}

// drainQueuedEvents observes parsed candidates that were already delivered
// before a validation completion won the select. This closes the cross-channel
// ordering window in which an obsolete result could otherwise be accepted.
func (h *ConfigChangeHandler) drainQueuedEvents() {
	for {
		select {
		case ev := <-h.eventChan:
			if parsed, ok := ev.(*events.ConfigParsedEvent); ok {
				h.recordParsed(parsed)
				continue
			}
			h.dispatchSideEvent(ev)
		default:
			return
		}
	}
}

func (h *ConfigChangeHandler) applyValidationOutcome(outcome validationOutcome) {
	if outcome.candidate.generation != h.candidateGeneration {
		h.logger.Debug("Discarding superseded config validation outcome",
			"version", outcome.candidate.event.Version)
		return
	}
	event := outcome.candidate.event
	if !outcome.valid {
		h.logger.Error("Config validation failed",
			"version", event.Version,
			"validation_errors", outcome.validationErrors)
		invalidEvent := events.NewConfigInvalidEvent(event.Version, event.TemplateConfig, outcome.validationErrors)
		invalidEvent.Sources = event.Sources
		h.eventBus.Publish(invalidEvent)
		return
	}

	config := event.Config
	if outcome.resolved != nil {
		config = outcome.resolved.Config
	}
	validatedEvent := events.NewConfigValidatedEvent(config, event.TemplateConfig, event.Version, h.credentialsVersion())
	validatedEvent.Sources = event.Sources
	validatedEvent.CandidateGeneration = outcome.candidate.generation
	h.configReplayer.Cache(validatedEvent)
	h.eventBus.Publish(validatedEvent)

	cfg, ok := config.(*coreconfig.Config)
	if !ok || outcome.rawConfig == nil {
		return
	}
	snapshot := &ValidatedSnapshot{
		RawConfig:      outcome.rawConfig,
		Config:         cfg,
		TemplateConfig: event.TemplateConfig,
		ConfigVersion:  event.Version,
		Sources:        append([]events.ConfigSourceRef(nil), event.Sources...),
	}
	if outcome.resolved != nil {
		snapshot.Resolution = outcome.resolved.Resolution
	}
	h.mu.RLock()
	snapshot.Credentials = h.currentCredentials
	snapshot.CredentialsVersion = h.currentCredentialsVersion
	h.mu.RUnlock()
	h.acceptValidatedSnapshot(snapshot)
}

func (h *ConfigChangeHandler) credentialsVersion() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.currentCredentialsVersion
}

func newActiveSnapshotRestore(active *events.ConfigValidatedEvent) *events.ConfigValidatedEvent {
	if active == nil {
		return nil
	}
	restored := events.NewConfigValidatedEvent(
		active.Config, active.TemplateConfig, active.Version, active.SecretVersion)
	restored.Sources = append([]events.ConfigSourceRef(nil), active.Sources...)
	restored.ActiveSnapshotRestore = true
	return restored
}

func (h *ConfigChangeHandler) effectiveReloadRequest() *ReloadRequest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	reasons := ReloadReasonEffectiveConfig
	base := h.acceptedCandidate
	if base == nil {
		base = h.activeSnapshot
	} else {
		reasons |= ReloadReasonConfig
	}
	if h.credentialsDirty {
		reasons |= ReloadReasonCredentials
	}
	snapshot := cloneSnapshot(base)
	if snapshot != nil {
		snapshot.Credentials = h.currentCredentials
		snapshot.CredentialsVersion = h.currentCredentialsVersion
	}
	if snapshot == nil {
		return nil
	}
	return &ReloadRequest{Snapshot: snapshot, Reasons: reasons}
}

func (h *ConfigChangeHandler) acceptedCandidateSnapshot() *ValidatedSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	snapshot := cloneSnapshot(h.acceptedCandidate)
	if snapshot != nil {
		snapshot.Credentials = h.currentCredentials
		snapshot.CredentialsVersion = h.currentCredentialsVersion
	}
	return snapshot
}

func (h *ConfigChangeHandler) takePendingStartupCredentials() *events.CredentialsUpdatedEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	event := h.pendingStartupCredentials
	h.pendingStartupCredentials = nil
	return event
}

func (h *ConfigChangeHandler) credentialsReloadSnapshotLocked() *ValidatedSnapshot {
	if !h.credentialsDirty {
		return nil
	}
	snapshot := cloneSnapshot(h.activeSnapshot)
	if snapshot == nil {
		return nil
	}
	snapshot.Credentials = h.currentCredentials
	snapshot.CredentialsVersion = h.currentCredentialsVersion
	return snapshot
}

func (h *ConfigChangeHandler) reloadForReasons(reasons ReloadReason) *ReloadRequest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	base := h.activeSnapshot
	if h.acceptedCandidate != nil {
		base = h.acceptedCandidate
	}
	if reasons.Has(ReloadReasonConfig) {
		if h.acceptedCandidate == nil {
			reasons &^= ReloadReasonConfig
		}
	}
	if reasons == 0 || base == nil {
		return nil
	}
	snapshot := cloneSnapshot(base)
	snapshot.Credentials = h.currentCredentials
	snapshot.CredentialsVersion = h.currentCredentialsVersion
	return &ReloadRequest{Snapshot: snapshot, Reasons: reasons}
}

func (h *ConfigChangeHandler) reviseQueuedReloadAfterCandidateRetired() {
	select {
	case queued := <-h.configChangeCh:
		if queued == nil || !queued.Reasons.Has(ReloadReasonConfig) {
			h.restoreQueuedReload(queued)
			return
		}
		h.restoreQueuedReload(h.reloadForReasons(queued.Reasons &^ ReloadReasonConfig))
	default:
	}
}

func (h *ConfigChangeHandler) augmentQueuedReload(reason ReloadReason) {
	select {
	case queued := <-h.configChangeCh:
		if queued == nil {
			return
		}
		h.restoreQueuedReload(h.reloadForReasons(queued.Reasons | reason))
	default:
	}
}

func (h *ConfigChangeHandler) restoreQueuedReload(reload *ReloadRequest) {
	if reload == nil {
		return
	}
	select {
	case h.configChangeCh <- reload:
	default:
		h.logger.Debug("Reload authority already accepted queued state")
	}
}

// dispatchSideEvent handles every subscribed event except ConfigParsedEvent.
func (h *ConfigChangeHandler) dispatchSideEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		h.handleConfigValidated(e)
	case *events.BecameLeaderEvent:
		h.handleBecameLeader(e)
	case *events.CredentialsUpdatedEvent:
		h.handleSecretRotation("credentials", e, &h.initialCredentialsVersion)
	default:
		h.logger.Warn("ConfigChangeHandler received an unhandled event type; dropping",
			"type", fmt.Sprintf("%T", event))
	}
}

// handleConfigValidated signals controller reinitialization when config is validated.
//
// Reinitialization signals are debounced to coalesce rapid CRD config changes.
// This prevents the race condition where reinitialization interrupts in-progress renders,
// ensuring all config changes are fully rendered before reinitialization starts.
func (h *ConfigChangeHandler) handleConfigValidated(event *events.ConfigValidatedEvent) {
	if event.CandidateGeneration != 0 || event.ActiveSnapshotRestore {
		return
	}

	// Always cache the event for leadership transition replay
	h.configReplayer.Cache(event)

	// Skip synthetic bootstrap events (version="initial") - these don't trigger reinitialization
	if event.Version == syntheticBootstrapVersion {
		h.mu.Lock()
		if h.activeReplay == nil {
			h.activeReplay = event
		}
		h.mu.Unlock()
		h.logger.Debug("Ignoring synthetic bootstrap ConfigValidatedEvent (version='initial')")
		return
	}

	cfg, ok := event.Config.(*coreconfig.Config)
	if !ok {
		h.logger.Error("ConfigValidatedEvent contains invalid config type",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", event.Config))
		return
	}
	h.mu.Lock()
	snapshot := &ValidatedSnapshot{
		RawConfig:          cfg,
		Config:             cfg,
		TemplateConfig:     event.TemplateConfig,
		ConfigVersion:      event.Version,
		Credentials:        h.currentCredentials,
		CredentialsVersion: h.currentCredentialsVersion,
		Sources:            append([]events.ConfigSourceRef(nil), event.Sources...),
	}
	h.mu.Unlock()
	h.acceptValidatedSnapshot(snapshot)
}

func (h *ConfigChangeHandler) acceptValidatedSnapshot(snapshot *ValidatedSnapshot) {
	h.mu.Lock()
	initialVersion := h.initialConfigVersion
	if initialVersion != "" && snapshot.ConfigVersion == initialVersion {
		if h.activeSnapshot == nil {
			h.activeSnapshot = cloneSnapshot(snapshot)
			h.activeReplay = events.NewConfigValidatedEvent(
				snapshot.Config, snapshot.TemplateConfig, snapshot.ConfigVersion, snapshot.CredentialsVersion)
			h.activeReplay.Sources = append([]events.ConfigSourceRef(nil), snapshot.Sources...)
		}
		h.mu.Unlock()
		h.logger.Debug("Ignoring validated config that matches the initial version",
			"version", snapshot.ConfigVersion)
		return
	}
	h.acceptedCandidate = cloneSnapshot(snapshot)
	if !h.reinitializationEnabled {
		h.pendingStartupConfig = cloneSnapshot(snapshot)
		h.mu.Unlock()
		h.augmentQueuedReload(ReloadReasonConfig)
		h.logger.Debug("Queued validated config received during startup",
			"version", snapshot.ConfigVersion)
		return
	}
	h.mu.Unlock()
	h.augmentQueuedReload(ReloadReasonConfig)
	h.scheduleReload(snapshot, ReloadReasonConfig)
}

func (h *ConfigChangeHandler) scheduleReload(snapshot *ValidatedSnapshot, reason ReloadReason) {
	reasons := reason
	if h.pendingReload != nil {
		reasons |= h.pendingReload.Reasons
	}
	h.pendingReload = &ReloadRequest{
		Snapshot: cloneSnapshot(snapshot),
		Reasons:  reasons,
	}
	h.logger.Debug("Validated state queued for iteration restart",
		"version", snapshot.ConfigVersion)
	h.debounceTimer.Reset(h.debounceInterval)
}

func (h *ConfigChangeHandler) retirePendingReload() {
	h.debounceTimer.Stop()
	h.pendingReload = nil
}

// handleSecretRotation restarts from the active config and the event's exact
// credentials; the bootstrap Secret version is ignored.
func (h *ConfigChangeHandler) handleSecretRotation(kind string, event *events.CredentialsUpdatedEvent, initialVersion *string) {
	version := event.SecretVersion
	if version == syntheticBootstrapVersion {
		h.logger.Debug("Ignoring synthetic bootstrap " + kind + " event (version='initial')")
		return
	}
	creds, ok := event.Credentials.(*coreconfig.Credentials)
	if !ok {
		h.logger.Error("CredentialsUpdatedEvent contains invalid credentials type",
			"got", fmt.Sprintf("%T", event.Credentials))
		return
	}

	h.mu.Lock()
	reinitEnabled := h.reinitializationEnabled
	bootstrap := *initialVersion
	if bootstrap != "" && version == bootstrap {
		h.mu.Unlock()
		h.logger.Debug("Ignoring "+kind+" Secret bootstrap event (matches initial version)",
			"version", version)
		return
	}
	h.currentCredentials = creds
	h.currentCredentialsVersion = version
	activeVersion := ""
	if h.activeSnapshot != nil {
		activeVersion = h.activeSnapshot.CredentialsVersion
	}
	h.credentialsDirty = version != activeVersion
	if !reinitEnabled {
		if kind == "credentials" {
			h.pendingStartupCredentials = event
		}
		h.mu.Unlock()
		h.augmentQueuedReload(ReloadReasonCredentials)
		h.logger.Debug("Queued "+kind+" Secret rotation received during startup",
			"version", version)
		return
	}
	h.pendingStartupCredentials = nil
	base := h.acceptedCandidate
	if base == nil {
		base = h.activeSnapshot
	}
	snapshot := cloneSnapshot(base)
	h.mu.Unlock()

	if snapshot == nil {
		h.logger.Warn("Cannot signal reinitialization for "+kind+" rotation: no validated config cached",
			"version", version)
		return
	}
	snapshot.Credentials = creds
	snapshot.CredentialsVersion = version

	h.augmentQueuedReload(ReloadReasonCredentials)
	h.logger.Info("Secret rotation detected; debouncing iteration restart",
		"kind", kind,
		"version", version)
	h.scheduleReload(snapshot, ReloadReasonCredentials)
}

// sendPendingReload sends the pending state to the controller.
// This is called after the debounce interval expires, ensuring rapid config changes are coalesced.
func (h *ConfigChangeHandler) sendPendingReload() {
	reload := h.pendingReload
	h.pendingReload = nil

	if reload == nil {
		// No pending config (e.g., already sent or cleared)
		return
	}

	h.logger.Info("Signaling controller reinitialization after debounce")

	h.sendReload(reload)
}

func (h *ConfigChangeHandler) sendReload(reload *ReloadRequest) {
	select {
	case h.configChangeCh <- reload:
		h.logger.Debug("Reinitialization signal sent")
	default:
		var queued *ReloadRequest
		select {
		case queued = <-h.configChangeCh:
		default:
		}
		if queued != nil {
			if merged := h.reloadForReasons(queued.Reasons | reload.Reasons); merged != nil {
				reload = merged
			}
		}
		select {
		case h.configChangeCh <- reload:
			h.logger.Debug("Reinitialization signal replaced with newer state")
		default:
			h.logger.Warn("Failed to replace reinitialization signal")
		}
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

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

// Package httpstore provides the event adapter for HTTP resource fetching.
//
// This package wraps the pure httpstore component (pkg/httpstore) with event
// coordination. It manages refresh timers and publishes events when content
// changes, allowing the reconciliation pipeline to validate new content before
// accepting it.
package httpstore

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "httpstore"

	// EventBufferSize is the size of the event subscription buffer.
	// Low-volume component handling validation events (~1-2 per reconciliation);
	// HTTP refresh operations are timer-driven, not event-driven.
	EventBufferSize = busevents.StandardSubscriberBuffer
)

// Component wraps HTTPStore with event coordination.
//
// It manages:
//   - Refresh timers for URLs with delay > 0
//   - Event publishing when content changes
//   - Pending content promotion/rejection based on proposal validation results
//   - Periodic eviction of unused cache entries
//
// Event subscriptions:
//   - ProposalValidationCompletedEvent: Check if our validation, promote/reject pending
//
// Event publications:
//   - ProposalValidationRequestedEvent: When refreshed content differs from accepted
//   - HTTPResourceAcceptedEvent: When pending content is promoted
//   - ReconciliationTriggeredEvent: After successful validation promotion
type Component struct {
	eventBus         *busevents.EventBus
	eventChan        <-chan busevents.Event
	store            *httpstore.HTTPStore
	refreshStoreURL  func(context.Context, string) (*httpstore.PendingVersion, error)
	evictStoreUnused func() []string
	logger           *slog.Logger

	// Refresh timer management
	mu                sync.Mutex
	refreshers        map[string]*time.Timer // URL -> refresh timer
	refreshManaged    map[string]bool
	refreshPending    map[string]bool
	refreshImmediate  map[string]bool
	refreshGeneration map[string]uint64
	refreshCallbacks  sync.WaitGroup
	ctx               context.Context
	cancel            context.CancelFunc
	stopped           bool

	// Eviction configuration
	evictionInterval time.Duration // How often to run eviction (0 = disabled)

	pendingValidation      *validationBatch
	queuedValidationSource string
}

type validationBatch struct {
	requestID string
	entries   []validationBatchEntry
}

type validationBatchEntry struct {
	url         string
	checksum    string
	revision    uint64
	contentSize int
}

// New creates a new HTTPStore event adapter component.
//
// The component subscribes to the EventBus during construction (before EventBus.Start())
// to ensure proper startup synchronization.
//
// Parameters:
//   - eventBus: The event bus for coordination
//   - logger: Logger for debug messages
//   - evictionMaxAge: Maximum age for unused entries before eviction (0 disables eviction)
func New(eventBus *busevents.EventBus, logger *slog.Logger, evictionMaxAge time.Duration) *Component {
	if logger == nil {
		logger = slog.Default()
	}

	// Subscribe to ProposalValidationCompleted events for HTTP content validation.
	// We handle both valid and invalid results via the same event type (Valid field).
	eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeProposalValidationCompleted,
	)
	store := httpstore.New(logger, evictionMaxAge)

	return &Component{
		eventBus:          eventBus,
		eventChan:         eventChan,
		store:             store,
		refreshStoreURL:   store.RefreshURLVersion,
		evictStoreUnused:  store.EvictUnused,
		logger:            logger.With("component", ComponentName),
		refreshers:        make(map[string]*time.Timer),
		refreshManaged:    make(map[string]bool),
		refreshPending:    make(map[string]bool),
		refreshImmediate:  make(map[string]bool),
		refreshGeneration: make(map[string]uint64),
		evictionInterval:  evictionMaxAge, // Run eviction at same cadence as maxAge
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the component's event loop.
//
// This method blocks until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	componentCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.ctx = componentCtx
	c.cancel = cancel
	c.mu.Unlock()

	c.logger.Debug("HTTP store starting",
		"eviction_interval", c.evictionInterval)

	// Create eviction ticker if eviction is enabled
	var evictionTicker *time.Ticker
	var evictionChan <-chan time.Time
	if c.evictionInterval > 0 {
		evictionTicker = time.NewTicker(c.evictionInterval)
		evictionChan = evictionTicker.C
		defer evictionTicker.Stop()
	}

	for {
		select {
		case event := <-c.eventChan:
			// Recover per-event so a panic in validation-event handling can't
			// tear down the goroutine (this component can't embed component.Base
			// — it adds an eviction-ticker arm).
			component.SafeDispatch(c.logger, ComponentName, event, func() {
				c.handleEvent(event)
			})

		case <-evictionChan:
			evictedURLs := c.evictUnused()
			if len(evictedURLs) > 0 {
				c.logger.Debug("HTTP store eviction ran", "evicted", len(evictedURLs))
			}

		case <-componentCtx.Done():
			c.logger.Info("HTTPStore adapter shutting down")
			c.stopAllRefreshers()
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(event busevents.Event) {
	if e, ok := event.(*events.ProposalValidationCompletedEvent); ok {
		c.handleProposalValidationCompleted(e)
	}
}

// handleProposalValidationCompleted handles validation completion for HTTP content.
// Only processes events that match our pending validation request ID.
func (c *Component) handleProposalValidationCompleted(event *events.ProposalValidationCompletedEvent) {
	c.mu.Lock()
	batch := c.pendingValidation
	if batch == nil || event.RequestID != batch.requestID {
		c.mu.Unlock()
		return
	}
	c.pendingValidation = nil

	accepted := false
	if event.Valid {
		accepted = c.handleValidationSuccess(batch)
	} else {
		c.handleValidationFailure(batch, event.Phase, event.Error)
	}

	nextSource := c.queuedValidationSource
	c.queuedValidationSource = ""
	nextRequest := c.beginValidationLocked(nextSource)
	c.mu.Unlock()

	if accepted {
		c.eventBus.Publish(events.NewReconciliationTriggeredEvent("http_content_validated", true))
	}
	c.publishValidationRequest(nextRequest)
}

func (c *Component) handleValidationSuccess(batch *validationBatch) bool {
	c.logger.Debug("HTTP content validation succeeded, promoting pending content",
		"url_count", len(batch.entries))

	promoted := false
	for _, entry := range batch.entries {
		if c.store.PromotePendingVersion(entry.url, entry.checksum, entry.revision) {
			promoted = true
			c.eventBus.Publish(events.NewHTTPResourceAcceptedEvent(
				entry.url,
				entry.checksum,
				entry.contentSize,
			))
		}
	}
	return promoted
}

func (c *Component) handleValidationFailure(batch *validationBatch, phase, errMsg string) {
	c.logger.Warn("HTTP content validation failed, rejecting pending content",
		"url_count", len(batch.entries),
		"phase", phase,
		"error", errMsg)

	for _, entry := range batch.entries {
		c.store.RejectPendingVersion(entry.url, entry.checksum, entry.revision)
	}
}

// GetStore returns the underlying HTTPStore.
// This is used by the wrapper to access cached content.
func (c *Component) GetStore() *httpstore.HTTPStore {
	return c.store
}

// RegisterURL registers a URL for refresh if it has a delay configured.
// This is called after successful initial fetch from template rendering.
func (c *Component) RegisterURL(url string) {
	delay := c.store.GetDelay(url)
	if delay == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}

	// Don't register if already registered
	if _, exists := c.refreshers[url]; exists {
		return
	}

	c.logger.Debug("Registering URL for periodic refresh",
		"url", url,
		"delay", delay.String())

	c.refreshGeneration[url]++
	c.armRefresherLocked(url, delay, c.refreshGeneration[url])
}

// refreshURL performs a refresh of the given URL.
func (c *Component) refreshURL(url string) {
	c.refreshURLForGeneration(url, 0)
}

func (c *Component) armRefresherLocked(url string, delay time.Duration, generation uint64) {
	c.refreshCallbacks.Add(1)
	c.refreshManaged[url] = true
	c.refreshPending[url] = true
	c.refreshers[url] = time.AfterFunc(delay, func() {
		c.runRefresher(url, generation)
	})
}

func (c *Component) runRefresher(url string, generation uint64) {
	c.mu.Lock()
	active := !c.stopped && c.refreshManaged[url] && c.refreshGeneration[url] == generation
	if active {
		c.refreshPending[url] = false
	}
	c.mu.Unlock()
	defer c.refreshCallbacks.Done()
	if active {
		c.refreshURLForGeneration(url, generation)
	}
}

func (c *Component) refreshURLForGeneration(url string, generation uint64) {
	c.mu.Lock()
	ctx := c.ctx
	stopped := c.stopped
	c.mu.Unlock()
	if stopped {
		return
	}
	if ctx == nil {
		c.rearmRefresher(url, generation)
		return
	}
	if ctx.Err() != nil {
		return
	}

	// Defensive check: verify entry still exists (may have been evicted)
	// This handles the race condition where the eviction timer fires between
	// EvictUnused() and StopRefresher() calls.
	if c.store.GetEntry(url) == nil {
		c.logger.Log(context.Background(), logging.LevelTrace, "skipping refresh for evicted URL", "url", url)
		return
	}

	c.logger.Log(context.Background(), logging.LevelTrace, "refreshing HTTP URL", "url", url)

	// Perform refresh
	version, err := c.refreshStoreURL(ctx, url)
	if err != nil {
		c.logger.Warn("HTTP refresh failed",
			"url", url,
			"error", err)
	}

	c.rearmRefresher(url, generation)

	// If content changed, trigger proposal validation before accepting
	c.mu.Lock()
	active := c.refresherActiveLocked(url, generation)
	c.mu.Unlock()
	if version != nil && (!active || ctx.Err() != nil) {
		if c.store.DiscardPendingVersion(url, version.Checksum, version.Revision) {
			c.requestImmediateRefresh(url)
		}
		return
	}
	if version != nil {
		c.triggerProposalValidation(url)
	}
}

func (c *Component) rearmRefresher(url string, generation uint64) {
	delay := c.store.GetDelay(url)
	if delay <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.refresherActiveLocked(url, generation) {
		return
	}
	if c.refreshImmediate[url] {
		delay = 0
		delete(c.refreshImmediate, url)
	}
	timer, exists := c.refreshers[url]
	if !exists {
		return
	}
	if generation != 0 {
		c.refreshCallbacks.Add(1)
		c.refreshPending[url] = true
	}
	timer.Reset(delay)
}

func (c *Component) requestImmediateRefresh(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	timer, exists := c.refreshers[url]
	if !exists || c.stopped || !c.refreshManaged[url] {
		return
	}
	if c.refreshPending[url] && timer.Stop() {
		timer.Reset(0)
		return
	}
	c.refreshImmediate[url] = true
}

func (c *Component) refresherActiveLocked(url string, generation uint64) bool {
	return !c.stopped && (generation == 0 ||
		(c.refreshManaged[url] && c.refreshGeneration[url] == generation))
}

// triggerProposalValidation publishes a ProposalValidationRequestedEvent with HTTPOverlay.
// This validates the pending HTTP content before promoting it to accepted.
func (c *Component) triggerProposalValidation(changedURL string) {
	entry := c.store.GetEntry(changedURL)
	if entry == nil || !entry.HasPending {
		return
	}

	c.logger.Debug("HTTP content changed, triggering proposal validation",
		"url", changedURL,
		"new_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

	c.mu.Lock()
	c.retireSupersededValidationLocked(changedURL, entry.PendingRevision)
	if c.pendingValidation != nil {
		c.queuedValidationSource = changedURL
	}
	req := c.beginValidationLocked(changedURL)
	c.mu.Unlock()
	c.publishValidationRequest(req)

	// Also publish HTTPResourceUpdatedEvent for observability
	c.eventBus.Publish(events.NewHTTPResourceUpdatedEvent(
		changedURL,
		entry.PendingChecksum,
		len(entry.PendingContent),
	))
}

func (c *Component) beginValidationLocked(source string) *events.ProposalValidationRequestedEvent {
	if c.pendingValidation != nil {
		return nil
	}

	overlay := httpstore.NewHTTPOverlay(c.store)
	if overlay.IsEmpty() {
		return nil
	}
	if source == "" {
		source = overlay.PendingURLs()[0]
	}
	req := events.NewProposalValidationRequestedEvent(nil, overlay, "httpstore", source)
	batch := &validationBatch{
		requestID: req.ID,
		entries:   make([]validationBatchEntry, 0, len(overlay.PendingURLs())),
	}
	for _, url := range overlay.PendingURLs() {
		pendingContent, contentExists := overlay.GetContent(url)
		pendingChecksum, pendingRevision, versionExists := overlay.PendingVersion(url)
		if !contentExists || !versionExists {
			continue
		}
		batch.entries = append(batch.entries, validationBatchEntry{
			url:         url,
			checksum:    pendingChecksum,
			revision:    pendingRevision,
			contentSize: len(pendingContent),
		})
	}
	if len(batch.entries) == 0 {
		return nil
	}
	c.pendingValidation = batch
	return req
}

func (c *Component) retireSupersededValidationLocked(url string, revision uint64) {
	if c.pendingValidation == nil {
		return
	}
	for _, entry := range c.pendingValidation.entries {
		if entry.url == url && entry.revision != revision {
			c.pendingValidation = nil
			c.queuedValidationSource = ""
			return
		}
	}
}

func (c *Component) publishValidationRequest(req *events.ProposalValidationRequestedEvent) {
	if req != nil {
		c.eventBus.Publish(req)
	}
}

// stopAllRefreshers stops all refresh timers.
func (c *Component) stopAllRefreshers() {
	c.mu.Lock()
	c.stopped = true
	cancel := c.cancel

	for url, timer := range c.refreshers {
		c.refreshGeneration[url]++
		if timer.Stop() && c.refreshManaged[url] && c.refreshPending[url] {
			c.refreshPending[url] = false
			c.refreshCallbacks.Done()
		}
		c.logger.Log(context.Background(), logging.LevelTrace, "stopped refresh timer", "url", url)
	}

	c.refreshers = make(map[string]*time.Timer)
	c.refreshManaged = make(map[string]bool)
	c.refreshPending = make(map[string]bool)
	c.refreshImmediate = make(map[string]bool)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.refreshCallbacks.Wait()
}

func (c *Component) evictUnused() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	evictedURLs := c.evictStoreUnused()
	for _, url := range evictedURLs {
		c.stopRefresherLocked(url)
	}
	return evictedURLs
}

// StopRefresher stops the refresh timer for a specific URL.
func (c *Component) StopRefresher(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopRefresherLocked(url)
}

func (c *Component) stopRefresherLocked(url string) {
	if timer, exists := c.refreshers[url]; exists {
		c.refreshGeneration[url]++
		if timer.Stop() && c.refreshManaged[url] && c.refreshPending[url] {
			c.refreshPending[url] = false
			c.refreshCallbacks.Done()
		}
		delete(c.refreshers, url)
		delete(c.refreshManaged, url)
		delete(c.refreshPending, url)
		delete(c.refreshImmediate, url)
		c.logger.Log(context.Background(), logging.LevelTrace, "stopped refresh timer", "url", url)
	}
}

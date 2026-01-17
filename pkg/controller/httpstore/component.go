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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "httpstore"

	// EventBufferSize is the size of the event subscription buffer.
	// Size 50: Low-volume component handling validation events (~1-2 per reconciliation).
	// HTTP refresh operations are timer-driven, not event-driven.
	EventBufferSize = 50
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
//   - HTTPResourceRejectedEvent: When pending content is rejected
//   - ReconciliationTriggeredEvent: After successful validation promotion
type Component struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	store     *httpstore.HTTPStore
	logger    *slog.Logger

	// Refresh timer management
	mu         sync.Mutex
	refreshers map[string]*time.Timer // URL -> refresh timer
	ctx        context.Context
	cancel     context.CancelFunc

	// Eviction configuration
	evictionInterval time.Duration // How often to run eviction (0 = disabled)

	// Pending validation request tracking
	pendingValidationID string // ID of pending validation request (empty if none)
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

	return &Component{
		eventBus:         eventBus,
		eventChan:        eventChan,
		store:            httpstore.New(logger, evictionMaxAge),
		logger:           logger.With("component", ComponentName),
		refreshers:       make(map[string]*time.Timer),
		evictionInterval: evictionMaxAge, // Run eviction at same cadence as maxAge
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
	c.ctx, c.cancel = context.WithCancel(ctx)

	c.logger.Debug("http store starting",
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
			c.handleEvent(event)

		case <-evictionChan:
			evictedURLs := c.store.EvictUnused()
			if len(evictedURLs) > 0 {
				c.logger.Debug("HTTP store eviction ran", "evicted", len(evictedURLs))
				// Stop refresh timers for evicted URLs to prevent timer leaks
				for _, url := range evictedURLs {
					c.StopRefresher(url)
				}
			}

		case <-c.ctx.Done():
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
	// Atomically check and clear pending request ID to prevent race conditions.
	// This ensures that between checking the ID and clearing it, no other goroutine
	// can modify pendingValidationID.
	c.mu.Lock()
	pendingID := c.pendingValidationID
	if pendingID == "" || event.RequestID != pendingID {
		c.mu.Unlock()
		return
	}
	c.pendingValidationID = ""
	c.mu.Unlock()

	if event.Valid {
		c.handleValidationSuccess()
	} else {
		c.handleValidationFailure(event.Phase, event.Error)
	}
}

// handleValidationSuccess promotes all pending HTTP content and triggers reconciliation.
func (c *Component) handleValidationSuccess() {
	pendingURLs := c.store.GetPendingURLs()
	if len(pendingURLs) == 0 {
		return
	}

	c.logger.Info("HTTP content validation succeeded, promoting pending content",
		"url_count", len(pendingURLs))

	for _, url := range pendingURLs {
		entry := c.store.GetEntry(url)
		if entry == nil {
			continue
		}

		if c.store.PromotePending(url) {
			c.eventBus.Publish(events.NewHTTPResourceAcceptedEvent(
				url,
				entry.PendingChecksum,
				len(entry.PendingContent),
			))
		}
	}

	// Trigger reconciliation with the newly accepted content.
	// This is coalescible since multiple HTTP content changes can be batched.
	c.eventBus.Publish(events.NewReconciliationTriggeredEvent("http_content_validated", true))
}

// handleValidationFailure rejects all pending HTTP content.
func (c *Component) handleValidationFailure(phase, errMsg string) {
	pendingURLs := c.store.GetPendingURLs()
	if len(pendingURLs) == 0 {
		return
	}

	c.logger.Warn("HTTP content validation failed, rejecting pending content",
		"url_count", len(pendingURLs),
		"phase", phase,
		"error", errMsg)

	// Format reason from validation error
	reason := "validation failed"
	if errMsg != "" {
		reason = errMsg
	}

	for _, url := range pendingURLs {
		entry := c.store.GetEntry(url)
		if entry == nil {
			continue
		}

		if c.store.RejectPending(url) {
			c.eventBus.Publish(events.NewHTTPResourceRejectedEvent(
				url,
				entry.PendingChecksum,
				reason,
			))
		}
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

	// Don't register if already registered
	if _, exists := c.refreshers[url]; exists {
		return
	}

	c.logger.Debug("registering URL for periodic refresh",
		"url", url,
		"delay", delay.String())

	// Create timer for first refresh
	timer := time.AfterFunc(delay, func() {
		c.refreshURL(url)
	})

	c.refreshers[url] = timer
}

// refreshURL performs a refresh of the given URL.
func (c *Component) refreshURL(url string) {
	// Check if we're still running
	if c.ctx == nil || c.ctx.Err() != nil {
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
	changed, err := c.store.RefreshURL(c.ctx, url)
	if err != nil {
		c.logger.Warn("HTTP refresh failed",
			"url", url,
			"error", err)
	}

	// Schedule next refresh
	delay := c.store.GetDelay(url)
	if delay > 0 {
		c.mu.Lock()
		if timer, exists := c.refreshers[url]; exists {
			timer.Reset(delay)
		}
		c.mu.Unlock()
	}

	// If content changed, trigger proposal validation before accepting
	if changed {
		c.triggerProposalValidation(url)
	}
}

// triggerProposalValidation publishes a ProposalValidationRequestedEvent with HTTPOverlay.
// This validates the pending HTTP content before promoting it to accepted.
func (c *Component) triggerProposalValidation(changedURL string) {
	entry := c.store.GetEntry(changedURL)
	if entry == nil {
		return
	}

	c.logger.Info("HTTP content changed, triggering proposal validation",
		"url", changedURL,
		"new_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

	// Create HTTPOverlay with current pending state
	httpOverlay := httpstore.NewHTTPOverlay(c.store)

	// Create validation request with HTTP overlay (no K8s overlays)
	req := events.NewProposalValidationRequestedEvent(
		nil,         // no K8s overlays
		httpOverlay, // HTTP overlay with pending content
		"httpstore",
		changedURL,
	)

	// Track this request so we can handle the response
	c.mu.Lock()
	c.pendingValidationID = req.ID
	c.mu.Unlock()

	c.eventBus.Publish(req)

	// Also publish HTTPResourceUpdatedEvent for observability
	c.eventBus.Publish(events.NewHTTPResourceUpdatedEvent(
		changedURL,
		entry.PendingChecksum,
		len(entry.PendingContent),
	))
}

// stopAllRefreshers stops all refresh timers.
func (c *Component) stopAllRefreshers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for url, timer := range c.refreshers {
		timer.Stop()
		c.logger.Log(context.Background(), logging.LevelTrace, "stopped refresh timer", "url", url)
	}

	c.refreshers = make(map[string]*time.Timer)
}

// StopRefresher stops the refresh timer for a specific URL.
func (c *Component) StopRefresher(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if timer, exists := c.refreshers[url]; exists {
		timer.Stop()
		delete(c.refreshers, url)
		c.logger.Log(context.Background(), logging.LevelTrace, "stopped refresh timer", "url", url)
	}
}

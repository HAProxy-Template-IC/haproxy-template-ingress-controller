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

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/httpstore"
)

const (
	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = 50
)

// Component wraps HTTPStore with event coordination.
//
// It manages:
//   - Refresh timers for URLs with delay > 0
//   - Event publishing when content changes
//   - Pending content promotion/rejection based on validation results
//
// Event subscriptions:
//   - ValidationCompletedEvent: Promote pending content to accepted
//   - ValidationFailedEvent: Reject pending content
//
// Event publications:
//   - HTTPResourceUpdatedEvent: When refreshed content differs from accepted
//   - HTTPResourceAcceptedEvent: When pending content is promoted
//   - HTTPResourceRejectedEvent: When pending content is rejected
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
}

// New creates a new HTTPStore event adapter component.
//
// The component subscribes to the EventBus during construction (before EventBus.Start())
// to ensure proper startup synchronization.
func New(eventBus *busevents.EventBus, logger *slog.Logger) *Component {
	if logger == nil {
		logger = slog.Default()
	}

	// Subscribe during construction per CLAUDE.md guidelines
	eventChan := eventBus.Subscribe(EventBufferSize)

	return &Component{
		eventBus:   eventBus,
		eventChan:  eventChan,
		store:      httpstore.New(logger),
		logger:     logger.With("component", "httpstore-adapter"),
		refreshers: make(map[string]*time.Timer),
	}
}

// Start begins the component's event loop.
//
// This method blocks until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	c.logger.Info("HTTPStore adapter starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-c.ctx.Done():
			c.logger.Info("HTTPStore adapter shutting down")
			c.stopAllRefreshers()
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(event busevents.Event) {
	switch e := event.(type) {
	case *events.ValidationCompletedEvent:
		c.handleValidationCompleted(e)

	case *events.ValidationFailedEvent:
		c.handleValidationFailed(e)
	}
}

// handleValidationCompleted promotes all pending HTTP content to accepted.
func (c *Component) handleValidationCompleted(_ *events.ValidationCompletedEvent) {
	pendingURLs := c.store.GetPendingURLs()
	if len(pendingURLs) == 0 {
		return
	}

	c.logger.Info("validation completed, promoting pending HTTP content",
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
}

// handleValidationFailed rejects all pending HTTP content.
func (c *Component) handleValidationFailed(event *events.ValidationFailedEvent) {
	pendingURLs := c.store.GetPendingURLs()
	if len(pendingURLs) == 0 {
		return
	}

	c.logger.Warn("validation failed, rejecting pending HTTP content",
		"url_count", len(pendingURLs),
		"errors", event.Errors)

	// Format reason from validation errors
	reason := "validation failed"
	if len(event.Errors) > 0 {
		reason = event.Errors[0]
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

	c.logger.Info("registering URL for periodic refresh",
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

	c.logger.Debug("refreshing HTTP URL", "url", url)

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

	// If content changed, publish event to trigger reconciliation
	if changed {
		entry := c.store.GetEntry(url)
		if entry != nil {
			c.logger.Info("HTTP content changed, triggering reconciliation",
				"url", url,
				"new_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

			c.eventBus.Publish(events.NewHTTPResourceUpdatedEvent(
				url,
				entry.PendingChecksum,
				len(entry.PendingContent),
			))
		}
	}
}

// stopAllRefreshers stops all refresh timers.
func (c *Component) stopAllRefreshers() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for url, timer := range c.refreshers {
		timer.Stop()
		c.logger.Debug("stopped refresh timer", "url", url)
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
		c.logger.Debug("stopped refresh timer", "url", url)
	}
}

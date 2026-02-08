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

// Package resourceloader provides shared event loop infrastructure for loader
// components that watch a single resource type and parse/transform its data.
//
// The pattern matches validator/base.go: each loader embeds a BaseLoader and
// implements EventProcessor to provide its resource-specific parsing logic.
package resourceloader

import (
	"context"
	"log/slog"
	"sync"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// EventProcessor defines the interface for loader-specific event handling logic.
//
// Each loader (configloader, credentialsloader, certloader) implements this
// interface to provide its specific parsing logic while reusing the common
// event loop infrastructure.
type EventProcessor interface {
	// ProcessEvent handles a single event from the EventBus.
	ProcessEvent(event busevents.Event)
}

// BaseLoader provides common event loop infrastructure for all loader components.
//
// It handles:
//   - Event subscription and routing
//   - Graceful shutdown via context or Stop()
//   - Stop idempotency
//
// Loaders embed this struct and provide an EventProcessor implementation
// for their specific parsing logic.
type BaseLoader struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	logger    *slog.Logger
	stopCh    chan struct{}
	stopOnce  sync.Once
	name      string
	processor EventProcessor
}

// NewBaseLoader creates a new base loader with the given configuration.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//   - name: Component name (used for logging and subscription)
//   - bufferSize: Event subscription buffer size
//   - processor: EventProcessor implementation for loader-specific logic
//   - eventTypes: Event types to subscribe to (for type-filtered subscription)
func NewBaseLoader(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	name string,
	bufferSize int,
	processor EventProcessor,
	eventTypes ...string,
) *BaseLoader {
	eventChan := eventBus.SubscribeTypes(name, bufferSize, eventTypes...)

	return &BaseLoader{
		eventBus:  eventBus,
		eventChan: eventChan,
		logger:    logger.With("component", name),
		stopCh:    make(chan struct{}),
		name:      name,
		processor: processor,
	}
}

// Start begins processing events from the EventBus.
//
// This method blocks until Stop() is called or the context is canceled.
// The component is already subscribed to the EventBus (subscription happens
// in the constructor via NewBaseLoader).
// Returns nil on graceful shutdown.
func (b *BaseLoader) Start(ctx context.Context) error {
	b.logger.Debug(b.name + " starting")

	for {
		select {
		case <-ctx.Done():
			b.logger.Info(b.name+" shutting down", "reason", ctx.Err())
			return nil
		case <-b.stopCh:
			b.logger.Info(b.name + " shutting down")
			return nil
		case event := <-b.eventChan:
			b.processor.ProcessEvent(event)
		}
	}
}

// Stop gracefully stops the loader.
// Safe to call multiple times.
func (b *BaseLoader) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
	})
}

// EventBus returns the event bus for use by the processor.
func (b *BaseLoader) EventBus() *busevents.EventBus {
	return b.eventBus
}

// Logger returns the logger for use by the processor.
func (b *BaseLoader) Logger() *slog.Logger {
	return b.logger
}

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
// BaseLoader is a thin wrapper over pkg/controller/component.Base that keeps
// the ProcessEvent naming familiar to existing loader implementations
// (configloader, credentialsloader, certloader).
package resourceloader

import (
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
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

// BaseLoader is a resource-loader flavoured wrapper around component.Base
// that exposes the same field accessors (EventBus, Logger, Name) as before
// and delegates event dispatch to the processor's ProcessEvent method.
//
// Panics inside ProcessEvent are caught by the embedded base, logged with
// the event type and then swallowed so the event loop keeps running.
type BaseLoader struct {
	*component.Base
	processor EventProcessor
}

// NewBaseLoader creates a new base loader with the given configuration.
func NewBaseLoader(
	eventBus *busevents.EventBus,
	logger *slog.Logger,
	name string,
	bufferSize int,
	processor EventProcessor,
	eventTypes ...string,
) *BaseLoader {
	b := &BaseLoader{processor: processor}
	b.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     logger,
		Name:       name,
		BufferSize: bufferSize,
		Handler:    b,
		EventTypes: eventTypes,
	})
	return b
}

// HandleEvent implements component.EventHandler by forwarding to the
// processor so loader implementations do not need to change their method
// name.
func (b *BaseLoader) HandleEvent(event busevents.Event) {
	b.processor.ProcessEvent(event)
}

// AssertUnstructured type-asserts a resource carried by an event to
// *unstructured.Unstructured. On a type mismatch it logs an error tagged with
// the event type name and returns (nil, false); callers should early-return on
// a false result. Every loader on the bus receives resources via watcher
// events whose payloads are unstructured, so this is the single chokepoint for
// the "invalid resource type" failure mode.
func (b *BaseLoader) AssertUnstructured(eventTypeName string, resource any) (*unstructured.Unstructured, bool) {
	u, ok := resource.(*unstructured.Unstructured)
	if !ok {
		b.Logger().Error(eventTypeName+" contains invalid resource type",
			"expected", "*unstructured.Unstructured",
			"got", fmt.Sprintf("%T", resource))
		return nil, false
	}
	return u, true
}

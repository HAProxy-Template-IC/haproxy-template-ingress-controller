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

package templating

import (
	"errors"
	"sort"
	"sync"
)

// RenderedEvent is a Kubernetes Event a template asked to emit during rendering,
// via the recordEvent() function. It is resource-agnostic: the involved object
// is identified purely by APIVersion/Kind/Namespace/Name supplied by the
// template, so the controller can emit it against any watched resource or CRD
// without a typed client (RULE #1).
type RenderedEvent struct {
	// Namespace of the involved (regarding) resource. Empty for cluster-scoped.
	Namespace string
	// Name of the involved resource.
	Name string
	// APIVersion of the involved resource (e.g. "networking.k8s.io/v1").
	APIVersion string
	// Kind of the involved resource (e.g. "Ingress").
	Kind string
	// Type is the Event type: "Warning" or "Normal".
	Type string
	// Reason is a short, machine-readable, PascalCase reason (e.g. "RouteConflict").
	Reason string
	// Message is the human-readable description.
	Message string
}

// EventTypeWarning / EventTypeNormal mirror corev1.EventType values without
// importing k8s.io/api into the pure templating package.
const (
	EventTypeWarning = "Warning"
	EventTypeNormal  = "Normal"
)

// renderedEventKey deduplicates identical events. The full tuple is the key:
// the same (resource, type, reason, message) emitted many times during a render
// — e.g. once per shard, or once per map file the losing path renders into —
// collapses to a single event.
func renderedEventKey(e *RenderedEvent) string {
	return e.Namespace + "/" + e.Name + "/" + e.APIVersion + "/" + e.Kind + "/" + e.Type + "/" + e.Reason + "/" + e.Message
}

// EventCollector collects Events registered by templates during rendering.
// It is thread-safe for concurrent writes from parallel template goroutines and
// created per render cycle (same lifecycle as StatusPatchCollector).
type EventCollector struct {
	mu     sync.Mutex
	events map[string]RenderedEvent // keyed by renderedEventKey
}

// NewEventCollector creates a new thread-safe collector.
func NewEventCollector() *EventCollector {
	return &EventCollector{events: make(map[string]RenderedEvent)}
}

// Register records an Event to emit. Duplicate (resource, type, reason, message)
// tuples collapse to one. name/apiVersion/kind/type/reason/message are required;
// namespace is optional (cluster-scoped resources).
func (c *EventCollector) Register(namespace, name, apiVersion, kind, eventType, reason, message string) error {
	if name == "" || apiVersion == "" || kind == "" {
		return errors.New("recordEvent: name, apiVersion, and kind are required")
	}
	if reason == "" || message == "" {
		return errors.New("recordEvent: reason and message are required")
	}
	if eventType != EventTypeWarning && eventType != EventTypeNormal {
		return errors.New("recordEvent: type must be \"Warning\" or \"Normal\"")
	}

	e := RenderedEvent{
		Namespace:  namespace,
		Name:       name,
		APIVersion: apiVersion,
		Kind:       kind,
		Type:       eventType,
		Reason:     reason,
		Message:    message,
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.events[renderedEventKey(&e)] = e
	return nil
}

// Events returns all collected events as a snapshot, sorted by key so the output
// is deterministic regardless of the (parallel, nondeterministic) registration
// order. Further Register calls do not affect the returned slice.
func (c *EventCollector) Events() []RenderedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := make([]string, 0, len(c.events))
	for k := range c.events {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]RenderedEvent, 0, len(keys))
	for _, k := range keys {
		result = append(result, c.events[k])
	}
	return result
}

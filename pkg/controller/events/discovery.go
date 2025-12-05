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

package events

import "time"

// -----------------------------------------------------------------------------
// HAProxy Pod Events.
// -----------------------------------------------------------------------------

// HAProxyPodsDiscoveredEvent is published when HAProxy pods are discovered or updated.
type HAProxyPodsDiscoveredEvent struct {
	// Endpoints is the list of discovered HAProxy Dataplane API endpoints.
	Endpoints []interface{}
	Count     int
	timestamp time.Time
}

// NewHAProxyPodsDiscoveredEvent creates a new HAProxyPodsDiscoveredEvent.
// Performs defensive copy of the endpoints slice.
func NewHAProxyPodsDiscoveredEvent(endpoints []interface{}, count int) *HAProxyPodsDiscoveredEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &HAProxyPodsDiscoveredEvent{
		Endpoints: endpointsCopy,
		Count:     count,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodsDiscoveredEvent) EventType() string    { return EventTypeHAProxyPodsDiscovered }
func (e *HAProxyPodsDiscoveredEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodAddedEvent is published when a new HAProxy pod is discovered.
type HAProxyPodAddedEvent struct {
	Endpoint  interface{}
	timestamp time.Time
}

// NewHAProxyPodAddedEvent creates a new HAProxyPodAddedEvent.
func NewHAProxyPodAddedEvent(endpoint interface{}) *HAProxyPodAddedEvent {
	return &HAProxyPodAddedEvent{
		Endpoint:  endpoint,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodAddedEvent) EventType() string    { return EventTypeHAProxyPodAdded }
func (e *HAProxyPodAddedEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodRemovedEvent is published when an HAProxy pod is removed.
type HAProxyPodRemovedEvent struct {
	Endpoint  interface{}
	timestamp time.Time
}

// NewHAProxyPodRemovedEvent creates a new HAProxyPodRemovedEvent.
func NewHAProxyPodRemovedEvent(endpoint interface{}) *HAProxyPodRemovedEvent {
	return &HAProxyPodRemovedEvent{
		Endpoint:  endpoint,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodRemovedEvent) EventType() string    { return EventTypeHAProxyPodRemoved }
func (e *HAProxyPodRemovedEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodTerminatedEvent is published when an HAProxy pod terminates.
//
// This triggers cleanup of the pod from all runtime config status fields.
type HAProxyPodTerminatedEvent struct {
	PodName      string
	PodNamespace string
	timestamp    time.Time
}

// NewHAProxyPodTerminatedEvent creates a new HAProxyPodTerminatedEvent.
func NewHAProxyPodTerminatedEvent(podName, podNamespace string) *HAProxyPodTerminatedEvent {
	return &HAProxyPodTerminatedEvent{
		PodName:      podName,
		PodNamespace: podNamespace,
		timestamp:    time.Now(),
	}
}

func (e *HAProxyPodTerminatedEvent) EventType() string    { return EventTypeHAProxyPodTerminated }
func (e *HAProxyPodTerminatedEvent) Timestamp() time.Time { return e.timestamp }

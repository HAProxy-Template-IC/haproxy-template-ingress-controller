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

import (
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// -----------------------------------------------------------------------------
// HAProxy Pod Events.
// -----------------------------------------------------------------------------

// HAProxyPodsDiscoveredEvent is published when HAProxy pods are discovered or updated.
// This event is always coalescible since it represents endpoint state where only the
// latest set of endpoints matters.
type HAProxyPodsDiscoveredEvent struct {
	// Endpoints is the list of discovered HAProxy Dataplane API endpoints.
	Endpoints []dataplane.Endpoint
	Count     int
	timestamp time.Time
}

// NewHAProxyPodsDiscoveredEvent creates a new HAProxyPodsDiscoveredEvent.
// Performs defensive copy of the endpoints slice.
func NewHAProxyPodsDiscoveredEvent(endpoints []dataplane.Endpoint, count int) *HAProxyPodsDiscoveredEvent {
	// Defensive copy of slice
	var endpointsCopy []dataplane.Endpoint
	if len(endpoints) > 0 {
		endpointsCopy = make([]dataplane.Endpoint, len(endpoints))
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

// Coalescible returns true because endpoint discovery events represent state
// where only the latest set of endpoints matters. Older discoveries can be
// safely skipped during high-frequency pod churn (scaling, rolling updates).
func (e *HAProxyPodsDiscoveredEvent) Coalescible() bool { return true }

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

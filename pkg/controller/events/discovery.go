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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// HAProxyPodsDiscoveredEvent is published when HAProxy pods are discovered or updated.
// This event is always coalescible since it represents endpoint state where only the
// latest set of endpoints matters.
type HAProxyPodsDiscoveredEvent struct {
	// Endpoints is the list of discovered HAProxy Dataplane API endpoints.
	Endpoints []dataplane.Endpoint
	Count     int
	timestamped
}

// NewHAProxyPodsDiscoveredEvent creates a new HAProxyPodsDiscoveredEvent.
// Performs defensive copy of the endpoints slice.
func NewHAProxyPodsDiscoveredEvent(endpoints []dataplane.Endpoint, count int) *HAProxyPodsDiscoveredEvent {
	return &HAProxyPodsDiscoveredEvent{
		Endpoints:   copySlice(endpoints),
		Count:       count,
		timestamped: newTimestamped(),
	}
}

func (e *HAProxyPodsDiscoveredEvent) EventType() string { return EventTypeHAProxyPodsDiscovered }

// Coalescible returns true because endpoint discovery events represent state
// where only the latest set of endpoints matters. Older discoveries can be
// safely skipped during high-frequency pod churn (scaling, rolling updates).
func (e *HAProxyPodsDiscoveredEvent) Coalescible() bool { return true }

// HAProxyPodTerminatedEvent is published when a HAProxy pod authority retires.
//
// This triggers cleanup of the pod from all runtime config status fields.
type HAProxyPodTerminatedEvent struct {
	PodName      string
	PodNamespace string
	PodUID       string
	timestamped
}

// NewHAProxyPodTerminatedEvent creates a new HAProxyPodTerminatedEvent.
func NewHAProxyPodTerminatedEvent(podName, podNamespace, podUID string) *HAProxyPodTerminatedEvent {
	return &HAProxyPodTerminatedEvent{
		PodName:      podName,
		PodNamespace: podNamespace,
		PodUID:       podUID,
		timestamped:  newTimestamped(),
	}
}

func (e *HAProxyPodTerminatedEvent) EventType() string { return EventTypeHAProxyPodTerminated }

// HAProxyPodRejectedEvent is published by the discovery component when a
// candidate HAProxy pod is refused admission. The most common cause is an
// unsupported DataPlane API or an HAProxy series mismatch with the controller's
// bundled binary. Surfaced via Prometheus
// (haptic_haproxy_pods_rejected_total{reason}) so operators can alert on
// "controller refuses to talk to N HAProxy pods" without log-grepping.
type HAProxyPodRejectedEvent struct {
	// PodName is the rejected pod's name (used for correlation with
	// k8s events / pod logs).
	PodName string
	// Reason categorises the rejection. Stable identifiers used as a
	// Prometheus label, so prefer a fixed enum:
	//   - "version_mismatch_older" — the probed version is older than supported
	//   - "version_mismatch_newer" — the probed version is newer than supported
	//   - "version_check_failed"   — could not probe remote version (transient)
	Reason string
	timestamped
}

// NewHAProxyPodRejectedEvent creates a new HAProxyPodRejectedEvent.
func NewHAProxyPodRejectedEvent(podName, reason string) *HAProxyPodRejectedEvent {
	return &HAProxyPodRejectedEvent{
		PodName:     podName,
		Reason:      reason,
		timestamped: newTimestamped(),
	}
}

func (e *HAProxyPodRejectedEvent) EventType() string { return EventTypeHAProxyPodRejected }

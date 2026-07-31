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

// RuntimeFastPathResultEvent reports one runtime-eligible fast-path apply
// attempt (one per HAProxy pod, per reconcile). The deployer's fast path
// publishes it on every fire so the metrics component can track the fire-vs-
// apply distinction without parsing DEBUG logs: ServerUpdates == 0 means the
// fast path fired but the in-memory render diff had no runtime-eligible server
// change to apply (the common steady-state case).
type RuntimeFastPathResultEvent struct {
	// ServerUpdates is the number of runtime-eligible server updates applied to
	// the live worker on this fire (0 = fired, nothing to do).
	ServerUpdates int
	// Failed is true when the apply errored. Best-effort: the scheduled deploy
	// is the correctness floor and converges the pod regardless.
	Failed bool
	timestamped
}

// NewRuntimeFastPathResultEvent builds a RuntimeFastPathResultEvent.
func NewRuntimeFastPathResultEvent(serverUpdates int, failed bool) *RuntimeFastPathResultEvent {
	return &RuntimeFastPathResultEvent{
		ServerUpdates: serverUpdates,
		Failed:        failed,
		timestamped:   newTimestamped(),
	}
}

// EventType returns the event type identifier.
func (e *RuntimeFastPathResultEvent) EventType() string { return EventTypeRuntimeFastPathResult }

// DeployRuntimeDivergenceEvent reports one endpoint whose post-reload
// read-back found the on-disk config STRUCTURALLY diverged from the body the
// deploy pushed (issue #84): a concurrent writer replaced the file between
// the deploy's write and the read-back, so the worker may be running
// pre-route content while the deploy would otherwise have reported success.
// The deployer publishes it alongside the endpoint's failure events; the
// metrics component counts it as haptic_deploy_runtime_divergence_total.
// Steady growth means bypass pushes are clobbering structural deploys — the
// defect class the baseline-derived bypass body is supposed to make
// impossible.
type DeployRuntimeDivergenceEvent struct {
	// PodName identifies the HAProxy pod whose read-back diverged.
	PodName string
	timestamped
}

// NewDeployRuntimeDivergenceEvent builds a DeployRuntimeDivergenceEvent.
func NewDeployRuntimeDivergenceEvent(podName string) *DeployRuntimeDivergenceEvent {
	return &DeployRuntimeDivergenceEvent{
		PodName:     podName,
		timestamped: newTimestamped(),
	}
}

// EventType returns the event type identifier.
func (e *DeployRuntimeDivergenceEvent) EventType() string { return EventTypeDeployRuntimeDivergence }

// RuntimeMapDivergenceEvent reports one runtime map whose post-apply read-back
// still disagreed with the desired content, costing that sync its reload-free
// lane (issue #48). The reload fallback is convergent, so a single occurrence
// is not a fault — but the metrics component counts it as
// haptic_runtime_map_divergence_total, and steady growth means endpoint churn
// is quietly reloading HAProxy, which is exactly the property the runtime lane
// exists to protect. Without this counter the degradation is visible only as a
// WARN line, which is how it went unnoticed.
type RuntimeMapDivergenceEvent struct {
	// PodName identifies the HAProxy pod whose runtime map diverged.
	PodName string
	// MapName is the map that diverged, e.g. "pod-names.map".
	MapName string
	timestamped
}

// NewRuntimeMapDivergenceEvent builds a RuntimeMapDivergenceEvent.
func NewRuntimeMapDivergenceEvent(podName, mapName string) *RuntimeMapDivergenceEvent {
	return &RuntimeMapDivergenceEvent{
		PodName:     podName,
		MapName:     mapName,
		timestamped: newTimestamped(),
	}
}

// EventType returns the event type identifier.
func (e *RuntimeMapDivergenceEvent) EventType() string { return EventTypeRuntimeMapDivergence }

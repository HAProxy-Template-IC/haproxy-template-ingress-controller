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
